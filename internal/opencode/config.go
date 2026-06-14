// Package opencode writes/merges OpenCode global JSON configuration.
package opencode

import (
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
	anthropicProvider        = "modelserver-anthropic"
	defaultModel             = "gpt-5.5"
	defaultBaseURL           = "http://127.0.0.1:53452/v1"
	defaultAPIKey            = "agentserver-local-proxy"
	defaultAPIKeyEnv         = "AGENTSERVER_CODEX_LOCAL_API_KEY"
	configSchema             = "https://opencode.ai/config.json"
	openAINPM                = "@ai-sdk/openai"
	openAICompatibleNPM      = "@ai-sdk/openai-compatible"
	anthropicNPM             = "@ai-sdk/anthropic"
	compatibleProviderName   = "modelserver-compatible"
	anthropicProviderName    = "modelserver-anthropic"
	defaultProviderModelName = "modelserver"
)

var responsesModels = []string{
	"gpt-5.5",
}

var compatibleModels = []string{
	"deepseek-v4-pro",
}

var anthropicModels = []string{
	"glm-5.1",
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
	root := hujson.Value{Value: &hujson.Object{}}
	if b, err := os.ReadFile(path); err == nil {
		if len(strings.TrimSpace(string(b))) > 0 {
			parsed, err := hujson.Parse(b)
			if err != nil {
				return fmt.Errorf("parse opencode config: %w", err)
			}
			if _, ok := parsed.Value.(*hujson.Object); !ok {
				return fmt.Errorf("parse opencode config: root must be an object")
			}
			root = parsed
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read opencode config: %w", err)
	}

	applySettings(&root, s)
	root.Format()
	return writeConfigFile(path, root.Pack())
}

func applySettings(root *hujson.Value, s Settings) {
	settings := normalizeSettings(s)
	rootObj := root.Value.(*hujson.Object)
	if stringObjectSetting(rootObj, "$schema") == "" {
		setObjectValue(rootObj, "$schema", jsonValue(configSchema))
	}
	setObjectValue(rootObj, "model", jsonValue(providerForModel(settings.Model)+"/"+settings.Model))
	providers := ensureObject(rootObj, "provider")
	mergeProvider(providers, providerSpec{
		key:    defaultProvider,
		npm:    openAINPM,
		name:   defaultProviderModelName,
		models: responsesModels,
	}, settings)
	mergeProvider(providers, providerSpec{
		key:    compatibleProvider,
		npm:    openAICompatibleNPM,
		name:   compatibleProviderName,
		models: compatibleModels,
	}, settings)
	mergeProvider(providers, providerSpec{
		key:    anthropicProvider,
		npm:    anthropicNPM,
		name:   anthropicProviderName,
		models: anthropicModels,
	}, settings)
}

type providerSpec struct {
	key    string
	npm    string
	name   string
	models []string
}

func mergeProvider(providers *hujson.Object, spec providerSpec, settings Settings) {
	provider := ensureObject(providers, spec.key)
	setObjectValue(provider, "npm", jsonValue(spec.npm))
	setObjectValue(provider, "name", jsonValue(spec.name))

	options := ensureObject(provider, "options")
	setObjectValue(options, "baseURL", jsonValue(settings.BaseURL))
	setObjectValue(options, "apiKey", jsonValue(settings.apiKeyValue()))
	headers := ensureObject(options, "headers")
	setObjectValue(headers, "X-AgentServer-Client", jsonValue("opencode"))

	models := ensureObject(provider, "models")
	for _, model := range modelsForProvider(spec.models, settings.Model) {
		entry := ensureObject(models, model)
		setObjectValue(entry, "name", jsonValue(model))
	}
}

func modelsForProvider(modelsList []string, selectedModel string) []string {
	models := append([]string(nil), modelsList...)
	if providerForModel(selectedModel) == providerForModels(modelsList) {
		models = appendIfMissing(models, selectedModel)
	}
	return models
}

func appendIfMissing(values []string, value string) []string {
	for _, candidate := range values {
		if candidate == value {
			return values
		}
	}
	return append(values, value)
}

func providerForModel(model string) string {
	if containsModel(anthropicModels, model) {
		return anthropicProvider
	}
	if containsModel(compatibleModels, model) {
		return compatibleProvider
	}
	return defaultProvider
}

func providerForModels(modelsList []string) string {
	if len(modelsList) > 0 && containsModel(anthropicModels, modelsList[0]) {
		return anthropicProvider
	}
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

func stringObjectSetting(obj *hujson.Object, key string) string {
	value := findObjectValue(obj, key)
	if value == nil {
		return ""
	}
	lit, ok := value.Value.(hujson.Literal)
	if !ok || lit.Kind() != '"' {
		return ""
	}
	return strings.TrimSpace(lit.String())
}

func ensureObject(obj *hujson.Object, key string) *hujson.Object {
	if value := findObjectValue(obj, key); value != nil {
		if child, ok := value.Value.(*hujson.Object); ok {
			return child
		}
		child := &hujson.Object{}
		value.Value = child
		return child
	}
	child := &hujson.Object{}
	obj.Members = append(obj.Members, hujson.ObjectMember{
		Name:  jsonValue(key),
		Value: hujson.Value{Value: child},
	})
	return child
}

func setObjectValue(obj *hujson.Object, key string, value hujson.Value) {
	if existing := findObjectValue(obj, key); existing != nil {
		value.BeforeExtra = existing.BeforeExtra
		value.AfterExtra = existing.AfterExtra
		*existing = value
		return
	}
	obj.Members = append(obj.Members, hujson.ObjectMember{
		Name:  jsonValue(key),
		Value: value,
	})
}

func findObjectValue(obj *hujson.Object, key string) *hujson.Value {
	for i := range obj.Members {
		name, ok := objectMemberName(obj.Members[i])
		if ok && name == key {
			return &obj.Members[i].Value
		}
	}
	return nil
}

func objectMemberName(member hujson.ObjectMember) (string, bool) {
	lit, ok := member.Name.Value.(hujson.Literal)
	if !ok || lit.Kind() != '"' {
		return "", false
	}
	return lit.String(), true
}

func jsonValue(v any) hujson.Value {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	value, err := hujson.Parse(b)
	if err != nil {
		panic(err)
	}
	return value
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
