package manager

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jesse/agent-inn/internal/config"
	appruntime "github.com/jesse/agent-inn/internal/runtime"
)

func TestRuntimeBuilderBuildsCompleteWorkerRuntime(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-env")
	builder := RuntimeBuilder{}
	cfg := config.Config{
		Plugins: map[string]config.PluginDefinition{
			"api_translate": {
				Kind:   "request_middleware",
				Source: "builtin",
			},
			"config_patch": {
				Kind:   "lifecycle_hook",
				Source: "builtin",
			},
		},
		Workers: map[string]config.WorkerConfig{
			"cli-openai": {
				Port:     11199,
				Role:     "cli",
				Upstream: "openai",
				LogLevel: "simple",
				RequestModules: map[string]config.ModuleConfig{
					"api_translate": {Enabled: true},
				},
				Hooks: map[string]config.ModuleConfig{
					"config_patch": {Enabled: true, Params: map[string]any{"config_path": "/tmp/codex-config.toml"}},
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
			"api_translate": {Enabled: true},
		},
		Hooks: map[string]appruntime.ModuleConfig{
			"config_patch": {Enabled: true, Params: map[string]any{"config_path": "/tmp/codex-config.toml"}},
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runtime mismatch:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestRuntimeBuilderRejectsUndefinedWorkerPlugin(t *testing.T) {
	cfg := runtimeBuilderConfig()
	worker := cfg.Workers["cli-openai"]
	worker.RequestModules["missing"] = config.ModuleConfig{Enabled: true}
	cfg.Workers["cli-openai"] = worker

	_, err := (RuntimeBuilder{}).Build(cfg, "cli-openai", 1)
	if err == nil || err.Error() != `worker "cli-openai" references undefined plugin "missing"` {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRuntimeBuilderRejectsPluginKindBoundToWrongWorkerSlot(t *testing.T) {
	cfg := runtimeBuilderConfig()
	worker := cfg.Workers["cli-openai"]
	worker.Hooks["api_translate"] = config.ModuleConfig{Enabled: true}
	cfg.Workers["cli-openai"] = worker

	_, err := (RuntimeBuilder{}).Build(cfg, "cli-openai", 1)
	if err == nil || err.Error() != `worker "cli-openai" binds plugin "api_translate" as lifecycle_hook but plugin kind is request_middleware` {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRuntimeBuilderRejectsExternalPluginWithoutPath(t *testing.T) {
	cfg := runtimeBuilderConfig()
	cfg.Plugins["external_filter"] = config.PluginDefinition{
		Kind:   "request_middleware",
		Source: "external",
	}
	worker := cfg.Workers["cli-openai"]
	worker.RequestModules["external_filter"] = config.ModuleConfig{Enabled: true}
	cfg.Workers["cli-openai"] = worker

	_, err := (RuntimeBuilder{}).Build(cfg, "cli-openai", 1)
	if err == nil || err.Error() != `external plugin "external_filter" requires path` {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRuntimeBuilderBuildsExternalRequestMiddlewareFromManifest(t *testing.T) {
	cfg := runtimeBuilderConfig()
	manifestPath := writeExternalPluginManifest(t, "external_filter", config.PluginKindRequestMiddleware)
	cfg.Plugins["external_filter"] = config.PluginDefinition{
		Kind:   config.PluginKindRequestMiddleware,
		Source: config.PluginSourceExternal,
		Path:   manifestPath,
	}
	worker := cfg.Workers["cli-openai"]
	worker.RequestModules["external_filter"] = config.ModuleConfig{Enabled: true, Params: map[string]any{"mode": "strict"}}
	cfg.Workers["cli-openai"] = worker

	got, err := (RuntimeBuilder{}).Build(cfg, "cli-openai", 3)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]appruntime.ModuleConfig{
		"api_translate":   {Enabled: true},
		"external_filter": {Enabled: true, Params: map[string]any{"mode": "strict"}},
	}
	if !reflect.DeepEqual(got.Modules, want) {
		t.Fatalf("external runtime modules mismatch:\ngot  %#v\nwant %#v", got.Modules, want)
	}
	if got.Plugins["external_filter"].Command == "" || got.Plugins["external_filter"].ProtocolVersion != "1" {
		t.Fatalf("external plugin runtime metadata missing: %#v", got.Plugins)
	}
}

func TestRuntimeBuilderBuildsExternalRequestMiddlewareManifestArgs(t *testing.T) {
	cfg := runtimeBuilderConfig()
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "plugin.py")
	if err := os.WriteFile(scriptPath, []byte("print('ok')\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, "plugin.yaml")
	if err := os.WriteFile(manifestPath, []byte("name: external_filter\nkind: request_middleware\nversion: 0.1.0\nprotocol_version: \"1\"\ncommand: python3\nargs:\n  - "+scriptPath+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.Plugins["external_filter"] = config.PluginDefinition{
		Kind:   config.PluginKindRequestMiddleware,
		Source: config.PluginSourceExternal,
		Path:   manifestPath,
	}
	worker := cfg.Workers["cli-openai"]
	worker.RequestModules["external_filter"] = config.ModuleConfig{Enabled: true}
	cfg.Workers["cli-openai"] = worker

	got, err := (RuntimeBuilder{}).Build(cfg, "cli-openai", 3)
	if err != nil {
		t.Fatal(err)
	}
	want := appruntime.PluginRuntime{
		Kind:            config.PluginKindRequestMiddleware,
		Source:          config.PluginSourceExternal,
		Path:            manifestPath,
		Command:         "python3",
		Args:            []string{scriptPath},
		ProtocolVersion: "1",
	}
	if !reflect.DeepEqual(got.Plugins["external_filter"], want) {
		t.Fatalf("bad external plugin runtime:\ngot  %#v\nwant %#v", got.Plugins["external_filter"], want)
	}
}

func TestRuntimeBuilderRejectsExternalPluginMissingManifest(t *testing.T) {
	cfg := runtimeBuilderConfig()
	cfg.Plugins["external_filter"] = config.PluginDefinition{
		Kind:   config.PluginKindRequestMiddleware,
		Source: config.PluginSourceExternal,
		Path:   filepath.Join(t.TempDir(), "plugin.yaml"),
	}
	worker := cfg.Workers["cli-openai"]
	worker.RequestModules["external_filter"] = config.ModuleConfig{Enabled: true}
	cfg.Workers["cli-openai"] = worker

	_, err := (RuntimeBuilder{}).Build(cfg, "cli-openai", 1)
	if err == nil || err.Error() != `external plugin "external_filter" manifest is not readable` {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRuntimeBuilderRejectsExternalPluginManifestNameMismatch(t *testing.T) {
	cfg := runtimeBuilderConfig()
	manifestPath := writeExternalPluginManifest(t, "other_filter", config.PluginKindRequestMiddleware)
	cfg.Plugins["external_filter"] = config.PluginDefinition{
		Kind:   config.PluginKindRequestMiddleware,
		Source: config.PluginSourceExternal,
		Path:   manifestPath,
	}
	worker := cfg.Workers["cli-openai"]
	worker.RequestModules["external_filter"] = config.ModuleConfig{Enabled: true}
	cfg.Workers["cli-openai"] = worker

	_, err := (RuntimeBuilder{}).Build(cfg, "cli-openai", 1)
	if err == nil || err.Error() != `external plugin "external_filter" manifest name is "other_filter"` {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRuntimeBuilderRejectsExternalPluginManifestKindMismatch(t *testing.T) {
	cfg := runtimeBuilderConfig()
	manifestPath := writeExternalPluginManifest(t, "external_filter", config.PluginKindLifecycleHook)
	cfg.Plugins["external_filter"] = config.PluginDefinition{
		Kind:   config.PluginKindRequestMiddleware,
		Source: config.PluginSourceExternal,
		Path:   manifestPath,
	}
	worker := cfg.Workers["cli-openai"]
	worker.RequestModules["external_filter"] = config.ModuleConfig{Enabled: true}
	cfg.Workers["cli-openai"] = worker

	_, err := (RuntimeBuilder{}).Build(cfg, "cli-openai", 1)
	if err == nil || err.Error() != `external plugin "external_filter" manifest kind is lifecycle_hook` {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRuntimeBuilderRejectsExternalPluginManifestWithoutExecutable(t *testing.T) {
	cfg := runtimeBuilderConfig()
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "plugin.yaml")
	if err := os.WriteFile(manifestPath, []byte(`name: external_filter
kind: request_middleware
version: 0.1.0
protocol_version: "1"
`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.Plugins["external_filter"] = config.PluginDefinition{
		Kind:   config.PluginKindRequestMiddleware,
		Source: config.PluginSourceExternal,
		Path:   manifestPath,
	}
	worker := cfg.Workers["cli-openai"]
	worker.RequestModules["external_filter"] = config.ModuleConfig{Enabled: true}
	cfg.Workers["cli-openai"] = worker

	_, err := (RuntimeBuilder{}).Build(cfg, "cli-openai", 1)
	if err == nil || err.Error() != `external plugin "external_filter" manifest requires command or path` {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRuntimeBuilderRejectsExternalPluginManifestUnsupportedProtocolVersion(t *testing.T) {
	cfg := runtimeBuilderConfig()
	dir := t.TempDir()
	executablePath := filepath.Join(dir, "plugin")
	if err := os.WriteFile(executablePath, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, "plugin.yaml")
	if err := os.WriteFile(manifestPath, []byte(`name: external_filter
kind: request_middleware
version: 0.1.0
protocol_version: "2"
command: ./plugin
`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.Plugins["external_filter"] = config.PluginDefinition{
		Kind:   config.PluginKindRequestMiddleware,
		Source: config.PluginSourceExternal,
		Path:   manifestPath,
	}
	worker := cfg.Workers["cli-openai"]
	worker.RequestModules["external_filter"] = config.ModuleConfig{Enabled: true}
	cfg.Workers["cli-openai"] = worker

	_, err := (RuntimeBuilder{}).Build(cfg, "cli-openai", 1)
	if err == nil || err.Error() != `external plugin "external_filter" manifest has unsupported protocol_version "2"` {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRuntimeBuilderRejectsExternalPluginManifestUnavailableExecutable(t *testing.T) {
	cfg := runtimeBuilderConfig()
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "plugin.yaml")
	if err := os.WriteFile(manifestPath, []byte(`name: external_filter
kind: request_middleware
version: 0.1.0
protocol_version: "1"
command: ./missing-plugin
`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.Plugins["external_filter"] = config.PluginDefinition{
		Kind:   config.PluginKindRequestMiddleware,
		Source: config.PluginSourceExternal,
		Path:   manifestPath,
	}
	worker := cfg.Workers["cli-openai"]
	worker.RequestModules["external_filter"] = config.ModuleConfig{Enabled: true}
	cfg.Workers["cli-openai"] = worker

	_, err := (RuntimeBuilder{}).Build(cfg, "cli-openai", 1)
	if err == nil || err.Error() != `external plugin "external_filter" executable is not available` {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRuntimeBuilderBuildsExternalLifecycleHookFromManifest(t *testing.T) {
	cfg := runtimeBuilderConfig()
	manifestPath := writeExternalPluginManifest(t, "external_hook", config.PluginKindLifecycleHook)
	cfg.Plugins["external_hook"] = config.PluginDefinition{
		Kind:   config.PluginKindLifecycleHook,
		Source: config.PluginSourceExternal,
		Path:   manifestPath,
	}
	worker := cfg.Workers["cli-openai"]
	worker.Hooks["external_hook"] = config.ModuleConfig{Enabled: true}
	cfg.Workers["cli-openai"] = worker

	got, err := (RuntimeBuilder{}).Build(cfg, "cli-openai", 1)
	if err != nil {
		t.Fatal(err)
	}
	wantHooks := map[string]appruntime.ModuleConfig{
		"config_patch":  {Enabled: true},
		"external_hook": {Enabled: true},
	}
	if !reflect.DeepEqual(got.Hooks, wantHooks) {
		t.Fatalf("bad hooks:\ngot  %#v\nwant %#v", got.Hooks, wantHooks)
	}
	if got.Plugins["external_hook"].Kind != config.PluginKindLifecycleHook ||
		got.Plugins["external_hook"].Source != config.PluginSourceExternal ||
		got.Plugins["external_hook"].Path != manifestPath ||
		got.Plugins["external_hook"].Command == "" ||
		got.Plugins["external_hook"].ProtocolVersion != "1" {
		t.Fatalf("external hook runtime metadata missing: %#v", got.Plugins["external_hook"])
	}
}

func runtimeBuilderConfig() config.Config {
	return config.Config{
		Plugins: testPluginDefinitions(),
		Workers: map[string]config.WorkerConfig{
			"cli-openai": {
				Port:     11199,
				Role:     "cli",
				Upstream: "openai",
				LogLevel: "simple",
				RequestModules: map[string]config.ModuleConfig{
					"api_translate": {Enabled: true},
				},
				Hooks: map[string]config.ModuleConfig{
					"config_patch": {Enabled: true},
				},
			},
		},
		Upstreams: map[string]config.UpstreamProfile{
			"openai": {
				BaseURL: "https://api.openai.com/v1",
			},
		},
	}
}

func writeExternalPluginManifest(t *testing.T, name string, kind string) string {
	t.Helper()
	dir := t.TempDir()
	executablePath := filepath.Join(dir, "plugin")
	if err := os.WriteFile(executablePath, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, "plugin.yaml")
	data := []byte("name: " + name + "\nkind: " + kind + "\nversion: 0.1.0\nprotocol_version: \"1\"\ncommand: " + executablePath + "\n")
	if err := os.WriteFile(manifestPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	return manifestPath
}

func testPluginDefinitions() map[string]config.PluginDefinition {
	return map[string]config.PluginDefinition{
		"api_translate": {
			Kind:   config.PluginKindRequestMiddleware,
			Source: config.PluginSourceBuiltin,
		},
		"image_filter": {
			Kind:   config.PluginKindRequestMiddleware,
			Source: config.PluginSourceBuiltin,
		},
		"model_override": {
			Kind:   config.PluginKindRequestMiddleware,
			Source: config.PluginSourceBuiltin,
		},
		"request_log": {
			Kind:   config.PluginKindRequestMiddleware,
			Source: config.PluginSourceBuiltin,
		},
		"debug_sse": {
			Kind:   config.PluginKindRequestMiddleware,
			Source: config.PluginSourceBuiltin,
		},
		"config_patch": {
			Kind:   config.PluginKindLifecycleHook,
			Source: config.PluginSourceBuiltin,
		},
	}
}
