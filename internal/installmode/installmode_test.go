package installmode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agentserver/agentserver-pkg/internal/state"
)

func TestReadMissingDefaultsToCodexDesktop(t *testing.T) {
	got, err := Read(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("Read missing: %v", err)
	}
	if got != state.FrontendModeCodexDesktop {
		t.Fatalf("mode = %q", got)
	}
}

func TestReadInvalidDefaultsToCodexDesktop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "install-mode.json")
	if err := os.WriteFile(path, []byte(`{"frontend_mode":"bad"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read invalid: %v", err)
	}
	if got != state.FrontendModeCodexDesktop {
		t.Fatalf("mode = %q", got)
	}
}

func TestWriteAndReadMinimalVSCode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "install-mode.json")
	if err := Write(path, state.FrontendModeMinimalVSCode); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != state.FrontendModeMinimalVSCode {
		t.Fatalf("mode = %q", got)
	}
}

func TestSyncStoreUsesInstallModeFile(t *testing.T) {
	dir := t.TempDir()
	modePath := filepath.Join(dir, "install-mode.json")
	statePath := filepath.Join(dir, "state.json")
	if err := Write(modePath, state.FrontendModeMinimalVSCode); err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(statePath)
	if err := SyncStore(store, modePath); err != nil {
		t.Fatalf("SyncStore: %v", err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.FrontendMode != state.FrontendModeMinimalVSCode {
		t.Fatalf("FrontendMode = %q", got.FrontendMode)
	}
}
