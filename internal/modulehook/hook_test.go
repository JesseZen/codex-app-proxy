package modulehook

import (
	"reflect"
	"testing"

	"github.com/jesse/agent-inn/internal/module"
)

func TestBuildIncludesEnabledConfigPatchHook(t *testing.T) {
	hooks, err := Build(map[string]module.ModuleConfig{
		"config_patch": {
			Enabled: true,
			Params: map[string]any{
				"config_path": "/tmp/codex-config.toml",
				"state_dir":   "/tmp/ainn",
			},
		},
	}, BuildDependencies{
		WorkerID:   "app",
		WorkerPort: 6767,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hooks) != 1 {
		t.Fatalf("expected one hook, got %d", len(hooks))
	}
	if hooks[0].Name() != "config_patch" {
		t.Fatalf("unexpected hook %q", hooks[0].Name())
	}
	if !reflect.DeepEqual(hooks[0].Config(), module.ModuleConfig{
		Enabled: true,
		Params: map[string]any{
			"config_path": "/tmp/codex-config.toml",
			"state_dir":   "/tmp/ainn",
		},
	}) {
		t.Fatalf("bad hook config: %#v", hooks[0].Config())
	}
}

func TestBuildIncludesExternalLifecycleHook(t *testing.T) {
	hooks, err := Build(map[string]module.ModuleConfig{
		"external_hook": {
			Enabled: true,
			Params:  map[string]any{"mode": "strict"},
		},
	}, BuildDependencies{
		WorkerID:   "app",
		WorkerPort: 6767,
		ExternalHooks: map[string]ExternalHookRuntime{
			"external_hook": {
				Command:         "/bin/cat",
				Args:            []string{"manifest-arg"},
				ProtocolVersion: "1",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hooks) != 1 {
		t.Fatalf("expected one hook, got %d", len(hooks))
	}
	if hooks[0].Name() != "external_hook" {
		t.Fatalf("unexpected hook %q", hooks[0].Name())
	}
	if !reflect.DeepEqual(hooks[0].Config(), module.ModuleConfig{
		Enabled: true,
		Params:  map[string]any{"mode": "strict"},
	}) {
		t.Fatalf("bad hook config: %#v", hooks[0].Config())
	}
}

func TestBuildRejectsUnknownLifecycleHook(t *testing.T) {
	_, err := Build(map[string]module.ModuleConfig{
		"unknown": {Enabled: true},
	}, BuildDependencies{WorkerID: "app", WorkerPort: 6767})
	if err == nil {
		t.Fatal("expected unknown lifecycle hook error")
	}
}
