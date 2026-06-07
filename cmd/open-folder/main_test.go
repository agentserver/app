package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentserver/agentserver-pkg/internal/installmode"
	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/state"
)

func TestOpenFolderMigratesVSCodeSettingsBeforeLaunch(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{
		VSCodeUserDataDir: filepath.Join(dir, "vscode-data"),
		VSCodeExtDir:      filepath.Join(dir, "vscode-extensions"),
		CodexConfigFile:   filepath.Join(dir, ".codex", "config.toml"),
		CodexExePath:      filepath.Join(dir, "bin", "codex.exe"),
	}
	settingsPath := filepath.Join(p.VSCodeUserDataDir, "User", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{
	  "agentserverVscode.panel.allowed": ["terminal", "output"],
	  "custom.key": "keep me"
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	err := openFolder(context.Background(), filepath.Join(dir, "missing-code.exe"), p, filepath.Join(dir, "work"), nil, "", "")
	if err == nil {
		t.Fatal("expected missing VS Code executable error")
	}

	b, readErr := os.ReadFile(settingsPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	var settings map[string]any
	if err := json.Unmarshal(b, &settings); err != nil {
		t.Fatal(err)
	}
	if _, ok := settings["agentserverVscode.panel.allowed"]; ok {
		t.Fatalf("agentserverVscode.panel.allowed should be removed")
	}
	if _, ok := settings["agentserverVscode.panel.hideViews"]; !ok {
		t.Fatalf("agentserverVscode.panel.hideViews should be written")
	}
	if settings["custom.key"] != "keep me" {
		t.Fatalf("custom.key=%v, want keep me", settings["custom.key"])
	}
}

func TestOpenFolderCodexDesktopUsesFolderDeepLink(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{CodexConfigFile: filepath.Join(dir, ".codex", "config.toml")}
	var opened string
	err := openFolderCodexDesktop(context.Background(), p, `C:\Project Folder`, nil, "", func(url string) error {
		opened = url
		return nil
	})
	if err != nil {
		t.Fatalf("openFolderCodexDesktop: %v", err)
	}
	if !strings.HasPrefix(opened, "codex://threads/new?path=") {
		t.Fatalf("opened=%q", opened)
	}
	if !strings.Contains(opened, "Project+Folder") {
		t.Fatalf("path not encoded: %q", opened)
	}
}

func TestLoadOpenFolderStateSyncsInstallModeFile(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{StateFile: filepath.Join(dir, "state.json")}
	modePath := filepath.Join(dir, "app", "install-mode.json")
	if err := installmode.Write(modePath, state.FrontendModeMinimalVSCode); err != nil {
		t.Fatal(err)
	}

	got, err := loadOpenFolderState(p, modePath)
	if err != nil {
		t.Fatalf("loadOpenFolderState: %v", err)
	}
	if got.FrontendMode != state.FrontendModeMinimalVSCode {
		t.Fatalf("FrontendMode = %q, want %q", got.FrontendMode, state.FrontendModeMinimalVSCode)
	}
}
