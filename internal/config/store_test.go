package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadAppliesDefaultsAndKeepsSecretRefs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	err := os.WriteFile(path, []byte(`
workers:
  codex-app:
    port: 6767
    upstream: openai
    modules:
      image_filter:
        enabled: true
upstreams:
  openai:
    base_url: https://api.openai.com/v1
    api_key: plain-key
`), 0600)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Defaults.LogDir == "" {
		t.Fatal("expected default log_dir")
	}
	if cfg.Upstreams["openai"].APIKey != "plain-key" {
		t.Fatalf("expected plain api key to load, got %#v", cfg.Upstreams["openai"])
	}
	if !cfg.Workers["codex-app"].Modules["image_filter"].Enabled {
		t.Fatal("expected module enabled")
	}
	if cfg.Workers["codex-app"].Role != "cli" {
		t.Fatalf("expected default cli role, got %q", cfg.Workers["codex-app"].Role)
	}
}

func TestLoadFileRemovesStaleConfigTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`
workers:
  app:
    port: 6767
    upstream: openai
upstreams:
  openai:
    base_url: https://api.openai.com/v1
`), 0600); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, ".config.yaml.tmp.stale")
	if err := os.WriteFile(stale, []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadFile(path); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("expected stale temp file removed, got %v", err)
	}
}

func TestAtomicSaveLeavesValidYAMLAndTracksGeneration(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "config.yaml"), Config{
		Workers: map[string]WorkerConfig{
			"one": {Port: 6767, Upstream: "openai"},
		},
		Upstreams: map[string]UpstreamProfile{
			"openai": {BaseURL: "https://api.openai.com/v1", APIKey: "plain-key"},
		},
	})

	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	if store.Status().Generation != 1 || store.Status().Dirty {
		t.Fatalf("unexpected status: %#v", store.Status())
	}

	loaded, err := LoadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Workers["one"].Port != 6767 {
		t.Fatalf("unexpected worker: %#v", loaded.Workers["one"])
	}
}

func TestAtomicWriteBeforeRenameKeepsPreviousConfigValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`
workers:
  app:
    port: 6767
    upstream: openai
upstreams:
  openai:
    base_url: https://old.example/v1
`), 0600); err != nil {
		t.Fatal(err)
	}

	restore := setAtomicWriteHooksForTest(nil, func(string, string) error {
		return errors.New("rename failed")
	}, nil)
	defer restore()

	err := atomicWriteFile(path, []byte(`
workers:
  app:
    port: 6767
    upstream: openai
upstreams:
  openai:
    base_url: https://new.example/v1
`), 0600)
	if err == nil {
		t.Fatal("expected atomic write to fail before rename")
	}

	loaded, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Upstreams["openai"].BaseURL != "https://old.example/v1" {
		t.Fatalf("expected previous config to remain valid, got %#v", loaded.Upstreams["openai"])
	}
}

func TestAtomicWriteAfterRenameStillLeavesLoadableConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`
workers:
  app:
    port: 6767
    upstream: openai
upstreams:
  openai:
    base_url: https://old.example/v1
`), 0600); err != nil {
		t.Fatal(err)
	}

	restore := setAtomicWriteHooksForTest(nil, nil, func(string) error {
		return errors.New("fsync dir failed")
	})
	defer restore()

	err := atomicWriteFile(path, []byte(`
workers:
  app:
    port: 6767
    upstream: openai
upstreams:
  openai:
    base_url: https://new.example/v1
`), 0600)
	if err == nil {
		t.Fatal("expected atomic write to fail after rename")
	}

	loaded, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Upstreams["openai"].BaseURL != "https://new.example/v1" {
		t.Fatalf("expected complete renamed config after post-rename failure, got %#v", loaded.Upstreams["openai"])
	}
}

func TestStoreAsyncSaveKeepsDirtyOnFailureAndRetriesLatest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	store := NewStore(path, Config{
		Workers: map[string]WorkerConfig{
			"one": {Port: 6767, Upstream: "openai"},
		},
		Upstreams: map[string]UpstreamProfile{
			"openai": {BaseURL: "https://api.openai.com/v1"},
		},
	})
	store.SetWriterForTest(func(string, []byte, os.FileMode) error {
		return errors.New("permission denied")
	})
	store.Update(func(cfg *Config) {
		cfg.Upstreams["openai"] = UpstreamProfile{BaseURL: "https://failed.example/v1"}
	})
	if err := store.Save(); err == nil {
		t.Fatal("expected save failure")
	}
	if !store.Status().Dirty || store.Status().LastSaveError == "" {
		t.Fatalf("expected dirty status after failed save: %#v", store.Status())
	}

	store.SetWriterForTest(nil)
	store.Update(func(cfg *Config) {
		cfg.Upstreams["openai"] = UpstreamProfile{BaseURL: "https://latest.example/v1"}
	})
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	if store.Status().Dirty || store.Status().Generation != 1 {
		t.Fatalf("expected clean generation 1, got %#v", store.Status())
	}
	loaded, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Upstreams["openai"].BaseURL != "https://latest.example/v1" {
		t.Fatalf("did not persist latest config: %#v", loaded.Upstreams["openai"])
	}
}

func TestStoreAsyncWriterDoesNotBlockUpdatesAndPersistsLatest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	store := NewStore(path, Config{
		Workers: map[string]WorkerConfig{
			"one": {Port: 6767, Upstream: "openai"},
		},
		Upstreams: map[string]UpstreamProfile{
			"openai": {BaseURL: "https://api.openai.com/v1"},
		},
	})

	firstWriteStarted := make(chan struct{})
	releaseFirstWrite := make(chan struct{})
	defer close(releaseFirstWrite)
	writes := 0
	store.SetWriterForTest(func(path string, data []byte, mode os.FileMode) error {
		writes++
		if writes == 1 {
			close(firstWriteStarted)
			<-releaseFirstWrite
			return errors.New("permission denied")
		}
		return os.WriteFile(path, data, mode)
	})
	stop := store.StartAsyncWriter()
	defer stop()

	store.Update(func(cfg *Config) {
		cfg.Upstreams["openai"] = UpstreamProfile{BaseURL: "https://first.example/v1"}
	})
	select {
	case <-firstWriteStarted:
	case <-time.After(time.Second):
		t.Fatal("async writer did not start")
	}

	updateReturned := make(chan struct{})
	go func() {
		store.Update(func(cfg *Config) {
			cfg.Upstreams["openai"] = UpstreamProfile{BaseURL: "https://latest.example/v1"}
		})
		close(updateReturned)
	}()
	select {
	case <-updateReturned:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("store update blocked behind disk writer")
	}
	releaseFirstWrite <- struct{}{}

	eventually(t, time.Second, func() bool {
		loaded, err := LoadFile(path)
		return err == nil && loaded.Upstreams["openai"].BaseURL == "https://latest.example/v1" && !store.Status().Dirty
	})
}

func eventually(t *testing.T, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}
