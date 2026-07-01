package module

import (
	"io"
	"reflect"
	"testing"
)

func TestBuildRequestMiddlewaresBuildsFixedOrderAndNormalizesAPITranslate(t *testing.T) {
	modules, configs, err := BuildRequestMiddlewares(map[string]ModuleConfig{
		"debug_sse":      {Enabled: true},
		"request_log":    {Enabled: true},
		"model_override": {Enabled: true, Params: map[string]any{"model": "gpt-test"}},
		"api_translate":  {Enabled: true},
		"image_filter":   {Enabled: true},
	}, BuildDependencies{
		APIFormat: "chat_completions",
		Stderr:    io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(requestMiddlewareNames(modules), []string{"image_filter", "debug_sse", "api_translate", "model_override", "request_log"}) {
		t.Fatalf("bad middleware order: %#v", requestMiddlewareNames(modules))
	}
	if !reflect.DeepEqual(configs["api_translate"], ModuleConfig{
		Enabled: true,
		Params: map[string]any{
			"api_format": "chat_completions",
		},
	}) {
		t.Fatalf("bad api_translate config: %#v", configs["api_translate"])
	}
}

func TestBuildRequestMiddlewaresRejectsUnknownName(t *testing.T) {
	_, _, err := BuildRequestMiddlewares(map[string]ModuleConfig{
		"unknown": {Enabled: true},
	}, BuildDependencies{Stderr: io.Discard})
	if err == nil {
		t.Fatal("expected unknown middleware error")
	}
}

func TestBuildRequestMiddlewaresBuildsExternalRequestMiddleware(t *testing.T) {
	modules, configs, err := BuildRequestMiddlewares(map[string]ModuleConfig{
		"api_translate":   {Enabled: true},
		"external_filter": {Enabled: true},
	}, BuildDependencies{
		APIFormat: "chat_completions",
		Stderr:    io.Discard,
		ExternalRequest: map[string]ExternalRequestRuntime{
			"external_filter": {Command: "/bin/cat", ProtocolVersion: "1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(requestMiddlewareNames(modules), []string{"image_filter", "debug_sse", "api_translate", "model_override", "request_log", "external_filter"}) {
		t.Fatalf("bad middleware order: %#v", requestMiddlewareNames(modules))
	}
	want := map[string]ModuleConfig{
		"image_filter":    {},
		"debug_sse":       {},
		"api_translate":   {Enabled: true, Params: map[string]any{"api_format": "chat_completions"}},
		"model_override":  {},
		"request_log":     {},
		"external_filter": {Enabled: true},
	}
	if !reflect.DeepEqual(configs, want) {
		t.Fatalf("bad configs:\ngot  %#v\nwant %#v", configs, want)
	}
}

func requestMiddlewareNames(modules []Middleware) []string {
	names := make([]string, len(modules))
	for i, middleware := range modules {
		names[i] = middleware.Name()
	}
	return names
}
