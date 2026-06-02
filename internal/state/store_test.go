package state

import (
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
