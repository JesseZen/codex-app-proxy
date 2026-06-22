package cmd

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jesse/codex-app-proxy/internal/module"
	appruntime "github.com/jesse/codex-app-proxy/internal/runtime"
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
	want := strings.Join([]string{"image_filter", "api_translate", "model_override", "request_log", "debug_sse"}, ",")
	if strings.Join(names, ",") != want {
		t.Fatalf("bad module order %v", names)
	}
}

func TestBuildConfigPatchFromRuntimeConfig(t *testing.T) {
	patch, enabled := buildConfigPatch(WorkerRuntimeConfig{
		Port: 6767,
		Modules: map[string]module.ModuleConfig{
			"config_patch": {
				Enabled: true,
				Params: map[string]any{
					"config_path": "/tmp/codex-config.toml",
					"state_dir":   "/tmp/codex-proxy",
				},
			},
		},
	})
	if !enabled {
		t.Fatal("expected config_patch enabled")
	}
	if patch == nil {
		t.Fatal("expected patch object")
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
	patch := &recordingWorkerPatch{events: &events, state: module.ConfigPatchActive}

	shutdown := newWorkerShutdown(server, patch, time.Second)
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
	state  module.ConfigPatchState
	stops  int
	detail map[string]string
}

func (p *recordingWorkerPatch) Start() error {
	return nil
}

func (p *recordingWorkerPatch) Stop() error {
	p.stops++
	*p.events = append(*p.events, "patch.Stop")
	return nil
}

func (p *recordingWorkerPatch) State() module.ConfigPatchState {
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
