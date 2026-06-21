package provider

import (
	"os"
	"strings"

	"github.com/jesse/codex-app-proxy/internal/config"
)

type RuntimeProvider struct {
	Name      string `json:"name"`
	BaseURL   string `json:"base_url"`
	APIKey    string `json:"api_key,omitempty"`
	APIFormat string `json:"api_format,omitempty"`
}

type RedactedProvider struct {
	Name      string `json:"name"`
	BaseURL   string `json:"base_url"`
	APIKey    string `json:"api_key,omitempty"`
	HasAPIKey bool   `json:"has_api_key"`
	APIFormat string `json:"api_format,omitempty"`
}

func Resolve(name string, profile config.ProviderProfile) (RuntimeProvider, error) {
	if apiKey := runtimeAPIKey(name, profile); apiKey != "" {
		return RuntimeProvider{Name: name, BaseURL: profile.BaseURL, APIKey: apiKey, APIFormat: profile.APIFormat}, nil
	}
	return RuntimeProvider{
		Name:      name,
		BaseURL:   profile.BaseURL,
		APIKey:    strings.TrimSpace(profile.APIKey),
		APIFormat: profile.APIFormat,
	}, nil
}

func (p RuntimeProvider) Redacted() RedactedProvider {
	return RedactedProvider{
		Name:      p.Name,
		BaseURL:   p.BaseURL,
		HasAPIKey: p.APIKey != "",
		APIFormat: p.APIFormat,
	}
}

func runtimeAPIKey(providerName string, profile config.ProviderProfile) string {
	name := strings.ToUpper(strings.TrimSpace(providerName))
	if name == "" {
		return ""
	}
	return strings.TrimSpace(os.Getenv(name + "_API_KEY"))
}
