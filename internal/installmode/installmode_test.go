package installmode

import (
	"os"
	"path/filepath"
	"strings"
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

func TestReadMalformedDefaultsToCodexDesktop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "install-mode.json")
	if err := os.WriteFile(path, []byte(`{not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read malformed: %v", err)
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

func TestWriteCreatesParentDirsAndTrailingNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "mode", "install-mode.json")
	if err := Write(path, state.FrontendModeMinimalVSCode); err != nil {
		t.Fatalf("Write: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if len(b) == 0 || b[len(b)-1] != '\n' {
		t.Fatalf("written file missing trailing newline: %q", string(b))
	}
}

func TestWriteNormalizesInvalidMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "install-mode.json")
	if err := Write(path, state.FrontendMode("bogus")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != state.FrontendModeCodexDesktop {
		t.Fatalf("mode = %q", got)
	}
}

func TestWriteWrapsWriteFileError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "install-mode.json")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	err := Write(path, state.FrontendModeCodexDesktop)
	if err == nil {
		t.Fatal("expected write error")
	}
	if !strings.Contains(err.Error(), "write install mode:") {
		t.Fatalf("error = %q, want write install mode wrapper", err)
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
