package codexdesktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const DefaultLocale = "zh-CN"

func LocaleGlobalStatePath(codexDir string) string {
	return filepath.Join(codexDir, ".codex-global-state.json")
}

func LocaleComputerUsePath(codexDir string) string {
	return filepath.Join(codexDir, "computer-use", "config.json")
}

func ConfigureLocale(globalStatePath, computerUsePath, locale string) error {
	if locale == "" {
		locale = DefaultLocale
	}
	if err := writeJSONStringField(globalStatePath, "localeOverride", locale); err != nil {
		return fmt.Errorf("configure Codex Desktop locale override: %w", err)
	}
	if err := writeJSONStringField(computerUsePath, "locale", locale); err != nil {
		return fmt.Errorf("configure Codex Desktop computer-use locale: %w", err)
	}
	return nil
}

func writeJSONStringField(path, key, value string) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}

	root := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if len(b) > 0 {
			if err := json.Unmarshal(b, &root); err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}

	root[key] = value
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}
