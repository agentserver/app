package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileFallbackRoundtrip(t *testing.T) {
	dir := t.TempDir()
	s := newFileStore(filepath.Join(dir, "secrets.json"))
	if err := s.Set("k1", "v1"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("k1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "v1" {
		t.Errorf("got %q want v1", got)
	}
	if err := s.Delete("k1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("k1"); err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestFileFallbackMissing(t *testing.T) {
	dir := t.TempDir()
	s := newFileStore(filepath.Join(dir, "missing.json"))
	if _, err := s.Get("nope"); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestFileFallbackPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.json")
	s := newFileStore(path)
	if err := s.Set("k", "v"); err != nil {
		t.Fatal(err)
	}
	info, err := stat(path)
	if err != nil {
		t.Fatal(err)
	}
	mode := info.Mode().Perm()
	// On Windows the umask may differ; only enforce on Unix.
	if mode > 0o600 {
		t.Errorf("secrets file too permissive: %v", mode)
	}
}

func TestFileFallbackSaveUsesAtomicSyncAndRename(t *testing.T) {
	body, err := os.ReadFile("secrets.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	for _, want := range []string{"os.CreateTemp(", ".Sync()", "os.Rename("} {
		if !strings.Contains(source, want) {
			t.Fatalf("secrets file fallback should use %s before publishing:\n%s", want, source)
		}
	}
	if strings.Contains(source, "os.WriteFile(f.path") {
		t.Fatalf("secrets file fallback should not publish directly with os.WriteFile:\n%s", source)
	}
}

func TestDarwinKeyringSetDoesNotPutSecretValueInArgv(t *testing.T) {
	body, err := os.ReadFile("keyring_darwin.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	if strings.Contains(source, `"-w", value`) {
		t.Fatalf("darwin keyring Set exposes the secret value in process argv:\n%s", source)
	}
	for _, want := range []string{"cmd.Stdin", "strings.NewReader(value"} {
		if !strings.Contains(source, want) {
			t.Fatalf("darwin keyring Set should pass secret via stdin; missing %q in:\n%s", want, source)
		}
	}
}
