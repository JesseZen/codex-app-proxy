package manager

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jesse/codex-app-proxy/internal/config"
	"github.com/jesse/codex-app-proxy/internal/constants"
	"github.com/pelletier/go-toml/v2"
)

type codexProfileFile struct {
	ModelProvider  string                       `toml:"model_provider"`
	ModelProviders map[string]codexProfileEntry `toml:"model_providers"`
}

type codexProfileEntry struct {
	Name    string `toml:"name"`
	BaseURL string `toml:"base_url"`
	WireAPI string `toml:"wire_api,omitempty"`
}

func writeCodexProfileFile(name string, profile config.UpstreamProfile) error {
	encoded, err := toml.Marshal(codexProfileFile{
		ModelProvider: name,
		ModelProviders: map[string]codexProfileEntry{
			name: {
				Name:    name,
				BaseURL: profile.BaseURL,
				WireAPI: "responses",
			},
		},
	})
	if err != nil {
		return err
	}
	return writeTextFile(codexProfilePath(name), string(encoded), 0600)
}

func codexProfilePath(name string) string {
	return expandHomePath(filepath.Join("~/.codex", name+".config.toml"))
}

func syncCodexProfileFiles(cfg config.Config) error {
	for name, worker := range cfg.Workers {
		profile := cfg.Upstreams[worker.Upstream]
		profile.BaseURL = fmt.Sprintf("http://%s:%d", constants.LocalhostAddr, worker.Port)
		if err := writeCodexProfileFile(name, profile); err != nil {
			return fmt.Errorf("write profile %s: %w", name, err)
		}
	}
	return nil
}

func writeTextFile(path string, text string, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	closed := false
	cleanup := func() {
		if !closed {
			_ = tmp.Close()
		}
		_ = os.Remove(tmpName)
	}
	if err := tmp.Chmod(mode); err != nil {
		cleanup()
		return err
	}
	if _, err := tmp.WriteString(text); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		closed = true
		cleanup()
		return err
	}
	closed = true
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return fsyncDir(dir)
}

func fsyncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
