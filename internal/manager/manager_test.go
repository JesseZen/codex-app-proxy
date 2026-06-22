package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jesse/codex-app-proxy/internal/config"
	"github.com/jesse/codex-app-proxy/internal/module"
	appruntime "github.com/jesse/codex-app-proxy/internal/runtime"
	"github.com/jesse/codex-app-proxy/internal/upstream"
)

func TestManagerDetectsManagedPortConflict(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"one": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
	})

	if err := m.CheckPortAvailable("two", 6767); err == nil || !strings.Contains(err.Error(), "worker 'one'") {
		t.Fatalf("expected managed port conflict, got %v", err)
	}
}

func TestManagerDetectsExternalPortConflict(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	m := New(Config{Config: config.Config{}})
	if err := m.CheckPortAvailable("new", port); err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("expected external port conflict, got %v", err)
	}
}

func TestManagerAPIListsWorkersAndProvidersWithoutSecrets(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-secret")
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"codex-app": {Role: "app", Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1", APIKey: "sk-file"},
			},
		},
	})

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "sk-secret") {
		t.Fatalf("workers API leaked secret: %s", res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"has_api_key":true`) {
		t.Fatalf("workers API did not expose key state: %s", res.Body.String())
	}
	if strings.Contains(res.Body.String(), "api_key_ref") {
		t.Fatalf("workers API leaked legacy key ref field: %s", res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"snapshot_generation":1`) {
		t.Fatalf("workers API did not expose snapshot generation: %s", res.Body.String())
	}

	res = httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/config", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected config status %d: %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "sk-secret") {
		t.Fatalf("config API leaked expanded secret: %s", res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"dirty"`) {
		t.Fatalf("config API missing status: %s", res.Body.String())
	}
}

func TestManagerSyncsCodexProfilesOnStartup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"cli-openai": {Port: 11199, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1", APIFormat: "responses"},
			},
		},
	})

	data, err := os.ReadFile(filepath.Join(home, ".codex", "cli-openai.config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `model_provider = 'cli-openai'`) {
		t.Fatalf("unexpected profile file: %s", data)
	}
	if !strings.Contains(string(data), `base_url = 'http://127.0.0.1:11199'`) {
		t.Fatalf("unexpected profile file: %s", data)
	}
}

func TestManagerLogSinkWritesToDefaultHomeLogDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
	})

	sink := m.LogSink("app")
	if sink == nil {
		t.Fatal("expected log sink")
	}
	if _, err := sink.Write([]byte("WARN upstream closed early\n")); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(home, ".codex-proxy", "logs", "worker-6767.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("expected default log file at %s: %v", logPath, err)
	}
	if !strings.Contains(string(data), "WARN upstream closed early") {
		t.Fatalf("unexpected log file content: %s", data)
	}
}

func TestManagerBuildsWorkerRuntimeConfigForFDWithoutSecretInArgs(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-secret")
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"codex-app": {Role: "app", Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1", APIKey: "sk-file"},
			},
		},
	})

	spawn, err := m.BuildWorkerSpawn("codex-app")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(spawn.Args, " "), "sk-secret") {
		t.Fatalf("secret leaked into argv: %#v", spawn.Args)
	}
	if !strings.Contains(string(spawn.RuntimeJSON), "sk-secret") {
		t.Fatalf("runtime fd payload missing resolved secret: %s", spawn.RuntimeJSON)
	}
	var decoded appruntime.WorkerRuntime
	if err := json.Unmarshal(spawn.RuntimeJSON, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ID != "codex-app" || decoded.ListenPort != 6767 || decoded.Generation != 1 {
		t.Fatalf("bad runtime payload: %#v", decoded)
	}
	if decoded.Upstream.ID != "openai" || decoded.Upstream.APIKey != "sk-secret" {
		t.Fatalf("bad runtime payload: %#v", decoded)
	}
}

func TestManagerStartWorkerUsesProviderSecretWhenConfigured(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-env")
	starter := &recordingStarter{}
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"codex-app": {Role: "app", Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1", APIKey: "sk-file"},
			},
		},
		Starter: starter,
	})

	if err := m.StartWorker("codex-app"); err != nil {
		t.Fatal(err)
	}
	if len(starter.spawns) != 1 {
		t.Fatalf("expected one worker spawn, got %d", len(starter.spawns))
	}
	if !strings.Contains(string(starter.spawns[0].RuntimeJSON), "sk-env") {
		t.Fatalf("expected env secret in runtime payload, got %s", starter.spawns[0].RuntimeJSON)
	}
}

func TestManagerWorkerSummariesExposeRole(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"codex-app": {Role: "app", Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
	})
	summaries := m.workerSummaries()
	if len(summaries) != 1 || summaries[0].Role != "app" {
		t.Fatalf("expected worker role in summaries: %#v", summaries)
	}
}

func TestManagerAPITogglesConfiguredWorkerModule(t *testing.T) {
	client := &recordingWorkerClient{}
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"codex-app": {
					Port:     6767,
					Upstream: "openai",
					Modules: map[string]config.ModuleConfig{
						"image_filter": {Enabled: false},
					},
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		WorkerClient: client,
	})
	m.statuses["codex-app"] = "running"

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/workers/6767/modules/image_filter/toggle", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected toggle status %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"enabled":true`) {
		t.Fatalf("toggle response did not enable module: %s", res.Body.String())
	}

	res = httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers", nil))
	if !strings.Contains(res.Body.String(), `"enabled":true`) {
		t.Fatalf("worker list did not reflect toggle: %s", res.Body.String())
	}
	if client.toggledPort != 6767 || client.toggledModule != "image_filter" {
		t.Fatalf("live worker toggle was not called: port=%d module=%s", client.toggledPort, client.toggledModule)
	}
}

func TestManagerAPIExposesDisabledBuiltInModulesForTransparentWorker(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"plain": {
					Port:     11199,
					Upstream: "openai",
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
	})

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers/11199", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected worker detail status %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"log_level":"simple"`) {
		t.Fatalf("worker detail missing default log level: %s", res.Body.String())
	}
	for _, moduleName := range []string{"image_filter", "api_translate", "model_override", "config_patch", "request_log", "debug_sse"} {
		want := `"` + moduleName + `":{"enabled":false`
		if !strings.Contains(res.Body.String(), want) {
			t.Fatalf("worker detail missing disabled built-in module %s: %s", moduleName, res.Body.String())
		}
	}
}

func TestManagerAPIWorkerDetailIncludesProviderFieldsAndConfigPatchState(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-live")
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"app": {
					Port:     6767,
					Upstream: "openai",
					Modules: map[string]config.ModuleConfig{
						"config_patch":   {Enabled: true},
						"model_override": {Enabled: true, Params: map[string]any{"model": "gpt-live"}},
					},
					LogLevel: "detail",
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1", APIKey: "sk-file", APIFormat: "chat_completions"},
			},
		},
		WorkerClient: &recordingWorkerClient{
			statusBody: `{"snapshot_generation":7,"upstream":{"name":"openai","base_url":"https://api.openai.com/v1","has_api_key":true,"api_format":"chat_completions"},"modules":{"config_patch":{"enabled":true},"model_override":{"enabled":true,"params":{"model":"gpt-live"}}},"config_patch_state":"unresolved","config_patch_detail":{"provider_name":"test","field_name":"base_url","previous_value":"https://example.com/v1","patched_value":"http://127.0.0.1:6767","current_value":"https://manual.example/v1"}}`,
		},
	})
	m.statuses["app"] = "running"

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers/6767", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected worker detail status %d: %s", res.Code, res.Body.String())
	}
	for _, want := range []string{
		`"base_url":"https://api.openai.com/v1"`,
		`"has_api_key":true`,
		`"api_format":"chat_completions"`,
		`"snapshot_generation":7`,
		`"log_level":"detail"`,
		`"config_patch_state":"unresolved"`,
		`"config_patch_detail"`,
		`"current_value":"https://manual.example/v1"`,
		`"model":"gpt-live"`,
	} {
		if !strings.Contains(res.Body.String(), want) {
			t.Fatalf("worker detail missing %s: %s", want, res.Body.String())
		}
	}
}

func TestManagerAPIUpdatesWorkerLogLevel(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"cli": {
					Port:     11199,
					Upstream: "openai",
					Modules: map[string]config.ModuleConfig{
						"api_translate": {Enabled: true},
					},
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
	})

	body := strings.NewReader(`{"port":11199,"upstream":"openai","log_level":"detail","modules":{"api_translate":{"enabled":true}}}`)
	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/workers/11199", body))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected update status %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"log_level":"detail"`) {
		t.Fatalf("update response did not expose detail log level: %s", res.Body.String())
	}

	res = httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers/11199", nil))
	if !strings.Contains(res.Body.String(), `"log_level":"detail"`) {
		t.Fatalf("worker detail did not persist log level: %s", res.Body.String())
	}
}

func TestManagerAPIPatchesRunningWorkerLogLevelWithoutRecheckingCurrentPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"cli": {
					Port:     port,
					Upstream: "openai",
					Modules: map[string]config.ModuleConfig{
						"api_translate": {Enabled: true},
					},
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: fakeStarter{},
	})
	if err := m.StartWorker("cli"); err != nil {
		t.Fatal(err)
	}

	body := strings.NewReader(fmt.Sprintf(`{"port":%d,"upstream":"openai","log_level":"detail","modules":{"api_translate":{"enabled":true}}}`, port))
	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, fmt.Sprintf("http://manager.local/api/workers/%d", port), body))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected update status %d: %s", res.Code, res.Body.String())
	}

	got, ok := m.workerConfig("cli")
	if !ok {
		t.Fatal("worker config missing")
	}
	want := config.WorkerConfig{
		Role:     "cli",
		Port:     port,
		Upstream: "openai",
		LogLevel: "detail",
		Modules: map[string]config.ModuleConfig{
			"api_translate": {Enabled: true},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected worker config %#v", got)
	}
}

func TestManagerAPIPatchesConfiguredWorkerModule(t *testing.T) {
	client := &recordingWorkerClient{}
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"codex-app": {
					Port:     6767,
					Upstream: "openai",
					Modules: map[string]config.ModuleConfig{
						"model_override": {Enabled: false, Params: map[string]any{"model": "old-model"}},
					},
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		WorkerClient: client,
	})
	m.statuses["codex-app"] = "running"

	res := httptest.NewRecorder()
	body := strings.NewReader(`{"enabled":true,"params":{"model":"new-model"}}`)
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/workers/6767/modules/model_override", body))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected patch status %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"enabled":true`) || !strings.Contains(res.Body.String(), `"model":"new-model"`) {
		t.Fatalf("patch response did not include updated module: %s", res.Body.String())
	}

	res = httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers/6767", nil))
	if !strings.Contains(res.Body.String(), `"enabled":true`) || !strings.Contains(res.Body.String(), `"model":"new-model"`) {
		t.Fatalf("worker detail did not reflect module patch: %s", res.Body.String())
	}
	if client.patchedPort != 6767 || client.patchedModule != "model_override" || !client.patchedConfig.Enabled || client.patchedConfig.Params["model"] != "new-model" {
		t.Fatalf("live worker patch was not called: port=%d module=%s config=%#v", client.patchedPort, client.patchedModule, client.patchedConfig)
	}
}

func TestManagerAPIToggleRejectsSecondRunningConfigPatch(t *testing.T) {
	client := &recordingWorkerClient{}
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"codex-app": {
					Port:     6767,
					Upstream: "openai",
					Modules: map[string]config.ModuleConfig{
						"config_patch": {Enabled: true},
					},
				},
				"cli": {
					Port:     11199,
					Upstream: "openai",
					Modules: map[string]config.ModuleConfig{
						"config_patch": {Enabled: false},
					},
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		WorkerClient: client,
	})
	m.statuses["codex-app"] = "running"
	m.statuses["cli"] = "running"

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/workers/11199/modules/config_patch/toggle", nil))
	if res.Code != http.StatusConflict {
		t.Fatalf("expected conflict, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "config_patch already active on another worker") {
		t.Fatalf("conflict response did not explain config_patch ownership: %s", res.Body.String())
	}
	if client.toggledPort != 0 || client.toggledModule != "" {
		t.Fatalf("live worker toggle should not be called on rejected config_patch: port=%d module=%s", client.toggledPort, client.toggledModule)
	}
}

func TestManagerAPIToggleRejectsConfigPatchWhenWorkerRecoveryStateIsUnresolved(t *testing.T) {
	client := &recordingWorkerClient{
		statusBody: `{"snapshot_generation":3,"upstream":{"name":"openai","base_url":"https://api.openai.com/v1"},"modules":{"config_patch":{"enabled":false}},"config_patch_state":"unresolved"}`,
	}
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"cli": {
					Port:     11199,
					Upstream: "openai",
					Modules: map[string]config.ModuleConfig{
						"config_patch": {Enabled: false},
					},
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		WorkerClient: client,
	})
	m.statuses["cli"] = "running"

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/workers/11199/modules/config_patch/toggle", nil))
	if res.Code != http.StatusConflict {
		t.Fatalf("expected unresolved config_patch toggle conflict, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "config_patch recovery state unresolved") {
		t.Fatalf("missing unresolved recovery error: %s", res.Body.String())
	}
	if client.toggledPort != 0 || client.toggledModule != "" {
		t.Fatalf("live worker toggle should not be called on unresolved config_patch: port=%d module=%s", client.toggledPort, client.toggledModule)
	}
}

func TestManagerAPIPatchRejectsSecondRunningConfigPatchEnable(t *testing.T) {
	client := &recordingWorkerClient{}
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"codex-app": {
					Port:     6767,
					Upstream: "openai",
					Modules: map[string]config.ModuleConfig{
						"config_patch": {Enabled: true},
					},
				},
				"cli": {
					Port:     11199,
					Upstream: "openai",
					Modules: map[string]config.ModuleConfig{
						"config_patch": {Enabled: false},
					},
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		WorkerClient: client,
	})
	m.statuses["codex-app"] = "running"
	m.statuses["cli"] = "running"

	res := httptest.NewRecorder()
	body := strings.NewReader(`{"enabled":true}`)
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/workers/11199/modules/config_patch", body))
	if res.Code != http.StatusConflict {
		t.Fatalf("expected conflict, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "config_patch already active on another worker") {
		t.Fatalf("conflict response did not explain config_patch ownership: %s", res.Body.String())
	}
	if client.patchedPort != 0 || client.patchedModule != "" {
		t.Fatalf("live worker patch should not be called on rejected config_patch: port=%d module=%s", client.patchedPort, client.patchedModule)
	}
}

func TestManagerAPICreatesAndStartsWorker(t *testing.T) {
	starter := &recordingStarter{}
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: starter,
	})

	body := strings.NewReader(`{"name":"cli-openai","port":11199,"upstream":"openai","modules":{"api_translate":{"enabled":true}}}`)
	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/workers", body))
	if res.Code != http.StatusCreated {
		t.Fatalf("unexpected create status %d: %s", res.Code, res.Body.String())
	}
	if len(starter.spawns) != 1 {
		t.Fatalf("expected worker to be started, got %d spawns", len(starter.spawns))
	}
	if !strings.Contains(res.Body.String(), `"name":"cli-openai"`) || !strings.Contains(res.Body.String(), `"status":"running"`) {
		t.Fatalf("create response missing worker summary: %s", res.Body.String())
	}

	res = httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers/11199", nil))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"api_translate":{"enabled":true`) {
		t.Fatalf("created worker not visible in manager API: status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestManagerAPICreateWorkerRejectsManagedPortConflict(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: fakeStarter{},
	})

	body := strings.NewReader(`{"name":"duplicate","port":6767,"upstream":"openai"}`)
	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/workers", body))
	if res.Code != http.StatusConflict {
		t.Fatalf("expected port conflict, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "worker 'app'") {
		t.Fatalf("conflict response did not name owning worker: %s", res.Body.String())
	}
}

func TestManagerAPIUpdatesWorkerPortByRespawning(t *testing.T) {
	starter := &recordingStarter{}
	checker := &recordingHealthChecker{results: map[int]bool{11200: true}}
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"cli-openai": {
					Port:     11199,
					Upstream: "openai",
					Modules: map[string]config.ModuleConfig{
						"api_translate": {Enabled: true},
					},
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter:       starter,
		HealthChecker: checker,
	})
	if err := m.StartWorker("cli-openai"); err != nil {
		t.Fatal(err)
	}

	body := strings.NewReader(`{"port":11200,"upstream":"openai","modules":{"api_translate":{"enabled":true}}}`)
	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/workers/11199", body))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected update status %d: %s", res.Code, res.Body.String())
	}
	if len(starter.spawns) != 2 {
		t.Fatalf("expected worker respawn on new port, got %d spawns", len(starter.spawns))
	}
	if starter.processes[0].stops != 1 {
		t.Fatalf("expected old process stopped after new port spawn, got %d stops", starter.processes[0].stops)
	}
	if checker.calls[11200] == 0 {
		t.Fatalf("expected manager to health-check the new port before stopping old worker")
	}
	if !strings.Contains(res.Body.String(), `"port":11200`) || !strings.Contains(res.Body.String(), `"status":"running"`) {
		t.Fatalf("update response did not expose new worker summary: %s", res.Body.String())
	}

	res = httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers/11199", nil))
	if res.Code != http.StatusNotFound {
		t.Fatalf("old port still resolves after port update: %d %s", res.Code, res.Body.String())
	}
	res = httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers/11200", nil))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"name":"cli-openai"`) {
		t.Fatalf("new port did not resolve after update: %d %s", res.Code, res.Body.String())
	}
}

func TestManagerAPIWorkerPortUpdateRejectsConflict(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
				"cli": {Port: 11199, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: fakeStarter{},
	})

	body := strings.NewReader(`{"port":6767,"upstream":"openai"}`)
	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/workers/11199", body))
	if res.Code != http.StatusConflict {
		t.Fatalf("expected port conflict, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "worker 'app'") {
		t.Fatalf("conflict response did not name owning worker: %s", res.Body.String())
	}
}

func TestManagerWorkerLifecycleStateTransitions(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"codex-app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: fakeStarter{},
	})

	if err := m.StartWorker("codex-app"); err != nil {
		t.Fatal(err)
	}
	assertWorkerStatus(t, m, "running")

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/workers/6767/restart", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected restart status %d: %s", res.Code, res.Body.String())
	}
	assertWorkerStatus(t, m, "running")

	res = httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodDelete, "http://manager.local/api/workers/6767", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected delete status %d: %s", res.Code, res.Body.String())
	}
	assertWorkerStatus(t, m, "stopped")
}

func TestManagerReportsForcedStopState(t *testing.T) {
	starter := &recordingStarter{forcedStop: true}
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"codex-app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: starter,
	})
	if err := m.StartWorker("codex-app"); err != nil {
		t.Fatal(err)
	}

	if err := m.StopWorker("codex-app"); err != nil {
		t.Fatal(err)
	}

	assertWorkerStatus(t, m, "stopped (forced)")
}

func TestManagerStartConfiguredWorkers(t *testing.T) {
	starter := &recordingStarter{}
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
				"cli": {Port: 11199, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: starter,
	})

	if err := m.StartConfiguredWorkers(); err != nil {
		t.Fatal(err)
	}
	if len(starter.spawns) != 2 {
		t.Fatalf("expected two worker spawns, got %d", len(starter.spawns))
	}
	assertWorkerStatus(t, m, "running")
}

func TestManagerStartConfiguredWorkersRecoversStaleConfigPatchBeforeSpawn(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configPath, []byte(strings.Join([]string{
		`model_provider = "test"`,
		``,
		`[model_providers.test]`,
		`base_url = "https://example.com/v1"`,
		``,
	}, "\n")), 0600); err != nil {
		t.Fatal(err)
	}

	patch := module.NewConfigPatch(module.ConfigPatchOptions{
		StateDir:    filepath.Join(dir, "state"),
		ConfigPath:  configPath,
		WorkerID:    "worker-6767",
		WorkerPort:  6767,
		PatchedBase: "http://127.0.0.1:6767",
	})
	if err := patch.Start(); err != nil {
		t.Fatal(err)
	}
	if err := patch.CloseLockForTest(); err != nil {
		t.Fatal(err)
	}

	starter := &recordingStarter{}
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"app": {
					Port:     6767,
					Upstream: "openai",
					Modules: map[string]config.ModuleConfig{
						"config_patch": {Enabled: true, Params: map[string]any{"config_path": configPath, "state_dir": filepath.Join(dir, "state")}},
					},
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: starter,
	})

	if err := m.StartConfiguredWorkers(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `base_url = "https://example.com/v1"`) {
		t.Fatalf("expected manager startup recovery to restore config, got:\n%s", data)
	}
	if _, err := os.Stat(filepath.Join(dir, "state", "config-patch-journal.json")); !os.IsNotExist(err) {
		t.Fatalf("expected stale journal removed before spawn, got %v", err)
	}
}

func TestManagerStartConfiguredWorkersLeavesManualEditConflictUnresolvedBeforeSpawn(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configPath, []byte(strings.Join([]string{
		`model_provider = "test"`,
		``,
		`[model_providers.test]`,
		`base_url = "https://example.com/v1"`,
		``,
	}, "\n")), 0600); err != nil {
		t.Fatal(err)
	}

	patch := module.NewConfigPatch(module.ConfigPatchOptions{
		StateDir:    filepath.Join(dir, "state"),
		ConfigPath:  configPath,
		WorkerID:    "worker-6767",
		WorkerPort:  6767,
		PatchedBase: "http://127.0.0.1:6767",
	})
	if err := patch.Start(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(strings.Join([]string{
		`model_provider = "test"`,
		``,
		`[model_providers.test]`,
		`base_url = "https://manual.example/v1"`,
		``,
	}, "\n")), 0600); err != nil {
		t.Fatal(err)
	}
	if err := patch.CloseLockForTest(); err != nil {
		t.Fatal(err)
	}

	starter := &recordingStarter{}
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"app": {
					Port:     6767,
					Upstream: "openai",
					Modules: map[string]config.ModuleConfig{
						"config_patch": {Enabled: true, Params: map[string]any{"config_path": configPath, "state_dir": filepath.Join(dir, "state")}},
					},
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: starter,
	})

	if err := m.StartConfiguredWorkers(); err == nil {
		t.Fatal("expected unresolved startup recovery to fail before spawn")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `base_url = "https://manual.example/v1"`) {
		t.Fatalf("expected manual edit preserved, got:\n%s", data)
	}
	matches, err := filepath.Glob(filepath.Join(dir, "state", "config-patch-journal.json.unresolved.*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected unresolved journal after startup recovery, got %#v", matches)
	}
	if len(starter.spawns) != 0 {
		t.Fatalf("expected worker spawn blocked on unresolved recovery, got %d spawns", len(starter.spawns))
	}
}

type recordingStarter struct {
	spawns     []WorkerSpawn
	processes  []*recordingProcess
	forcedStop bool
}

func (s *recordingStarter) Start(spawn WorkerSpawn) (ManagedProcess, error) {
	s.spawns = append(s.spawns, spawn)
	process := &recordingProcess{forcedStop: s.forcedStop}
	s.processes = append(s.processes, process)
	return process, nil
}

type recordingProcess struct {
	stops      int
	forcedStop bool
}

func (p *recordingProcess) Stop() error {
	p.stops++
	return nil
}

func (p *recordingProcess) ForcedStop() bool {
	return p.forcedStop
}

func TestManagerStartConfiguredWorkersReportsFailure(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"bad": {Port: 6767, Upstream: "missing"},
			},
		},
		Starter: fakeStarter{},
	})
	if err := m.StartConfiguredWorkers(); err == nil {
		t.Fatal("expected missing provider failure")
	}
}

func TestManagerConfigAndProviderPersistenceAPI(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	client := &recordingWorkerClient{}
	m := New(Config{
		ConfigPath: configPath,
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1", APIKey: "sk-file"},
			},
		},
		WorkerClient: client,
	})
	defer m.Close()
	m.statuses["app"] = "running"

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/upstreams/openai", strings.NewReader(`{"base_url":"https://relay.example/v1","api_key":"sk-file","api_format":"chat_completions"}`)))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected provider update status %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "relay.example") || strings.Contains(res.Body.String(), "sk-") {
		t.Fatalf("bad provider update response: %s", res.Body.String())
	}

	res = httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPut, "http://manager.local/api/config", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected config save status %d: %s", res.Code, res.Body.String())
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "https://relay.example/v1") || !strings.Contains(string(data), "api_key: sk-file") || strings.Contains(string(data), "api_key_ref") {
		t.Fatalf("bad persisted config:\n%s", data)
	}
	if client.appliedRuntimes[6767].Upstream.ID != "openai" || client.appliedRuntimes[6767].Upstream.BaseURL != "https://relay.example/v1" || client.appliedRuntimes[6767].Upstream.APIFormat != "chat_completions" {
		t.Fatalf("live runtime apply was not called: %#v", client.appliedRuntimes)
	}
}

func TestManagerUpstreamUpdatePersistsDesiredStateAndMarksFailedApplyOutOfSync(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	client := &recordingWorkerClient{applyErrByPort: map[int]error{11199: errors.New("worker rejected runtime")}}
	m := New(Config{
		ConfigPath: configPath,
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
				"cli": {Port: 11199, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1", APIKey: "sk-file"},
			},
		},
		WorkerClient: client,
	})
	defer m.Close()
	m.statuses["app"] = "running"
	m.statuses["cli"] = "running"

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/upstreams/openai", strings.NewReader(`{"base_url":"https://relay.example/v1","api_key":"sk-file","api_format":"chat_completions"}`)))
	if res.Code != http.StatusOK {
		t.Fatalf("expected desired update success, got %d: %s", res.Code, res.Body.String())
	}

	cfg := m.store.Config()
	if cfg.Upstreams["openai"].BaseURL != "https://relay.example/v1" {
		t.Fatalf("upstream update was not persisted: %#v", cfg.Upstreams["openai"])
	}
	if client.appliedRuntimes[6767].Upstream.BaseURL != "https://relay.example/v1" {
		t.Fatalf("healthy worker did not receive runtime: %#v", client.appliedRuntimes)
	}
	if client.appliedRuntimes[11199].Upstream.BaseURL != "https://relay.example/v1" {
		t.Fatalf("failing worker was not attempted: %#v", client.appliedRuntimes)
	}
	if got := m.workerStatus("app"); got != WorkerStateRunning {
		t.Fatalf("healthy worker status changed: %s", got)
	}
	if got := m.workerStatus("cli"); got != WorkerStateOutOfSync {
		t.Fatalf("failing worker status = %s, want %s", got, WorkerStateOutOfSync)
	}
}

func TestManagerConfigUpdatesPersistAsynchronously(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	m := New(Config{
		ConfigPath: configPath,
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1", APIKey: "sk-file"},
			},
		},
	})
	defer m.Close()

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/upstreams/openai", strings.NewReader(`{"base_url":"https://async.example/v1","api_key":"sk-file","api_format":"chat_completions"}`)))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected provider update status %d: %s", res.Code, res.Body.String())
	}

	eventually(t, time.Second, func() bool {
		loaded, err := config.LoadFile(configPath)
		return err == nil && loaded.Upstreams["openai"].BaseURL == "https://async.example/v1"
	})

	eventually(t, time.Second, func() bool {
		res = httptest.NewRecorder()
		m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/config", nil))
		return strings.Contains(res.Body.String(), `"dirty":false`) && strings.Contains(res.Body.String(), `"generation":1`)
	})
}

func TestManagerHealthMonitorMarksFailedAfterRetryLimit(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: fakeStarter{},
	})
	if err := m.StartWorker("app"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		m.RecordHealth("app", false)
	}
	assertWorkerStatus(t, m, "failed")
}

func TestManagerHealthFailureRestartsWorker(t *testing.T) {
	starter := &recordingStarter{}
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: starter,
	})
	if err := m.StartWorker("app"); err != nil {
		t.Fatal(err)
	}
	if len(starter.processes) != 1 {
		t.Fatalf("expected initial process, got %d", len(starter.processes))
	}

	m.RecordHealth("app", false)

	if len(starter.spawns) != 2 {
		t.Fatalf("expected unhealthy worker to be respawned, got %d spawns", len(starter.spawns))
	}
	if starter.processes[0].stops != 1 {
		t.Fatalf("expected old process to be stopped before respawn, got %d stops", starter.processes[0].stops)
	}
	assertWorkerStatus(t, m, "running")
}

func TestManagerHealthFailureStopsRestartingAfterRetryLimit(t *testing.T) {
	starter := &recordingStarter{}
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: starter,
	})
	if err := m.StartWorker("app"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		m.RecordHealth("app", false)
	}
	spawnsAfterFailure := len(starter.spawns)
	m.RecordHealth("app", false)

	assertWorkerStatus(t, m, "failed")
	if len(starter.spawns) != spawnsAfterFailure {
		t.Fatalf("expected failed worker not to respawn again, before=%d after=%d", spawnsAfterFailure, len(starter.spawns))
	}
}

func TestManagerManualRestartResetsHealthRetryCounter(t *testing.T) {
	starter := &recordingStarter{}
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: starter,
	})
	if err := m.StartWorker("app"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 9; i++ {
		m.RecordHealth("app", false)
	}

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/workers/6767/restart", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected restart status %d: %s", res.Code, res.Body.String())
	}
	m.RecordHealth("app", false)

	assertWorkerStatus(t, m, "running")
	if len(starter.spawns) < 11 {
		t.Fatalf("expected failed health after manual restart to retry instead of fail, got %d spawns", len(starter.spawns))
	}
}

func TestManagerHealthMonitorDoesNotResetRetryCounterBeforeHealthyWindow(t *testing.T) {
	starter := &recordingStarter{}
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: starter,
	})
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	m.clock = func() time.Time { return now }
	if err := m.StartWorker("app"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 9; i++ {
		m.RecordHealth("app", false)
	}
	m.RecordHealth("app", true)
	now = now.Add(59 * time.Second)
	m.RecordHealth("app", false)

	assertWorkerStatus(t, m, "failed")
	if len(starter.spawns) != 10 {
		t.Fatalf("expected brief success not to allow another respawn, got %d spawns", len(starter.spawns))
	}
}

func TestManagerHealthMonitorResetsRetryCounterAfterHealthyWindow(t *testing.T) {
	starter := &recordingStarter{}
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: starter,
	})
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	m.clock = func() time.Time { return now }
	if err := m.StartWorker("app"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 9; i++ {
		m.RecordHealth("app", false)
	}
	m.RecordHealth("app", true)
	now = now.Add(60 * time.Second)
	m.RecordHealth("app", true)
	m.RecordHealth("app", false)

	assertWorkerStatus(t, m, "running")
	if len(starter.spawns) < 11 {
		t.Fatalf("expected retry after healthy window reset, got %d spawns", len(starter.spawns))
	}
}

func TestManagerHealthMonitorLoopRecordsCheckerResults(t *testing.T) {
	checker := &sequenceHealthChecker{results: []bool{false, false, true}}
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter:       fakeStarter{},
		HealthChecker: checker,
	})
	if err := m.StartWorker("app"); err != nil {
		t.Fatal(err)
	}
	stop := m.StartHealthMonitor(5 * time.Millisecond)
	defer stop()
	time.Sleep(30 * time.Millisecond)
	assertWorkerStatus(t, m, "running")
	if checker.Calls() == 0 {
		t.Fatal("health checker was not called")
	}
}

func TestManagerWorkerLogsAreRedactedAndExposed(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
	})
	if _, err := m.LogSink("app").Write([]byte("Authorization: Bearer sk-secret\n")); err != nil {
		t.Fatal(err)
	}

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers/6767/logs", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected logs status %d: %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "sk-secret") || !strings.Contains(res.Body.String(), "***REDACTED***") {
		t.Fatalf("logs response was not redacted: %s", res.Body.String())
	}
}

func TestManagerLogSinkHonorsWorkerLogLevel(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai", LogLevel: "simple"},
				"cli": {Port: 11199, Upstream: "openai", LogLevel: "detail"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
	})

	if _, err := m.LogSink("app").Write([]byte("INFO request started\nWARN upstream retrying\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.LogSink("cli").Write([]byte("INFO request started\nWARN upstream retrying\n")); err != nil {
		t.Fatal(err)
	}

	appLines := m.LogSink("app").Lines()
	if len(appLines) != 1 || !strings.Contains(appLines[0], "WARN upstream retrying") {
		t.Fatalf("simple worker should keep warn only, got %#v", appLines)
	}
	if strings.Contains(strings.Join(appLines, "\n"), "INFO request started") {
		t.Fatalf("simple worker kept info line: %#v", appLines)
	}

	cliLines := m.LogSink("cli").Lines()
	if len(cliLines) != 2 {
		t.Fatalf("detail worker should keep both lines, got %#v", cliLines)
	}
	if !strings.Contains(strings.Join(cliLines, "\n"), "INFO request started") {
		t.Fatalf("detail worker dropped info line: %#v", cliLines)
	}
}

type sequenceHealthChecker struct {
	mu      sync.Mutex
	results []bool
	calls   int
}

func (c *sequenceHealthChecker) Check(port int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	result := true
	if c.calls < len(c.results) {
		result = c.results[c.calls]
	}
	c.calls++
	return result
}

func (c *sequenceHealthChecker) Calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

type recordingHealthChecker struct {
	results map[int]bool
	calls   map[int]int
}

func (c *recordingHealthChecker) Check(port int) bool {
	if c.calls == nil {
		c.calls = map[int]int{}
	}
	c.calls[port]++
	return c.results[port]
}

type fakeStarter struct{}

func (fakeStarter) Start(spawn WorkerSpawn) (ManagedProcess, error) {
	return fakeProcess{}, nil
}

type fakeProcess struct{}

func (fakeProcess) Stop() error { return nil }

type recordingWorkerClient struct {
	toggledPort      int
	toggledModule    string
	patchedPort      int
	patchedModule    string
	patchedConfig    config.ModuleConfig
	switchedPort     int
	switchedProvider upstream.RuntimeUpstream
	switchErr        error
	appliedPort      int
	appliedRuntime   appruntime.WorkerRuntime
	appliedRuntimes  map[int]appruntime.WorkerRuntime
	applyErr         error
	applyErrByPort   map[int]error
	statusBody       string
}

func (c *recordingWorkerClient) ToggleModule(port int, moduleName string) error {
	c.toggledPort = port
	c.toggledModule = moduleName
	return nil
}

func (c *recordingWorkerClient) PatchModule(port int, moduleName string, cfg config.ModuleConfig) error {
	c.patchedPort = port
	c.patchedModule = moduleName
	c.patchedConfig = cfg
	return nil
}

func (c *recordingWorkerClient) SwitchUpstream(port int, runtime upstream.RuntimeUpstream) error {
	c.switchedPort = port
	c.switchedProvider = runtime
	return c.switchErr
}

func (c *recordingWorkerClient) ApplyRuntime(port int, runtime appruntime.WorkerRuntime) (ApplyRuntimeStatus, error) {
	c.appliedPort = port
	c.appliedRuntime = runtime
	if c.appliedRuntimes == nil {
		c.appliedRuntimes = map[int]appruntime.WorkerRuntime{}
	}
	c.appliedRuntimes[port] = runtime
	if c.applyErrByPort != nil {
		if err := c.applyErrByPort[port]; err != nil {
			return ApplyRuntimeStatus{}, err
		}
	}
	if c.applyErr != nil {
		return ApplyRuntimeStatus{}, c.applyErr
	}
	return ApplyRuntimeStatus{AppliedGeneration: runtime.Generation}, nil
}

func (c *recordingWorkerClient) GetStatus(port int) (WorkerStatus, error) {
	if c.statusBody == "" {
		return WorkerStatus{}, nil
	}
	var status WorkerStatus
	if err := json.Unmarshal([]byte(c.statusBody), &status); err != nil {
		return WorkerStatus{}, err
	}
	return status, nil
}

func assertWorkerStatus(t *testing.T, m *Manager, want string) {
	t.Helper()
	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers", nil))
	if !strings.Contains(res.Body.String(), `"status":"`+want+`"`) {
		t.Fatalf("expected worker status %q, got: %s", want, res.Body.String())
	}
}

func eventually(t *testing.T, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}
