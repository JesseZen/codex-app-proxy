package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/manager"
	"github.com/jesse/agent-inn/internal/module"
	"github.com/jesse/agent-inn/internal/modulehook"
	appruntime "github.com/jesse/agent-inn/internal/runtime"
	"github.com/jesse/agent-inn/internal/worker"
)

func TestRunWorkerReadsRuntimeConfigFromFD(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := writer.Write([]byte(`{"id":"codex-app","generation":1,"listen_port":6767,"upstream":{"id":"openai","base_url":"https://api.openai.com/v1","api_key":"sk-secret"}}`)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	var called bool
	restore := SetWorkerRunnerForTest(func(cfg WorkerRuntimeConfig) error {
		called = true
		if cfg.ID != appruntime.WorkerID("codex-app") || cfg.ListenPort != 6767 || cfg.Upstream.APIKey != "sk-secret" {
			t.Fatalf("bad worker runtime config: %#v", cfg)
		}
		return nil
	})
	defer restore()

	code := runWorkerWithFD([]string{"--port", "6767", "--config-fd", "3"}, &bytes.Buffer{}, &bytes.Buffer{}, map[int]*os.File{3: reader})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if !called {
		t.Fatal("worker runner was not called")
	}
}

func TestRunWorkerReadsExternalPluginRuntimeConfigFromFD(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := writer.Write([]byte(`{"id":"codex-app","generation":1,"listen_port":6767,"upstream":{"id":"openai","base_url":"https://api.openai.com/v1"},"plugins":{"external_filter":{"kind":"request_middleware","source":"external","command":"/bin/cat","args":["--mode","strict"],"protocol_version":"1"}},"modules":{"external_filter":{"enabled":true}}}`)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	var called bool
	restore := SetWorkerRunnerForTest(func(cfg WorkerRuntimeConfig) error {
		called = true
		plugin := cfg.Plugins["external_filter"]
		if plugin.Command != "/bin/cat" || strings.Join(plugin.Args, ",") != "--mode,strict" || plugin.ProtocolVersion != "1" || !cfg.Modules["external_filter"].Enabled {
			t.Fatalf("bad external plugin runtime config: %#v", cfg)
		}
		return nil
	})
	defer restore()

	code := runWorkerWithFD([]string{"--port", "6767", "--config-fd", "3"}, &bytes.Buffer{}, &bytes.Buffer{}, map[int]*os.File{3: reader})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if !called {
		t.Fatal("worker runner was not called")
	}
}

func TestRunWorkerRejectsMissingFDConfig(t *testing.T) {
	var stderr bytes.Buffer
	code := runWorkerWithFD([]string{"--port", "6767", "--config-fd", "3"}, &bytes.Buffer{}, &stderr, map[int]*os.File{})
	if code == 0 {
		t.Fatal("expected failure")
	}
	if !bytes.Contains(stderr.Bytes(), []byte("config fd 3 unavailable")) {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}

func TestBuildModulesIncludesFixedOrderAuxiliaryModules(t *testing.T) {
	modules := buildModules(map[string]module.ModuleConfig{
		"debug_sse":      {Enabled: true},
		"request_log":    {Enabled: true},
		"model_override": {Enabled: true, Params: map[string]any{"model": "gpt-test"}},
		"api_translate":  {Enabled: true},
		"image_filter":   {Enabled: true},
	}, "chat_completions")

	var names []string
	for _, middleware := range modules {
		names = append(names, middleware.Name())
	}
	want := strings.Join([]string{"image_filter", "debug_sse", "api_translate", "model_override", "request_log"}, ",")
	if strings.Join(names, ",") != want {
		t.Fatalf("bad module order %v", names)
	}
}

func TestBuildModulesDebugSSEWrapsTranslatedResponsesStream(t *testing.T) {
	var logBuf bytes.Buffer
	modules := buildModules(map[string]module.ModuleConfig{
		"debug_sse":     {Enabled: true},
		"api_translate": {Enabled: true},
	}, "chat_completions")
	for i, middleware := range modules {
		if middleware.Name() == "debug_sse" {
			modules[i] = module.NewDebugSSE(module.ModuleConfig{Enabled: true}, &logBuf)
		}
	}

	resp := &module.ProxyResponse{
		StatusCode:  http.StatusOK,
		Headers:     http.Header{"Content-Type": []string{"text/event-stream"}},
		ContentType: "text/event-stream",
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"id":"chatcmpl-test","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}` + "\n\n",
			`data: {"id":"chatcmpl-test","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n",
			"data: [DONE]\n\n",
		}, ""))),
	}
	req := &module.ProxyRequest{Path: "/v1/responses"}

	var err error
	for i := len(modules) - 1; i >= 0; i-- {
		resp, err = modules[i].WrapResponse(context.Background(), req, resp)
		if err != nil {
			t.Fatal(err)
		}
	}

	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "event: response.completed") {
		t.Fatalf("missing translated response completion event: %s", out)
	}
	if !strings.Contains(logBuf.String(), "response_completed=true") {
		t.Fatalf("debug_sse did not observe translated completion event: %s", logBuf.String())
	}
}

func TestRunWorkerServerRejectsInvalidUpstreamWithoutPanic(t *testing.T) {
	err := runWorkerServer(WorkerRuntimeConfig{
		Port:     0,
		Upstream: appruntime.UpstreamRuntime{BaseURL: "://bad-url"},
	}, os.Stdin)
	if err == nil {
		t.Fatal("expected invalid upstream error")
	}
}

func TestRunWorkerServerPassesExternalRequestMiddlewareArgs(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/arg" {
			t.Fatalf("external middleware did not rewrite path: %s", r.URL.Path)
		}
		if r.Header.Get("X-External-Arg") != "expected-token" {
			t.Fatalf("external middleware arg header missing: %q", r.Header.Get("X-External-Arg"))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "external.sh")
	if err := os.WriteFile(scriptPath, []byte(`#!/bin/sh
if [ "$1" != "expected-token" ]; then
  exit 42
fi
printf '%s' '{"method":"POST","path":"/arg","headers":{"X-External-Arg":["expected-token"]},"body":"payload","content_type":"text/plain"}'
`), 0700); err != nil {
		t.Fatal(err)
	}

	previousNewWorkerServer := newWorkerServer
	newWorkerServer = func(addr string, w *worker.Worker) workerServer {
		return requestWorkerServer{worker: w}
	}
	defer func() { newWorkerServer = previousNewWorkerServer }()

	err := runWorkerServer(WorkerRuntimeConfig{
		Port:     0,
		Upstream: appruntime.UpstreamRuntime{BaseURL: upstream.URL},
		Plugins: map[string]appruntime.PluginRuntime{
			"external_filter": {
				Kind:            "request_middleware",
				Source:          "external",
				Command:         scriptPath,
				Args:            []string{"expected-token"},
				ProtocolVersion: "1",
			},
		},
		Modules: map[string]module.ModuleConfig{
			"external_filter": {Enabled: true},
		},
	}, os.Stdin)
	if err != nil {
		t.Fatal(err)
	}
}

func TestRunWorkerServerRunsExternalLifecycleHookStartStatusStop(t *testing.T) {
	dir := t.TempDir()
	actionsPath := filepath.Join(dir, "actions")
	scriptPath := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(scriptPath, []byte(`#!/bin/sh
printf '%s %s\n' "$1" "$2" >> "`+actionsPath+`"
case "$2" in
  start) printf '%s' '{"state":"active","detail":{"action":"start"}}' ;;
  status) printf '%s' '{"state":"active","detail":{"action":"status"}}' ;;
  stop) printf '%s' '{"state":"clean","detail":{"action":"stop"}}' ;;
esac
`), 0700); err != nil {
		t.Fatal(err)
	}

	server := &statusThenShutdownWorkerServer{}
	previousNewWorkerServer := newWorkerServer
	newWorkerServer = func(addr string, w *worker.Worker) workerServer {
		server.worker = w
		return server
	}
	defer func() { newWorkerServer = previousNewWorkerServer }()

	err := runWorkerServer(WorkerRuntimeConfig{
		ID:   "app",
		Port: 6767,
		Upstream: appruntime.UpstreamRuntime{
			ID:      "openai",
			BaseURL: "http://127.0.0.1:1",
		},
		Plugins: map[string]appruntime.PluginRuntime{
			"external_hook": {
				Kind:            "lifecycle_hook",
				Source:          "external",
				Command:         scriptPath,
				Args:            []string{"manifest-arg"},
				ProtocolVersion: "1",
			},
		},
		Hooks: map[string]module.ModuleConfig{
			"external_hook": {Enabled: true},
		},
	}, os.Stdin)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(server.statusBody, `"hook_statuses":{"external_hook":{"state":"active","detail":{"action":"status"}}}`) {
		t.Fatalf("status missing external hook status: %s", server.statusBody)
	}
	actions, err := os.ReadFile(actionsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(actions)) != "manifest-arg start\nmanifest-arg status\nmanifest-arg stop" {
		t.Fatalf("bad actions:\n%s", string(actions))
	}
}

func TestRunWorkerServerRunsMixedHooksFromManagerRuntime(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	stateDir := filepath.Join(dir, "state")
	if err := os.WriteFile(configPath, []byte(strings.Join([]string{
		`model_provider = "test"`,
		``,
		`[model_providers.test]`,
		`base_url = "https://example.com/v1"`,
		``,
	}, "\n")), 0600); err != nil {
		t.Fatal(err)
	}

	actionsPath := filepath.Join(dir, "actions")
	scriptPath := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(scriptPath, []byte(`#!/bin/sh
if [ "$1" != "manifest-arg" ]; then
  echo "missing manifest arg" >&2
  exit 41
fi
if ! grep -q 'base_url = "http://127.0.0.1:6767"' "`+configPath+`"; then
  echo "$2 did not observe config_patch" >&2
  exit 42
fi
printf '%s %s\n' "$1" "$2" >> "`+actionsPath+`"
case "$2" in
  start) printf '%s' '{"state":"active","detail":{"action":"start"}}' ;;
  status) printf '%s' '{"state":"active","detail":{"action":"status"}}' ;;
  stop) printf '%s' '{"state":"clean","detail":{"action":"stop"}}' ;;
esac
`), 0700); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, "plugin.yaml")
	if err := os.WriteFile(manifestPath, []byte(`name: external_hook
kind: lifecycle_hook
version: 0.1.0
protocol_version: "1"
command: `+scriptPath+`
args:
  - manifest-arg
`), 0600); err != nil {
		t.Fatal(err)
	}

	runtimeCfg, err := manager.RuntimeBuilder{}.Build(config.Config{
		Plugins: map[string]config.PluginDefinition{
			"config_patch":  {Kind: config.PluginKindLifecycleHook, Source: config.PluginSourceBuiltin},
			"external_hook": {Kind: config.PluginKindLifecycleHook, Source: config.PluginSourceExternal, Path: manifestPath},
		},
		Workers: map[string]config.WorkerConfig{
			"app": {
				Port:     6767,
				Upstream: "openai",
				Hooks: map[string]config.ModuleConfig{
					"config_patch": {
						Enabled: true,
						Params: map[string]any{
							"config_path": configPath,
							"state_dir":   stateDir,
						},
					},
					"external_hook": {Enabled: true},
				},
			},
		},
		Upstreams: map[string]config.UpstreamProfile{
			"openai": {BaseURL: "http://127.0.0.1:1"},
		},
	}, "app", 7)
	if err != nil {
		t.Fatal(err)
	}
	var workerCfg WorkerRuntimeConfig
	runtimeBytes, err := json.Marshal(runtimeCfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(runtimeBytes, &workerCfg); err != nil {
		t.Fatal(err)
	}

	server := &statusThenShutdownWorkerServer{}
	previousNewWorkerServer := newWorkerServer
	newWorkerServer = func(addr string, w *worker.Worker) workerServer {
		server.worker = w
		return server
	}
	defer func() { newWorkerServer = previousNewWorkerServer }()

	if err := runWorkerServer(workerCfg, os.Stdin); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(server.statusBody, `"config_patch":{"state":"active"`) {
		t.Fatalf("status missing config_patch status: %s", server.statusBody)
	}
	if !strings.Contains(server.statusBody, `"external_hook":{"state":"active","detail":{"action":"status"}}`) {
		t.Fatalf("status missing external_hook status: %s", server.statusBody)
	}
	actions, err := os.ReadFile(actionsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(actions)) != "manifest-arg start\nmanifest-arg status\nmanifest-arg stop" {
		t.Fatalf("bad actions:\n%s", string(actions))
	}
	restored, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(restored), `base_url = "https://example.com/v1"`) || strings.Contains(string(restored), `base_url = "http://127.0.0.1:6767"`) {
		t.Fatalf("config_patch was not restored:\n%s", string(restored))
	}
}

func TestRunWorkerServerFailsWhenExternalLifecycleHookStartFails(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 42\n"), 0700); err != nil {
		t.Fatal(err)
	}

	err := runWorkerServer(WorkerRuntimeConfig{
		ID:   "app",
		Port: 6767,
		Upstream: appruntime.UpstreamRuntime{
			ID:      "openai",
			BaseURL: "http://127.0.0.1:1",
		},
		Plugins: map[string]appruntime.PluginRuntime{
			"external_hook": {
				Kind:            "lifecycle_hook",
				Source:          "external",
				Command:         scriptPath,
				ProtocolVersion: "1",
			},
		},
		Hooks: map[string]module.ModuleConfig{
			"external_hook": {Enabled: true},
		},
	}, os.Stdin)
	if err == nil || !strings.Contains(err.Error(), `external lifecycle hook "external_hook" start failed`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunWorkerServerStopsOnOrphanEOF(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	done := make(chan error, 1)
	go func() {
		done <- runWorkerServer(WorkerRuntimeConfig{
			Port:     0,
			Upstream: appruntime.UpstreamRuntime{BaseURL: "http://127.0.0.1:1"},
		}, reader)
	}()

	time.Sleep(50 * time.Millisecond)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected clean orphan shutdown, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker server did not stop after stdin EOF")
	}
}

func TestWorkerShutdownRestoresConfigPatchBeforeDrainingHTTP(t *testing.T) {
	events := []string{}
	server := &recordingWorkerServer{events: &events}
	patch := &recordingWorkerPatch{events: &events, state: modulehook.ConfigPatchActive}

	shutdown := newWorkerShutdown(server, []modulehook.Hook{patch}, time.Second)
	shutdown()
	shutdown()

	if strings.Join(events, ",") != "patch.Stop,server.Shutdown" {
		t.Fatalf("unexpected shutdown order: %v", events)
	}
	if patch.stops != 1 || server.shutdowns != 1 {
		t.Fatalf("shutdown was not idempotent: patch stops=%d server shutdowns=%d", patch.stops, server.shutdowns)
	}
}

func TestWorkerShutdownClosesServerWhenDrainTimesOut(t *testing.T) {
	events := []string{}
	server := &recordingWorkerServer{events: &events, waitForDeadline: true}

	shutdown := newWorkerShutdown(server, nil, 10*time.Millisecond)
	shutdown()

	if strings.Join(events, ",") != "server.Shutdown,server.Close" {
		t.Fatalf("expected forced close after shutdown timeout, got %v", events)
	}
}

type recordingWorkerPatch struct {
	events *[]string
	state  modulehook.ConfigPatchState
	stops  int
	detail map[string]string
}

func (p *recordingWorkerPatch) Name() string {
	return "config_patch"
}

func (p *recordingWorkerPatch) Config() module.ModuleConfig {
	return module.ModuleConfig{Enabled: true}
}

func (p *recordingWorkerPatch) UpdateConfig(module.ModuleConfig) error {
	return nil
}

func (p *recordingWorkerPatch) Start() error {
	return nil
}

func (p *recordingWorkerPatch) Stop() error {
	p.stops++
	*p.events = append(*p.events, "patch.Stop")
	return nil
}

func (p *recordingWorkerPatch) State() modulehook.ConfigPatchState {
	return p.state
}

func (p *recordingWorkerPatch) Detail() map[string]string {
	return p.detail
}

type recordingWorkerServer struct {
	events          *[]string
	shutdowns       int
	closes          int
	waitForDeadline bool
}

type requestWorkerServer struct {
	worker *worker.Worker
}

func (s requestWorkerServer) ListenAndServe() error {
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy.local/start", strings.NewReader("original"))
	s.worker.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		return errors.New("unexpected worker response: " + res.Body.String())
	}
	return nil
}

func (s requestWorkerServer) Shutdown(context.Context) error {
	return nil
}

func (s requestWorkerServer) Close() error {
	return nil
}

func (s requestWorkerServer) InstallOrphanWatcher(*os.File, func()) {}

type statusThenShutdownWorkerServer struct {
	worker     *worker.Worker
	shutdown   func()
	statusBody string
}

func (s *statusThenShutdownWorkerServer) ListenAndServe() error {
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://proxy.local/_proxy/status", nil)
	s.worker.ServeHTTP(res, req)
	s.statusBody = res.Body.String()
	s.shutdown()
	return http.ErrServerClosed
}

func (s *statusThenShutdownWorkerServer) Shutdown(context.Context) error {
	return nil
}

func (s *statusThenShutdownWorkerServer) Close() error {
	return nil
}

func (s *statusThenShutdownWorkerServer) InstallOrphanWatcher(_ *os.File, shutdown func()) {
	s.shutdown = shutdown
}

func (s *recordingWorkerServer) ListenAndServe() error {
	return nil
}

func (s *recordingWorkerServer) Shutdown(ctx context.Context) error {
	s.shutdowns++
	*s.events = append(*s.events, "server.Shutdown")
	if s.waitForDeadline {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (s *recordingWorkerServer) Close() error {
	s.closes++
	*s.events = append(*s.events, "server.Close")
	return nil
}

func (s *recordingWorkerServer) InstallOrphanWatcher(*os.File, func()) {}
