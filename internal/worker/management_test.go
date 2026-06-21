package worker

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jesse/codex-app-proxy/internal/module"
	"github.com/jesse/codex-app-proxy/internal/upstream"
)

func TestWorkerManagementStatusRedactsSecretsAndIncludesGeneration(t *testing.T) {
	w := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 7,
			Upstream: upstream.RuntimeUpstream{
				Name:      "openai",
				BaseURL:   "https://api.openai.com/v1",
				APIKey:    "sk-secret",
				APIFormat: "responses",
			},
			Modules: []module.Middleware{module.NewImageFilter(module.ModuleConfig{Enabled: true})},
		},
	})

	res := httptest.NewRecorder()
	w.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://proxy.local/_proxy/status", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "sk-secret") {
		t.Fatalf("status leaked secret: %s", res.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["snapshot_generation"].(float64) != 7 {
		t.Fatalf("missing generation: %#v", body)
	}
	upstreamBody := body["upstream"].(map[string]any)
	if upstreamBody["has_api_key"] != true || upstreamBody["api_key"] != nil {
		t.Fatalf("bad upstream redaction: %#v", upstreamBody)
	}
}

func TestWorkerManagementStatusIncludesConfigPatchState(t *testing.T) {
	w := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation:       1,
			Upstream:         upstream.RuntimeUpstream{Name: "openai", BaseURL: "https://api.openai.com/v1"},
			ConfigPatchState: module.ConfigPatchActive,
		},
	})

	res := httptest.NewRecorder()
	w.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://proxy.local/_proxy/status", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"config_patch_state":"active"`) {
		t.Fatalf("status missing config_patch_state: %s", res.Body.String())
	}
}

func TestWorkerManagementStatusIncludesConfigPatchRecoveryDetail(t *testing.T) {
	w := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation:       2,
			Upstream:         upstream.RuntimeUpstream{Name: "openai", BaseURL: "https://api.openai.com/v1"},
			ConfigPatchState: module.ConfigPatchUnresolved,
			ConfigPatchDetail: map[string]string{
				"provider_name":  "test",
				"field_name":     "base_url",
				"previous_value": "https://example.com/v1",
				"patched_value":  "http://127.0.0.1:6767",
				"current_value":  "https://manual.example/v1",
			},
		},
	})

	res := httptest.NewRecorder()
	w.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://proxy.local/_proxy/status", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	for _, want := range []string{
		`"config_patch_state":"unresolved"`,
		`"config_patch_detail"`,
		`"current_value":"https://manual.example/v1"`,
		`"patched_value":"http://127.0.0.1:6767"`,
	} {
		if !strings.Contains(res.Body.String(), want) {
			t.Fatalf("status missing %s: %s", want, res.Body.String())
		}
	}
}

func TestWorkerManagementSwitchValidatesBeforeSwap(t *testing.T) {
	w := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			Upstream:   upstream.RuntimeUpstream{Name: "old", BaseURL: "https://old.example/v1"},
		},
	})

	invalid := httptest.NewRecorder()
	w.ServeHTTP(invalid, httptest.NewRequest(http.MethodPost, "http://proxy.local/_proxy/switch", strings.NewReader(`{"upstream":{"name":"bad","base_url":""}}`)))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid switch to fail, got %d: %s", invalid.Code, invalid.Body.String())
	}

	status := httptest.NewRecorder()
	w.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "http://proxy.local/_proxy/status", nil))
	if !strings.Contains(status.Body.String(), `"snapshot_generation":1`) {
		t.Fatalf("invalid switch changed generation: %s", status.Body.String())
	}

	valid := httptest.NewRecorder()
	w.ServeHTTP(valid, httptest.NewRequest(http.MethodPost, "http://proxy.local/_proxy/switch", strings.NewReader(`{"upstream":{"name":"new","base_url":"https://new.example/v1","api_format":"chat_completions","api_key":"sk-new"}}`)))
	if valid.Code != http.StatusOK {
		t.Fatalf("expected valid switch, got %d: %s", valid.Code, valid.Body.String())
	}

	status = httptest.NewRecorder()
	w.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "http://proxy.local/_proxy/status", nil))
	if !strings.Contains(status.Body.String(), `"snapshot_generation":2`) || strings.Contains(status.Body.String(), "sk-new") {
		t.Fatalf("bad status after switch: %s", status.Body.String())
	}
}

func TestWorkerManagementModuleToggle(t *testing.T) {
	w := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			Upstream:   upstream.RuntimeUpstream{Name: "openai", BaseURL: "https://api.openai.com/v1"},
			Modules:    []module.Middleware{module.NewImageFilter(module.ModuleConfig{Enabled: false})},
		},
	})

	res := httptest.NewRecorder()
	w.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://proxy.local/_proxy/modules/image_filter/toggle", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected toggle status %d: %s", res.Code, res.Body.String())
	}

	status := httptest.NewRecorder()
	w.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "http://proxy.local/_proxy/modules/image_filter", nil))
	if !strings.Contains(status.Body.String(), `"enabled":true`) {
		t.Fatalf("module was not toggled: %s", status.Body.String())
	}
}

func TestWorkerManagementModulePatch(t *testing.T) {
	w := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			Upstream:   upstream.RuntimeUpstream{Name: "openai", BaseURL: "https://api.openai.com/v1"},
			Modules:    []module.Middleware{module.NewModelOverride(module.ModuleConfig{Enabled: false})},
		},
	})

	res := httptest.NewRecorder()
	w.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://proxy.local/_proxy/modules/model_override", strings.NewReader(`{"enabled":true,"params":{"model":"gpt-test"}}`)))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected patch status %d: %s", res.Code, res.Body.String())
	}

	status := httptest.NewRecorder()
	w.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "http://proxy.local/_proxy/modules/model_override", nil))
	if !strings.Contains(status.Body.String(), `"enabled":true`) || !strings.Contains(status.Body.String(), "gpt-test") {
		t.Fatalf("module was not patched: %s", status.Body.String())
	}
}
