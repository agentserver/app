package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/codex"
	"github.com/agentserver/agentserver-pkg/internal/console"
	"github.com/agentserver/agentserver-pkg/internal/installmode"
	"github.com/agentserver/agentserver-pkg/internal/modelproxy"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/state"
	"github.com/agentserver/agentserver-pkg/internal/tray"
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

func TestCompletedConsoleOrchestratorStartsModelserverLogin(t *testing.T) {
	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	if err := store.Update(func(s *state.State) error {
		s.Onboarding.Status = state.StatusComplete
		s.Onboarding.CompletedSteps = []string{"modelserver_login", "agentserver_login", "shortcuts_created"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	port := freeTCPPort(t)
	opened := make(chan string, 1)
	orch := newCompletedConsoleOrchestrator(completedOrchestratorInput{
		State:   store,
		Secrets: secrets.New(filepath.Join(dir, "secrets.json")),
		MSOAuth: oauth.AuthCodeConfig{
			Endpoint:     "https://codeapi.example",
			AuthPath:     "/oauth2/auth",
			TokenPath:    "/oauth2/token",
			ClientID:     "client-x",
			CallbackPath: "/oauth/modelserver/callback",
			Ports:        []int{port},
			LoginTimeout: time.Second,
		},
		OpenBrowser: func(url string) { opened <- url },
	})
	defer orch.Abort(context.Background())

	url, err := orch.LoginModelserver(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(url, "https://codeapi.example/oauth2/auth?") {
		t.Fatalf("oauth url=%q", url)
	}
	select {
	case got := <-opened:
		if got != url {
			t.Fatalf("opened=%q, want %q", got, url)
		}
	case <-time.After(time.Second):
		t.Fatal("OpenBrowser was not called")
	}
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestTrayStateFromConsoleFormatsQuotaRows(t *testing.T) {
	got := trayStateFromConsole(console.State{
		Quotas: []console.QuotaWindow{
			{Window: "5h", Percentage: 58.2, RemainingPercentage: 41.8},
			{Window: "7d", Percentage: 22.4, RemainingPercentage: 77.6},
		},
	})

	if got.FiveHour != "5小时额度：已用 58%，剩余约 42%" {
		t.Fatalf("FiveHour=%q", got.FiveHour)
	}
	if got.SevenDay != "7天额度：已用 22%，剩余约 78%" {
		t.Fatalf("SevenDay=%q", got.SevenDay)
	}
	wantTooltip := "星池指挥官\n5小时额度：已用 58%，剩余约 42%\n7天额度：已用 22%，剩余约 78%"
	if got.Tooltip != wantTooltip {
		t.Fatalf("Tooltip=%q, want %q", got.Tooltip, wantTooltip)
	}
}

func TestTrayStateFromConsoleDefaultsWhenQuotaUnavailable(t *testing.T) {
	got := trayStateFromConsole(console.State{})

	if got.FiveHour != "5小时额度：暂不可用" {
		t.Fatalf("FiveHour=%q", got.FiveHour)
	}
	if got.SevenDay != "7天额度：暂不可用" {
		t.Fatalf("SevenDay=%q", got.SevenDay)
	}
	wantTooltip := "星池指挥官\n5小时额度：暂不可用\n7天额度：暂不可用"
	if got.Tooltip != wantTooltip {
		t.Fatalf("Tooltip=%q, want %q", got.Tooltip, wantTooltip)
	}
}

func TestQuotaLabelFallsBackForUnknownWindow(t *testing.T) {
	if got := quotaLabel("monthly"); got != "monthly" {
		t.Fatalf("quotaLabel()=%q", got)
	}
}

func TestUpdateTrayOnceUpdatesStateAndSendsReminder(t *testing.T) {
	app := &fakeTrayApp{}
	ctrl := &fakeTrayController{
		state: console.State{
			Quotas: []console.QuotaWindow{
				{Window: "5h", Percentage: 50, RemainingPercentage: 50, ResetsAt: "reset-1"},
				{Window: "7d", Percentage: 42, RemainingPercentage: 58, ResetsAt: "reset-7"},
			},
		},
	}

	err := updateTrayOnce(context.Background(), app, ctrl, console.ReminderEngine{Store: console.NewMemoryReminderStore()})
	if err != nil {
		t.Fatal(err)
	}
	if ctrl.calls != 1 {
		t.Fatalf("State calls=%d", ctrl.calls)
	}
	if len(app.updates) != 1 || app.updates[0].FiveHour != "5小时额度：已用 50%，剩余约 50%" {
		t.Fatalf("updates=%+v", app.updates)
	}
	if len(app.notifications) != 1 {
		t.Fatalf("notifications=%+v", app.notifications)
	}
	if app.notifications[0].title != "星池指挥官额度提醒" {
		t.Fatalf("title=%q", app.notifications[0].title)
	}
	if app.notifications[0].message != "5小时额度已用 50%" {
		t.Fatalf("message=%q", app.notifications[0].message)
	}
}

func TestUpdateTrayOnceDoesNotRepeatSeenReminder(t *testing.T) {
	app := &fakeTrayApp{}
	ctrl := &fakeTrayController{
		state: console.State{
			Quotas: []console.QuotaWindow{
				{Window: "7d", Percentage: 80, RemainingPercentage: 20, ResetsAt: "reset-1"},
			},
		},
	}
	engine := console.ReminderEngine{Store: console.NewMemoryReminderStore()}

	if err := updateTrayOnce(context.Background(), app, ctrl, engine); err != nil {
		t.Fatal(err)
	}
	if err := updateTrayOnce(context.Background(), app, ctrl, engine); err != nil {
		t.Fatal(err)
	}
	if len(app.notifications) != 2 {
		t.Fatalf("notifications=%+v", app.notifications)
	}
	if app.notifications[0].message != "7天额度已用 50%" || app.notifications[1].message != "7天额度已用 80%" {
		t.Fatalf("notifications=%+v", app.notifications)
	}
}

func TestUpdateTrayOnceReturnsStateError(t *testing.T) {
	app := &fakeTrayApp{}
	stateErr := errors.New("state failed")
	ctrl := &fakeTrayController{err: stateErr}

	err := updateTrayOnce(context.Background(), app, ctrl, console.ReminderEngine{Store: console.NewMemoryReminderStore()})
	if !errors.Is(err, stateErr) {
		t.Fatalf("err=%v, want %v", err, stateErr)
	}
	if len(app.updates) != 0 {
		t.Fatalf("updates=%+v", app.updates)
	}
}

type fakeTrayController struct {
	state console.State
	err   error
	calls int
}

func (f *fakeTrayController) State(context.Context) (console.State, error) {
	f.calls++
	return f.state, f.err
}

type fakeTrayNotification struct {
	title   string
	message string
}

type fakeTrayApp struct {
	updates       []tray.State
	notifications []fakeTrayNotification
}

func (f *fakeTrayApp) Run(context.Context, tray.Actions) error { return nil }

func (f *fakeTrayApp) Update(st tray.State) {
	f.updates = append(f.updates, st)
}

func (f *fakeTrayApp) Notify(title, message string) error {
	f.notifications = append(f.notifications, fakeTrayNotification{title: title, message: message})
	return nil
}

func TestStopTrayAndWaitCancelsAndObservesDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		<-ctx.Done()
		close(done)
	}()

	if ok := stopTrayAndWait(cancel, done, time.Second); !ok {
		t.Fatal("tray shutdown should complete")
	}
}

func TestStopTrayAndWaitTimesOut(t *testing.T) {
	cancelCalled := false
	done := make(chan struct{})

	ok := stopTrayAndWait(func() {
		cancelCalled = true
	}, done, time.Nanosecond)

	if ok {
		t.Fatal("tray shutdown should time out")
	}
	if !cancelCalled {
		t.Fatal("cancel was not called")
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
		`base_url = "` + modelproxy.DefaultBaseURL + `"`,
		`env_key = "` + codex.LocalProxyAPIKeyEnv + `"`,
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
		CodexConfigFile:                   filepath.Join(dir, ".codex", "config.toml"),
		CodexDesktopGlobalStateFile:       filepath.Join(dir, ".codex", ".codex-global-state.json"),
		CodexDesktopComputerUseConfigFile: filepath.Join(dir, ".codex", "computer-use", "config.json"),
	}
	var opened string
	err := launchCompletedCodexDesktop(context.Background(), p, nil, "", func(url string) error {
		assertJSONField(t, p.CodexDesktopGlobalStateFile, "localeOverride", "zh-CN")
		assertJSONField(t, p.CodexDesktopComputerUseConfigFile, "locale", "zh-CN")
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
	if !strings.Contains(string(b), `base_url = "`+modelproxy.DefaultBaseURL+`"`) {
		t.Fatalf("config missing local proxy base_url:\n%s", b)
	}
	if !strings.Contains(string(b), `env_key = "`+codex.LocalProxyAPIKeyEnv+`"`) {
		t.Fatalf("config missing local proxy env_key:\n%s", b)
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
