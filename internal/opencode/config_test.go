package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tailscale/hujson"
)

func TestUpdateConfigCreatesModelserverProxyProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode", "opencode.jsonc")
	err := UpdateConfig(path, Settings{
		BaseURL:   "http://127.0.0.1:53452/v1",
		APIKeyEnv: "AGENTSERVER_CODEX_LOCAL_API_KEY",
		Model:     "gpt-5.5",
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
	for _, model := range []string{"gpt-5.5"} {
		entry, ok := models[model].(map[string]any)
		if !ok {
			t.Fatalf("model %q missing from %#v", model, models)
		}
		if entry["name"] != model {
			t.Fatalf("model %q name = %v", model, entry["name"])
		}
	}
	for _, model := range []string{"glm-5.1", "deepseek-v4-pro"} {
		if _, ok := models[model]; ok {
			t.Fatalf("responses provider should not expose chat-completions model %q: %#v", model, models)
		}
	}
	options := provider["options"].(map[string]any)
	if options["baseURL"] != "http://127.0.0.1:53452/v1" {
		t.Fatalf("baseURL = %v", options["baseURL"])
	}
	if options["apiKey"] != "{env:AGENTSERVER_CODEX_LOCAL_API_KEY}" {
		t.Fatalf("apiKey = %v", options["apiKey"])
	}
	assertOpenCodeProxyHeaders(t, options)

	compatible := got["provider"].(map[string]any)["modelserver-compatible"].(map[string]any)
	if compatible["npm"] != "@ai-sdk/openai-compatible" {
		t.Fatalf("compatible npm = %v", compatible["npm"])
	}
	compatibleOptions := compatible["options"].(map[string]any)
	if compatibleOptions["baseURL"] != "http://127.0.0.1:53452/v1" {
		t.Fatalf("compatible baseURL = %v", compatibleOptions["baseURL"])
	}
	if compatibleOptions["apiKey"] != "{env:AGENTSERVER_CODEX_LOCAL_API_KEY}" {
		t.Fatalf("compatible apiKey = %v", compatibleOptions["apiKey"])
	}
	assertOpenCodeProxyHeaders(t, compatibleOptions)
	compatibleModels := compatible["models"].(map[string]any)
	for _, model := range []string{"deepseek-v4-pro"} {
		entry, ok := compatibleModels[model].(map[string]any)
		if !ok {
			t.Fatalf("compatible model %q missing from %#v", model, compatibleModels)
		}
		if entry["name"] != model {
			t.Fatalf("compatible model %q name = %v", model, entry["name"])
		}
	}
	if _, ok := compatibleModels["glm-5.1"]; ok {
		t.Fatalf("compatible provider should not expose Anthropic-only model glm-5.1: %#v", compatibleModels)
	}

	anthropic := got["provider"].(map[string]any)["modelserver-anthropic"].(map[string]any)
	if anthropic["npm"] != "@ai-sdk/anthropic" {
		t.Fatalf("anthropic npm = %v", anthropic["npm"])
	}
	anthropicOptions := anthropic["options"].(map[string]any)
	if anthropicOptions["baseURL"] != "http://127.0.0.1:53452/v1" {
		t.Fatalf("anthropic baseURL = %v", anthropicOptions["baseURL"])
	}
	if anthropicOptions["apiKey"] != "{env:AGENTSERVER_CODEX_LOCAL_API_KEY}" {
		t.Fatalf("anthropic apiKey = %v", anthropicOptions["apiKey"])
	}
	assertOpenCodeProxyHeaders(t, anthropicOptions)
	anthropicModels := anthropic["models"].(map[string]any)
	entry, ok := anthropicModels["glm-5.1"].(map[string]any)
	if !ok {
		t.Fatalf("anthropic model glm-5.1 missing from %#v", anthropicModels)
	}
	if entry["name"] != "glm-5.1" {
		t.Fatalf("anthropic model glm-5.1 name = %v", entry["name"])
	}
}

func TestUpdateConfigSelectsAnthropicProviderForGLM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode", "opencode.jsonc")
	err := UpdateConfig(path, Settings{
		BaseURL: "http://127.0.0.1:53452/v1",
		APIKey:  "local-proxy-token",
		Model:   "glm-5.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	readJSONFile(t, path, &got)
	if got["model"] != "modelserver-anthropic/glm-5.1" {
		t.Fatalf("model = %v", got["model"])
	}
}

func TestDefaultProxySettingsKeepsProxyTokenOutOfConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode", "opencode.jsonc")
	if err := UpdateConfig(path, DefaultProxySettings()); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), defaultAPIKey) {
		t.Fatalf("opencode config must not contain raw local proxy token:\n%s", body)
	}
	if !strings.Contains(string(body), `"{env:AGENTSERVER_CODEX_LOCAL_API_KEY}"`) {
		t.Fatalf("opencode config must use env substitution:\n%s", body)
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
	modelserver := providers["modelserver"].(map[string]any)
	if modelserver["name"] != "modelserver" {
		t.Fatalf("modelserver provider known name field was not updated: %#v", modelserver)
	}
}

func TestUpdateConfigMergesManagedProviderBlocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.jsonc")
	existing := `{
	  "provider": {
	    "modelserver": {
	      "disabled": false,
	      "options": {
	        "timeout": 120,
	        "headers": {
	          "X-Custom": "keep"
	        }
	      },
	      "models": {
	        "custom-model": {"name": "Custom", "reasoning": true},
	        "gpt-5.5": {"name": "old-gpt", "temperature": 0.2}
	      }
	    },
	    "modelserver-compatible": {
	      "tools": {"allow": ["shell"]},
	      "models": {
	        "deepseek-v4-pro": {"name": "old-deepseek", "extra": true}
	      }
	    },
	    "modelserver-anthropic": {
	      "transform": "keep"
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
	providers := got["provider"].(map[string]any)
	modelserver := providers["modelserver"].(map[string]any)
	if modelserver["disabled"] != false {
		t.Fatalf("modelserver disabled field was not preserved: %#v", modelserver)
	}
	options := modelserver["options"].(map[string]any)
	if options["timeout"] != float64(120) {
		t.Fatalf("modelserver options timeout was not preserved: %#v", options)
	}
	headers := options["headers"].(map[string]any)
	if headers["X-Custom"] != "keep" || headers["X-AgentServer-Client"] != "opencode" {
		t.Fatalf("modelserver headers were not merged: %#v", headers)
	}
	models := modelserver["models"].(map[string]any)
	if custom := models["custom-model"].(map[string]any); custom["reasoning"] != true {
		t.Fatalf("custom model entry was not preserved: %#v", models)
	}
	if gpt := models["gpt-5.5"].(map[string]any); gpt["temperature"] != float64(0.2) || gpt["name"] != "gpt-5.5" {
		t.Fatalf("managed model entry was not merged: %#v", gpt)
	}
	compatible := providers["modelserver-compatible"].(map[string]any)
	if tools := compatible["tools"].(map[string]any); tools["allow"] == nil {
		t.Fatalf("compatible tools field was not preserved: %#v", compatible)
	}
	deepseek := compatible["models"].(map[string]any)["deepseek-v4-pro"].(map[string]any)
	if deepseek["extra"] != true || deepseek["name"] != "deepseek-v4-pro" {
		t.Fatalf("compatible model entry was not merged: %#v", deepseek)
	}
	anthropic := providers["modelserver-anthropic"].(map[string]any)
	if anthropic["transform"] != "keep" {
		t.Fatalf("anthropic transform field was not preserved: %#v", anthropic)
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

func TestUpdateConfigPreservesJSONCCommentsAndExistingKeyOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.jsonc")
	existing := `{
	  // user theme comment
	  "theme": "dark",
	  // provider comment
	  "provider": {
	    "anthropic": {
	      // keep model comment
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
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{
		"// user theme comment",
		"// provider comment",
		"// keep model comment",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("updated JSONC lost comment %q:\n%s", want, text)
		}
	}
	themeIdx := strings.Index(text, `"theme"`)
	providerIdx := strings.Index(text, `"provider"`)
	if themeIdx < 0 || providerIdx < 0 || themeIdx > providerIdx {
		t.Fatalf("existing key order should keep theme before provider:\n%s", text)
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
	std, err := hujson.Standardize(b)
	if err != nil {
		t.Fatalf("standardize %s: %v\n%s", path, err, string(b))
	}
	if err := json.Unmarshal(std, v); err != nil {
		t.Fatalf("unmarshal %s: %v\n%s", path, err, string(b))
	}
}

func assertOpenCodeProxyHeaders(t *testing.T, options map[string]any) {
	t.Helper()
	headers, ok := options["headers"].(map[string]any)
	if !ok {
		t.Fatalf("options missing headers: %#v", options)
	}
	if headers["X-AgentServer-Client"] != "opencode" {
		t.Fatalf("X-AgentServer-Client header = %v", headers["X-AgentServer-Client"])
	}
}
