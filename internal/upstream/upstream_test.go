package upstream

import (
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
