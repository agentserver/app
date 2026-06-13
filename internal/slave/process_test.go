package slave

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestManagerCreateWritesConfigAndStartsProcess(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	runner := &fakeRunner{pid: 4321, authURL: "https://agent.cs.ac.cn/device?user_code=ABCD"}
	manager := NewManager(ManagerDeps{
		Machines:  NewMachineStore(filepath.Join(dir, "machine.json")),
		Registry:  NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves")),
		Runner:    runner,
		SlaveExe:  filepath.Join(dir, "slave-agent.exe"),
		ServerURL: "https://agent.cs.ac.cn",
		CodexBin:  "codex",
	})
	if _, err := manager.Machines.Ensure("61414-PC"); err != nil {
		t.Fatal(err)
	}

	got, err := manager.CreateAndStart(context.Background(), CreateInput{Folder: folder, Name: "worker"})
	if err != nil {
		t.Fatalf("CreateAndStart: %v", err)
	}
	if got.Status != StatusAuthRequired || got.PID != 4321 || got.AuthURL != runner.authURL {
		t.Fatalf("slave=%+v", got)
	}
	if runner.startedConfig != got.ConfigPath {
		t.Fatalf("startedConfig=%q want %q", runner.startedConfig, got.ConfigPath)
	}
}

func TestManagerCreateAndStartOpensImmediateAuthURL(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	authURL := "https://agent.cs.ac.cn/device?user_code=ABCD"
	opened := make(chan string, 1)
	manager := NewManager(ManagerDeps{
		Machines:    NewMachineStore(filepath.Join(dir, "machine.json")),
		Registry:    NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves")),
		Runner:      &fakeRunner{pid: 4321, authURL: authURL},
		SlaveExe:    filepath.Join(dir, "slave-agent.exe"),
		OpenAuthURL: func(url string) { opened <- url },
	})
	if _, err := manager.Machines.Ensure("61414-PC"); err != nil {
		t.Fatal(err)
	}

	if _, err := manager.CreateAndStart(context.Background(), CreateInput{Folder: folder, Name: "worker"}); err != nil {
		t.Fatalf("CreateAndStart: %v", err)
	}

	select {
	case got := <-opened:
		if got != authURL {
			t.Fatalf("opened auth URL=%q, want %q", got, authURL)
		}
	case <-time.After(time.Second):
		t.Fatal("auth URL was not opened")
	}
}

func TestManagerDelayedAuthURLOpensBrowser(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	authURL := "https://agent.cs.ac.cn/device?user_code=ABCD"
	authURLs := make(chan string, 1)
	opened := make(chan string, 1)
	manager := NewManager(ManagerDeps{
		Machines:    NewMachineStore(filepath.Join(dir, "machine.json")),
		Registry:    NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves")),
		Runner:      &fakeRunner{pid: 4321, authURLs: authURLs},
		SlaveExe:    filepath.Join(dir, "slave-agent.exe"),
		OpenAuthURL: func(url string) { opened <- url },
	})
	if _, err := manager.Machines.Ensure("61414-PC"); err != nil {
		t.Fatal(err)
	}

	sl, err := manager.CreateAndStart(context.Background(), CreateInput{Folder: folder, Name: "worker"})
	if err != nil {
		t.Fatalf("CreateAndStart: %v", err)
	}
	authURLs <- authURL

	select {
	case got := <-opened:
		if got != authURL {
			t.Fatalf("opened auth URL=%q, want %q", got, authURL)
		}
	case <-time.After(time.Second):
		t.Fatal("auth URL was not opened")
	}
	waitForSlave(t, manager.Registry, sl.ID, func(sl Slave) bool {
		return sl.Status == StatusAuthRequired && sl.AuthURL == authURL
	})
}

func TestManagerDuplicateDelayedAuthURLOpensBrowserOnce(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	authURL := "https://agent.cs.ac.cn/device?user_code=ABCD"
	authURLs := make(chan string, 2)
	opened := make(chan string, 2)
	manager := NewManager(ManagerDeps{
		Machines:    NewMachineStore(filepath.Join(dir, "machine.json")),
		Registry:    NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves")),
		Runner:      &fakeRunner{pid: 4321, authURLs: authURLs},
		SlaveExe:    filepath.Join(dir, "slave-agent.exe"),
		OpenAuthURL: func(url string) { opened <- url },
	})
	if _, err := manager.Machines.Ensure("61414-PC"); err != nil {
		t.Fatal(err)
	}

	if _, err := manager.CreateAndStart(context.Background(), CreateInput{Folder: folder, Name: "worker"}); err != nil {
		t.Fatalf("CreateAndStart: %v", err)
	}
	authURLs <- authURL
	if got := receiveOpenedURL(t, opened); got != authURL {
		t.Fatalf("opened auth URL=%q, want %q", got, authURL)
	}
	authURLs <- authURL
	select {
	case got := <-opened:
		t.Fatalf("duplicate auth URL opened browser again: %q", got)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestManagerPauseRestartAndDelete(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	runner := &fakeRunner{pid: 1111, authURL: "https://agent.cs.ac.cn/device?user_code=ABCD"}
	manager := NewManager(ManagerDeps{
		Machines: NewMachineStore(filepath.Join(dir, "machine.json")),
		Registry: NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves")),
		Runner:   runner,
		SlaveExe: filepath.Join(dir, "slave-agent.exe"),
	})
	_, _ = manager.Machines.Ensure("PC")
	sl, err := manager.CreateAndStart(context.Background(), CreateInput{Folder: folder})
	if err != nil {
		t.Fatal(err)
	}

	paused, err := manager.Pause(context.Background(), sl.ID)
	if err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if paused.Status != StatusPaused || paused.AuthURL != "" || !runner.stopped[1111] {
		t.Fatalf("paused=%+v stopped=%+v", paused, runner.stopped)
	}

	runner.pid = 2222
	runner.authURL = ""
	restarted, err := manager.Restart(context.Background(), sl.ID)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if restarted.Status != StatusStarting || restarted.PID != 2222 {
		t.Fatalf("restarted=%+v", restarted)
	}

	if err := manager.Delete(context.Background(), sl.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	all, err := manager.Registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("slaves after delete=%+v", all)
	}
}

func TestManagerRestartAndDeleteSameSlaveAreSerialized(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	startEntered := make(chan struct{}, 1)
	releaseStart := make(chan struct{}, 2)
	runner := &fakeRunner{
		pid: 1111,
		onStart: func() {
			select {
			case startEntered <- struct{}{}:
				<-releaseStart
			default:
			}
		},
	}
	manager := NewManager(ManagerDeps{
		Machines: NewMachineStore(filepath.Join(dir, "machine.json")),
		Registry: NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves")),
		Runner:   runner,
		SlaveExe: filepath.Join(dir, "slave-agent.exe"),
	})
	_, _ = manager.Machines.Ensure("PC")
	releaseStart <- struct{}{}
	sl, err := manager.CreateAndStart(context.Background(), CreateInput{Folder: folder})
	if err != nil {
		t.Fatal(err)
	}
	<-startEntered

	runner.pid = 2222
	restartDone := make(chan error, 1)
	go func() {
		_, err := manager.Restart(context.Background(), sl.ID)
		restartDone <- err
	}()
	select {
	case <-startEntered:
	case <-time.After(time.Second):
		t.Fatal("restart did not reach Start")
	}

	deleteDone := make(chan error, 1)
	go func() {
		deleteDone <- manager.Delete(context.Background(), sl.ID)
	}()
	select {
	case <-deleteDone:
		t.Fatal("Delete returned while Restart for same slave was still in progress")
	case <-time.After(100 * time.Millisecond):
	}

	releaseStart <- struct{}{}
	if err := <-restartDone; err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if err := <-deleteDone; err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestCopyAndDetectURLHandlesLongLogLineAndLogsScannerErrors(t *testing.T) {
	longURL := "https://agent.cs.ac.cn/device?user_code=" + strings.Repeat("A", 70*1024)
	authCh := make(chan string, 1)
	var out bytes.Buffer

	copyAndDetectURL(strings.NewReader(longURL+"\n"), &out, authCh)

	select {
	case got := <-authCh:
		if got != longURL {
			t.Fatalf("auth URL length=%d, want %d", len(got), len(longURL))
		}
	default:
		t.Fatal("auth URL from long log line was not detected")
	}
	if !strings.Contains(out.String(), longURL) {
		t.Fatal("long log line was not copied")
	}

	out.Reset()
	copyAndDetectURL(strings.NewReader(strings.Repeat("x", 2*1024*1024)), &out, authCh)
	if !strings.Contains(out.String(), "slave log scan error") {
		t.Fatalf("scanner error was not logged: %q", out.String())
	}
}

func TestRecordAuthURLRejectsUnsafeURLSchemes(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	authURLs := make(chan string, 1)
	opened := make(chan string, 1)
	manager := NewManager(ManagerDeps{
		Machines:    NewMachineStore(filepath.Join(dir, "machine.json")),
		Registry:    NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves")),
		Runner:      &fakeRunner{pid: 4321, authURLs: authURLs},
		SlaveExe:    filepath.Join(dir, "slave-agent.exe"),
		OpenAuthURL: func(url string) { opened <- url },
	})
	_, _ = manager.Machines.Ensure("PC")

	sl, err := manager.CreateAndStart(context.Background(), CreateInput{Folder: folder})
	if err != nil {
		t.Fatalf("CreateAndStart: %v", err)
	}
	authURLs <- "javascript:fetch('/api/console/quit',{method:'POST'})"

	time.Sleep(100 * time.Millisecond)
	got, err := manager.Registry.Get(sl.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AuthURL != "" || got.Status == StatusAuthRequired {
		t.Fatalf("unsafe auth URL was persisted: %+v", got)
	}
	select {
	case url := <-opened:
		t.Fatalf("unsafe auth URL was opened: %q", url)
	default:
	}
}

func TestManagerDelayedAuthURLAfterStartupUpdatesRegistry(t *testing.T) {
	withStartupTimeout(t, 20*time.Millisecond)
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	exe := writeLinuxShellScript(t, "sleep 0.1\necho 'https://agent.cs.ac.cn/device?user_code=ABCD'\nexec sleep 30\n")
	manager := NewManager(ManagerDeps{
		Machines: NewMachineStore(filepath.Join(dir, "machine.json")),
		Registry: NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves")),
		SlaveExe: exe,
	})
	_, _ = manager.Machines.Ensure("PC")

	sl, err := manager.CreateAndStart(context.Background(), CreateInput{Folder: folder})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = manager.Delete(context.Background(), sl.ID)
	})
	if sl.Status != StatusStarting || sl.AuthURL != "" {
		t.Fatalf("initial slave=%+v", sl)
	}

	got := waitForSlave(t, manager.Registry, sl.ID, func(sl Slave) bool {
		return sl.Status == StatusAuthRequired && sl.AuthURL == "https://agent.cs.ac.cn/device?user_code=ABCD"
	})
	if got.PID != sl.PID {
		t.Fatalf("PID changed from %d to %d", sl.PID, got.PID)
	}
}

func TestManagerProcessExitAfterStartupUpdatesRegistryError(t *testing.T) {
	withStartupTimeout(t, 20*time.Millisecond)
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	exe := writeLinuxShellScript(t, "sleep 0.1\nexit 7\n")
	manager := NewManager(ManagerDeps{
		Machines: NewMachineStore(filepath.Join(dir, "machine.json")),
		Registry: NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves")),
		SlaveExe: exe,
	})
	_, _ = manager.Machines.Ensure("PC")

	sl, err := manager.CreateAndStart(context.Background(), CreateInput{Folder: folder})
	if err != nil {
		t.Fatal(err)
	}
	if sl.Status != StatusStarting || sl.PID == 0 {
		t.Fatalf("initial slave=%+v", sl)
	}

	got := waitForSlave(t, manager.Registry, sl.ID, func(sl Slave) bool {
		return sl.Status == StatusError && sl.PID == 0 && sl.LastError != ""
	})
	if got.AuthURL != "" {
		t.Fatalf("AuthURL=%q, want cleared", got.AuthURL)
	}
}

func TestManagerStartingSlaveTimesOutWhenCredentialsNeverArrive(t *testing.T) {
	withReadinessTimeout(t, 30*time.Millisecond)
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	manager := NewManager(ManagerDeps{
		Machines: NewMachineStore(filepath.Join(dir, "machine.json")),
		Registry: NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves")),
		Runner:   &fakeRunner{pid: 1111},
		SlaveExe: filepath.Join(dir, "slave-agent.exe"),
	})
	_, _ = manager.Machines.Ensure("PC")
	sl, err := manager.CreateAndStart(context.Background(), CreateInput{Folder: folder})
	if err != nil {
		t.Fatal(err)
	}
	if sl.Status != StatusStarting || sl.PID == 0 {
		t.Fatalf("initial slave=%+v", sl)
	}

	got := waitForSlave(t, manager.Registry, sl.ID, func(sl Slave) bool {
		return sl.Status == StatusError && sl.PID == 0 && strings.Contains(sl.LastError, "startup timeout")
	})
	if got.AuthURL != "" {
		t.Fatalf("AuthURL=%q, want cleared", got.AuthURL)
	}
}

func TestManagerAuthRequiredSlaveBecomesRunningWhenCredentialsAreWritten(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	runner := &fakeRunner{pid: 1111, authURL: "https://agent.cs.ac.cn/device?user_code=ABCD"}
	manager := NewManager(ManagerDeps{
		Machines: NewMachineStore(filepath.Join(dir, "machine.json")),
		Registry: NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves")),
		Runner:   runner,
		SlaveExe: filepath.Join(dir, "slave-agent.exe"),
	})
	_, _ = manager.Machines.Ensure("PC")
	sl, err := manager.CreateAndStart(context.Background(), CreateInput{Folder: folder})
	if err != nil {
		t.Fatal(err)
	}
	if sl.Status != StatusAuthRequired || sl.AuthURL == "" {
		t.Fatalf("initial slave=%+v", sl)
	}

	writeReadyCredentials(t, sl.ConfigPath)

	got := waitForSlave(t, manager.Registry, sl.ID, func(sl Slave) bool {
		return sl.Status == StatusRunning
	})
	if got.PID != sl.PID || got.AuthURL != "" || got.LastError != "" {
		t.Fatalf("running slave=%+v, want same PID with cleared auth/error", got)
	}
}

func TestManagerRemoteIdentityReadsAuthenticatedConfig(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	manager := NewManager(ManagerDeps{
		Machines:  NewMachineStore(filepath.Join(dir, "machine.json")),
		Registry:  NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves")),
		Runner:    &fakeRunner{pid: 1111},
		SlaveExe:  filepath.Join(dir, "slave-agent.exe"),
		ServerURL: "https://agent.example/",
	})
	_, _ = manager.Machines.Ensure("PC")
	sl, err := manager.CreateAndStart(context.Background(), CreateInput{Folder: folder})
	if err != nil {
		t.Fatal(err)
	}
	writeReadyCredentials(t, sl.ConfigPath)

	got, err := manager.RemoteIdentity(context.Background(), sl.ID)
	if err != nil {
		t.Fatalf("RemoteIdentity: %v", err)
	}
	if got.ServerURL != "https://agent.example/" || got.WorkspaceID != "workspace-1" || got.SandboxID != "sandbox-1" {
		t.Fatalf("remote identity=%+v", got)
	}
}

func TestManagerRemoteIdentityUnavailableBeforeAuthentication(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	manager := NewManager(ManagerDeps{
		Machines: NewMachineStore(filepath.Join(dir, "machine.json")),
		Registry: NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves")),
		Runner:   &fakeRunner{pid: 1111},
		SlaveExe: filepath.Join(dir, "slave-agent.exe"),
	})
	_, _ = manager.Machines.Ensure("PC")
	sl, err := manager.CreateAndStart(context.Background(), CreateInput{Folder: folder})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := manager.RemoteIdentity(context.Background(), sl.ID); !errors.Is(err, ErrRemoteIdentityUnavailable) {
		t.Fatalf("RemoteIdentity error=%v, want ErrRemoteIdentityUnavailable", err)
	}
}

func TestManagerStartingSlaveBecomesRunningWhenCredentialsAreWritten(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	runner := &fakeRunner{pid: 2222}
	manager := NewManager(ManagerDeps{
		Machines: NewMachineStore(filepath.Join(dir, "machine.json")),
		Registry: NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves")),
		Runner:   runner,
		SlaveExe: filepath.Join(dir, "slave-agent.exe"),
	})
	_, _ = manager.Machines.Ensure("PC")
	sl, err := manager.CreateAndStart(context.Background(), CreateInput{Folder: folder})
	if err != nil {
		t.Fatal(err)
	}
	if sl.Status != StatusStarting {
		t.Fatalf("initial slave=%+v", sl)
	}

	writeReadyCredentials(t, sl.ConfigPath)

	got := waitForSlave(t, manager.Registry, sl.ID, func(sl Slave) bool {
		return sl.Status == StatusRunning
	})
	if got.PID != sl.PID || got.AuthURL != "" || got.LastError != "" {
		t.Fatalf("running slave=%+v, want same PID with cleared auth/error", got)
	}
}

func TestManagerReadinessDoesNotOverwritePausedSlave(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	runner := &fakeRunner{pid: 3333, authURL: "https://agent.cs.ac.cn/device?user_code=ABCD"}
	manager := NewManager(ManagerDeps{
		Machines: NewMachineStore(filepath.Join(dir, "machine.json")),
		Registry: NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves")),
		Runner:   runner,
		SlaveExe: filepath.Join(dir, "slave-agent.exe"),
	})
	_, _ = manager.Machines.Ensure("PC")
	sl, err := manager.CreateAndStart(context.Background(), CreateInput{Folder: folder})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Pause(context.Background(), sl.ID); err != nil {
		t.Fatalf("Pause: %v", err)
	}

	writeReadyCredentials(t, sl.ConfigPath)
	time.Sleep(150 * time.Millisecond)

	got, err := manager.Registry.Get(sl.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusPaused || got.PID != 0 {
		t.Fatalf("paused slave was overwritten after credentials=%+v", got)
	}
}

func TestManagerStopsStartedProcessWhenInitialRegistryUpdateFails(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	registry := NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves"))
	runner := &fakeRunner{pid: 1111}
	manager := NewManager(ManagerDeps{
		Machines: NewMachineStore(filepath.Join(dir, "machine.json")),
		Registry: registry,
		Runner:   runner,
		SlaveExe: filepath.Join(dir, "slave-agent.exe"),
	})
	runner.onStart = func() {
		if err := os.Remove(registry.path); err != nil {
			t.Fatalf("remove registry file: %v", err)
		}
		if err := mkdir(registry.path); err != nil {
			t.Fatalf("replace registry file with dir: %v", err)
		}
	}
	_, _ = manager.Machines.Ensure("PC")

	if _, err := manager.CreateAndStart(context.Background(), CreateInput{Folder: folder}); err == nil {
		t.Fatal("expected registry update failure")
	}
	if !runner.stopped[1111] {
		t.Fatalf("stopped=%+v, want PID 1111 stopped", runner.stopped)
	}
}

func TestManagerRestartReturnsStopErrorWithoutStarting(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	stopErr := errors.New("stop failed")
	runner := &fakeRunner{pid: 1111}
	manager := NewManager(ManagerDeps{
		Machines: NewMachineStore(filepath.Join(dir, "machine.json")),
		Registry: NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves")),
		Runner:   runner,
		SlaveExe: filepath.Join(dir, "slave-agent.exe"),
	})
	_, _ = manager.Machines.Ensure("PC")
	sl, err := manager.CreateAndStart(context.Background(), CreateInput{Folder: folder})
	if err != nil {
		t.Fatal(err)
	}
	runner.stopErr = stopErr

	if _, err := manager.Restart(context.Background(), sl.ID); !errors.Is(err, stopErr) {
		t.Fatalf("Restart error=%v, want %v", err, stopErr)
	}
	if runner.startCalls != 1 {
		t.Fatalf("startCalls=%d, want original create start only", runner.startCalls)
	}
}

func TestManagerRestartReturnsUntrackedProcessErrorWithoutStarting(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	runner := &fakeRunner{pid: 1111}
	manager := NewManager(ManagerDeps{
		Machines: NewMachineStore(filepath.Join(dir, "machine.json")),
		Registry: NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves")),
		Runner:   runner,
		SlaveExe: filepath.Join(dir, "slave-agent.exe"),
	})
	_, _ = manager.Machines.Ensure("PC")
	sl, err := manager.CreateAndStart(context.Background(), CreateInput{Folder: folder})
	if err != nil {
		t.Fatal(err)
	}
	runner.stopErr = ErrProcessNotTracked

	if _, err := manager.Restart(context.Background(), sl.ID); !errors.Is(err, ErrProcessNotTracked) {
		t.Fatalf("Restart error=%v, want ErrProcessNotTracked", err)
	}
	if runner.startCalls != 1 {
		t.Fatalf("startCalls=%d, want original create start only", runner.startCalls)
	}
}

func TestManagerStartFailureClearsStaleRuntimeFields(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	startErr := errors.New("start failed")
	runner := &fakeRunner{pid: 1111, authURL: "https://agent.cs.ac.cn/device?user_code=ABCD"}
	manager := NewManager(ManagerDeps{
		Machines: NewMachineStore(filepath.Join(dir, "machine.json")),
		Registry: NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves")),
		Runner:   runner,
		SlaveExe: filepath.Join(dir, "slave-agent.exe"),
	})
	_, _ = manager.Machines.Ensure("PC")
	sl, err := manager.CreateAndStart(context.Background(), CreateInput{Folder: folder})
	if err != nil {
		t.Fatal(err)
	}

	runner.pid = 0
	runner.authURL = ""
	runner.startErr = startErr
	if _, err := manager.Restart(context.Background(), sl.ID); !errors.Is(err, startErr) {
		t.Fatalf("Restart error=%v, want %v", err, startErr)
	}
	got, err := manager.Registry.Get(sl.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusError || got.PID != 0 || got.AuthURL != "" || got.LastError == "" {
		t.Fatalf("slave after start failure=%+v", got)
	}
}

func TestManagerPreStartFailureMarksRegistryError(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	manager := NewManager(ManagerDeps{
		Machines: NewMachineStore(filepath.Join(dir, "machine.json")),
		Registry: NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves")),
		Runner:   &fakeRunner{pid: 1111, authURL: "https://agent.cs.ac.cn/device?user_code=ABCD"},
	})
	_, _ = manager.Machines.Ensure("PC")

	if _, err := manager.CreateAndStart(context.Background(), CreateInput{Folder: folder}); err == nil {
		t.Fatal("expected pre-start failure")
	}
	all, err := manager.Registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("slaves after pre-start failure=%+v", all)
	}
	got := all[0]
	if got.Status != StatusError || got.PID != 0 || got.AuthURL != "" || got.LastError == "" {
		t.Fatalf("slave after pre-start failure=%+v", got)
	}
}

func TestManagerCreateAndStartConfigWriteFailureMarksRegistryError(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses /proc as a deterministic unwritable config parent")
	}
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	runner := &fakeRunner{pid: 1111}
	manager := NewManager(ManagerDeps{
		Machines: NewMachineStore(filepath.Join(dir, "machine.json")),
		Registry: NewRegistry(filepath.Join(dir, "slaves.json"), "/proc"),
		Runner:   runner,
		SlaveExe: filepath.Join(dir, "slave-agent.exe"),
	})
	_, _ = manager.Machines.Ensure("PC")

	if _, err := manager.CreateAndStart(context.Background(), CreateInput{Folder: folder}); err == nil {
		t.Fatal("expected config write failure")
	}
	all, err := manager.Registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("slaves after config write failure=%+v", all)
	}
	got := all[0]
	if got.Status != StatusError || got.PID != 0 || got.AuthURL != "" || got.LastError == "" {
		t.Fatalf("slave after config write failure=%+v", got)
	}
	if runner.startCalls != 0 {
		t.Fatalf("startCalls=%d, want no process start", runner.startCalls)
	}
}

func TestManagerDeletePreservesRegistryEntryWhenConfigCleanupFails(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses /proc as a deterministic cleanup failure")
	}
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	registry := NewRegistry(filepath.Join(dir, "slaves.json"), "/proc")
	sl, err := registry.Create(Machine{MachineID: "machine-1", ComputerName: "PC"}, CreateInput{Folder: folder})
	if err != nil {
		t.Fatal(err)
	}
	corruptID := "self"
	if _, err := registry.Update(sl.ID, func(s *Slave) error {
		s.ID = corruptID
		s.Status = StatusAuthRequired
		s.PID = 1111
		s.AuthURL = "https://agent.cs.ac.cn/device?user_code=ABCD"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(ManagerDeps{
		Registry: registry,
		Runner:   &fakeRunner{},
		SlaveExe: filepath.Join(dir, "slave-agent.exe"),
	})

	if err := manager.Delete(context.Background(), corruptID); err == nil {
		t.Fatal("expected cleanup failure")
	}
	all, err := manager.Registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].ID != corruptID {
		t.Fatalf("slaves after failed delete=%+v, want preserved %s", all, corruptID)
	}
	got := all[0]
	if got.Status != StatusError || got.PID != 0 || got.LastError == "" || got.AuthURL != "" {
		t.Fatalf("slave after failed delete=%+v", got)
	}
}

func TestManagerDeleteReturnsUntrackedProcessErrorAndPreservesRegistry(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	registry := NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves"))
	sl, err := registry.Create(Machine{MachineID: "machine-1", ComputerName: "PC"}, CreateInput{Folder: folder})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Update(sl.ID, func(s *Slave) error {
		s.PID = 1111
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(ManagerDeps{
		Registry: registry,
		Runner:   &fakeRunner{stopErr: ErrProcessNotTracked},
		SlaveExe: filepath.Join(dir, "slave-agent.exe"),
	})

	if err := manager.Delete(context.Background(), sl.ID); !errors.Is(err, ErrProcessNotTracked) {
		t.Fatalf("Delete error=%v, want ErrProcessNotTracked", err)
	}
	all, err := manager.Registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].ID != sl.ID || all[0].PID != 1111 {
		t.Fatalf("slaves after failed delete=%+v", all)
	}
}

func TestManagerDeleteUsesTrustedStorageDirWhenConfigPathIsCorrupt(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	registry := NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves"))
	sl, err := registry.Create(Machine{MachineID: "machine-1", ComputerName: "PC"}, CreateInput{Folder: folder})
	if err != nil {
		t.Fatal(err)
	}
	trustedDir, err := registry.storageDir(sl.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := mkdir(trustedDir); err != nil {
		t.Fatal(err)
	}
	outsideDir := filepath.Join(dir, "outside")
	if err := mkdir(outsideDir); err != nil {
		t.Fatal(err)
	}
	outsideConfig := filepath.Join(outsideDir, "config.yaml")
	if err := os.WriteFile(outsideConfig, []byte("do not remove"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Update(sl.ID, func(s *Slave) error {
		s.ConfigPath = outsideConfig
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(ManagerDeps{
		Registry: registry,
		Runner:   &fakeRunner{},
		SlaveExe: filepath.Join(dir, "slave-agent.exe"),
	})

	if err := manager.Delete(context.Background(), sl.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(trustedDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("trusted dir still exists after delete: %v", err)
	}
	if _, err := os.Stat(outsideConfig); err != nil {
		t.Fatalf("outside config was removed or unavailable: %v", err)
	}
	all, err := manager.Registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("slaves after delete=%+v", all)
	}
}

func TestManagerPauseStopsPersistedUntrackedPID(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	registry := NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves"))
	sl, err := registry.Create(Machine{MachineID: "machine-1", ComputerName: "PC"}, CreateInput{Folder: folder})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Update(sl.ID, func(s *Slave) error {
		s.Status = StatusAuthRequired
		s.PID = 1111
		s.AuthURL = "https://agent.cs.ac.cn/device?user_code=ABCD"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	manager := NewManager(ManagerDeps{
		Registry: registry,
		Runner:   runner,
		SlaveExe: filepath.Join(dir, "slave-agent.exe"),
	})

	paused, err := manager.Pause(context.Background(), sl.ID)
	if err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if !runner.stopped[1111] {
		t.Fatalf("Stop was not called for persisted PID")
	}
	if paused.Status != StatusPaused || paused.PID != 0 || paused.AuthURL != "" || paused.LastError != "" {
		t.Fatalf("paused slave=%+v", paused)
	}
}

func TestManagerDeleteStopsPersistedUntrackedPID(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	registry := NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves"))
	sl, err := registry.Create(Machine{MachineID: "machine-1", ComputerName: "PC"}, CreateInput{Folder: folder})
	if err != nil {
		t.Fatal(err)
	}
	trustedDir, err := registry.storageDir(sl.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := mkdir(trustedDir); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Update(sl.ID, func(s *Slave) error {
		s.Status = StatusStarting
		s.PID = 2222
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	manager := NewManager(ManagerDeps{
		Registry: registry,
		Runner:   runner,
		SlaveExe: filepath.Join(dir, "slave-agent.exe"),
	})

	if err := manager.Delete(context.Background(), sl.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !runner.stopped[2222] {
		t.Fatalf("Stop was not called for persisted PID")
	}
	if _, err := os.Stat(trustedDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("trusted dir still exists after delete: %v", err)
	}
	all, err := manager.Registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("slaves after delete=%+v", all)
	}
}

func TestManagerListReconcilesDeadPersistedPID(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	registry := NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves"))
	sl, err := registry.Create(Machine{MachineID: "machine-1", ComputerName: "PC"}, CreateInput{Folder: folder})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Update(sl.ID, func(s *Slave) error {
		s.Status = StatusRunning
		s.PID = 3333
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(ManagerDeps{
		Machines: NewMachineStore(filepath.Join(dir, "machine.json")),
		Registry: registry,
		Runner:   &inspectingFakeRunner{},
		SlaveExe: filepath.Join(dir, "slave-agent.exe"),
	})
	if _, err := manager.Machines.Ensure("PC"); err != nil {
		t.Fatal(err)
	}

	_, slaves, err := manager.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(slaves) != 1 {
		t.Fatalf("slaves=%+v", slaves)
	}
	got := slaves[0]
	if got.Status != StatusError || got.PID != 0 || got.AuthURL != "" || !strings.Contains(got.LastError, ErrProcessNotRunning.Error()) {
		t.Fatalf("reconciled slave=%+v", got)
	}
	persisted, err := manager.Registry.Get(sl.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != StatusError || persisted.PID != 0 {
		t.Fatalf("persisted slave=%+v", persisted)
	}
}

func TestManagerListPreservesLiveMatchingPersistedPID(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	registry := NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves"))
	sl, err := registry.Create(Machine{MachineID: "machine-1", ComputerName: "PC"}, CreateInput{Folder: folder})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Update(sl.ID, func(s *Slave) error {
		s.Status = StatusRunning
		s.PID = 3333
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(ManagerDeps{
		Machines: NewMachineStore(filepath.Join(dir, "machine.json")),
		Registry: registry,
		Runner:   &inspectingFakeRunner{matches: map[int]bool{3333: true}},
		SlaveExe: filepath.Join(dir, "slave-agent.exe"),
	})
	if _, err := manager.Machines.Ensure("PC"); err != nil {
		t.Fatal(err)
	}

	_, slaves, err := manager.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(slaves) != 1 {
		t.Fatalf("slaves=%+v", slaves)
	}
	got := slaves[0]
	if got.Status != StatusRunning || got.PID != 3333 || got.LastError != "" {
		t.Fatalf("live slave was reconciled incorrectly=%+v", got)
	}
}

func TestManagerPauseDoesNotStopPIDThatDoesNotMatchSlaveExe(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	registry := NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves"))
	sl, err := registry.Create(Machine{MachineID: "machine-1", ComputerName: "PC"}, CreateInput{Folder: folder})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Update(sl.ID, func(s *Slave) error {
		s.Status = StatusRunning
		s.PID = 4444
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	runner := &inspectingFakeRunner{matches: map[int]bool{4444: false}}
	manager := NewManager(ManagerDeps{
		Registry: registry,
		Runner:   runner,
		SlaveExe: filepath.Join(dir, "slave-agent.exe"),
	})

	got, err := manager.Pause(context.Background(), sl.ID)
	if err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if runner.stopped[4444] {
		t.Fatalf("Stop was called for PID that does not match slave executable")
	}
	if got.Status != StatusError || got.PID != 0 || !strings.Contains(got.LastError, ErrProcessNotRunning.Error()) {
		t.Fatalf("reconciled slave=%+v", got)
	}
}

func TestManagerDeleteReturnsInspectionErrorAndPreservesRegistry(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	registry := NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves"))
	sl, err := registry.Create(Machine{MachineID: "machine-1", ComputerName: "PC"}, CreateInput{Folder: folder})
	if err != nil {
		t.Fatal(err)
	}
	trustedDir, err := registry.storageDir(sl.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := mkdir(trustedDir); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Update(sl.ID, func(s *Slave) error {
		s.Status = StatusRunning
		s.PID = 5555
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	inspectErr := errors.New("query process image: access denied")
	runner := &failingInspectionRunner{inspectErr: inspectErr}
	manager := NewManager(ManagerDeps{
		Registry: registry,
		Runner:   runner,
		SlaveExe: filepath.Join(dir, "slave-agent.exe"),
	})

	if err := manager.Delete(context.Background(), sl.ID); !errors.Is(err, inspectErr) {
		t.Fatalf("Delete error=%v, want %v", err, inspectErr)
	}
	if runner.stopped[5555] {
		t.Fatalf("Stop was called after process inspection failed")
	}
	if _, err := os.Stat(trustedDir); err != nil {
		t.Fatalf("trusted dir should be preserved after inspection failure: %v", err)
	}
	got, err := manager.Registry.Get(sl.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PID != 5555 || got.Status != StatusRunning {
		t.Fatalf("slave should preserve runtime state after inspection failure=%+v", got)
	}
}

func TestExecRunnerStartReturnsErrorOnEarlyProcessExit(t *testing.T) {
	dir := t.TempDir()
	exe := writeLinuxShellScript(t, "exit 7\n")

	_, err := (execRunner{}).Start(context.Background(), StartRequest{
		Exe:        exe,
		ConfigPath: filepath.Join(dir, "config.yaml"),
		LogPath:    filepath.Join(dir, "logs", "slave.log"),
		WorkDir:    dir,
	})
	if err == nil {
		t.Fatal("expected early process exit error")
	}
	if !strings.Contains(err.Error(), "startup") && !strings.Contains(err.Error(), "exit") {
		t.Fatalf("early exit error=%v", err)
	}
}

func TestExecRunnerStartLeavesChildAliveAfterStartupContextCancel(t *testing.T) {
	dir := t.TempDir()
	exe := writeLinuxShellScript(t, "echo 'https://agent.cs.ac.cn/device?user_code=ABCD'\nexec sleep 30\n")
	ctx, cancel := context.WithCancel(context.Background())
	res, err := (execRunner{}).Start(ctx, StartRequest{
		Exe:        exe,
		ConfigPath: filepath.Join(dir, "config.yaml"),
		LogPath:    filepath.Join(dir, "logs", "slave.log"),
		WorkDir:    dir,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = (execRunner{}).Stop(context.Background(), res.PID)
	})
	if res.PID == 0 || res.AuthURL == "" {
		t.Fatalf("Start result=%+v", res)
	}

	cancel()
	time.Sleep(100 * time.Millisecond)
	if !linuxProcessExists(res.PID) {
		t.Fatalf("process %d was killed by startup context cancellation", res.PID)
	}
}

func TestExecRunnerStartCancelsAndKillsChildBeforeStartupSuccess(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	exe := writeLinuxShellScript(t, "echo $$ > \""+pidFile+"\"\nexec sleep 30\n")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	_, err := (execRunner{}).Start(ctx, StartRequest{
		Exe:        exe,
		ConfigPath: filepath.Join(dir, "config.yaml"),
		LogPath:    filepath.Join(dir, "logs", "slave.log"),
		WorkDir:    dir,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Start error=%v, want context.Canceled", err)
	}

	pidText, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidText)))
	if err != nil {
		t.Fatalf("parse pid %q: %v", pidText, err)
	}
	assertLinuxProcessExits(t, pid)
}

func TestExecRunnerStartPassesConfigArgAndLogsStdoutAndStderr(t *testing.T) {
	dir := t.TempDir()
	argFile := filepath.Join(dir, "arg")
	exe := writeLinuxShellScript(t, "printf '%s' \"$1\" > \""+argFile+"\"\necho stdout-line\necho stderr-line >&2\necho 'https://agent.cs.ac.cn/device?user_code=ABCD'\nexec sleep 30\n")
	logPath := filepath.Join(dir, "logs", "slave.log")
	configPath := filepath.Join(dir, "config.yaml")

	res, err := (execRunner{}).Start(context.Background(), StartRequest{
		Exe:        exe,
		ConfigPath: configPath,
		LogPath:    logPath,
		WorkDir:    dir,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = (execRunner{}).Stop(context.Background(), res.PID)
	})

	gotArg, err := os.ReadFile(argFile)
	if err != nil {
		t.Fatalf("read arg file: %v", err)
	}
	if string(gotArg) != configPath {
		t.Fatalf("argv[1]=%q want %q", gotArg, configPath)
	}
	assertLogContains(t, logPath, "stdout-line", "stderr-line")
}

func TestExecRunnerStartHidesSlaveProcessWindow(t *testing.T) {
	s := readPackageSourceFile(t, "process.go")
	command := strings.Index(s, "cmd := exec.Command(req.Exe, req.ConfigPath)")
	hide := strings.Index(s, "process.HideWindow(cmd)")
	start := strings.Index(s, "if err := cmd.Start(); err != nil")
	if command < 0 || hide < 0 || start < 0 {
		t.Fatal("execRunner.Start should create, hide, then start the slave process")
	}
	if command > hide || hide > start {
		t.Fatal("execRunner.Start should call process.HideWindow before cmd.Start")
	}
}

func TestExecRunnerStopStopsTrackedProcessAndWaits(t *testing.T) {
	dir := t.TempDir()
	exe := writeLinuxShellScript(t, "echo 'https://agent.cs.ac.cn/device?user_code=ABCD'\nexec sleep 30\n")
	res, err := (execRunner{}).Start(context.Background(), StartRequest{
		Exe:        exe,
		ConfigPath: filepath.Join(dir, "config.yaml"),
		LogPath:    filepath.Join(dir, "logs", "slave.log"),
		WorkDir:    dir,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if res.PID == 0 {
		t.Fatalf("Start result=%+v", res)
	}

	if err := (execRunner{}).Stop(context.Background(), res.PID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	assertLinuxProcessExits(t, res.PID)
}

func TestExecRunnerStopStopsUntrackedOSProcess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses Linux shell and /proc process checks")
	}
	exe := writeLinuxShellScript(t, "exec sleep 30\n")
	cmd := exec.Command(exe)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start untracked process: %v", err)
	}
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()
	t.Cleanup(func() {
		if linuxProcessExists(cmd.Process.Pid) {
			_ = cmd.Process.Kill()
		}
		<-waitCh
	})

	if err := (execRunner{}).Stop(context.Background(), cmd.Process.Pid); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	assertLinuxProcessExits(t, cmd.Process.Pid)
}

func TestExecRunnerStopProcessStopsUntrackedMatchingExecutable(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses Linux shell and /proc process checks")
	}
	sleepPath, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatalf("find sleep: %v", err)
	}
	cmd := exec.Command(sleepPath, "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start untracked process: %v", err)
	}
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()
	t.Cleanup(func() {
		if linuxProcessExists(cmd.Process.Pid) {
			_ = cmd.Process.Kill()
		}
		<-waitCh
	})

	if err := (execRunner{}).StopProcess(context.Background(), cmd.Process.Pid, sleepPath); err != nil {
		t.Fatalf("StopProcess: %v", err)
	}
	assertLinuxProcessExits(t, cmd.Process.Pid)
}

func TestExecRunnerStopProcessRefusesUntrackedNonMatchingExecutable(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses Linux shell and /proc process checks")
	}
	dir := t.TempDir()
	exe := writeLinuxShellScript(t, "exec sleep 30\n")
	otherExe := filepath.Join(dir, "other-agent")
	if err := os.WriteFile(otherExe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start untracked process: %v", err)
	}
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()
	t.Cleanup(func() {
		if linuxProcessExists(cmd.Process.Pid) {
			_ = cmd.Process.Kill()
		}
		<-waitCh
	})

	err := (execRunner{}).StopProcess(context.Background(), cmd.Process.Pid, otherExe)
	if !errors.Is(err, ErrProcessNotRunning) {
		t.Fatalf("StopProcess error=%v, want ErrProcessNotRunning", err)
	}
	if !linuxProcessExists(cmd.Process.Pid) {
		t.Fatalf("non-matching process %d was killed", cmd.Process.Pid)
	}
}

func TestExecRunnerStopReturnsProcessNotRunningForMissingPID(t *testing.T) {
	if err := (execRunner{}).Stop(context.Background(), 999999); !errors.Is(err, ErrProcessNotRunning) {
		t.Fatalf("Stop error=%v, want ErrProcessNotRunning", err)
	}
}

func TestAuthURLDetectorIgnoresGenericURLAndCapturesDeviceURL(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "device user_code URL",
			body: "docs: https://example.com/help\nopen: https://agent.cs.ac.cn/device?user_code=ABCD\n",
			want: "https://agent.cs.ac.cn/device?user_code=ABCD",
		},
		{
			name: "hyphen user-code URL",
			body: "docs: https://example.com/help\nopen: https://agent.cs.ac.cn/user-code?code=ABCD\n",
			want: "https://agent.cs.ac.cn/user-code?code=ABCD",
		},
		{
			name: "verification URL",
			body: "docs: https://example.com/help\nopen: https://agent.cs.ac.cn/verification?code=ABCD\n",
			want: "https://agent.cs.ac.cn/verification?code=ABCD",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authCh := make(chan string, 1)
			copyAndDetectURL(strings.NewReader(tt.body), io.Discard, authCh)

			select {
			case got := <-authCh:
				if got != tt.want {
					t.Fatalf("auth URL=%q want %q", got, tt.want)
				}
			default:
				t.Fatal("auth URL not detected")
			}
		})
	}
}

func TestManagerMethodsReturnErrorsForNilDependencies(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	machines := NewMachineStore(filepath.Join(dir, "machine.json"))
	if _, err := machines.Ensure("PC"); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "List nil machines",
			call: func() error {
				_, _, err := NewManager(ManagerDeps{}).List(ctx)
				return err
			},
		},
		{
			name: "CreateAndStart nil machines",
			call: func() error {
				_, err := NewManager(ManagerDeps{Registry: NewRegistry(filepath.Join(dir, "a.json"), filepath.Join(dir, "a")), Runner: &fakeRunner{}}).CreateAndStart(ctx, CreateInput{Folder: folder})
				return err
			},
		},
		{
			name: "CreateAndStart nil registry",
			call: func() error {
				_, err := NewManager(ManagerDeps{Machines: machines, Runner: &fakeRunner{}}).CreateAndStart(ctx, CreateInput{Folder: folder})
				return err
			},
		},
		{
			name: "Restart nil registry",
			call: func() error {
				_, err := NewManager(ManagerDeps{Runner: &fakeRunner{}}).Restart(ctx, "missing")
				return err
			},
		},
		{
			name: "Pause nil registry",
			call: func() error {
				_, err := NewManager(ManagerDeps{Runner: &fakeRunner{}}).Pause(ctx, "missing")
				return err
			},
		},
		{
			name: "Delete nil registry",
			call: func() error {
				return NewManager(ManagerDeps{Runner: &fakeRunner{}}).Delete(ctx, "missing")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("method panicked: %v", r)
				}
			}()
			if err := tt.call(); err == nil {
				t.Fatal("expected configuration error")
			}
		})
	}
}

type fakeRunner struct {
	pid           int
	authURL       string
	authURLs      <-chan string
	startedConfig string
	stopped       map[int]bool
	startCalls    int
	startErr      error
	stopErr       error
	onStart       func()
}

func (f *fakeRunner) Start(_ context.Context, req StartRequest) (StartResult, error) {
	f.startCalls++
	if f.stopped == nil {
		f.stopped = map[int]bool{}
	}
	if f.startErr != nil {
		return StartResult{}, f.startErr
	}
	f.startedConfig = req.ConfigPath
	if f.onStart != nil {
		f.onStart()
	}
	return StartResult{PID: f.pid, AuthURL: f.authURL, AuthURLs: f.authURLs}, nil
}

func (f *fakeRunner) Stop(_ context.Context, pid int) error {
	if f.stopped == nil {
		f.stopped = map[int]bool{}
	}
	f.stopped[pid] = true
	return f.stopErr
}

type inspectingFakeRunner struct {
	fakeRunner
	matches map[int]bool
}

func (f *inspectingFakeRunner) InspectProcess(pid int, _ string) (processInspection, error) {
	if f.matches[pid] {
		return processMatch, nil
	}
	return processMismatch, nil
}

type failingInspectionRunner struct {
	fakeRunner
	inspectErr error
}

func (f *failingInspectionRunner) InspectProcess(int, string) (processInspection, error) {
	return processUnknown, f.inspectErr
}

func writeReadyCredentials(t *testing.T, path string) {
	t.Helper()
	cfg := readConfig(t, path)
	cfg.Credentials = parsedCredentials{
		SandboxID:   "sandbox-1",
		TunnelToken: "tunnel-token",
		ProxyToken:  "proxy-token",
		WorkspaceID: "workspace-1",
		ShortID:     "short-1",
	}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write ready config: %v", err)
	}
}

func writeLinuxShellScript(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("uses Linux shell and /proc process checks")
	}
	path := filepath.Join(t.TempDir(), "slave-agent")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func linuxProcessExists(pid int) bool {
	_, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid)))
	return err == nil
}

func assertLinuxProcessExits(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !linuxProcessExists(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("process %d still exists", pid)
}

func assertLogContains(t *testing.T, path string, wants ...string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var body []byte
	for time.Now().Before(deadline) {
		var err error
		body, err = os.ReadFile(path)
		if err == nil {
			allFound := true
			for _, want := range wants {
				if !strings.Contains(string(body), want) {
					allFound = false
					break
				}
			}
			if allFound {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("log %s missing %v; body=%q", path, wants, body)
}

func receiveOpenedURL(t *testing.T, opened <-chan string) string {
	t.Helper()
	select {
	case got := <-opened:
		return got
	case <-time.After(time.Second):
		t.Fatal("auth URL was not opened")
		return ""
	}
}

func withStartupTimeout(t *testing.T, timeout time.Duration) {
	t.Helper()
	old := startupTimeout
	startupTimeout = timeout
	t.Cleanup(func() {
		startupTimeout = old
	})
}

func withReadinessTimeout(t *testing.T, timeout time.Duration) {
	t.Helper()
	old := readinessTimeout
	readinessTimeout = timeout
	t.Cleanup(func() {
		readinessTimeout = old
	})
}

func waitForSlave(t *testing.T, reg *Registry, id string, pred func(Slave) bool) Slave {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last Slave
	for time.Now().Before(deadline) {
		sl, err := reg.Get(id)
		if err == nil {
			last = sl
			if pred(sl) {
				return sl
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("slave %s did not reach expected state; last=%+v", id, last)
	return Slave{}
}
