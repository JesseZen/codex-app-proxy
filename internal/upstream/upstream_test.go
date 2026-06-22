package upstream

import (
	"strings"
	"testing"

	"github.com/jesse/codex-app-proxy/internal/config"
)

func TestResolveUpstreamUsesEnvApiKeyFirst(t *testing.T) {
	t.Setenv("JC_API_KEY", "sk-env")
	profile := config.UpstreamProfile{
		BaseURL: "https://localhost:34891",
		APIKey:  "sk-file",
	}

	runtime, err := Resolve("jc", profile)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.APIKey != "sk-env" {
		t.Fatalf("expected env key, got %q", runtime.APIKey)
	}
}

func TestResolveUpstreamFallsBackToConfigApiKey(t *testing.T) {
	profile := config.UpstreamProfile{
		BaseURL: "https://localhost:34891",
		APIKey:  "sk-file",
	}

	runtime, err := Resolve("jc", profile)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.APIKey != "sk-file" {
		t.Fatalf("expected file key, got %q", runtime.APIKey)
	}
}

func TestResolveUpstreamIgnoresLegacyApiKeyRef(t *testing.T) {
	t.Setenv("JC_API_KEY", "")
	profile := config.UpstreamProfile{
		BaseURL: "https://localhost:34891",
		APIKey:  "sk-file",
	}

	runtime, err := Resolve("jc", profile)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.APIKey != "sk-file" {
		t.Fatalf("expected file key with no env override, got %q", runtime.APIKey)
	}
}

func TestResolveRuntimeRejectsMissingBaseURLForWorkerRuntime(t *testing.T) {
	_, err := ResolveRuntime("openai", config.UpstreamProfile{APIKey: "sk-file"})
	if err == nil || !strings.Contains(err.Error(), "base URL is required") {
		t.Fatalf("expected base URL error, got %v", err)
	}
}

func TestCompilePrecomputesAuthorizationHeader(t *testing.T) {
	runtime, err := ResolveRuntime("openai", config.UpstreamProfile{
		BaseURL:   "https://api.openai.com/v1",
		APIKey:    "sk-file",
		APIFormat: "chat_completions",
	})
	if err != nil {
		t.Fatal(err)
	}

	compiled, err := Compile(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.AuthorizationHeader != "Bearer sk-file" {
		t.Fatalf("bad auth header: %#v", compiled)
	}
	if compiled.APIFormat != "chat_completions" {
		t.Fatalf("bad api format: %#v", compiled)
	}

	got, err := compiled.Join("/responses", "stream=true")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://api.openai.com/v1/responses?stream=true" {
		t.Fatalf("bad joined URL: %s", got)
	}
}
