package manager

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jesse/codex-app-proxy/internal/config"
	"github.com/pelletier/go-toml/v2"
)

func TestWriteCodexProfileFileUsesResponsesWireAPIForWorkerProfiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := writeCodexProfileFile("cli-openai", config.UpstreamProfile{
		BaseURL:   "http://127.0.0.1:6767",
		APIFormat: "chat_completions",
	}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(home, ".codex", "cli-openai.config.toml"))
	if err != nil {
		t.Fatal(err)
	}

	var got codexProfileFile
	if err := toml.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}

	want := codexProfileFile{
		ModelProvider: "cli-openai",
		ModelProviders: map[string]codexProfileEntry{
			"cli-openai": {
				Name:    "cli-openai",
				BaseURL: "http://127.0.0.1:6767",
				WireAPI: "responses",
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected profile: got %#v want %#v", got, want)
	}
}
