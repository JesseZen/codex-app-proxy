package upstream

import (
	"net/url"
	"strings"

	appruntime "github.com/jesse/codex-app-proxy/internal/runtime"
)

type Compiled struct {
	ID                  appruntime.UpstreamID
	BaseURL             *url.URL
	AuthorizationHeader string
	APIFormat           appruntime.APIFormat
}

func Compile(runtime appruntime.UpstreamRuntime) (Compiled, error) {
	baseURL, err := url.Parse(runtime.BaseURL)
	if err != nil {
		return Compiled{}, err
	}
	compiled := Compiled{
		ID:        runtime.ID,
		BaseURL:   baseURL,
		APIFormat: runtime.APIFormat,
	}
	if runtime.APIKey != "" {
		compiled.AuthorizationHeader = "Bearer " + runtime.APIKey
	}
	return compiled, nil
}

func (c Compiled) Join(requestPath string, rawQuery string) (string, error) {
	next := *c.BaseURL
	basePath := strings.TrimRight(next.Path, "/")
	if requestPath == "" {
		requestPath = "/"
	}
	next.Path = basePath + requestPath
	next.RawQuery = rawQuery
	return next.String(), nil
}
