// Package opencode writes/merges OpenCode global JSON configuration.
package opencode

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tailscale/hujson"
)

const (
	defaultProvider          = "modelserver"
	compatibleProvider       = "modelserver-compatible"
	defaultModel             = "gpt-5.5"
	defaultBaseURL           = "http://127.0.0.1:53452/v1"
	defaultAPIKey            = "agentserver-local-proxy"
	defaultAPIKeyEnv         = "AGENTSERVER_CODEX_LOCAL_API_KEY"
	configSchema             = "https://opencode.ai/config.json"
	openAINPM                = "@ai-sdk/openai"
	openAICompatibleNPM      = "@ai-sdk/openai-compatible"
	compatibleProviderName   = "modelserver-compatible"
	defaultProviderModelName = "modelserver"
)

var responsesModels = []string{
	"gpt-5.5",
}

var compatibleModels = []string{
	"glm-5.1",
	"deepseek-v4-pro",
}

type Settings struct {
	BaseURL   string
	APIKey    string
	APIKeyEnv string
	Model     string
}

func DefaultProxySettings() Settings {
	return Settings{
		BaseURL: defaultBaseURL,
		APIKey:  defaultAPIKey,
		Model:   defaultModel,
	}
}

func UpdateConfig(path string, s Settings) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("opencode config path required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir opencode config dir: %w", err)
	}
	root := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if len(bytes.TrimSpace(b)) > 0 {
			std, err := hujson.Standardize(b)
			if err != nil {
				return fmt.Errorf("parse opencode config: %w", err)
			}
			if err := json.Unmarshal(std, &root); err != nil {
				return fmt.Errorf("parse opencode config: %w", err)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read opencode config: %w", err)
	}

	applySettings(root, s)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(root); err != nil {
		return fmt.Errorf("marshal opencode config: %w", err)
	}
	return writeConfigFile(path, buf.Bytes())
}

func applySettings(root map[string]any, s Settings) {
	settings := normalizeSettings(s)
	if stringSetting(root, "$schema") == "" {
		root["$schema"] = configSchema
	}
	root["model"] = providerForModel(settings.Model) + "/" + settings.Model
	providers, _ := root["provider"].(map[string]any)
	if providers == nil {
		providers = map[string]any{}
	}
	providers[defaultProvider] = map[string]any{
		"npm":  openAINPM,
		"name": defaultProviderModelName,
		"options": map[string]any{
			"baseURL": settings.BaseURL,
			"apiKey":  settings.apiKeyValue(),
		},
		"models": modelEntries(responsesModels, settings.Model),
	}
	providers[compatibleProvider] = map[string]any{
		"npm":  openAICompatibleNPM,
		"name": compatibleProviderName,
		"options": map[string]any{
			"baseURL": settings.BaseURL,
			"apiKey":  settings.apiKeyValue(),
		},
		"models": modelEntries(compatibleModels, settings.Model),
	}
	root["provider"] = providers
}

func modelEntries(modelsList []string, selectedModel string) map[string]any {
	models := map[string]any{}
	for _, model := range modelsList {
		models[model] = map[string]any{"name": model}
	}
	if providerForModel(selectedModel) == providerForModels(modelsList) {
		if _, ok := models[selectedModel]; ok {
			return models
		}
		models[selectedModel] = map[string]any{"name": selectedModel}
	}
	return models
}

func providerForModel(model string) string {
	if containsModel(compatibleModels, model) {
		return compatibleProvider
	}
	return defaultProvider
}

func providerForModels(modelsList []string) string {
	if len(modelsList) > 0 && containsModel(compatibleModels, modelsList[0]) {
		return compatibleProvider
	}
	return defaultProvider
}

func containsModel(models []string, model string) bool {
	for _, candidate := range models {
		if candidate == model {
			return true
		}
	}
	return false
}

func normalizeSettings(s Settings) Settings {
	if strings.TrimSpace(s.BaseURL) == "" {
		s.BaseURL = defaultBaseURL
	}
	if strings.TrimSpace(s.APIKeyEnv) == "" {
		s.APIKeyEnv = defaultAPIKeyEnv
	}
	if strings.TrimSpace(s.Model) == "" {
		s.Model = defaultModel
	}
	s.BaseURL = strings.TrimRight(strings.TrimSpace(s.BaseURL), "/")
	s.APIKey = strings.TrimSpace(s.APIKey)
	s.APIKeyEnv = strings.TrimSpace(s.APIKeyEnv)
	s.Model = strings.TrimSpace(s.Model)
	return s
}

func (s Settings) apiKeyValue() string {
	if s.APIKey != "" {
		return s.APIKey
	}
	return "{env:" + s.APIKeyEnv + "}"
}

func stringSetting(root map[string]any, key string) string {
	value, _ := root[key].(string)
	return strings.TrimSpace(value)
}

func writeConfigFile(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		if tmpPath != "" {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	tmpPath = ""
	return os.Chmod(path, 0o600)
}
