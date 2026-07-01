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

	"github.com/jesse/agent-inn/internal/constants"
	"github.com/jesse/agent-inn/internal/module"
	"github.com/jesse/agent-inn/internal/modulehook"
	appruntime "github.com/jesse/agent-inn/internal/runtime"
	"github.com/jesse/agent-inn/internal/upstream"
	"github.com/jesse/agent-inn/internal/worker"
)

func runWorker(args []string, stdout io.Writer, stderr io.Writer) int {
	return runWorkerWithFD(args, stdout, stderr, map[int]*os.File{3: os.NewFile(uintptr(3), "config-fd")})
}

type WorkerRuntimeConfig struct {
	ID         appruntime.WorkerID                 `json:"id,omitempty"`
	Generation appruntime.Generation               `json:"generation,omitempty"`
	ListenPort int                                 `json:"listen_port,omitempty"`
	Port       int                                 `json:"port,omitempty"`
	Role       appruntime.WorkerRole               `json:"role,omitempty"`
	LogLevel   appruntime.LogLevel                 `json:"log_level,omitempty"`
	Upstream   appruntime.UpstreamRuntime          `json:"upstream"`
	Plugins    map[string]appruntime.PluginRuntime `json:"plugins,omitempty"`
	Modules    map[string]module.ModuleConfig      `json:"modules,omitempty"`
	Hooks      map[string]module.ModuleConfig      `json:"hooks,omitempty"`
}

type workerHookStatusRefresher interface {
	RefreshStatus() error
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
	newWorkerServer = func(addr string, w *worker.Worker) workerServer {
		return worker.NewServer(addr, w)
	}
	workerShutdownTimeout = 10 * time.Second
)

func runWorkerServer(cfg WorkerRuntimeConfig, stdin *os.File) error {
	externalRequest := map[string]module.ExternalRequestRuntime{}
	for name, plugin := range cfg.Plugins {
		if plugin.Source == "external" && plugin.Kind == "request_middleware" {
			externalRequest[name] = module.ExternalRequestRuntime{
				Command:         plugin.Command,
				Args:            append([]string(nil), plugin.Args...),
				ProtocolVersion: plugin.ProtocolVersion,
			}
		}
	}
	modules, requestStates, err := module.BuildRequestMiddlewares(cfg.Modules, module.BuildDependencies{
		APIFormat:       string(cfg.Upstream.APIFormat),
		Stderr:          os.Stderr,
		ExternalRequest: externalRequest,
	})
	if err != nil {
		return err
	}
	generation := int(cfg.Generation)
	if generation == 0 {
		generation = 1
	}
	port := cfg.ListenPort
	if port == 0 {
		port = cfg.Port
	}
	snapshot := worker.RuntimeConfigSnapshot{
		Generation: generation,
		Upstream: upstream.RuntimeUpstream{
			Name:      string(cfg.Upstream.ID),
			BaseURL:   cfg.Upstream.BaseURL,
			APIKey:    cfg.Upstream.APIKey,
			APIFormat: string(cfg.Upstream.APIFormat),
		},
		RequestModuleConfigs: module.CloneModuleConfigs(cfg.Modules),
		RequestModuleStates:  requestStates,
		HookConfigs:          module.CloneModuleConfigs(cfg.Hooks),
		Plugins:              cfg.Plugins,
		Modules:              modules,
	}
	compiledUpstream, err := upstream.Compile(cfg.Upstream)
	if err != nil {
		return err
	}
	snapshot.CompiledUpstream = compiledUpstream
	externalHooks := map[string]modulehook.ExternalHookRuntime{}
	for name, plugin := range cfg.Plugins {
		if plugin.Source == "external" && plugin.Kind == "lifecycle_hook" {
			externalHooks[name] = modulehook.ExternalHookRuntime{
				Command:         plugin.Command,
				Args:            append([]string(nil), plugin.Args...),
				ProtocolVersion: plugin.ProtocolVersion,
			}
		}
	}
	hooks, err := modulehook.Build(cfg.Hooks, modulehook.BuildDependencies{
		WorkerID:      string(cfg.ID),
		WorkerPort:    workerPort(cfg),
		ExternalHooks: externalHooks,
	})
	if err != nil {
		return err
	}
	startedHooks := []modulehook.Hook{}
	if len(hooks) > 0 {
		snapshot.HookStatuses = map[string]modulehook.Status{}
	}
	for _, hook := range hooks {
		if err := hook.Start(); err != nil {
			for i := len(startedHooks) - 1; i >= 0; i-- {
				_ = startedHooks[i].Stop()
			}
			return err
		}
		startedHooks = append(startedHooks, hook)
		if refresher, ok := hook.(workerHookStatusRefresher); ok {
			if err := refresher.RefreshStatus(); err != nil {
				for i := len(startedHooks) - 1; i >= 0; i-- {
					_ = startedHooks[i].Stop()
				}
				return err
			}
		}
		snapshot.HookStatuses[hook.Name()] = modulehook.Status{State: hook.State(), Detail: hook.Detail()}
	}
	w, err := worker.New(worker.Options{Snapshot: snapshot})
	if err != nil {
		return err
	}
	server := newWorkerServer(constants.LocalhostAddr+":"+strconv.Itoa(port), w)
	shutdown := newWorkerShutdown(server, startedHooks, workerShutdownTimeout)
	server.InstallOrphanWatcher(stdin, shutdown)
	stopSignals := make(chan os.Signal, 1)
	signal.Notify(stopSignals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(stopSignals)
	go func() {
		<-stopSignals
		shutdown()
	}()
	err = server.ListenAndServe()
	if err == nil || err == http.ErrServerClosed {
		return nil
	}
	return err
}

func newWorkerShutdown(server workerServer, hooks []modulehook.Hook, timeout time.Duration) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			for i := len(hooks) - 1; i >= 0; i-- {
				_ = hooks[i].Stop()
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
	modules, _, err := module.BuildRequestMiddlewares(configs, module.BuildDependencies{
		APIFormat: apiFormat,
		Stderr:    os.Stderr,
	})
	if err != nil {
		panic(err)
	}
	return modules
}

func workerPort(cfg WorkerRuntimeConfig) int {
	if cfg.ListenPort != 0 {
		return cfg.ListenPort
	}
	return cfg.Port
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
	if cfg.ListenPort == 0 {
		cfg.ListenPort = *port
	}
	if cfg.Port == 0 {
		cfg.Port = cfg.ListenPort
	}
	if cfg.ID == "" {
		cfg.ID = appruntime.WorkerID(fmt.Sprintf("worker-%d", cfg.ListenPort))
	}
	if err := workerRunner(cfg); err != nil {
		fmt.Fprintf(stderr, "failed to start worker: %v\n", err)
		return 1
	}
	return 0
}
