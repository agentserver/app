package vscode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteSettings_Empty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "User", "settings.json")
	err := WriteSettings(path, SettingsInput{CodexAbsPath: `C:\bin\codex.exe`})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("not valid json: %v", err)
	}
	if m["locale"] != "zh-cn" {
		t.Errorf("locale: %v", m["locale"])
	}
	if m["workbench.editor.languageDetection"] != false {
		t.Errorf("languageDetection: %v", m["workbench.editor.languageDetection"])
	}
	if m["agentserverVscode.terminal.profileName"] != "codex" {
		t.Errorf("profile: %v", m["agentserverVscode.terminal.profileName"])
	}
	profiles := m["terminal.integrated.profiles.windows"].(map[string]any)
	codex := profiles["codex"].(map[string]any)
	args := codex["args"].([]any)
	if args[1] != `C:\bin\codex.exe` {
		t.Errorf("codex path not embedded: %v", args)
	}
}

func TestWriteSettings_PreservesUserKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "User", "settings.json")
	os.MkdirAll(filepath.Dir(path), 0o755)
	prior := `{"editor.fontSize": 14, "custom.key": "keep me"}`
	os.WriteFile(path, []byte(prior), 0o644)

	err := WriteSettings(path, SettingsInput{CodexAbsPath: `C:\bin\codex.exe`})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	var m map[string]any
	json.Unmarshal(b, &m)
	if m["editor.fontSize"] != float64(14) {
		t.Errorf("editor.fontSize lost: %v", m["editor.fontSize"])
	}
	if m["custom.key"] != "keep me" {
		t.Errorf("custom.key lost: %v", m["custom.key"])
	}
	if m["locale"] != "zh-cn" {
		t.Errorf("locale not added: %v", m["locale"])
	}
}

func readSettingsMap(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("not valid json: %v", err)
	}
	return m
}

func TestWriteSettings_MinimalModeDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "User", "settings.json")
	if err := WriteSettings(path, SettingsInput{CodexAbsPath: `C:\bin\codex.exe`}); err != nil {
		t.Fatal(err)
	}
	m := readSettingsMap(t, path)
	want := map[string]any{
		"workbench.statusBar.visible":      false,
		"workbench.panel.opensMaximized":   "never",
		"window.menuBarVisibility":         "hidden",
		"window.commandCenter":             false,
		"workbench.layoutControl.enabled":  false,
		"breadcrumbs.enabled":              false,
		"editor.minimap.enabled":           false,
		"editor.stickyScroll.enabled":      false,
		"workbench.editor.showTabs":        "single",
		"workbench.editor.empty.hint":      "hidden",
		"workbench.tips.enabled":           false,
		"update.showReleaseNotes":          false,
		"extensions.ignoreRecommendations": true,
	}
	for key, expected := range want {
		if got := m[key]; got != expected {
			t.Errorf("%s=%v, want %v", key, got, expected)
		}
	}
}

func TestWriteSettings_OverwritesManagedMinimalModeKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "User", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	prior := `{
	  "window.menuBarVisibility": "classic",
	  "window.commandCenter": true,
	  "workbench.statusBar.visible": true,
	  "workbench.panel.opensMaximized": "always",
	  "editor.minimap.enabled": true,
	  "custom.key": "keep me"
	}`
	if err := os.WriteFile(path, []byte(prior), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteSettings(path, SettingsInput{CodexAbsPath: `C:\bin\codex.exe`}); err != nil {
		t.Fatal(err)
	}
	m := readSettingsMap(t, path)
	checks := map[string]any{
		"window.menuBarVisibility":       "hidden",
		"window.commandCenter":           false,
		"workbench.statusBar.visible":    false,
		"workbench.panel.opensMaximized": "never",
		"editor.minimap.enabled":         false,
		"custom.key":                     "keep me",
	}
	for key, expected := range checks {
		if got := m[key]; got != expected {
			t.Errorf("%s=%v, want %v", key, got, expected)
		}
	}
}
