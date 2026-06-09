package slave

import (
	"context"
	"errors"
	"io"
	"os"
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

func TestManagerPauseReconcilesPersistedUntrackedPIDWithoutStopping(t *testing.T) {
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
	runner := &trackingFakeRunner{}
	manager := NewManager(ManagerDeps{
		Registry: registry,
		Runner:   runner,
		SlaveExe: filepath.Join(dir, "slave-agent.exe"),
	})

	reconciled, err := manager.Pause(context.Background(), sl.ID)
	if err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if runner.stopped[1111] {
		t.Fatalf("Stop was called for untracked PID")
	}
	if reconciled.Status != StatusError || reconciled.PID != 0 || reconciled.AuthURL != "" || !strings.Contains(reconciled.LastError, ErrProcessNotTracked.Error()) {
		t.Fatalf("reconciled slave=%+v", reconciled)
	}

	paused, err := manager.Pause(context.Background(), sl.ID)
	if err != nil {
		t.Fatalf("second Pause: %v", err)
	}
	if paused.Status != StatusPaused || paused.PID != 0 {
		t.Fatalf("paused after reconciliation=%+v", paused)
	}
}

func TestManagerDeleteReconcilesPersistedUntrackedPIDWithoutStopping(t *testing.T) {
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
	runner := &trackingFakeRunner{}
	manager := NewManager(ManagerDeps{
		Registry: registry,
		Runner:   runner,
		SlaveExe: filepath.Join(dir, "slave-agent.exe"),
	})

	if err := manager.Delete(context.Background(), sl.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if runner.stopped[2222] {
		t.Fatalf("Stop was called for untracked PID")
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

func TestExecRunnerStopReturnsErrorForUntrackedNonzeroPID(t *testing.T) {
	if err := (execRunner{}).Stop(context.Background(), 999999); !errors.Is(err, ErrProcessNotTracked) {
		t.Fatalf("Stop error=%v, want ErrProcessNotTracked", err)
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
	return StartResult{PID: f.pid, AuthURL: f.authURL}, nil
}

func (f *fakeRunner) Stop(_ context.Context, pid int) error {
	if f.stopped == nil {
		f.stopped = map[int]bool{}
	}
	f.stopped[pid] = true
	return f.stopErr
}

type trackingFakeRunner struct {
	fakeRunner
	tracked map[int]bool
}

func (f *trackingFakeRunner) IsTracked(pid int) bool {
	return f.tracked[pid]
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

func withStartupTimeout(t *testing.T, timeout time.Duration) {
	t.Helper()
	old := startupTimeout
	startupTimeout = timeout
	t.Cleanup(func() {
		startupTimeout = old
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
