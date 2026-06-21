package provider

import (
	"testing"

	"github.com/jesse/codex-app-proxy/internal/config"
)

func TestResolveProviderUsesEnvApiKeyFirst(t *testing.T) {
	t.Setenv("JC_API_KEY", "sk-env")
	profile := config.ProviderProfile{
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

func TestResolveProviderFallsBackToConfigApiKey(t *testing.T) {
	profile := config.ProviderProfile{
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

func TestResolveProviderIgnoresLegacyApiKeyRef(t *testing.T) {
	t.Setenv("JC_API_KEY", "")
	profile := config.ProviderProfile{
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

