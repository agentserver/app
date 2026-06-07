package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentserver/agentserver-pkg/internal/console"
	"github.com/agentserver/agentserver-pkg/internal/installmode"
	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/state"
)

func TestLauncherOptionsDefaultOpensPageAndFrontend(t *testing.T) {
	got := parseLauncherOptions([]string{})
	if got.Background || !got.OpenPage || !got.OpenFrontend {
		t.Fatalf("options=%+v", got)
	}
}

func TestLauncherOptionsBackgroundDoesNotOpenPageOrFrontend(t *testing.T) {
	got := parseLauncherOptions([]string{"--background"})
	if !got.Background || got.OpenPage || got.OpenFrontend {
		t.Fatalf("options=%+v", got)
	}
}

func TestCompletedLauncherReusesExistingConsole(t *testing.T) {
	called := launcherCalls{}
	err := runCompletedConsole(context.Background(), completedConsoleDeps{
		Options:  launcherOptions{OpenPage: true, OpenFrontend: true},
		PortFile: "ignored",
		Discover: func(context.Context, string) (console.InstanceInfo, bool) {
			return console.InstanceInfo{Port: 34567}, true
		},
		OpenBrowser: func(url string) error {
			called.openedURL = url
			return nil
		},
		Post: func(ctx context.Context, url string) error {
			called.posted = append(called.posted, url)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(called.openedURL, "127.0.0.1:34567") {
		t.Fatalf("openedURL=%q", called.openedURL)
	}
	if len(called.posted) != 1 || !strings.Contains(called.posted[0], "/api/console/open-frontend") {
		t.Fatalf("posted=%+v", called.posted)
	}
}

type launcherCalls struct {
	openedURL string
	posted    []string
}

func TestCompletedLauncherAttemptsFrontendWhenOpenPageFails(t *testing.T) {
	called := launcherCalls{}
	browserErr := errors.New("browser failed")
	err := runCompletedConsole(context.Background(), completedConsoleDeps{
		Options:  launcherOptions{OpenPage: true, OpenFrontend: true},
		PortFile: "ignored",
		Discover: func(context.Context, string) (console.InstanceInfo, bool) {
			return console.InstanceInfo{Port: 34567}, true
		},
		OpenBrowser: func(url string) error {
			called.openedURL = url
			return browserErr
		},
		Post: func(ctx context.Context, url string) error {
			called.posted = append(called.posted, url)
			return nil
		},
	})
	if !errors.Is(err, browserErr) {
		t.Fatalf("err=%v, want %v", err, browserErr)
	}
	if !strings.Contains(called.openedURL, "127.0.0.1:34567") {
		t.Fatalf("openedURL=%q", called.openedURL)
	}
	if len(called.posted) != 1 || !strings.Contains(called.posted[0], "/api/console/open-frontend") {
		t.Fatalf("posted=%+v", called.posted)
	}
}

func TestCompletedStateOrchestratorLoadsDashboardState(t *testing.T) {
	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	if err := store.Update(func(s *state.State) error {
		s.Onboarding.Status = state.StatusComplete
		s.Onboarding.CompletedSteps = []string{"modelserver_login", "agentserver_login", "shortcuts_created"}
		s.FrontendMode = state.FrontendModeMinimalVSCode
		s.Modelserver.ProjectID = "proj-1"
		s.Agentserver.WorkspaceID = "workspace-1"
		s.VSCode.Path = filepath.Join(dir, "Code.exe")
		s.VSCode.Version = "1.2.3"
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	got, err := (completedStateOrchestrator{store: store}).State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.OnboardingStatus != string(state.StatusComplete) {
		t.Fatalf("OnboardingStatus=%q", got.OnboardingStatus)
	}
	if got.FrontendMode != string(state.FrontendModeMinimalVSCode) {
		t.Fatalf("FrontendMode=%q", got.FrontendMode)
	}
	if got.FrontendName != "极简界面" {
		t.Fatalf("FrontendName=%q", got.FrontendName)
	}
	if got.ModelserverProjectID != "proj-1" || got.AgentserverWorkspaceID != "workspace-1" {
		t.Fatalf("state=%+v", got)
	}
	if len(got.CompletedSteps) != 3 {
		t.Fatalf("CompletedSteps=%+v", got.CompletedSteps)
	}
}

func TestRemoveConsolePortFileIfMatchesKeepsNewerInstance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console-port.json")
	oldInfo := console.InstanceInfo{Port: 12345, PID: 111}
	newInfo := console.InstanceInfo{Port: 23456, PID: 222}
	if err := console.WriteInstanceInfo(path, oldInfo); err != nil {
		t.Fatal(err)
	}
	if err := console.WriteInstanceInfo(path, newInfo); err != nil {
		t.Fatal(err)
	}

	if err := removeConsolePortFileIfMatches(path, oldInfo); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("newer port file should remain: %v", err)
	}
}

func TestRemoveConsolePortFileIfMatchesRemovesMatchingInstance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console-port.json")
	info := console.InstanceInfo{Port: 12345, PID: 111}
	if err := console.WriteInstanceInfo(path, info); err != nil {
		t.Fatal(err)
	}

	if err := removeConsolePortFileIfMatches(path, info); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("matching port file should be removed, err=%v", err)
	}
}

func TestExecVSCodeEnsuresCodexConfigBeforeLaunch(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{
		VSCodeUserDataDir: filepath.Join(dir, "vscode-data"),
		VSCodeExtDir:      filepath.Join(dir, "vscode-extensions"),
		CodexConfigFile:   filepath.Join(dir, ".codex", "config.toml"),
	}

	err := execVSCode(filepath.Join(dir, "missing-code.exe"), p, "", nil, "")
	if err == nil {
		t.Fatal("expected missing VS Code executable error")
	}

	b, readErr := os.ReadFile(p.CodexConfigFile)
	if readErr != nil {
		t.Fatalf("expected codex config to be written before launching VS Code: %v", readErr)
	}
	s := string(b)
	for _, want := range []string{
		`model_provider = "modelserver"`,
		`[windows]`,
		`sandbox = "unelevated"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
}

func TestPreferredIconPathUsesCacheBustingIconWhenPresent(t *testing.T) {
	dir := t.TempDir()
	versioned := filepath.Join(dir, "icon-abc123.ico")
	if err := os.WriteFile(filepath.Join(dir, "icon.ico"), []byte("base"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(versioned, []byte("versioned"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := preferredIconPath(dir); got != versioned {
		t.Fatalf("preferredIconPath() = %q, want %q", got, versioned)
	}
}

func TestLaunchCompletedInstallMigratesVSCodeSettingsBeforeLaunch(t *testing.T) {
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
	prior := `{
	  "agentserverVscode.panel.allowed": ["terminal", "output"],
	  "custom.key": "keep me"
	}`
	if err := os.WriteFile(settingsPath, []byte(prior), 0o644); err != nil {
		t.Fatal(err)
	}

	err := launchCompletedInstall(context.Background(), filepath.Join(dir, "missing-code.exe"), p, nil, "", "")
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

func TestLaunchCompletedCodexDesktopWritesConfigAndOpensDeepLink(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{
		CodexConfigFile: filepath.Join(dir, ".codex", "config.toml"),
	}
	var opened string
	err := launchCompletedCodexDesktop(context.Background(), p, nil, "", func(url string) error {
		opened = url
		return nil
	})
	if err != nil {
		t.Fatalf("launchCompletedCodexDesktop: %v", err)
	}
	if opened != "codex://threads/new" {
		t.Fatalf("opened=%q", opened)
	}
	b, err := os.ReadFile(p.CodexConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `model_provider = "modelserver"`) {
		t.Fatalf("config missing modelserver provider:\n%s", b)
	}
}

func TestSyncInstallModeIfPresentPreservesExistingModeWhenFileMissing(t *testing.T) {
	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	if err := store.Update(func(s *state.State) error {
		s.FrontendMode = state.FrontendModeMinimalVSCode
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := installmode.SyncStoreIfPresent(store, filepath.Join(dir, "missing", "install-mode.json")); err != nil {
		t.Fatalf("SyncStoreIfPresent: %v", err)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.FrontendMode != state.FrontendModeMinimalVSCode {
		t.Fatalf("FrontendMode=%q, want %q", got.FrontendMode, state.FrontendModeMinimalVSCode)
	}
}
