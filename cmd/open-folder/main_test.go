package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentserver/agentserver-pkg/internal/codex"
	"github.com/agentserver/agentserver-pkg/internal/console"
	"github.com/agentserver/agentserver-pkg/internal/installmode"
	"github.com/agentserver/agentserver-pkg/internal/modelproxy"
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
	  "agentserverApp.panel.allowed": ["terminal", "output"],
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
	if _, ok := settings["agentserverApp.panel.allowed"]; ok {
		t.Fatalf("agentserverApp.panel.allowed should be removed")
	}
	if _, ok := settings["agentserverApp.panel.hideViews"]; !ok {
		t.Fatalf("agentserverApp.panel.hideViews should be written")
	}
	if settings["custom.key"] != "keep me" {
		t.Fatalf("custom.key=%v, want keep me", settings["custom.key"])
	}
	b, readErr = os.ReadFile(p.CodexConfigFile)
	if readErr != nil {
		t.Fatalf("expected codex config to be written before launching VS Code: %v", readErr)
	}
	for _, want := range []string{
		`base_url = "` + modelproxy.DefaultBaseURL + `"`,
		`env_key = "` + codex.LocalProxyAPIKeyEnv + `"`,
	} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %q in:\n%s", want, b)
		}
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
	b, readErr := os.ReadFile(p.CodexConfigFile)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, want := range []string{
		`base_url = "` + modelproxy.DefaultBaseURL + `"`,
		`env_key = "` + codex.LocalProxyAPIKeyEnv + `"`,
	} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %q in:\n%s", want, b)
		}
	}
}

func TestOpenFolderCodexDesktopWritesUILocaleBeforeLaunch(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, ".codex", ".codex-global-state.json")
	computerUsePath := filepath.Join(dir, ".codex", "computer-use", "config.json")
	p := paths.Paths{
		CodexConfigFile:                   filepath.Join(dir, ".codex", "config.toml"),
		CodexDesktopGlobalStateFile:       globalPath,
		CodexDesktopComputerUseConfigFile: computerUsePath,
	}
	err := openFolderCodexDesktop(context.Background(), p, `C:\Project Folder`, nil, "", func(url string) error {
		assertJSONField(t, globalPath, "localeOverride", "zh-CN")
		assertJSONField(t, computerUsePath, "locale", "zh-CN")
		return nil
	})
	if err != nil {
		t.Fatalf("openFolderCodexDesktop: %v", err)
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

func TestEnsureConsoleStartsLauncherWhenMissing(t *testing.T) {
	expectedLauncher := filepath.Join("install-dir", "launcher.exe")
	expectedPortFile := "console-port.json"
	calls := 0
	discoverCalls := 0
	err := ensureConsoleBackground(context.Background(), consoleBackgroundDeps{
		LauncherExe: expectedLauncher,
		PortFile:    expectedPortFile,
		Discover: func(_ context.Context, portFile string) (console.InstanceInfo, bool) {
			discoverCalls++
			if portFile != expectedPortFile {
				t.Fatalf("portFile=%q, want %q", portFile, expectedPortFile)
			}
			return console.InstanceInfo{}, false
		},
		Start: func(exe string, args ...string) error {
			calls++
			if exe != expectedLauncher {
				t.Fatalf("exe=%q, want %q", exe, expectedLauncher)
			}
			if len(args) != 1 || args[0] != "--background" {
				t.Fatalf("args=%v, want [--background]", args)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if discoverCalls != 1 {
		t.Fatalf("discover calls=%d", discoverCalls)
	}
	if calls != 1 {
		t.Fatalf("start calls=%d", calls)
	}
}

func TestEnsureConsoleDoesNotStartWhenHealthy(t *testing.T) {
	calls := 0
	err := ensureConsoleBackground(context.Background(), consoleBackgroundDeps{
		LauncherExe: "launcher.exe",
		PortFile:    "console-port.json",
		Discover: func(context.Context, string) (console.InstanceInfo, bool) {
			return console.InstanceInfo{Port: 1234}, true
		},
		Start: func(string, ...string) error {
			calls++
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("start calls=%d", calls)
	}
}

func TestStartDetachedHidesConsoleWindow(t *testing.T) {
	body, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "process.HideWindow(cmd)") {
		t.Fatalf("startDetached should hide the child console window:\n%s", body)
	}
}

func assertJSONField(t *testing.T, path, key, want string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("parse %s: %v\n%s", path, err, b)
	}
	if got := root[key]; got != want {
		t.Fatalf("%s[%q]=%v, want %q", path, key, got, want)
	}
}

func TestEnsureConsoleDoesNotStartWithoutLauncher(t *testing.T) {
	calls := 0
	err := ensureConsoleBackground(context.Background(), consoleBackgroundDeps{
		LauncherExe: "",
		PortFile:    "console-port.json",
		Discover: func(context.Context, string) (console.InstanceInfo, bool) {
			return console.InstanceInfo{}, false
		},
		Start: func(string, ...string) error {
			calls++
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("start calls=%d", calls)
	}
}
