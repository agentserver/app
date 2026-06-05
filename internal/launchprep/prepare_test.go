package launchprep

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/vscode"
	_ "modernc.org/sqlite"
)

func TestPrepareVSCodeMigratesSettingsAndRefreshesVSIX(t *testing.T) {
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

	var gotInstaller vscode.Installer
	oldInstallExtensions := installExtensions
	installExtensions = func(_ context.Context, in vscode.Installer) error {
		gotInstaller = in
		return nil
	}
	t.Cleanup(func() { installExtensions = oldInstallExtensions })

	vsixPath := filepath.Join(dir, "agentserver-vscode.vsix")
	if err := PrepareVSCode(context.Background(), Input{
		CodeExe:          filepath.Join(dir, "Code.exe"),
		Paths:            p,
		EmbeddedVSIXPath: vsixPath,
	}); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
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

	codexConfig, err := os.ReadFile(p.CodexConfigFile)
	if err != nil {
		t.Fatalf("expected codex config: %v", err)
	}
	if string(codexConfig) == "" {
		t.Fatal("codex config should not be empty")
	}
	if gotInstaller.CodeExe == "" {
		t.Fatal("expected VSIX refresh")
	}
	if gotInstaller.CodeExe != filepath.Join(dir, "Code.exe") {
		t.Fatalf("CodeExe=%q", gotInstaller.CodeExe)
	}
	if gotInstaller.UserDataDir != p.VSCodeUserDataDir {
		t.Fatalf("UserDataDir=%q", gotInstaller.UserDataDir)
	}
	if gotInstaller.ExtensionsDir != p.VSCodeExtDir {
		t.Fatalf("ExtensionsDir=%q", gotInstaller.ExtensionsDir)
	}
	if len(gotInstaller.Extensions) != 1 || gotInstaller.Extensions[0] != vsixPath {
		t.Fatalf("Extensions=%v, want [%s]", gotInstaller.Extensions, vsixPath)
	}

	db, err := sql.Open("sqlite", filepath.Join(p.VSCodeUserDataDir, "User", "globalStorage", "state.vscdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var rawPinnedPanels string
	if err := db.QueryRow(`SELECT value FROM ItemTable WHERE key = 'workbench.panel.pinnedPanels'`).Scan(&rawPinnedPanels); err != nil {
		t.Fatalf("expected seeded pinned panel state: %v", err)
	}
	var pinnedPanels []map[string]any
	if err := json.Unmarshal([]byte(rawPinnedPanels), &pinnedPanels); err != nil {
		t.Fatal(err)
	}
	for _, entry := range pinnedPanels {
		id, _ := entry["id"].(string)
		pinned, _ := entry["pinned"].(bool)
		if id == "terminal" && !pinned {
			t.Fatalf("terminal should stay pinned: %#v", entry)
		}
		if id != "terminal" && pinned {
			t.Fatalf("%s should be unpinned: %#v", id, entry)
		}
	}
}
