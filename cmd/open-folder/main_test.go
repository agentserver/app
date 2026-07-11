package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentserver/agentserver-pkg/internal/codex"
	"github.com/agentserver/agentserver-pkg/internal/codexdesktop"
	"github.com/agentserver/agentserver-pkg/internal/console"
	"github.com/agentserver/agentserver-pkg/internal/installmode"
	"github.com/agentserver/agentserver-pkg/internal/modelproxy"
	"github.com/agentserver/agentserver-pkg/internal/opencodedesktop"
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
		`experimental_bearer_token = "` + codex.LegacyLocalProxyAPIKeyValue + `"`,
	} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %q in:\n%s", want, b)
		}
	}
}

func TestOpenFolderCodexDesktopUsesFolderDeepLink(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{CodexConfigFile: filepath.Join(dir, ".codex", "config.toml")}
	var openedFolder string
	err := openFolderCodexDesktop(context.Background(), p, `C:\Project Folder`, nil, "", func(_ context.Context, folder string) error {
		openedFolder = folder
		return nil
	})
	if err != nil {
		t.Fatalf("openFolderCodexDesktop: %v", err)
	}
	if openedFolder != `C:\Project Folder` {
		t.Fatalf("opened folder=%q", openedFolder)
	}
	b, readErr := os.ReadFile(p.CodexConfigFile)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, want := range []string{
		`base_url = "` + modelproxy.DefaultBaseURL + `"`,
		`experimental_bearer_token = "` + codex.LegacyLocalProxyAPIKeyValue + `"`,
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
	err := openFolderCodexDesktop(context.Background(), p, `C:\Project Folder`, nil, "", func(context.Context, string) error {
		assertJSONField(t, globalPath, "localeOverride", "zh-CN")
		assertJSONField(t, computerUsePath, "locale", "zh-CN")
		return nil
	})
	if err != nil {
		t.Fatalf("openFolderCodexDesktop: %v", err)
	}
}

func TestOpenFolderCodexDesktopReturnsVisibleLaunchFailure(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{CodexConfigFile: filepath.Join(dir, ".codex", "config.toml")}
	shellErr := errors.New("deep link failed")
	err := openFolderCodexDesktop(context.Background(), p, `C:\Project`, nil, "", func(context.Context, string) error {
		return fmt.Errorf("%w: ChatGPT / Codex launch failed; Repair Reset Reinstall: %w", codexdesktop.ErrLaunchFailed, shellErr)
	})
	if !errors.Is(err, shellErr) || !errors.Is(err, codexdesktop.ErrLaunchFailed) {
		t.Fatalf("err=%v, want deep-link cause and ErrLaunchFailed", err)
	}
	for _, want := range []string{"ChatGPT / Codex", "Repair", "Reset", "Reinstall"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err=%v, missing %q", err, want)
		}
	}
}

func TestOpenFolderOpenCodeDesktopWritesConfigAndUsesFolderWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	proxyToken := "open-folder-local-proxy-token"
	p := paths.Paths{
		InstallRoot:        filepath.Join(dir, ".agentserver-app"),
		CodexConfigFile:    filepath.Join(dir, ".codex", "config.toml"),
		OpenCodeConfigFile: filepath.Join(dir, ".config", "opencode", "opencode.jsonc"),
	}
	if err := os.MkdirAll(p.InstallRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.InstallRoot, "proxy-token"), []byte(proxyToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var gotFolder string
	err := openFolderOpenCodeDesktop(context.Background(), p, `C:\Project Folder`, opencodedesktop.Detected{
		Installed: true,
		Path:      `C:\OpenCode\OpenCode.exe`,
	}, "", func(ctx context.Context, opts opencodedesktop.LaunchOptions) error {
		gotFolder = opts.Folder
		if opts.Detected.Path != `C:\OpenCode\OpenCode.exe` {
			t.Fatalf("OpenCode path = %q", opts.Detected.Path)
		}
		if opts.Config.Path != p.OpenCodeConfigFile {
			t.Fatalf("OpenCode config path = %q, want %q", opts.Config.Path, p.OpenCodeConfigFile)
		}
		if opts.Config.APIKeyEnv != "AGENTSERVER_LOCAL_MODEL_PROXY_API_KEY" {
			t.Fatalf("OpenCode API key env = %q", opts.Config.APIKeyEnv)
		}
		if opts.Config.APIKey != proxyToken {
			t.Fatalf("OpenCode API key = %q, want proxy token", opts.Config.APIKey)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("openFolderOpenCodeDesktop: %v", err)
	}
	if gotFolder != `C:\Project Folder` {
		t.Fatalf("Folder = %q", gotFolder)
	}
	if _, err := os.Stat(p.OpenCodeConfigFile); err != nil {
		t.Fatalf("opencode config not written: %v", err)
	}
	b, readErr := os.ReadFile(p.CodexConfigFile)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, want := range []string{
		`base_url = "` + modelproxy.DefaultBaseURL + `"`,
		`experimental_bearer_token = "` + proxyToken + `"`,
	} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %q in:\n%s", want, b)
		}
	}
	ob, readOpenCodeErr := os.ReadFile(p.OpenCodeConfigFile)
	if readOpenCodeErr != nil {
		t.Fatal(readOpenCodeErr)
	}
	if strings.Contains(string(ob), proxyToken) {
		t.Fatalf("opencode config should not persist local proxy token:\n%s", ob)
	}
	if strings.Contains(string(ob), "AGENTSERVER_CODEX_LOCAL_API_KEY") {
		t.Fatalf("opencode config should not use Codex-specific env names:\n%s", ob)
	}
	if !strings.Contains(string(ob), "{env:AGENTSERVER_LOCAL_MODEL_PROXY_API_KEY}") {
		t.Fatalf("opencode config should use env substitution:\n%s", ob)
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

func TestStartDetachedPreparesHiddenDetachedCommand(t *testing.T) {
	hideCalled := false
	startCalled := false
	err := startDetachedWithDeps("launcher.exe", []string{"--background"},
		func(cmd *exec.Cmd) {
			hideCalled = true
			if cmd.Path != "launcher.exe" {
				t.Fatalf("Path = %q", cmd.Path)
			}
			if len(cmd.Args) != 2 || cmd.Args[1] != "--background" {
				t.Fatalf("Args = %#v", cmd.Args)
			}
		},
		func(cmd *exec.Cmd) error {
			startCalled = true
			if cmd.Stdin != nil || cmd.Stdout != nil || cmd.Stderr != nil {
				t.Fatalf("stdio should be detached: stdin=%v stdout=%v stderr=%v", cmd.Stdin, cmd.Stdout, cmd.Stderr)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !hideCalled {
		t.Fatal("hide hook was not called")
	}
	if !startCalled {
		t.Fatal("start hook was not called")
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
