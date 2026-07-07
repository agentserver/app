package console

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDriverDaemonStoreMissingDefaultsEnabled(t *testing.T) {
	store := NewDriverDaemonStore(filepath.Join(t.TempDir(), "driver-daemon.json"))

	st, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !st.Enabled {
		t.Fatalf("Enabled=false, want true for missing state")
	}
}

func TestDriverDaemonStoreCorruptStateFailsClosedAndCanBeOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "driver-daemon.json")
	if err := os.WriteFile(path, []byte("{bad json"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewDriverDaemonStore(path)

	st, err := store.Load()
	if err == nil || st.Enabled {
		t.Fatalf("Load err=%v enabled=%v, want corrupt fail-closed", err, st.Enabled)
	}

	if err := store.Save(DriverDaemonPersistedState{Enabled: false}); err != nil {
		t.Fatalf("Save overwrite corrupt: %v", err)
	}
	st, err = store.Load()
	if err != nil || st.Enabled {
		t.Fatalf("Load after overwrite err=%v enabled=%v, want disabled", err, st.Enabled)
	}
}

func TestDriverDaemonStoreWrites0600OnPOSIX(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows relies on per-user directory ACLs")
	}
	path := filepath.Join(t.TempDir(), "driver-daemon.json")
	store := NewDriverDaemonStore(path)

	if err := store.Save(DriverDaemonPersistedState{Enabled: false}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode=%#o, want 0600", got)
	}
}

func TestControllerDriverDaemonStateReturnsSanitizedUnavailable(t *testing.T) {
	dir := t.TempDir()
	rawPath := filepath.Join(dir, "driver-agent.exe")
	rawToken := "secret-token-value"
	c := NewController(Deps{
		DriverDaemonStore: NewDriverDaemonStore(filepath.Join(dir, "driver-daemon.json")),
		DriverDaemonRuntime: &fakeDriverDaemonRuntime{
			runningErr: errors.New(rawPath + " failed with " + rawToken),
		},
	})

	got, err := c.DriverDaemonState(context.Background())
	if err != nil {
		t.Fatalf("DriverDaemonState: %v", err)
	}
	if got.LastErrorCode != DriverDaemonStatusUnknown {
		t.Fatalf("LastErrorCode=%q, want %q", got.LastErrorCode, DriverDaemonStatusUnknown)
	}
	if strings.Contains(got.LastErrorMessage, rawPath) || strings.Contains(got.LastErrorMessage, rawToken) {
		t.Fatalf("state leaks raw error: %+v", got)
	}
}

func TestControllerSetDriverDaemonDisabledPersistsIntentBeforeStopError(t *testing.T) {
	dir := t.TempDir()
	store := NewDriverDaemonStore(filepath.Join(dir, "driver-daemon.json"))
	if err := store.Save(DriverDaemonPersistedState{
		Enabled: true,
		Processes: []DriverProcessRecord{{
			PID:       1234,
			Exe:       filepath.Join(dir, "driver-agent.exe"),
			Args:      []string{"serve-daemon", "--config", filepath.Join(dir, "driver.yaml")},
			CreatedAt: "linux:boot:1",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	c := NewController(Deps{
		DriverDaemonStore:   store,
		DriverDaemonRuntime: &fakeDriverDaemonRuntime{stopErr: errors.New("cannot stop " + dir)},
	})

	got, err := c.SetDriverDaemonEnabled(context.Background(), false)
	if err != nil {
		t.Fatalf("SetDriverDaemonEnabled: %v", err)
	}
	if got.Enabled {
		t.Fatalf("Enabled=true, want false after disable stop failure")
	}
	if got.LastErrorCode != DriverDaemonStopFailed {
		t.Fatalf("LastErrorCode=%q, want %q", got.LastErrorCode, DriverDaemonStopFailed)
	}
	persisted, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if persisted.Enabled {
		t.Fatalf("persisted enabled=true, want false")
	}
	if strings.Contains(persisted.LastErrorMessage, dir) {
		t.Fatalf("persisted error leaks path: %+v", persisted)
	}
}

func TestControllerSetDriverDaemonEnableDoesNotPersistOnStartError(t *testing.T) {
	dir := t.TempDir()
	store := NewDriverDaemonStore(filepath.Join(dir, "driver-daemon.json"))
	if err := store.Save(DriverDaemonPersistedState{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	c := NewController(Deps{
		DriverDaemonStore:   store,
		DriverDaemonRuntime: &fakeDriverDaemonRuntime{startErr: errors.New("missing " + dir)},
	})

	got, err := c.SetDriverDaemonEnabled(context.Background(), true)
	if err != nil {
		t.Fatalf("SetDriverDaemonEnabled: %v", err)
	}
	if got.Enabled {
		t.Fatalf("Enabled=true, want false after start failure")
	}
	persisted, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if persisted.Enabled {
		t.Fatalf("persisted enabled=true, want false")
	}
	if persisted.LastErrorCode != DriverDaemonStartFailed {
		t.Fatalf("LastErrorCode=%q, want %q", persisted.LastErrorCode, DriverDaemonStartFailed)
	}
}

func TestControllerSetDriverDaemonEnableSuccessPersistsProcessMetadata(t *testing.T) {
	dir := t.TempDir()
	store := NewDriverDaemonStore(filepath.Join(dir, "driver-daemon.json"))
	if err := store.Save(DriverDaemonPersistedState{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	record := DriverProcessRecord{
		PID:       1234,
		Exe:       filepath.Join(dir, "driver-agent.exe"),
		Args:      []string{"serve-daemon", "--config", filepath.Join(dir, "driver.yaml")},
		CreatedAt: "linux:boot:123",
	}
	c := NewController(Deps{
		DriverDaemonStore:   store,
		DriverDaemonRuntime: &fakeDriverDaemonRuntime{startRecords: []DriverProcessRecord{record}, running: true},
	})

	got, err := c.SetDriverDaemonEnabled(context.Background(), true)
	if err != nil {
		t.Fatalf("SetDriverDaemonEnabled: %v", err)
	}
	if !got.Enabled || !got.Running {
		t.Fatalf("state=%+v, want enabled and running", got)
	}
	persisted, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(persisted.Processes) != 1 || persisted.Processes[0].PID != record.PID ||
		persisted.Processes[0].Exe != record.Exe || persisted.Processes[0].CreatedAt != record.CreatedAt {
		t.Fatalf("persisted processes=%+v, want %+v", persisted.Processes, record)
	}
}

func TestControllerSetDriverDaemonEnableWithoutRunningStateDoesNotPersistEnabled(t *testing.T) {
	dir := t.TempDir()
	store := NewDriverDaemonStore(filepath.Join(dir, "driver-daemon.json"))
	if err := store.Save(DriverDaemonPersistedState{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	c := NewController(Deps{
		DriverDaemonStore:   store,
		DriverDaemonRuntime: &fakeDriverDaemonRuntime{running: false},
	})

	got, err := c.SetDriverDaemonEnabled(context.Background(), true)
	if err != nil {
		t.Fatalf("SetDriverDaemonEnabled: %v", err)
	}
	if got.Enabled || got.LastErrorCode != DriverDaemonUnavailable {
		t.Fatalf("state=%+v, want disabled unavailable", got)
	}
	persisted, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if persisted.Enabled {
		t.Fatalf("persisted enabled=true, want false")
	}
}

func TestControllerDriverDaemonMutationsAreSerialized(t *testing.T) {
	dir := t.TempDir()
	store := NewDriverDaemonStore(filepath.Join(dir, "driver-daemon.json"))
	runtime := &fakeDriverDaemonRuntime{startDelay: 10 * time.Millisecond}
	c := NewController(Deps{DriverDaemonStore: store, DriverDaemonRuntime: runtime})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(enabled bool) {
			defer wg.Done()
			if _, err := c.SetDriverDaemonEnabled(context.Background(), enabled); err != nil {
				t.Errorf("SetDriverDaemonEnabled(%v): %v", enabled, err)
			}
		}(i%2 == 0)
	}
	wg.Wait()

	if runtime.maxConcurrent > 1 {
		t.Fatalf("mutations overlapped: maxConcurrent=%d", runtime.maxConcurrent)
	}
}

type fakeDriverDaemonRuntime struct {
	mu            sync.Mutex
	running       bool
	runningErr    error
	startErr      error
	stopErr       error
	startDelay    time.Duration
	startRecords  []DriverProcessRecord
	active        int
	maxConcurrent int
}

func (f *fakeDriverDaemonRuntime) Running(context.Context, []DriverProcessRecord) (bool, error) {
	if f.runningErr != nil {
		return false, f.runningErr
	}
	return f.running, nil
}

func (f *fakeDriverDaemonRuntime) Start(context.Context) ([]DriverProcessRecord, error) {
	f.enter()
	defer f.leave()
	if f.startDelay > 0 {
		time.Sleep(f.startDelay)
	}
	if f.startErr != nil {
		return nil, f.startErr
	}
	if len(f.startRecords) > 0 {
		f.running = true
	}
	return append([]DriverProcessRecord(nil), f.startRecords...), nil
}

func (f *fakeDriverDaemonRuntime) Stop(context.Context, []DriverProcessRecord) error {
	f.enter()
	defer f.leave()
	if f.stopErr != nil {
		return f.stopErr
	}
	f.running = false
	return nil
}

func (f *fakeDriverDaemonRuntime) enter() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.active++
	if f.active > f.maxConcurrent {
		f.maxConcurrent = f.active
	}
}

func (f *fakeDriverDaemonRuntime) leave() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.active--
}
