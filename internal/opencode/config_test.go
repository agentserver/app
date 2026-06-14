package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateConfigCreatesModelserverProxyProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode", "opencode.jsonc")
	err := UpdateConfig(path, Settings{
		BaseURL: "http://127.0.0.1:53452/v1",
		APIKey:  "local-proxy-token",
		Model:   "gpt-5.5",
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	readJSONFile(t, path, &got)
	if got["model"] != "modelserver/gpt-5.5" {
		t.Fatalf("model = %v", got["model"])
	}
	provider := got["provider"].(map[string]any)["modelserver"].(map[string]any)
	if provider["npm"] != "@ai-sdk/openai" {
		t.Fatalf("npm = %v", provider["npm"])
	}
	models := provider["models"].(map[string]any)
	for _, model := range []string{"gpt-5.5", "glm-5.1", "deepseek-v4-pro"} {
		entry, ok := models[model].(map[string]any)
		if !ok {
			t.Fatalf("model %q missing from %#v", model, models)
		}
		if entry["name"] != model {
			t.Fatalf("model %q name = %v", model, entry["name"])
		}
	}
	options := provider["options"].(map[string]any)
	if options["baseURL"] != "http://127.0.0.1:53452/v1" {
		t.Fatalf("baseURL = %v", options["baseURL"])
	}
	if options["apiKey"] != "local-proxy-token" {
		t.Fatalf("apiKey = %v", options["apiKey"])
	}
}

func TestUpdateConfigPreservesUnrelatedSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.jsonc")
	existing := `{
	  "$schema": "https://opencode.ai/config.json",
	  "theme": "system",
	  "provider": {
	    "anthropic": {
	      "models": {
	        "claude": {"name": "Claude"}
	      }
	    },
	    "modelserver": {
	      "name": "old"
	    }
	  }
	}`
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpdateConfig(path, DefaultProxySettings()); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	readJSONFile(t, path, &got)
	if got["theme"] != "system" {
		t.Fatalf("theme was not preserved: %#v", got["theme"])
	}
	providers := got["provider"].(map[string]any)
	if _, ok := providers["anthropic"]; !ok {
		t.Fatalf("anthropic provider was removed: %#v", providers)
	}
	if providers["modelserver"].(map[string]any)["name"] != "modelserver" {
		t.Fatalf("modelserver provider was not overwritten: %#v", providers["modelserver"])
	}
}

func TestUpdateConfigParsesJSONC(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.jsonc")
	existing := `{
	  // user theme
	  "theme": "dark",
	  "provider": {
	    "anthropic": {
	      "models": {},
	    },
	  },
	}`
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpdateConfig(path, DefaultProxySettings()); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	readJSONFile(t, path, &got)
	if got["theme"] != "dark" {
		t.Fatalf("theme = %v", got["theme"])
	}
}

func TestUpdateConfigReportsInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.jsonc")
	if err := os.WriteFile(path, []byte(`{"provider":`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := UpdateConfig(path, DefaultProxySettings())
	if err == nil || !strings.Contains(err.Error(), "parse opencode config") {
		t.Fatalf("err = %v, want parse opencode config", err)
	}
}

func readJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("unmarshal %s: %v\n%s", path, err, string(b))
	}
}
