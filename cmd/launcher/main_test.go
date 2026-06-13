package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/appversion"
	"github.com/agentserver/agentserver-pkg/internal/codex"
	"github.com/agentserver/agentserver-pkg/internal/console"
	"github.com/agentserver/agentserver-pkg/internal/installmode"
	"github.com/agentserver/agentserver-pkg/internal/modelproxy"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/state"
	"github.com/agentserver/agentserver-pkg/internal/tray"
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
	var opened string
	err := launchCompletedCodexDesktop(context.Background(), nil, p, nil, "", "", func(url string) error {
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

	if err := launchCompletedFrontend(context.Background(), st, p, sec, installDir, "", "", func(string) error {
		return nil
	}); err != nil {
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
		CodexExePath:    filepath.Join(dir, "bin", "codex.exe"),
		CodexConfigFile: filepath.Join(dir, ".codex", "config.toml"),
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
		`bin: "codex"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("driver.yaml missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, filepath.ToSlash(p.CodexExePath)) {
		t.Fatalf("Codex Desktop driver should use Codex Desktop's codex command, not local VS Code codex path:\n%s", text)
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

	err := launchCompletedFrontend(context.Background(), st, p, sec, installDir, "", "", nil)
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
