// Package codex writes/merges ~/.codex/config.toml for the codex CLI.
package codex

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

type Settings struct {
	Provider string // e.g. "modelserver"
	Model    string // e.g. "gpt-5.5"
	BaseURL  string // e.g. "https://code.ai.cs.ac.cn/v1"
	EnvKey   string // e.g. "OPENAI_API_KEY"
	WireAPI  string // e.g. "responses"
}

// UpdateConfig merges Settings into the config.toml at `path`, preserving
// any unrelated top-level keys and any [model_providers.X] tables other
// than ours. The original is backed up to path.bak.<unix-ts> first.
func UpdateConfig(path string, s Settings) error {
	if s.Provider == "" {
		return errors.New("Settings.Provider required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir codex dir: %w", err)
	}

	root := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if _, err := toml.Decode(string(b), &root); err != nil {
			return fmt.Errorf("parse existing config.toml: %w", err)
		}
		// backup
		backup := fmt.Sprintf("%s.bak.%d", path, time.Now().Unix())
		_ = os.WriteFile(backup, b, 0o644)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read config.toml: %w", err)
	}

	root["model_provider"] = s.Provider
	if s.Model != "" {
		root["model"] = s.Model
	}
	providers, _ := root["model_providers"].(map[string]any)
	if providers == nil {
		providers = map[string]any{}
	}
	providers[s.Provider] = map[string]any{
		"name":     s.Provider,
		"base_url": s.BaseURL,
		"env_key":  s.EnvKey,
		"wire_api": s.WireAPI,
	}
	root["model_providers"] = providers

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(root); err != nil {
		return fmt.Errorf("marshal config.toml: %w", err)
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
