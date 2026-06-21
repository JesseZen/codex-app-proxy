package upstream

import (
	"fmt"
	"os"
	"strings"

	"github.com/jesse/codex-app-proxy/internal/config"
	appruntime "github.com/jesse/codex-app-proxy/internal/runtime"
)

type RuntimeUpstream struct {
	Name      string `json:"name"`
	BaseURL   string `json:"base_url"`
	APIKey    string `json:"api_key,omitempty"`
	APIFormat string `json:"api_format,omitempty"`
}

type RedactedUpstream struct {
	Name      string `json:"name"`
	BaseURL   string `json:"base_url"`
	APIKey    string `json:"api_key,omitempty"`
	HasAPIKey bool   `json:"has_api_key"`
	APIFormat string `json:"api_format,omitempty"`
}

func Resolve(name string, profile config.UpstreamProfile) (RuntimeUpstream, error) {
	if apiKey := runtimeAPIKey(name, profile); apiKey != "" {
		return RuntimeUpstream{Name: name, BaseURL: profile.BaseURL, APIKey: apiKey, APIFormat: profile.APIFormat}, nil
	}
	return RuntimeUpstream{
		Name:      name,
		BaseURL:   profile.BaseURL,
		APIKey:    strings.TrimSpace(profile.APIKey),
		APIFormat: profile.APIFormat,
	}, nil
}

func ResolveRuntime(name string, profile config.UpstreamProfile) (appruntime.UpstreamRuntime, error) {
	name = strings.TrimSpace(name)
	if strings.TrimSpace(profile.BaseURL) == "" {
		return appruntime.UpstreamRuntime{ID: appruntime.UpstreamID(name)}, fmt.Errorf("upstream base URL is required")
	}
	if apiKey := runtimeAPIKey(name, profile); apiKey != "" {
		return appruntime.UpstreamRuntime{
			ID:        appruntime.UpstreamID(name),
			BaseURL:   strings.TrimSpace(profile.BaseURL),
			APIKey:    apiKey,
			APIFormat: appruntime.APIFormat(strings.TrimSpace(profile.APIFormat)),
		}, nil
	}
	return appruntime.UpstreamRuntime{
		ID:        appruntime.UpstreamID(name),
		BaseURL:   strings.TrimSpace(profile.BaseURL),
		APIKey:    strings.TrimSpace(profile.APIKey),
		APIFormat: appruntime.APIFormat(strings.TrimSpace(profile.APIFormat)),
	}, nil
}

func (p RuntimeUpstream) Redacted() RedactedUpstream {
	return RedactedUpstream{
		Name:      p.Name,
		BaseURL:   p.BaseURL,
		HasAPIKey: p.APIKey != "",
		APIFormat: p.APIFormat,
	}
}

func runtimeAPIKey(upstreamName string, profile config.UpstreamProfile) string {
	name := strings.ToUpper(strings.TrimSpace(upstreamName))
	if name == "" {
		return ""
	}
	return strings.TrimSpace(os.Getenv(name + "_API_KEY"))
}
