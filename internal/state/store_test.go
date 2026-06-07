package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
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
