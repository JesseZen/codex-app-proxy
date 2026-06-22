package manager

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jesse/codex-app-proxy/internal/config"
	appruntime "github.com/jesse/codex-app-proxy/internal/runtime"
	"github.com/jesse/codex-app-proxy/internal/upstream"
)

func TestHTTPWorkerClientPatchesAndTogglesWorkerModules(t *testing.T) {
	var sawPatch bool
	var sawToggle bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/_proxy/modules/model_override":
			var cfg config.ModuleConfig
			if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
				t.Fatal(err)
			}
			if !cfg.Enabled || cfg.Params["model"] != "gpt-live" {
				t.Fatalf("bad patch payload: %#v", cfg)
			}
			sawPatch = true
		case r.Method == http.MethodPost && r.URL.Path == "/_proxy/modules/image_filter/toggle":
			sawToggle = true
		case r.Method == http.MethodPost && r.URL.Path == "/_proxy/switch":
			var payload struct {
				Upstream upstream.RuntimeUpstream `json:"upstream"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload.Upstream.Name != "openai" || payload.Upstream.APIKey != "sk-live" {
				t.Fatalf("bad provider payload: %#v", payload.Upstream)
			}
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	port := server.Listener.Addr().(*net.TCPAddr).Port
	client := HTTPWorkerClient{Client: server.Client()}
	if err := client.PatchModule(port, "model_override", config.ModuleConfig{Enabled: true, Params: map[string]any{"model": "gpt-live"}}); err != nil {
		t.Fatal(err)
	}
	if err := client.ToggleModule(port, "image_filter"); err != nil {
		t.Fatal(err)
	}
	if err := client.SwitchUpstream(port, upstream.RuntimeUpstream{Name: "openai", BaseURL: "https://api.openai.com/v1", APIKey: "sk-live"}); err != nil {
		t.Fatal(err)
	}
	if !sawPatch || !sawToggle {
		t.Fatalf("missing calls patch=%v toggle=%v", sawPatch, sawToggle)
	}
}

func TestHTTPWorkerClientAppliesRuntime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/_proxy/runtime" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var payload appruntime.WorkerRuntime
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.ID != "cli-openai" || payload.Generation != 4 {
			t.Fatalf("bad payload: %#v", payload)
		}
		writeJSON(w, http.StatusOK, map[string]any{"applied_generation": 4})
	}))
	defer server.Close()

	port := server.Listener.Addr().(*net.TCPAddr).Port
	client := HTTPWorkerClient{Client: server.Client()}
	status, err := client.ApplyRuntime(port, appruntime.WorkerRuntime{
		ID:         "cli-openai",
		Generation: 4,
		ListenPort: port,
		Upstream: appruntime.UpstreamRuntime{
			ID:      "openai",
			BaseURL: "https://api.openai.com/v1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if status.AppliedGeneration != 4 {
		t.Fatalf("bad applied generation: %#v", status)
	}
}
