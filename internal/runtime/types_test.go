package runtime

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestWorkerRuntimeJSONContract(t *testing.T) {
	input := WorkerRuntime{
		ID:         WorkerID("cli-openai"),
		Generation: Generation(3),
		ListenPort: 11199,
		Role:       WorkerRoleCLI,
		LogLevel:   LogLevelSimple,
		Upstream: UpstreamRuntime{
			ID:        UpstreamID("openai"),
			BaseURL:   "https://api.openai.com/v1",
			APIKey:    "sk-runtime",
			APIFormat: APIFormatChatCompletions,
		},
		Modules: map[string]ModuleConfig{
			"api_translate": {Enabled: true, Params: map[string]any{"api_format": "chat_completions"}},
		},
	}

	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}

	var got WorkerRuntime
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(got, input) {
		t.Fatalf("runtime contract changed: got %#v want %#v", got, input)
	}
}

func TestUpstreamRuntimePublicRedactsSecret(t *testing.T) {
	public := UpstreamRuntime{
		ID:      "openai",
		BaseURL: "https://api.openai.com/v1",
		APIKey:  "sk-secret",
	}.Public()

	data, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}

	if !public.HasAPIKey || strings.Contains(string(data), "sk-secret") {
		t.Fatalf("bad public upstream: %#v json=%s", public, data)
	}
}
