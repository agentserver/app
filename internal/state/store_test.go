package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestStoreLoadMissing(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "state.json"))
	s, err := store.Load()
	if err != nil {
		t.Fatalf("load missing: %v", err)
	}
	if s.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("expected fresh state with schema %d, got %d",
			CurrentSchemaVersion, s.SchemaVersion)
	}
	if s.InstallID == "" {
		t.Errorf("expected generated install_id")
	}
}

func TestStoreSaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	store := NewStore(path)

	s, _ := store.Load()
	s.Onboarding.AddCompleted("modelserver_login")
	if err := store.Save(s); err != nil {
		t.Fatalf("save: %v", err)
	}

	store2 := NewStore(path)
	loaded, err := store2.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !loaded.Onboarding.HasCompleted("modelserver_login") {
		t.Errorf("step not persisted")
	}
}

func TestStoreLoadLegacyStateDefaultsFrontendMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := writeBytes(path, []byte(`{"schema_version":1,"install_id":"legacy-1"}`)); err != nil {
		t.Fatal(err)
	}

	store := NewStore(path)
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.FrontendMode != FrontendModeCodexDesktop {
		t.Fatalf("FrontendMode = %q, want %q", loaded.FrontendMode, FrontendModeCodexDesktop)
	}
}

func TestStoreSaveNormalizesFrontendMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	store := NewStore(path)

	if err := store.Save(&State{
		SchemaVersion: CurrentSchemaVersion,
		InstallID:     "save-1",
		FrontendMode:  FrontendMode("bogus"),
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved state: %v", err)
	}
	var got State
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal saved state: %v", err)
	}
	if got.FrontendMode != FrontendModeCodexDesktop {
		t.Fatalf("saved FrontendMode = %q, want %q", got.FrontendMode, FrontendModeCodexDesktop)
	}
}

func TestStoreUpdateConcurrent(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "state.json"))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = store.Update(func(s *State) error {
				s.Onboarding.AddCompleted("step")
				return nil
			})
		}(i)
	}
	wg.Wait()
	s, _ := store.Load()
	if len(s.Onboarding.CompletedSteps) != 1 {
		t.Errorf("dedup failed under concurrency: %v", s.Onboarding.CompletedSteps)
	}
}

func TestStoreRejectsReentrantAccessInsteadOfDeadlocking(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "state.json"))
	errCh := make(chan error, 1)
	go func() {
		errCh <- store.Update(func(*State) error {
			return store.Update(func(*State) error {
				return nil
			})
		})
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, ErrReentrantStoreAccess) {
			t.Fatalf("Update error=%v, want ErrReentrantStoreAccess", err)
		}
	case <-time.After(time.Second):
		t.Fatal("reentrant Store.Update deadlocked")
	}
}

func TestStoreUpdateUsesInterprocessLock(t *testing.T) {
	if path := os.Getenv("AGENTSERVER_STATE_LOCK_CHILD_PATH"); path != "" {
		runStoreLockChild(t, path, os.Getenv("AGENTSERVER_STATE_LOCK_CHILD_READY"))
		return
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	readyPath := filepath.Join(dir, "child-ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestStoreUpdateUsesInterprocessLock$")
	cmd.Env = append(os.Environ(),
		"AGENTSERVER_STATE_LOCK_CHILD_PATH="+path,
		"AGENTSERVER_STATE_LOCK_CHILD_READY="+readyPath,
	)
	var childOutput bytes.Buffer
	cmd.Stdout = &childOutput
	cmd.Stderr = &childOutput
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child test process: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
	waitForPath(t, readyPath)

	started := time.Now()
	if err := NewStore(path).Update(func(s *State) error {
		s.Modelserver.ProjectID = "parent"
		return nil
	}); err != nil {
		t.Fatalf("parent Update: %v", err)
	}
	if elapsed := time.Since(started); elapsed < 200*time.Millisecond {
		t.Fatalf("parent Update returned before child released file lock after %s", elapsed)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("child test process failed: %v\n%s", err, childOutput.String())
	}
	loaded, err := NewStore(path).Load()
	if err != nil {
		t.Fatalf("load final state: %v", err)
	}
	if loaded.Agentserver.WorkspaceID != "child" || loaded.Modelserver.ProjectID != "parent" {
		t.Fatalf("final state lost serialized updates: %+v", loaded)
	}
}

func runStoreLockChild(t *testing.T, path, readyPath string) {
	t.Helper()
	if err := NewStore(path).Update(func(s *State) error {
		s.Agentserver.WorkspaceID = "child"
		if err := os.WriteFile(readyPath, []byte("ready"), 0o644); err != nil {
			return err
		}
		time.Sleep(300 * time.Millisecond)
		return nil
	}); err != nil {
		t.Fatalf("child Update: %v", err)
	}
}

func waitForPath(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func TestStoreCorruptionRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	// Write garbage
	if err := writeBytes(path, []byte("{not json")); err != nil {
		t.Fatal(err)
	}
	store := NewStore(path)
	s, err := store.Load()
	if err != nil {
		t.Fatalf("expected recovery, got err: %v", err)
	}
	if s.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("expected fresh state after corruption")
	}
	// Backup file should exist
	matches, _ := filepath.Glob(path + ".corrupt-*")
	if len(matches) == 0 {
		t.Errorf("expected backup file")
	}
}

func writeBytes(path string, b []byte) error {
	return writeFile(path, b)
}

func TestStoreSaveRenameFailureCleansTmp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	// Force rename to fail by making the destination a directory.
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	store := NewStore(path)
	err := store.Save(&State{SchemaVersion: CurrentSchemaVersion, InstallID: "x"})
	if err == nil {
		t.Fatal("expected save error when target is a directory")
	}
	// No .tmp leftovers in dir
	matches, _ := filepath.Glob(filepath.Join(dir, "state.json.*.tmp"))
	if len(matches) != 0 {
		t.Errorf("tmp files leaked: %v", matches)
	}
}
