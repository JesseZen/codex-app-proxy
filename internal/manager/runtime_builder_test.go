package manager

import (
	"reflect"
	"testing"

	"github.com/jesse/codex-app-proxy/internal/config"
	appruntime "github.com/jesse/codex-app-proxy/internal/runtime"
)

func TestRuntimeBuilderBuildsCompleteWorkerRuntime(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-env")
	builder := RuntimeBuilder{}
	cfg := config.Config{
		Workers: map[string]config.WorkerConfig{
			"cli-openai": {
				Port:     11199,
				Role:     "cli",
				Upstream: "openai",
				LogLevel: "simple",
				Modules: map[string]config.ModuleConfig{
					"api_translate": {Enabled: true},
				},
			},
		},
		Upstreams: map[string]config.UpstreamProfile{
			"openai": {
				BaseURL:   "https://api.openai.com/v1",
				APIKey:    "sk-file",
				APIFormat: "chat_completions",
			},
		},
	}

	got, err := builder.Build(cfg, "cli-openai", 7)
	if err != nil {
		t.Fatal(err)
	}

	want := appruntime.WorkerRuntime{
		ID:         "cli-openai",
		Generation: 7,
		ListenPort: 11199,
		Role:       "cli",
		LogLevel:   "simple",
		Upstream: appruntime.UpstreamRuntime{
			ID:        "openai",
			BaseURL:   "https://api.openai.com/v1",
			APIKey:    "sk-env",
			APIFormat: "chat_completions",
		},
		Modules: map[string]appruntime.ModuleConfig{
			"api_translate": {Enabled: true, Params: map[string]any{"api_format": "chat_completions"}},
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runtime mismatch:\ngot  %#v\nwant %#v", got, want)
	}
}
