package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/jesse/codex-app-proxy/internal/constants"
	"github.com/jesse/codex-app-proxy/internal/module"
	"github.com/jesse/codex-app-proxy/internal/upstream"
	"github.com/jesse/codex-app-proxy/internal/worker"
)

func runWorker(args []string, stdout io.Writer, stderr io.Writer) int {
	return runWorkerWithFD(args, stdout, stderr, map[int]*os.File{3: os.NewFile(uintptr(3), "config-fd")})
}

type WorkerRuntimeConfig struct {
	Port     int                            `json:"port"`
	Role     string                         `json:"role,omitempty"`
	Upstream upstream.RuntimeUpstream       `json:"upstream"`
	Modules  map[string]module.ModuleConfig `json:"modules,omitempty"`
}

type workerPatch interface {
	Start() error
	Stop() error
	State() module.ConfigPatchState
	Detail() map[string]string
}

type workerServer interface {
	ListenAndServe() error
	Shutdown(context.Context) error
	Close() error
	InstallOrphanWatcher(*os.File, func())
}

var workerRunner = func(cfg WorkerRuntimeConfig) error {
	return runWorkerServer(cfg, os.Stdin)
}

var (
	buildWorkerPatch = func(cfg WorkerRuntimeConfig) (workerPatch, bool) {
		return buildConfigPatch(cfg)
	}
	newWorkerServer = func(addr string, w *worker.Worker) workerServer {
		return worker.NewServer(addr, w)
	}
	workerShutdownTimeout = 10 * time.Second
)

func runWorkerServer(cfg WorkerRuntimeConfig, stdin *os.File) error {
	modules := buildModules(cfg.Modules, cfg.Upstream.APIFormat)
	snapshot := worker.RuntimeConfigSnapshot{
		Generation: 1,
		Upstream:   cfg.Upstream,
		Modules:    modules,
	}
	var patch workerPatch
	if candidate, enabled := buildWorkerPatch(cfg); enabled {
		patch = candidate
		if err := patch.Start(); err != nil {
			return err
		}
		snapshot.ConfigPatchState = patch.State()
		snapshot.ConfigPatchDetail = patch.Detail()
	}
	w := worker.New(worker.Options{Snapshot: snapshot})
	server := newWorkerServer(constants.LocalhostAddr+":"+strconv.Itoa(cfg.Port), w)
	shutdown := newWorkerShutdown(server, patch, workerShutdownTimeout)
	server.InstallOrphanWatcher(stdin, shutdown)
	stopSignals := make(chan os.Signal, 1)
	signal.Notify(stopSignals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(stopSignals)
	go func() {
		<-stopSignals
		shutdown()
	}()
	err := server.ListenAndServe()
	if err == nil || err == http.ErrServerClosed {
		return nil
	}
	return err
}

func newWorkerShutdown(server workerServer, patch workerPatch, timeout time.Duration) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			if patch != nil {
				_ = patch.Stop()
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			if err := server.Shutdown(ctx); err != nil && errors.Is(err, context.DeadlineExceeded) {
				_ = server.Close()
			}
		})
	}
}

func SetWorkerRunnerForTest(runner func(WorkerRuntimeConfig) error) func() {
	previous := workerRunner
	workerRunner = runner
	return func() { workerRunner = previous }
}

func buildModules(configs map[string]module.ModuleConfig, apiFormat string) []module.Middleware {
	names := []string{"image_filter", "debug_sse", "api_translate", "model_override", "request_log"}
	modules := make([]module.Middleware, 0, len(names))
	for _, name := range names {
		cfg := configs[name]
		if cfg.Params == nil {
			cfg.Params = map[string]any{}
		}
		if name == "api_translate" && cfg.Params["api_format"] == nil {
			cfg.Params["api_format"] = apiFormat
		}
		switch name {
		case "image_filter":
			modules = append(modules, module.NewImageFilter(cfg))
		case "api_translate":
			modules = append(modules, module.NewAPITranslate(cfg))
		case "model_override":
			modules = append(modules, module.NewModelOverride(cfg))
		case "request_log":
			modules = append(modules, module.NewRequestLog(cfg, os.Stderr))
		case "debug_sse":
			modules = append(modules, module.NewDebugSSE(cfg, os.Stderr))
		}
	}
	return modules
}

func buildConfigPatch(cfg WorkerRuntimeConfig) (*module.ConfigPatch, bool) {
	moduleCfg, ok := cfg.Modules["config_patch"]
	if !ok || !moduleCfg.Enabled {
		return nil, false
	}
	configPath, _ := moduleCfg.Params["config_path"].(string)
	if configPath == "" {
		configPath = expandHome("~/.codex/config.toml")
	}
	stateDir, _ := moduleCfg.Params["state_dir"].(string)
	if stateDir == "" {
		stateDir = expandHome("~/.codex-proxy")
	}
	return module.NewConfigPatch(module.ConfigPatchOptions{
		StateDir:    stateDir,
		ConfigPath:  configPath,
		WorkerID:    fmt.Sprintf("worker-%d", cfg.Port),
		WorkerPort:  cfg.Port,
		PatchedBase: fmt.Sprintf("http://%s:%d", constants.LocalhostAddr, cfg.Port),
	}), true
}

func runWorkerWithFD(args []string, stdout io.Writer, stderr io.Writer, files map[int]*os.File) int {
	flags := flag.NewFlagSet("worker", flag.ContinueOnError)
	flags.SetOutput(stderr)
	port := flags.Int("port", 0, "worker port")
	configFD := flags.Int("config-fd", 0, "runtime config fd")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *port == 0 || *configFD == 0 {
		fmt.Fprintln(stderr, "worker mode requires --port and --config-fd")
		return 2
	}
	file := files[*configFD]
	if file == nil {
		fmt.Fprintf(stderr, "config fd %d unavailable\n", *configFD)
		return 2
	}
	var cfg WorkerRuntimeConfig
	if err := json.NewDecoder(file).Decode(&cfg); err != nil {
		fmt.Fprintf(stderr, "failed to read runtime config: %v\n", err)
		return 1
	}
	if cfg.Port == 0 {
		cfg.Port = *port
	}
	if err := workerRunner(cfg); err != nil {
		fmt.Fprintf(stderr, "failed to start worker: %v\n", err)
		return 1
	}
	return 0
}
