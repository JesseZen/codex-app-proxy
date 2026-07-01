package modulehook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jesse/agent-inn/internal/module"
)

func TestConfigPatchNormalRestore(t *testing.T) {
	dir := t.TempDir()
	configPath := writeCodexConfig(t, dir, `base_url = "https://example.com/v1"`)
	patch := newTestConfigPatch(configPath, filepath.Join(dir, "state"), "worker-6767", 6767)

	if err := patch.Start(); err != nil {
		t.Fatal(err)
	}
	assertFileContains(t, configPath, `base_url = "http://127.0.0.1:6767"`)
	if _, err := os.Stat(filepath.Join(dir, "state", "config-patch-journal.json")); err != nil {
		t.Fatal(err)
	}

	if err := patch.Stop(); err != nil {
		t.Fatal(err)
	}
	assertFileContains(t, configPath, `base_url = "https://example.com/v1"`)
	if _, err := os.Stat(filepath.Join(dir, "state", "config-patch-journal.json")); !os.IsNotExist(err) {
		t.Fatalf("expected journal removed, got %v", err)
	}
}

func TestConfigPatchRecoversStaleJournal(t *testing.T) {
	dir := t.TempDir()
	configPath := writeCodexConfig(t, dir, `base_url = "https://example.com/v1"`)
	first := newTestConfigPatch(configPath, filepath.Join(dir, "state"), "worker-6767", 6767)
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	if err := first.CloseLockForTest(); err != nil {
		t.Fatal(err)
	}

	second := newTestConfigPatch(configPath, filepath.Join(dir, "state"), "worker-6767", 6767)
	if err := second.RecoverStaleJournal(); err != nil {
		t.Fatal(err)
	}
	assertFileContains(t, configPath, `base_url = "https://example.com/v1"`)
	if second.State() != ConfigPatchRecovered {
		t.Fatalf("expected recovered state, got %s", second.State())
	}
}

func TestConfigPatchManualEditConflictIsUnresolved(t *testing.T) {
	dir := t.TempDir()
	configPath := writeCodexConfig(t, dir, `base_url = "https://example.com/v1"`)
	first := newTestConfigPatch(configPath, filepath.Join(dir, "state"), "worker-6767", 6767)
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(codexConfigText(`base_url = "https://manual.example/v1"`)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := first.CloseLockForTest(); err != nil {
		t.Fatal(err)
	}

	second := newTestConfigPatch(configPath, filepath.Join(dir, "state"), "worker-6767", 6767)
	if err := second.RecoverStaleJournal(); err != nil {
		t.Fatal(err)
	}
	assertFileContains(t, configPath, `base_url = "https://manual.example/v1"`)
	if second.State() != ConfigPatchUnresolved {
		t.Fatalf("expected unresolved state, got %s", second.State())
	}
	matches, err := filepath.Glob(filepath.Join(dir, "state", "config-patch-journal.json.unresolved.*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected unresolved journal, got %#v", matches)
	}
}

func TestConfigPatchStartStopsAfterUnresolvedRecovery(t *testing.T) {
	dir := t.TempDir()
	configPath := writeCodexConfig(t, dir, `base_url = "https://example.com/v1"`)
	first := newTestConfigPatch(configPath, filepath.Join(dir, "state"), "worker-6767", 6767)
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(codexConfigText(`base_url = "https://manual.example/v1"`)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := first.CloseLockForTest(); err != nil {
		t.Fatal(err)
	}

	second := newTestConfigPatch(configPath, filepath.Join(dir, "state"), "worker-6767", 6767)
	if err := second.Start(); err == nil {
		t.Fatal("expected Start to stop on unresolved recovery")
	}
	if second.State() != ConfigPatchUnresolved {
		t.Fatalf("expected unresolved state, got %s", second.State())
	}
	assertFileContains(t, configPath, `base_url = "https://manual.example/v1"`)
	assertFileNotContains(t, configPath, `base_url = "http://127.0.0.1:6767"`)
}

func TestConfigPatchLockPreventsSecondActivePatch(t *testing.T) {
	dir := t.TempDir()
	configPath := writeCodexConfig(t, dir, `base_url = "https://example.com/v1"`)
	first := newTestConfigPatch(configPath, filepath.Join(dir, "state"), "worker-6767", 6767)
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	defer first.Stop()

	second := newTestConfigPatch(configPath, filepath.Join(dir, "state"), "worker-11199", 11199)
	if err := second.Start(); err == nil {
		t.Fatal("expected second patch to fail while lock is held")
	}
}

func writeCodexConfig(t *testing.T, dir string, baseURLLine string) string {
	t.Helper()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(codexConfigText(baseURLLine)), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func newTestConfigPatch(configPath string, stateDir string, workerID string, workerPort int) *ConfigPatch {
	return NewConfigPatch(module.ModuleConfig{
		Enabled: true,
		Params: map[string]any{
			"config_path": configPath,
			"state_dir":   stateDir,
		},
	}, BuildDependencies{
		WorkerID:   workerID,
		WorkerPort: workerPort,
	})
}

func codexConfigText(baseURLLine string) string {
	return strings.Join([]string{
		`model_provider = "test"`,
		``,
		`[model_providers.test]`,
		baseURLLine,
		`experimental_bearer_token = "orig-token"`,
		``,
	}, "\n")
}

func assertFileContains(t *testing.T, path string, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), want) {
		t.Fatalf("expected %s to contain %q, got:\n%s", path, want, data)
	}
}

func assertFileNotContains(t *testing.T, path string, unwanted string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), unwanted) {
		t.Fatalf("expected %s to not contain %q, got:\n%s", path, unwanted, data)
	}
}
