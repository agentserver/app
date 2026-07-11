package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/appversion"
	"github.com/agentserver/agentserver-pkg/internal/codex"
	"github.com/agentserver/agentserver-pkg/internal/codexdesktop"
	"github.com/agentserver/agentserver-pkg/internal/console"
	"github.com/agentserver/agentserver-pkg/internal/installmode"
	"github.com/agentserver/agentserver-pkg/internal/modelproxy"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/agentserver/agentserver-pkg/internal/opencodedesktop"
	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/slave"
	"github.com/agentserver/agentserver-pkg/internal/state"
	"github.com/agentserver/agentserver-pkg/internal/tray"
	"github.com/agentserver/agentserver-pkg/internal/ui"
	"github.com/agentserver/agentserver-pkg/internal/updater"
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

func TestLauncherOptionsIgnoresUnknownFlag(t *testing.T) {
	got := parseLauncherOptions([]string{"--backgrond"})
	if got.Background || !got.OpenPage || !got.OpenFrontend {
		t.Fatalf("options=%+v, want default foreground behavior", got)
	}
}

func TestStartCompletedConsoleUsesBackgroundOption(t *testing.T) {
	var gotExe string
	var gotArgs []string
	err := startCompletedConsoleWithStarter(context.Background(), `C:\Program Files\agentserver\launcher.exe`, func(exe string, args ...string) error {
		gotExe = exe
		gotArgs = append([]string(nil), args...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotExe != `C:\Program Files\agentserver\launcher.exe` {
		t.Fatalf("exe=%q", gotExe)
	}
	if len(gotArgs) != 1 || gotArgs[0] != "--background" {
		t.Fatalf("args=%q, want [--background]", gotArgs)
	}
}

func TestSanitizedFrontendLaunchErrorMinimalVSCodeOmitsRawDetails(t *testing.T) {
	raw := errors.New(`launch C:\Users\alice\Code.exe --api-key top-secret HKEY_CLASSES_ROOT\vscode`)
	safeErr := ui.SafeFrontendLaunchError(state.FrontendModeMinimalVSCode, raw)
	if safeErr == nil {
		t.Fatal("SafeFrontendLaunchError returned nil")
	}
	got := safeErr.Error()
	want := "极简界面启动失败。请确认 VS Code 已安装且可正常打开，然后重试。"
	if got != want {
		t.Fatalf("sanitized error=%q, want %q", got, want)
	}
	for _, forbidden := range []string{"alice", "top-secret", "HKEY_CLASSES_ROOT"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("sanitized error leaked %q: %q", forbidden, got)
		}
	}
}

func TestLaunchCompletedFrontendAndRecordReturnsAndPersistsSafeErrorsForAllModes(t *testing.T) {
	tests := []struct {
		name string
		mode state.FrontendMode
		wrap func(error) error
		want string
	}{
		{
			name: "ChatGPT Codex",
			mode: state.FrontendModeCodexDesktop,
			wrap: func(cause error) error {
				return fmt.Errorf("%w: %w", codexdesktop.ErrLaunchFailed, cause)
			},
			want: "ChatGPT / Codex 桌面应用本身无法启动。请在 Windows 已安装的应用 > ChatGPT > 高级选项中依次尝试 Repair、Reset；仍失败请从 Microsoft Store Reinstall。",
		},
		{
			name: "OpenCode Desktop",
			mode: state.FrontendModeOpenCodeDesktop,
			wrap: func(cause error) error { return cause },
			want: "OpenCode Desktop 启动失败。请确认应用已安装且可正常打开，然后重试。",
		},
		{
			name: "minimal VS Code",
			mode: state.FrontendModeMinimalVSCode,
			wrap: func(cause error) error { return cause },
			want: "极简界面启动失败。请确认 VS Code 已安装且可正常打开，然后重试。",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			store := state.NewStore(filepath.Join(dir, "state.json"))
			if err := store.Update(func(s *state.State) error {
				s.FrontendMode = tt.mode
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			cause := errors.New(`open C:\Users\alice\secret.txt token=top-secret HKEY_CLASSES_ROOT\codex`)
			launchErr := tt.wrap(cause)

			err := launchCompletedFrontendAndRecord(context.Background(), store, func(context.Context, *state.State) error {
				return launchErr
			})

			assertSafeFrontendError(t, err, tt.want)
			if !errors.Is(err, cause) {
				t.Fatalf("errors.Is(err, cause)=false; err=%v", err)
			}
			if tt.mode == state.FrontendModeCodexDesktop && !errors.Is(err, codexdesktop.ErrLaunchFailed) {
				t.Fatalf("errors.Is(err, ErrLaunchFailed)=false; err=%v", err)
			}
			persisted, loadErr := store.Load()
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			assertSafeFrontendMessage(t, persisted.FrontendError, tt.want)
		})
	}
}

func TestLaunchCompletedFrontendAndRecordSanitizesPersistenceFailureAfterLaunch(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "CUsers-alice-secret.txt-token=top-secret-HKEY_CLASSES_ROOT-codex")
	statePath := filepath.Join(stateDir, "state.json")
	store := state.NewStore(statePath)
	if err := store.Update(func(s *state.State) error {
		s.FrontendMode = state.FrontendModeMinimalVSCode
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	launched := false

	err := launchCompletedFrontendAndRecord(context.Background(), store, func(context.Context, *state.State) error {
		launched = true
		if err := os.RemoveAll(stateDir); err != nil {
			return err
		}
		if err := os.WriteFile(stateDir, []byte("block state directory"), 0o600); err != nil {
			return err
		}
		return nil
	})

	if !launched {
		t.Fatal("frontend launch callback did not run")
	}
	assertSafeFrontendError(t, err, "前端已启动，但无法保存启动状态。请重试。")
}

func TestLaunchCompletedFrontendAndRecordSanitizesStateReadFailure(t *testing.T) {
	dir := t.TempDir()
	blockedParent := filepath.Join(dir, "alice-secret.txt-token=top-secret-HKEY_CLASSES_ROOT-codex")
	if err := os.WriteFile(blockedParent, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(filepath.Join(blockedParent, "state.json"))
	launched := false

	err := launchCompletedFrontendAndRecord(context.Background(), store, func(context.Context, *state.State) error {
		launched = true
		return nil
	})

	if launched {
		t.Fatal("frontend launch ran after state read failure")
	}
	assertSafeFrontendError(t, err, "无法读取前端启动状态，请重试。")
}

func assertSafeFrontendError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected frontend error")
	}
	assertSafeFrontendMessage(t, err.Error(), want)
}

func assertSafeFrontendMessage(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("frontend error=%q, want %q", got, want)
	}
	for _, forbidden := range []string{`C:\Users\alice\secret.txt`, "alice", "token=top-secret", "HKEY_CLASSES_ROOT\\codex"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("frontend error leaked %q: %q", forbidden, got)
		}
	}
}

func TestCompletedLauncherReusesExistingConsole(t *testing.T) {
	called := launcherCalls{}
	err := runCompletedConsole(context.Background(), completedConsoleDeps{
		Options:  launcherOptions{OpenPage: true, OpenFrontend: true},
		PortFile: "ignored",
		Discover: func(context.Context, string) (console.InstanceInfo, bool) {
			return console.InstanceInfo{Port: 34567, Token: "token-123"}, true
		},
		OpenBrowser: func(url string) error {
			called.openedURL = url
			return nil
		},
		Post: func(ctx context.Context, url, token string) error {
			called.posted = append(called.posted, url)
			called.postedToken = token
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
	if called.postedToken != "token-123" {
		t.Fatalf("postedToken=%q", called.postedToken)
	}
}

type launcherCalls struct {
	openedURL   string
	posted      []string
	postedToken string
}

func TestCompletedLauncherAttemptsFrontendWhenOpenPageFails(t *testing.T) {
	called := launcherCalls{}
	browserErr := errors.New("browser failed")
	err := runCompletedConsole(context.Background(), completedConsoleDeps{
		Options:  launcherOptions{OpenPage: true, OpenFrontend: true},
		PortFile: "ignored",
		Discover: func(context.Context, string) (console.InstanceInfo, bool) {
			return console.InstanceInfo{Port: 34567, Token: "token-123"}, true
		},
		OpenBrowser: func(url string) error {
			called.openedURL = url
			return browserErr
		},
		Post: func(ctx context.Context, url, token string) error {
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

func TestCompletedSlaveManagerDepsRecoversBadMachineIdentity(t *testing.T) {
	dir := t.TempDir()
	machinePath := filepath.Join(dir, ".agentserver-app", "machine.json")
	if err := os.MkdirAll(filepath.Dir(machinePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(machinePath, []byte(`{"machine_id":"broken"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COMPUTERNAME", "RECOVERED-PC")

	deps, err := completedSlaveManagerDeps(completedServeInput{
		Paths: paths.Paths{
			MachineFile:  machinePath,
			SlavesFile:   filepath.Join(dir, ".agentserver-app", "slaves.json"),
			SlavesDir:    filepath.Join(dir, ".agentserver-app", "slaves"),
			CodexDir:     filepath.Join(dir, ".codex"),
			CodexExePath: filepath.Join(dir, "local-appdata", "agentserver-app", "bin", "codex.exe"),
		},
		InstallDir: filepath.Join(dir, "app"),
	})
	if err != nil {
		t.Fatalf("completedSlaveManagerDeps: %v", err)
	}
	got, err := deps.Machines.Load()
	if err != nil {
		t.Fatalf("Load recovered machine identity: %v", err)
	}
	if got.ComputerName != "RECOVERED-PC" || got.MachineID == "" {
		t.Fatalf("machine=%+v", got)
	}
}

func TestServeCompletedConsoleRefreshesLoomDriverFromChineseMachineName(t *testing.T) {
	dir := t.TempDir()
	installDir := createLauncherTestDriverInstall(t, dir)
	p := paths.Paths{
		UserHome:                 dir,
		InstallRoot:              filepath.Join(dir, ".agentserver-app"),
		MachineFile:              filepath.Join(dir, ".agentserver-app", "machine.json"),
		SlavesFile:               filepath.Join(dir, ".agentserver-app", "slaves.json"),
		SlavesDir:                filepath.Join(dir, ".agentserver-app", "slaves"),
		ConsolePortFile:          filepath.Join(dir, ".agentserver-app", "console-port.json"),
		PendingSlaveRestartsFile: filepath.Join(dir, ".agentserver-app", "pending-slave-restarts.json"),
		ConsoleNotificationsFile: filepath.Join(dir, ".agentserver-app", "console-notifications.json"),
		UpdateStateFile:          filepath.Join(dir, ".agentserver-app", "update-state.json"),
		UpdatesCacheDir:          filepath.Join(dir, ".agentserver-app", "updates"),
		CodexExePath:             filepath.Join(dir, "bin", "codex.exe"),
		CodexConfigFile:          filepath.Join(dir, ".codex", "config.toml"),
	}
	if _, err := slave.NewMachineStore(p.MachineFile).Ensure("测试电脑"); err != nil {
		t.Fatal(err)
	}
	st := launcherTestDriverState()
	st.SchemaVersion = state.CurrentSchemaVersion
	st.Onboarding.Status = state.StatusComplete
	store := state.NewStore(filepath.Join(p.InstallRoot, "state.json"))
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
	if err := console.NewDriverDaemonStore(driverDaemonStatePath(p)).Save(console.DriverDaemonPersistedState{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	loomPath := filepath.Join(dir, ".config", "multi-agent", "driver.yaml")
	if err := os.MkdirAll(filepath.Dir(loomPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(loomPath, []byte(`display_name: "WIN-8650DR8KQKD"`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sec := launcherTestDriverSecrets(t, dir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- serveCompletedConsole(ctx, completedServeInput{
			Paths:      p,
			State:      store,
			Secrets:    sec,
			InstallDir: installDir,
			Options:    launcherOptions{OpenPage: false, OpenFrontend: false},
			OpenBrowser: func(string) error {
				return nil
			},
		})
	}()
	info := waitConsolePortForTest(t, p.ConsolePortFile, errCh)
	defer stopConsoleForTest(t, info, errCh)

	text := waitFileContainsForTest(t, loomPath, `display_name: "测试电脑"`)
	if !strings.Contains(text, `description: "测试电脑 本地协作驱动。"`) {
		t.Fatalf("driver.yaml missing Chinese description:\n%s", text)
	}
}

func TestServeCompletedConsoleAutomaticFrontendFailurePersistsAndSuccessClears(t *testing.T) {
	in, store := completedConsoleLaunchTestInput(t, state.FrontendModeCodexDesktop)
	in.Options.OpenFrontend = true
	var calls atomic.Int32
	callCh := make(chan int32, 3)
	in.LaunchFrontend = func(context.Context, *state.State) error {
		call := calls.Add(1)
		callCh <- call
		if call == 1 {
			return fmt.Errorf("%w: ChatGPT / Codex launch failed; Repair Reset Reinstall; C:\\Users\\alice\\secret.txt; HKEY_CLASSES_ROOT\\codex token=top-secret", codexdesktop.ErrLaunchFailed)
		}
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- serveCompletedConsole(ctx, in) }()
	info := waitConsolePortForTest(t, in.Paths.ConsolePortFile, errCh)
	defer stopConsoleForTest(t, info, errCh)

	select {
	case call := <-callCh:
		if call != 1 {
			t.Fatalf("first automatic launch call=%d", call)
		}
	case <-time.After(time.Second):
		t.Fatal("automatic frontend launch did not run")
	}
	want := "ChatGPT / Codex 桌面应用本身无法启动。请在 Windows 已安装的应用 > ChatGPT > 高级选项中依次尝试 Repair、Reset；仍失败请从 Microsoft Store Reinstall。"
	if got := waitFrontendErrorForTest(t, store, func(value string) bool { return value != "" }); got != want {
		t.Fatalf("FrontendError=%q, want %q", got, want)
	}
	select {
	case call := <-callCh:
		t.Fatalf("automatic frontend launch ran more than once; call=%d", call)
	case <-time.After(100 * time.Millisecond):
	}

	base := fmt.Sprintf("http://127.0.0.1:%d", info.Port)
	if err := postConsole(context.Background(), base+"/api/console/open-frontend", info.Token); err != nil {
		t.Fatalf("later successful frontend launch: %v", err)
	}
	if got := waitFrontendErrorForTest(t, store, func(value string) bool { return value == "" }); got != "" {
		t.Fatalf("FrontendError=%q, want cleared after successful launch", got)
	}
	if calls.Load() != 2 {
		t.Fatalf("launch calls=%d, want one automatic failure and one manual success", calls.Load())
	}
}

func TestServeCompletedConsoleTrayFrontendFailurePersistsAndSuccessClears(t *testing.T) {
	in, store := completedConsoleLaunchTestInput(t, state.FrontendModeOpenCodeDesktop)
	actionsCh := make(chan tray.Actions, 1)
	in.TrayApp = &capturingTrayApp{actions: actionsCh}
	var calls atomic.Int32
	in.LaunchFrontend = func(context.Context, *state.State) error {
		if calls.Add(1) == 1 {
			return errors.New(`open C:\Users\alice\AppData\OpenCode.exe --api-key top-secret HKEY_CLASSES_ROOT\opencode`)
		}
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- serveCompletedConsole(ctx, in) }()
	info := waitConsolePortForTest(t, in.Paths.ConsolePortFile, errCh)
	defer stopConsoleForTest(t, info, errCh)

	var actions tray.Actions
	select {
	case actions = <-actionsCh:
	case <-time.After(time.Second):
		t.Fatal("tray actions were not registered")
	}
	actions.OpenFrontend()
	want := "OpenCode Desktop 启动失败。请确认应用已安装且可正常打开，然后重试。"
	if got := waitFrontendErrorForTest(t, store, func(value string) bool { return value != "" }); got != want {
		t.Fatalf("FrontendError=%q, want %q", got, want)
	}
	actions.OpenFrontend()
	if got := waitFrontendErrorForTest(t, store, func(value string) bool { return value == "" }); got != "" {
		t.Fatalf("FrontendError=%q, want cleared after tray launch succeeds", got)
	}
	if calls.Load() != 2 {
		t.Fatalf("tray launch calls=%d, want 2", calls.Load())
	}
}

func TestServeCompletedConsoleOpenFrontendHTTPReturnsOnlySanitizedError(t *testing.T) {
	in, store := completedConsoleLaunchTestInput(t, state.FrontendModeCodexDesktop)
	cause := errors.New(`open C:\Users\alice\secret.txt token=top-secret HKEY_CLASSES_ROOT\codex`)
	in.LaunchFrontend = func(context.Context, *state.State) error {
		return fmt.Errorf("%w: %w", codexdesktop.ErrLaunchFailed, cause)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- serveCompletedConsole(ctx, in) }()
	info := waitConsolePortForTest(t, in.Paths.ConsolePortFile, errCh)
	defer stopConsoleForTest(t, info, errCh)

	base := fmt.Sprintf("http://127.0.0.1:%d", info.Port)
	req, err := http.NewRequest(http.MethodPost, base+"/api/console/open-frontend", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(ui.ConsoleInstanceTokenHeader, info.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	want := "ChatGPT / Codex 桌面应用本身无法启动。请在 Windows 已安装的应用 > ChatGPT > 高级选项中依次尝试 Repair、Reset；仍失败请从 Microsoft Store Reinstall。"
	if body["error"] != want {
		t.Fatalf("HTTP error=%q, want sanitized diagnosis %q", body["error"], want)
	}
	for _, forbidden := range []string{`C:\Users\alice\secret.txt`, "token=top-secret", "HKEY_CLASSES_ROOT\\codex"} {
		if strings.Contains(body["error"], forbidden) {
			t.Fatalf("HTTP error leaked %q: %q", forbidden, body["error"])
		}
	}
	persisted, loadErr := store.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	assertSafeFrontendMessage(t, persisted.FrontendError, want)
}

func completedConsoleLaunchTestInput(t *testing.T, mode state.FrontendMode) (completedServeInput, *state.Store) {
	t.Helper()
	dir := t.TempDir()
	p := paths.Paths{
		UserHome:                 dir,
		InstallRoot:              filepath.Join(dir, ".agentserver-app"),
		StateFile:                filepath.Join(dir, ".agentserver-app", "state.json"),
		SecretsFile:              filepath.Join(dir, ".agentserver-app", "secrets.json"),
		MachineFile:              filepath.Join(dir, ".agentserver-app", "machine.json"),
		SlavesFile:               filepath.Join(dir, ".agentserver-app", "slaves.json"),
		SlavesDir:                filepath.Join(dir, ".agentserver-app", "slaves"),
		ConsolePortFile:          filepath.Join(dir, ".agentserver-app", "console-port.json"),
		PendingSlaveRestartsFile: filepath.Join(dir, ".agentserver-app", "pending-slave-restarts.json"),
		ConsoleNotificationsFile: filepath.Join(dir, ".agentserver-app", "console-notifications.json"),
		UpdateStateFile:          filepath.Join(dir, ".agentserver-app", "update-state.json"),
		UpdatesCacheDir:          filepath.Join(dir, ".agentserver-app", "updates"),
		CodexExePath:             filepath.Join(dir, "bin", "codex.exe"),
		CodexConfigFile:          filepath.Join(dir, ".codex", "config.toml"),
		OpenCodeConfigFile:       filepath.Join(dir, ".config", "opencode", "opencode.jsonc"),
	}
	store := state.NewStore(p.StateFile)
	if err := store.Update(func(s *state.State) error {
		s.Onboarding.Status = state.StatusComplete
		s.FrontendMode = mode
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return completedServeInput{
		Paths:       p,
		State:       store,
		Secrets:     secrets.New(p.SecretsFile),
		InstallDir:  filepath.Join(dir, "install"),
		OpenBrowser: func(string) error { return nil },
	}, store
}

func waitFrontendErrorForTest(t *testing.T, store *state.Store, ready func(string) bool) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		st, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		last = st.FrontendError
		if ready(last) {
			return last
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for frontend error state; last=%q", last)
	return ""
}

type capturingTrayApp struct {
	actions chan<- tray.Actions
}

func (a *capturingTrayApp) Run(ctx context.Context, actions tray.Actions) error {
	select {
	case a.actions <- actions:
	case <-ctx.Done():
		return ctx.Err()
	}
	<-ctx.Done()
	return ctx.Err()
}

func (*capturingTrayApp) Update(tray.State) {}

func (*capturingTrayApp) Notify(string, string) error { return nil }

func TestNewCompletedUpdaterUsesDefaultManifestAndPaths(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{
		UpdateStateFile: filepath.Join(dir, "update-state.json"),
		UpdatesCacheDir: filepath.Join(dir, "updates"),
	}

	got := newCompletedUpdater(p)

	if got.ManifestURL != updater.DefaultManifestURL {
		t.Fatalf("ManifestURL=%q, want %q", got.ManifestURL, updater.DefaultManifestURL)
	}
	if got.CurrentVersion != appversion.Version {
		t.Fatalf("CurrentVersion=%q, want %q", got.CurrentVersion, appversion.Version)
	}
	if got.CacheDir != p.UpdatesCacheDir {
		t.Fatalf("CacheDir=%q, want %q", got.CacheDir, p.UpdatesCacheDir)
	}
	if got.State == nil {
		t.Fatal("State is nil")
	}
	if len(got.Sources) != 2 {
		t.Fatalf("Sources len=%d, want 2", len(got.Sources))
	}
	if got.Sources[0].Name() != "github" || got.Sources[1].Name() != "cdn" {
		t.Fatalf("Sources order=[%s,%s], want [github,cdn]", got.Sources[0].Name(), got.Sources[1].Name())
	}
}

func TestAutomaticUpdateCheckCancellationBeforeDelayPreventsManifestRequest(t *testing.T) {
	hit := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit <- struct{}{}
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	done := scheduleAutomaticUpdateCheck(ctx, &updater.Service{
		CurrentVersion: appversion.Version,
		ManifestURL:    server.URL,
		State:          updater.NewStateStore(filepath.Join(t.TempDir(), "state.json")),
	}, 25*time.Millisecond)
	cancel()
	waitAutomaticUpdateCheckStopped(t, done)

	select {
	case <-hit:
		t.Fatal("manifest server was hit after cancellation")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestAutomaticUpdateCheckRunsOnceAndOnlyChecksManifest(t *testing.T) {
	manifestHit := make(chan struct{}, 1)
	updateVersion := nextPatchVersion(t, appversion.Version)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case manifestHit <- struct{}{}:
		default:
			t.Errorf("manifest server hit more than once")
		}
		manifest := updater.Manifest{
			Version: updateVersion,
			URL:     "https://assets.agent.cs.ac.cn/agentserver-app/windows/agentserver-app-" + updateVersion + "-setup.exe",
			SHA256:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Size:    123,
			Notes:   "release notes",
		}
		if err := json.NewEncoder(w).Encode(manifest); err != nil {
			t.Fatalf("Encode manifest: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	statePath := filepath.Join(t.TempDir(), "update-state.json")
	store := updater.NewStateStore(statePath)
	ctx, cancel := context.WithCancel(context.Background())
	done := scheduleAutomaticUpdateCheck(ctx, &updater.Service{
		CurrentVersion: appversion.Version,
		ManifestURL:    server.URL,
		State:          store,
		StartInstaller: func(context.Context, string) error {
			t.Fatal("StartInstaller should not be called by automatic manifest check")
			return nil
		},
	}, 10*time.Millisecond)
	defer func() {
		cancel()
		waitAutomaticUpdateCheckStopped(t, done)
	}()

	select {
	case <-manifestHit:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for automatic manifest check")
	}
	time.Sleep(50 * time.Millisecond)
	cancel()

	saved, err := store.Load()
	if err != nil {
		t.Fatalf("Load saved update state: %v", err)
	}
	if saved.Status != updater.StatusAvailable {
		t.Fatalf("saved Status=%q, want %q", saved.Status, updater.StatusAvailable)
	}
}

func nextPatchVersion(t *testing.T, version string) string {
	t.Helper()
	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	if len(parts) != 3 {
		t.Fatalf("version %q should be MAJOR.MINOR.PATCH", version)
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		t.Fatalf("version %q patch should be numeric: %v", version, err)
	}
	return parts[0] + "." + parts[1] + "." + strconv.Itoa(patch+1)
}

func TestAutomaticUpdateCheckLogsNonCanceledErrors(t *testing.T) {
	var logs lockedLogBuffer
	previousWriter := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "manifest failed", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	done := scheduleAutomaticUpdateCheck(ctx, &updater.Service{
		CurrentVersion: appversion.Version,
		ManifestURL:    server.URL,
		State:          updater.NewStateStore(filepath.Join(t.TempDir(), "state.json")),
	}, 10*time.Millisecond)
	defer func() {
		cancel()
		waitAutomaticUpdateCheckStopped(t, done)
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(logs.String(), "launcher: automatic update check:") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("automatic update check error was not logged; logs=%q", logs.String())
}

func TestAutomaticUpdateCheckRetriesSoonAfterError(t *testing.T) {
	hits := make(chan time.Time, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits <- time.Now()
		http.Error(w, "manifest failed", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	done := scheduleAutomaticUpdateCheckWithRetry(ctx, &updater.Service{
		CurrentVersion: appversion.Version,
		ManifestURL:    server.URL,
		State:          updater.NewStateStore(filepath.Join(t.TempDir(), "state.json")),
		AutoCheckEvery: time.Nanosecond,
	}, time.Millisecond, time.Hour, 20*time.Millisecond, 50*time.Millisecond, func(d time.Duration) time.Duration {
		return d
	})

	select {
	case <-hits:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first automatic update check")
	}
	select {
	case <-hits:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("automatic update check did not retry soon after error")
	}
	cancel()
	waitAutomaticUpdateCheckStopped(t, done)
}

func TestAutomaticUpdateCheckAppliesSuccessJitter(t *testing.T) {
	hits := make(chan time.Time, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits <- time.Now()
		manifest := updater.Manifest{
			Version: "99.0.0",
			URL:     "https://assets.agent.cs.ac.cn/agentserver-app/windows/agentserver-app-99.0.0-setup.exe",
			SHA256:  strings.Repeat("a", 64),
			Size:    123,
		}
		if err := json.NewEncoder(w).Encode(manifest); err != nil {
			t.Fatalf("Encode manifest: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	done := scheduleAutomaticUpdateCheckWithRetry(ctx, &updater.Service{
		CurrentVersion: appversion.Version,
		ManifestURL:    server.URL,
		State:          updater.NewStateStore(filepath.Join(t.TempDir(), "state.json")),
		AutoCheckEvery: time.Nanosecond,
	}, time.Millisecond, time.Hour, time.Hour, 50*time.Millisecond, func(time.Duration) time.Duration {
		return 20 * time.Millisecond
	})

	select {
	case <-hits:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first automatic update check")
	}
	select {
	case <-hits:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("automatic update check did not use success jitter delay")
	}
	cancel()
	waitAutomaticUpdateCheckStopped(t, done)
}

func TestAutomaticUpdateCheckTimesOutBlockingManifestRequest(t *testing.T) {
	var logs lockedLogBuffer
	previousWriter := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
	})

	requestCanceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		close(requestCanceled)
	}))
	t.Cleanup(server.Close)

	store := updater.NewStateStore(filepath.Join(t.TempDir(), "state.json"))
	ctx, cancel := context.WithCancel(context.Background())
	done := scheduleAutomaticUpdateCheckWithTiming(ctx, &updater.Service{
		CurrentVersion: appversion.Version,
		ManifestURL:    server.URL,
		State:          store,
	}, 1*time.Millisecond, time.Hour, 25*time.Millisecond)
	defer func() {
		cancel()
		waitAutomaticUpdateCheckStopped(t, done)
	}()

	select {
	case <-requestCanceled:
	case <-time.After(time.Second):
		t.Fatal("blocking manifest request was not canceled by automatic check timeout")
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		saved, err := store.Load()
		if err == nil && saved.Status == updater.StatusError && strings.Contains(saved.LastError, "context deadline exceeded") && strings.Contains(logs.String(), "launcher: automatic update check:") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	saved, _ := store.Load()
	t.Fatalf("automatic update timeout was not logged and saved; state=%+v logs=%q", saved, logs.String())
}

type lockedLogBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (b *lockedLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func TestAutomaticUpdateCheckStopsAfterContextCancel(t *testing.T) {
	hits := make(chan struct{}, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits <- struct{}{}
		http.Error(w, "manifest failed", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	done := scheduleAutomaticUpdateCheckWithRetry(ctx, &updater.Service{
		CurrentVersion: appversion.Version,
		ManifestURL:    server.URL,
		State:          updater.NewStateStore(filepath.Join(t.TempDir(), "state.json")),
		AutoCheckEvery: time.Nanosecond,
	}, time.Millisecond, time.Hour, 20*time.Millisecond, 50*time.Millisecond, func(d time.Duration) time.Duration {
		return d
	})

	select {
	case <-hits:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first automatic update check")
	}
	cancel()
	waitAutomaticUpdateCheckStopped(t, done)

	select {
	case <-hits:
		t.Fatal("manifest server was hit after automatic update context cancellation")
	case <-time.After(100 * time.Millisecond):
	}
}

func waitAutomaticUpdateCheckStopped(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("automatic update check goroutine did not stop")
	}
}

func waitConsolePortForTest(t *testing.T, path string, errCh <-chan error) console.InstanceInfo {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			t.Fatalf("serveCompletedConsole returned before writing port file: %v", err)
		default:
		}
		b, err := os.ReadFile(path)
		if err == nil {
			var info console.InstanceInfo
			if json.Unmarshal(b, &info) == nil && info.Port > 0 && info.Token != "" {
				return info
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for console port file %s", path)
	return console.InstanceInfo{}
}

func stopConsoleForTest(t *testing.T, info console.InstanceInfo, errCh <-chan error) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:"+strconv.Itoa(info.Port)+"/api/console/quit", nil)
	if err != nil {
		t.Errorf("create console quit request: %v", err)
		return
	}
	req.Header.Set(ui.ConsoleInstanceTokenHeader, info.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Errorf("post console quit: %v", err)
	} else {
		resp.Body.Close()
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("serveCompletedConsole returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Errorf("serveCompletedConsole did not stop")
	}
}

func waitFileContainsForTest(t *testing.T, path, want string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last string
	var lastErr error
	for time.Now().Before(deadline) {
		body, err := os.ReadFile(path)
		if err == nil {
			last = string(body)
			if strings.Contains(last, want) {
				return last
			}
		} else {
			lastErr = err
		}
		time.Sleep(10 * time.Millisecond)
	}
	if lastErr != nil && last == "" {
		t.Fatalf("timed out waiting for %s in %s: %v", want, path, lastErr)
	}
	t.Fatalf("timed out waiting for %s in %s:\n%s", want, path, last)
	return ""
}

func TestRestorePendingSlaveRestartsCallsManager(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pending-slave-restarts.json")
	if err := os.WriteFile(path, []byte(`{"reason":"app_update","version":"0.1.2","created_at":"2026-06-12T00:00:00Z","slave_ids":["a","b"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var got []string

	err := restorePendingSlaveRestarts(context.Background(), path, "0.1.2", func(_ context.Context, id string) error {
		got = append(got, id)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if strings.Join(got, ",") != "a,b" {
		t.Fatalf("restarted=%v, want [a b]", got)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending file err=%v, want removed", err)
	}
}

func TestRestorePendingSlaveRestartsOlderCurrentVersionKeepsFileAndSkipsRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pending-slave-restarts.json")
	if err := os.WriteFile(path, []byte(`{"reason":"app_update","version":"0.1.2","created_at":"2026-06-12T00:00:00Z","slave_ids":["a"]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	err := restorePendingSlaveRestarts(context.Background(), path, "0.1.1", func(context.Context, string) error {
		t.Fatal("restart should not be called when current version is older than pending target")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("pending file should remain for later app update: %v", err)
	}
}

func TestRestorePendingSlaveRestartsNewerCurrentVersionRestoresAndRemovesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pending-slave-restarts.json")
	if err := os.WriteFile(path, []byte(`{"reason":"app_update","version":"0.1.2","created_at":"2026-06-12T00:00:00Z","slave_ids":["a","b"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var got []string

	err := restorePendingSlaveRestarts(context.Background(), path, "0.1.3", func(_ context.Context, id string) error {
		got = append(got, id)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "a,b" {
		t.Fatalf("restarted=%v, want [a b]", got)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending file err=%v, want removed", err)
	}
}

func TestRestorePendingSlaveRestartsInvalidVersionReturnsErrorAndKeepsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pending-slave-restarts.json")
	if err := os.WriteFile(path, []byte(`{"reason":"app_update","version":"broken","created_at":"2026-06-12T00:00:00Z","slave_ids":["a"]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	err := restorePendingSlaveRestarts(context.Background(), path, "0.1.2", func(context.Context, string) error {
		t.Fatal("restart should not be called when version comparison fails")
		return nil
	})
	if err == nil {
		t.Fatal("expected version comparison error")
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("pending file should remain after version comparison error: %v", statErr)
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
		FrontendName: "OpenCode Desktop",
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
	if got.OpenFrontendLabel != "启动 OpenCode Desktop" {
		t.Fatalf("OpenFrontendLabel=%q", got.OpenFrontendLabel)
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
	if got.OpenFrontendLabel != "启动 ChatGPT / Codex" {
		t.Fatalf("OpenFrontendLabel=%q", got.OpenFrontendLabel)
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

func TestPreferredCompletedConsoleInstanceReadsStaleInstanceFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console-port.json")
	if err := console.WriteInstanceInfo(path, console.InstanceInfo{Port: 58212, PID: 123, Token: "old-token"}); err != nil {
		t.Fatal(err)
	}

	got := preferredCompletedConsoleInstance(path)
	if got.Port != 58212 || got.Token != "old-token" {
		t.Fatalf("preferredCompletedConsoleInstance=%+v, want port 58212 and old token", got)
	}
}

func TestPreferredCompletedConsoleInstanceAcceptsUTF8BOM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console-port.json")
	body := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"port":58212,"pid":123,"token":"old-token"}`)...)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}

	got := preferredCompletedConsoleInstance(path)
	if got.Port != 58212 || got.Token != "old-token" {
		t.Fatalf("preferredCompletedConsoleInstance with BOM=%+v, want port 58212 and old token", got)
	}
}

func TestCompletedConsoleTokenReusesPreferredTokenForSamePort(t *testing.T) {
	got, err := completedConsoleToken(console.InstanceInfo{Port: 58212, Token: "old-token"}, 58212)
	if err != nil {
		t.Fatal(err)
	}
	if got != "old-token" {
		t.Fatalf("completedConsoleToken=%q, want old token", got)
	}
}

func TestCompletedConsoleTokenRegeneratesWhenPortChanges(t *testing.T) {
	got, err := completedConsoleToken(console.InstanceInfo{Port: 58212, Token: "old-token"}, 58213)
	if err != nil {
		t.Fatal(err)
	}
	if got == "" || got == "old-token" {
		t.Fatalf("completedConsoleToken=%q, want a new non-empty token", got)
	}
}

func TestListenCompletedConsoleReusesPreferredPort(t *testing.T) {
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := reserved.Addr().(*net.TCPAddr).Port
	if err := reserved.Close(); err != nil {
		t.Fatal(err)
	}

	ln, err := listenCompletedConsole(port)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	if got := ln.Addr().(*net.TCPAddr).Port; got != port {
		t.Fatalf("listenCompletedConsole port=%d, want preferred %d", got, port)
	}
}

func TestListenCompletedConsoleWaitsForPreferredPortRelease(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := busy.Addr().(*net.TCPAddr).Port
	go func() {
		time.Sleep(30 * time.Millisecond)
		_ = busy.Close()
	}()

	ln, err := listenCompletedConsoleWithRetry(port, time.Second, 5*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	if got := ln.Addr().(*net.TCPAddr).Port; got != port {
		t.Fatalf("listenCompletedConsoleWithRetry port=%d, want released preferred %d", got, port)
	}
}

func TestListenCompletedConsoleFallsBackWhenPreferredPortBusy(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	port := busy.Addr().(*net.TCPAddr).Port

	ln, err := listenCompletedConsoleWithRetry(port, 10*time.Millisecond, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	if got := ln.Addr().(*net.TCPAddr).Port; got == port {
		t.Fatalf("listenCompletedConsole reused busy port %d", port)
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
		`experimental_bearer_token = "` + codex.LegacyLocalProxyAPIKeyValue + `"`,
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
	  "agentserverApp.panel.allowed": ["terminal", "output"],
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
	if _, ok := settings["agentserverApp.panel.allowed"]; ok {
		t.Fatalf("agentserverApp.panel.allowed should be removed")
	}
	if _, ok := settings["agentserverApp.panel.hideViews"]; !ok {
		t.Fatalf("agentserverApp.panel.hideViews should be written")
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
	var openedFolder string
	err := launchCompletedCodexDesktop(context.Background(), nil, p, nil, "", "", func(_ context.Context, folder string) error {
		assertJSONField(t, p.CodexDesktopGlobalStateFile, "localeOverride", "zh-CN")
		assertJSONField(t, p.CodexDesktopComputerUseConfigFile, "locale", "zh-CN")
		openedFolder = folder
		return nil
	})
	if err != nil {
		t.Fatalf("launchCompletedCodexDesktop: %v", err)
	}
	if openedFolder != "" {
		t.Fatalf("opened folder=%q, want empty", openedFolder)
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
	if !strings.Contains(string(b), `experimental_bearer_token = "`+codex.LegacyLocalProxyAPIKeyValue+`"`) {
		t.Fatalf("config missing local proxy bearer token:\n%s", b)
	}
}

func TestLaunchCompletedCodexDesktopReturnsVisibleLaunchFailure(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{
		CodexConfigFile:                   filepath.Join(dir, ".codex", "config.toml"),
		CodexDesktopGlobalStateFile:       filepath.Join(dir, ".codex", ".codex-global-state.json"),
		CodexDesktopComputerUseConfigFile: filepath.Join(dir, ".codex", "computer-use", "config.json"),
	}
	shellErr := errors.New("Shell helper failed")
	err := launchCompletedCodexDesktop(context.Background(), nil, p, nil, "", "", func(context.Context, string) error {
		return fmt.Errorf("%w: ChatGPT / Codex launch failed; Repair Reset Reinstall: %w", codexdesktop.ErrLaunchFailed, shellErr)
	})
	if !errors.Is(err, shellErr) || !errors.Is(err, codexdesktop.ErrLaunchFailed) {
		t.Fatalf("err=%v, want Shell cause and ErrLaunchFailed", err)
	}
	for _, want := range []string{"ChatGPT / Codex", "Repair", "Reset", "Reinstall"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err=%v, missing %q", err, want)
		}
	}
}

func TestLaunchCompletedFrontendOpenCodeDesktopWritesConfigAndLaunches(t *testing.T) {
	dir := t.TempDir()
	proxyToken := "launcher-local-proxy-token"
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
	s := &state.State{FrontendMode: state.FrontendModeOpenCodeDesktop}
	s.OpenCodeDesktop.Installed = true
	s.OpenCodeDesktop.Path = `C:\OpenCode\OpenCode.exe`
	launched := false
	err := launchCompletedFrontend(context.Background(), s, p, nil, "", "", "", nil, func(ctx context.Context, opts opencodedesktop.LaunchOptions) error {
		launched = true
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
		t.Fatalf("launchCompletedFrontend: %v", err)
	}
	if !launched {
		t.Fatal("OpenCode was not launched")
	}
	if _, err := os.Stat(p.OpenCodeConfigFile); err != nil {
		t.Fatalf("opencode config not written: %v", err)
	}
	b, err := os.ReadFile(p.OpenCodeConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), proxyToken) {
		t.Fatalf("opencode config should not persist local proxy token:\n%s", b)
	}
	if strings.Contains(string(b), "AGENTSERVER_CODEX_LOCAL_API_KEY") {
		t.Fatalf("opencode config should not use Codex-specific env names:\n%s", b)
	}
	if !strings.Contains(string(b), "{env:AGENTSERVER_LOCAL_MODEL_PROXY_API_KEY}") {
		t.Fatalf("opencode config should use env substitution:\n%s", b)
	}
}

func TestLaunchCompletedFrontendCodexDesktopRegistersLoomDriverMCP(t *testing.T) {
	dir := t.TempDir()
	installDir := filepath.Join(dir, "install")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	driverPath := filepath.Join(installDir, "driver-agent.exe")
	if err := os.WriteFile(driverPath, []byte("driver"), 0o755); err != nil {
		t.Fatal(err)
	}
	sec := secrets.New(filepath.Join(dir, "secrets.json"))
	for key, value := range map[string]string{
		"agentserver_ws_api_key":   "sandbox-proxy-token",
		"agentserver_tunnel_token": "tunnel-token",
	} {
		if err := sec.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	p := paths.Paths{
		UserHome:                          dir,
		CodexConfigFile:                   filepath.Join(dir, ".codex", "config.toml"),
		CodexDesktopGlobalStateFile:       filepath.Join(dir, ".codex", ".codex-global-state.json"),
		CodexDesktopComputerUseConfigFile: filepath.Join(dir, ".codex", "computer-use", "config.json"),
	}
	if err := os.MkdirAll(filepath.Dir(p.CodexConfigFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.CodexConfigFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	st := &state.State{}
	st.Onboarding.Status = state.StatusComplete
	st.Agentserver.SandboxID = "sb-1"
	st.Agentserver.WorkspaceID = "ws-1"
	st.Agentserver.ShortID = "abc123"

	if err := launchCompletedFrontend(context.Background(), st, p, sec, installDir, "", "", func(context.Context, string) error {
		return nil
	}, nil); err != nil {
		t.Fatalf("launchCompletedFrontend: %v", err)
	}

	body, err := os.ReadFile(p.CodexConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{
		`[mcp_servers.driver]`,
		`command = "` + filepath.ToSlash(driverPath) + `"`,
		`"serve-mcp"`,
		`"--config"`,
		`"` + filepath.ToSlash(filepath.Join(dir, ".config", "multi-agent", "driver.yaml")) + `"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("config.toml missing %q:\n%s", want, text)
		}
	}
}

func TestConfigureCompletedLoomDriverUsesDefaultObserver(t *testing.T) {
	dir := t.TempDir()
	installDir := filepath.Join(dir, "install")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "driver-agent.exe"), []byte("driver"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeLauncherTestTarGz(t, filepath.Join(installDir, "driver-skills.tar.gz"), map[string]string{
		"skills/multiagent/SKILL.md": "---\nname: multiagent\n---\nUse driver tools.\n",
	})
	writeLauncherTestTarGz(t, filepath.Join(installDir, "driver-superpower-skills.tar.gz"), map[string]string{
		"using-superpowers/SKILL.md":       "---\nname: using-superpowers\n---\nUse skills.\n",
		"test-driven-development/SKILL.md": "---\nname: test-driven-development\n---\nWrite tests first.\n",
	})
	writeLauncherTestTarGz(t, filepath.Join(installDir, "driver-codex-prompts.tar.gz"), map[string]string{
		"prompts-codex/AGENTS.md": "# Multi-Agent Driver\n\nUse `role == \"slave\"`.\n",
	})
	sec := secrets.New(filepath.Join(dir, "secrets.json"))
	for key, value := range map[string]string{
		"agentserver_ws_api_key":   "sandbox-proxy-token",
		"agentserver_tunnel_token": "tunnel-token",
	} {
		if err := sec.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	st := &state.State{}
	st.Agentserver.SandboxID = "sb-1"
	st.Agentserver.WorkspaceID = "ws-1"
	st.Agentserver.WorkspaceName = "Readable workspace"
	st.Agentserver.ShortID = "abc123"
	p := paths.Paths{
		UserHome:        dir,
		CodexDir:        filepath.Join(dir, "codex-desktop-home"),
		CodexExePath:    filepath.Join(dir, "bin", "codex.exe"),
		CodexConfigFile: filepath.Join(dir, "codex-desktop-home", "config.toml"),
	}

	if err := configureCompletedLoomDriver(p, st, sec, installDir); err != nil {
		t.Fatalf("configureCompletedLoomDriver: %v", err)
	}

	loomPath := filepath.Join(dir, ".config", "multi-agent", "driver.yaml")
	body, err := os.ReadFile(loomPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{
		`enabled: true`,
		`url: "https://loom.nj.cs.ac.cn:10062/"`,
		`workspace_id: "ws-1"`,
		`workspace_name: "Readable workspace"`,
		`agent_id: "driver-abc123"`,
		`api_key: "sandbox-proxy-token"`,
		`token_state_path: "` + filepath.ToSlash(filepath.Join(filepath.Dir(loomPath), "observer.token")) + `"`,
		`bin: "` + filepath.ToSlash(p.CodexExePath) + `"`,
		`codex_home: "` + filepath.ToSlash(p.CodexDir) + `"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("driver.yaml missing %q:\n%s", want, text)
		}
	}
	codexText, err := os.ReadFile(p.CodexConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`startup_timeout_sec = 30`,
		`tool_timeout_sec = 120`,
		`enabled = true`,
	} {
		if !strings.Contains(string(codexText), want) {
			t.Fatalf("config.toml missing %q:\n%s", want, codexText)
		}
	}
	for _, path := range []string{
		filepath.Join(dir, ".agents", "skills", "multiagent", "SKILL.md"),
		filepath.Join(dir, ".codex", "skills", "multiagent", "SKILL.md"),
		filepath.Join(dir, ".agents", "skills", "using-superpowers", "SKILL.md"),
		filepath.Join(dir, ".codex", "skills", "using-superpowers", "SKILL.md"),
		filepath.Join(dir, ".agents", "skills", "test-driven-development", "SKILL.md"),
		filepath.Join(dir, ".codex", "skills", "test-driven-development", "SKILL.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected Loom driver skill at %s: %v", path, err)
		}
	}
	agentsText, err := os.ReadFile(filepath.Join(dir, ".codex", "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agentsText), "role == \"slave\"") {
		t.Fatalf("AGENTS.md missing Loom Codex driver prompt:\n%s", agentsText)
	}
}

func TestConfigureCompletedLoomDriverUsesMachineComputerName(t *testing.T) {
	dir := t.TempDir()
	installDir := createLauncherTestDriverInstall(t, dir)
	sec := launcherTestDriverSecrets(t, dir)
	st := launcherTestDriverState()
	p := paths.Paths{
		UserHome:        dir,
		InstallRoot:     filepath.Join(dir, ".agentserver-app"),
		MachineFile:     filepath.Join(dir, ".agentserver-app", "machine.json"),
		CodexExePath:    filepath.Join(dir, "bin", "codex.exe"),
		CodexConfigFile: filepath.Join(dir, ".codex", "config.toml"),
	}
	if _, err := slave.NewMachineStore(p.MachineFile).Ensure("TEST-PC"); err != nil {
		t.Fatal(err)
	}
	resetCompletedDriverHooksForTest(t)

	if err := configureCompletedLoomDriver(p, st, sec, installDir); err != nil {
		t.Fatalf("configureCompletedLoomDriver: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dir, ".config", "multi-agent", "driver.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{
		`display_name: "TEST-PC"`,
		`description: "TEST-PC 本地协作驱动。"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("driver.yaml missing %q:\n%s", want, text)
		}
	}
}

func TestConfigureCompletedLoomDriverUsesAgentctlCodexDebugWrapper(t *testing.T) {
	dir := t.TempDir()
	installDir := createLauncherTestDriverInstall(t, dir)
	sec := launcherTestDriverSecrets(t, dir)
	st := launcherTestDriverState()
	p := paths.Paths{
		UserHome:        dir,
		InstallRoot:     filepath.Join(dir, ".agentserver-app"),
		CodexExePath:    filepath.Join(dir, "bin", "codex.exe"),
		CodexConfigFile: filepath.Join(dir, ".codex", "config.toml"),
	}

	if err := configureCompletedLoomDriver(p, st, sec, installDir); err != nil {
		t.Fatalf("configureCompletedLoomDriver: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dir, ".config", "multi-agent", "driver.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{
		`bin: "` + filepath.ToSlash(filepath.Join(installDir, "codex-debug-wrapper.exe")) + `"`,
		`extra_args: []`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("driver.yaml missing %q:\n%s", want, text)
		}
	}
	wrapperConfig, err := os.ReadFile(filepath.Join(installDir, "codex-debug-wrapper.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wrapperConfig), strconv.Quote(p.CodexExePath)) {
		t.Fatalf("wrapper config should point at real codex path %q:\n%s", p.CodexExePath, wrapperConfig)
	}
}

func TestConfigureCompletedLoomDriverCodexDesktopUsesCodexDesktopCodexPath(t *testing.T) {
	dir := t.TempDir()
	installDir := createLauncherTestDriverInstall(t, dir)
	sec := launcherTestDriverSecrets(t, dir)
	st := launcherTestDriverState()
	st.FrontendMode = state.FrontendModeCodexDesktop
	desktopCodexPath := filepath.Join(dir, "Microsoft", "WindowsApps", "codex.exe")
	p := paths.Paths{
		UserHome:              dir,
		InstallRoot:           filepath.Join(dir, ".agentserver-app"),
		CodexExePath:          filepath.Join(dir, "agentserver-app", "bin", "codex.exe"),
		CodexDesktopCodexPath: desktopCodexPath,
		CodexConfigFile:       filepath.Join(dir, ".codex", "config.toml"),
	}

	if err := configureCompletedLoomDriver(p, st, sec, installDir); err != nil {
		t.Fatalf("configureCompletedLoomDriver: %v", err)
	}

	wrapperConfig, err := os.ReadFile(filepath.Join(installDir, "codex-debug-wrapper.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wrapperConfig), strconv.Quote(desktopCodexPath)) {
		t.Fatalf("wrapper config should point at Codex Desktop CLI %q:\n%s", desktopCodexPath, wrapperConfig)
	}
	if strings.Contains(string(wrapperConfig), strconv.Quote(p.CodexExePath)) {
		t.Fatalf("wrapper config should not point at managed Codex runtime %q:\n%s", p.CodexExePath, wrapperConfig)
	}
}

func TestConfigureCompletedLoomDriverUsesChineseMachineComputerName(t *testing.T) {
	dir := t.TempDir()
	installDir := createLauncherTestDriverInstall(t, dir)
	sec := launcherTestDriverSecrets(t, dir)
	st := launcherTestDriverState()
	p := paths.Paths{
		UserHome:        dir,
		InstallRoot:     filepath.Join(dir, ".agentserver-app"),
		MachineFile:     filepath.Join(dir, ".agentserver-app", "machine.json"),
		CodexExePath:    filepath.Join(dir, "bin", "codex.exe"),
		CodexConfigFile: filepath.Join(dir, ".codex", "config.toml"),
	}
	if _, err := slave.NewMachineStore(p.MachineFile).Ensure("测试电脑"); err != nil {
		t.Fatal(err)
	}
	resetCompletedDriverHooksForTest(t)

	if err := configureCompletedLoomDriver(p, st, sec, installDir); err != nil {
		t.Fatalf("configureCompletedLoomDriver: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dir, ".config", "multi-agent", "driver.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{
		`display_name: "测试电脑"`,
		`description: "测试电脑 本地协作驱动。"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("driver.yaml missing %q:\n%s", want, text)
		}
	}
}

func TestConfigureCompletedLoomDriverFallbackDisplayNameHasStableSuffix(t *testing.T) {
	st := &state.State{InstallID: "install-abcdef1234567890"}
	got := completedDriverComputerName(paths.Paths{}, st)
	if got != "local-computer-1234567890" {
		t.Fatalf("fallback name=%q, want stable install suffix", got)
	}
}

func TestConfigureCompletedLoomDriverDoesNotAutoStartWhenDriverDisabled(t *testing.T) {
	dir := t.TempDir()
	installDir := createLauncherTestDriverInstall(t, dir)
	sec := launcherTestDriverSecrets(t, dir)
	st := launcherTestDriverState()
	p := paths.Paths{
		UserHome:        dir,
		InstallRoot:     filepath.Join(dir, ".agentserver-app"),
		MachineFile:     filepath.Join(dir, ".agentserver-app", "machine.json"),
		CodexExePath:    filepath.Join(dir, "bin", "codex.exe"),
		CodexConfigFile: filepath.Join(dir, ".codex", "config.toml"),
	}
	if err := console.NewDriverDaemonStore(driverDaemonStatePath(p)).Save(console.DriverDaemonPersistedState{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	started := false
	resetCompletedDriverHooksForTest(t)
	startCompletedLoomDriverDaemon = func(string, string) error {
		started = true
		return nil
	}

	if err := configureCompletedLoomDriver(p, st, sec, installDir); err != nil {
		t.Fatalf("configureCompletedLoomDriver: %v", err)
	}
	if started {
		t.Fatal("driver daemon auto-started while disabled")
	}
}

func TestConfigureCompletedLoomDriverDoesNotAutoStartWhenDriverStateCorrupt(t *testing.T) {
	dir := t.TempDir()
	installDir := createLauncherTestDriverInstall(t, dir)
	sec := launcherTestDriverSecrets(t, dir)
	st := launcherTestDriverState()
	p := paths.Paths{
		UserHome:        dir,
		InstallRoot:     filepath.Join(dir, ".agentserver-app"),
		MachineFile:     filepath.Join(dir, ".agentserver-app", "machine.json"),
		CodexExePath:    filepath.Join(dir, "bin", "codex.exe"),
		CodexConfigFile: filepath.Join(dir, ".codex", "config.toml"),
	}
	if err := os.MkdirAll(filepath.Dir(driverDaemonStatePath(p)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(driverDaemonStatePath(p), []byte("{bad json"), 0o600); err != nil {
		t.Fatal(err)
	}
	started := false
	resetCompletedDriverHooksForTest(t)
	startCompletedLoomDriverDaemon = func(string, string) error {
		started = true
		return nil
	}

	if err := configureCompletedLoomDriver(p, st, sec, installDir); err != nil {
		t.Fatalf("configureCompletedLoomDriver: %v", err)
	}
	if started {
		t.Fatal("driver daemon auto-started with corrupt driver state")
	}
}

func TestConfigureCompletedLoomDriverMinimalVSCodeUsesVSCodeCodexPath(t *testing.T) {
	dir := t.TempDir()
	installDir := filepath.Join(dir, "install")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "driver-agent.exe"), []byte("driver"), 0o755); err != nil {
		t.Fatal(err)
	}
	sec := secrets.New(filepath.Join(dir, "secrets.json"))
	for key, value := range map[string]string{
		"agentserver_ws_api_key":   "sandbox-proxy-token",
		"agentserver_tunnel_token": "tunnel-token",
	} {
		if err := sec.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	st := &state.State{FrontendMode: state.FrontendModeMinimalVSCode}
	st.Agentserver.SandboxID = "sb-1"
	st.Agentserver.WorkspaceID = "ws-1"
	st.Agentserver.ShortID = "abc123"
	p := paths.Paths{
		UserHome:     dir,
		CodexExePath: filepath.Join(dir, "bin", "codex.exe"),
	}

	if err := configureCompletedLoomDriver(p, st, sec, installDir); err != nil {
		t.Fatalf("configureCompletedLoomDriver: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dir, ".config", "multi-agent", "driver.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	want := `bin: "` + filepath.ToSlash(p.CodexExePath) + `"`
	if !strings.Contains(string(body), want) {
		t.Fatalf("driver.yaml missing %q:\n%s", want, body)
	}
}

func TestCompletedDriverCodexBinCodexDesktopUsesCodexDesktopPath(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{
		CodexExePath:          filepath.Join(dir, "agentserver-app", "bin", "codex.exe"),
		CodexDesktopCodexPath: filepath.Join(dir, "Microsoft", "WindowsApps", "codex.exe"),
	}
	st := &state.State{FrontendMode: state.FrontendModeCodexDesktop}

	got := completedDriverCodexBin(p, st)
	if got != p.CodexDesktopCodexPath {
		t.Fatalf("codex bin=%q, want Codex Desktop CLI %q", got, p.CodexDesktopCodexPath)
	}
	if got == "codex" {
		t.Fatalf("Codex Desktop driver must not rely on PATH command: %q", got)
	}
}

func TestCompletedDriverCodexBinMinimalVSCodeUsesManagedCodexPath(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{
		CodexExePath:          filepath.Join(dir, "agentserver-app", "bin", "codex.exe"),
		CodexDesktopCodexPath: filepath.Join(dir, "Microsoft", "WindowsApps", "codex.exe"),
	}
	st := &state.State{FrontendMode: state.FrontendModeMinimalVSCode}

	got := completedDriverCodexBin(p, st)
	if got != p.CodexExePath {
		t.Fatalf("codex bin=%q, want managed Codex runtime %q", got, p.CodexExePath)
	}
}

func TestLaunchCompletedFrontendMinimalVSCodeConfiguresLoomDriver(t *testing.T) {
	dir := t.TempDir()
	installDir := filepath.Join(dir, "install")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "driver-agent.exe"), []byte("driver"), 0o755); err != nil {
		t.Fatal(err)
	}
	sec := secrets.New(filepath.Join(dir, "secrets.json"))
	for key, value := range map[string]string{
		"agentserver_ws_api_key":   "sandbox-proxy-token",
		"agentserver_tunnel_token": "tunnel-token",
	} {
		if err := sec.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	st := &state.State{FrontendMode: state.FrontendModeMinimalVSCode}
	st.VSCode.Path = filepath.Join(dir, "missing-code.exe")
	st.Agentserver.SandboxID = "sb-1"
	st.Agentserver.WorkspaceID = "ws-1"
	st.Agentserver.ShortID = "abc123"
	p := paths.Paths{
		UserHome:          dir,
		VSCodeUserDataDir: filepath.Join(dir, "vscode-data"),
		VSCodeExtDir:      filepath.Join(dir, "vscode-ext"),
		CodexConfigFile:   filepath.Join(dir, ".codex", "config.toml"),
		CodexExePath:      filepath.Join(dir, "bin", "codex.exe"),
	}

	err := launchCompletedFrontend(context.Background(), st, p, sec, installDir, "", "", nil, nil)
	if err == nil {
		t.Fatal("expected missing VS Code executable error")
	}

	body, readErr := os.ReadFile(filepath.Join(dir, ".config", "multi-agent", "driver.yaml"))
	if readErr != nil {
		t.Fatalf("expected minimal VS Code launch to configure driver before launching: %v", readErr)
	}
	want := `bin: "` + filepath.ToSlash(p.CodexExePath) + `"`
	if !strings.Contains(string(body), want) {
		t.Fatalf("driver.yaml missing %q:\n%s", want, body)
	}
}

func TestConfigureCompletedLoomDriverFallsBackToExistingDriverTokens(t *testing.T) {
	dir := t.TempDir()
	installDir := filepath.Join(dir, "install")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "driver-agent.exe"), []byte("driver"), 0o755); err != nil {
		t.Fatal(err)
	}
	loomPath := filepath.Join(dir, ".config", "multi-agent", "driver.yaml")
	if err := os.MkdirAll(filepath.Dir(loomPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(loomPath, []byte(`credentials:
  tunnel_token: "old-tunnel-token"
  proxy_token: "old-proxy-token"

observer:
  enabled: false
  telemetry_enabled: false
`), 0o600); err != nil {
		t.Fatal(err)
	}
	st := &state.State{}
	st.Agentserver.SandboxID = "sb-1"
	st.Agentserver.WorkspaceID = "ws-1"
	st.Agentserver.ShortID = "abc123"
	p := paths.Paths{
		UserHome:     dir,
		CodexExePath: filepath.Join(dir, "bin", "codex.exe"),
	}

	if err := configureCompletedLoomDriver(p, st, secrets.New(filepath.Join(dir, "missing-secrets.json")), installDir); err != nil {
		t.Fatalf("configureCompletedLoomDriver: %v", err)
	}

	body, err := os.ReadFile(loomPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{
		`enabled: true`,
		`url: "https://loom.nj.cs.ac.cn:10062/"`,
		`tunnel_token: "old-tunnel-token"`,
		`api_key: "old-proxy-token"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("driver.yaml missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "telemetry_enabled") {
		t.Fatalf("driver.yaml contains unsupported observer telemetry field:\n%s", text)
	}
}

func createLauncherTestDriverInstall(t *testing.T, dir string) string {
	t.Helper()
	installDir := filepath.Join(dir, "install")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "driver-agent.exe"), []byte("driver"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "agentctl.exe"), []byte("agentctl"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "codex-debug-wrapper.exe"), []byte("wrapper"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeLauncherTestTarGz(t, filepath.Join(installDir, "driver-skills.tar.gz"), map[string]string{
		"skills/multiagent/SKILL.md": "---\nname: multiagent\n---\nUse driver tools.\n",
	})
	writeLauncherTestTarGz(t, filepath.Join(installDir, "driver-superpower-skills.tar.gz"), map[string]string{
		"using-superpowers/SKILL.md":       "---\nname: using-superpowers\n---\nUse skills.\n",
		"test-driven-development/SKILL.md": "---\nname: test-driven-development\n---\nWrite tests first.\n",
	})
	writeLauncherTestTarGz(t, filepath.Join(installDir, "driver-codex-prompts.tar.gz"), map[string]string{
		"prompts-codex/AGENTS.md": "# Multi-Agent Driver\n\nUse `role == \"slave\"`.\n",
	})
	return installDir
}

func launcherTestDriverSecrets(t *testing.T, dir string) secrets.Store {
	t.Helper()
	sec := secrets.New(filepath.Join(dir, "secrets.json"))
	for key, value := range map[string]string{
		"agentserver_ws_api_key":   "sandbox-proxy-token",
		"agentserver_tunnel_token": "tunnel-token",
	} {
		if err := sec.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	return sec
}

func launcherTestDriverState() *state.State {
	st := &state.State{InstallID: "install-abcdef1234567890"}
	st.Agentserver.SandboxID = "sb-1"
	st.Agentserver.WorkspaceID = "ws-1"
	st.Agentserver.WorkspaceName = "Readable workspace"
	st.Agentserver.ShortID = "abc123"
	return st
}

func resetCompletedDriverHooksForTest(t *testing.T) {
	t.Helper()
	origStart := startCompletedLoomDriverDaemon
	t.Cleanup(func() {
		startCompletedLoomDriverDaemon = origStart
	})
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

func writeLauncherTestTarGz(t *testing.T, path string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gw := gzip.NewWriter(f)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()
	for name, content := range files {
		b := []byte(content)
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(b))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(b); err != nil {
			t.Fatal(err)
		}
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
