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
