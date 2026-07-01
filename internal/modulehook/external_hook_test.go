package modulehook

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jesse/agent-inn/internal/module"
)

func TestExternalHookRunsStartStatusStopActions(t *testing.T) {
	dir := t.TempDir()
	actionsPath := filepath.Join(dir, "actions")
	scriptPath := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(scriptPath, []byte(`#!/bin/sh
printf '%s %s\n' "$1" "$2" >> "`+actionsPath+`"
case "$2" in
  start) printf '%s' '{"state":"active","detail":{"action":"start"}}' ;;
  status) printf '%s' '{"state":"active","detail":{"action":"status"}}' ;;
  stop) printf '%s' '{"state":"clean","detail":{"action":"stop"}}' ;;
esac
`), 0700); err != nil {
		t.Fatal(err)
	}
	hook := NewExternalHook("external_hook", module.ModuleConfig{
		Enabled: true,
		Params:  map[string]any{"mode": "strict"},
	}, ExternalHookRuntime{
		Command:         scriptPath,
		Args:            []string{"manifest-arg"},
		ProtocolVersion: "1",
	}, BuildDependencies{WorkerID: "app", WorkerPort: 6767})

	if err := hook.Start(); err != nil {
		t.Fatal(err)
	}
	if err := hook.RefreshStatus(); err != nil {
		t.Fatal(err)
	}
	if err := hook.Stop(); err != nil {
		t.Fatal(err)
	}

	actions, err := os.ReadFile(actionsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(actions)) != "manifest-arg start\nmanifest-arg status\nmanifest-arg stop" {
		t.Fatalf("bad actions:\n%s", string(actions))
	}
	want := Status{State: "clean", Detail: map[string]string{"action": "stop"}}
	if !reflect.DeepEqual(hook.Status(), want) {
		t.Fatalf("bad status: got %#v want %#v", hook.Status(), want)
	}
}

func TestExternalHookReturnsActionFailure(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 42\n"), 0700); err != nil {
		t.Fatal(err)
	}
	hook := NewExternalHook("external_hook", module.ModuleConfig{Enabled: true}, ExternalHookRuntime{
		Command:         scriptPath,
		ProtocolVersion: "1",
	}, BuildDependencies{WorkerID: "app", WorkerPort: 6767})

	err := hook.Start()
	if err == nil || !strings.Contains(err.Error(), `external lifecycle hook "external_hook" start failed`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExternalHookRejectsInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf 'not json'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	hook := NewExternalHook("external_hook", module.ModuleConfig{Enabled: true}, ExternalHookRuntime{
		Command:         scriptPath,
		ProtocolVersion: "1",
	}, BuildDependencies{WorkerID: "app", WorkerPort: 6767})

	err := hook.RefreshStatus()
	if err == nil || err.Error() != `external lifecycle hook "external_hook" status returned invalid JSON` {
		t.Fatalf("unexpected error: %v", err)
	}
}
