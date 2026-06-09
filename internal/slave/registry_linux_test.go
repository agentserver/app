//go:build linux

package slave

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistrySaveDoesNotCorruptExistingRegistryOnWriteFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "slaves.json")
	existing := []byte("[]\n")
	if err := os.WriteFile(path, existing, 0o600); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(dir, "repo")
	if err := mkdir(folder); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(path, filepath.Join(dir, "slaves"))
	m := Machine{MachineID: "machine-1", ComputerName: "61414-PC"}

	var createErr error
	withFileSizeLimit(t, 0, func() {
		_, createErr = reg.Create(m, CreateInput{Folder: folder})
	})
	if createErr == nil {
		t.Fatal("expected save failure")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, existing) {
		t.Fatalf("registry file changed after failed save: got %q, want %q", got, existing)
	}
	loaded, err := reg.List()
	if err != nil {
		t.Fatalf("List after failed save: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("loaded=%+v, want no slaves", loaded)
	}
	assertNoRegistryTempFiles(t, dir)
}

func assertNoRegistryTempFiles(t *testing.T, dir string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".slaves-") && strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("temporary registry file left behind: %s", entry.Name())
		}
	}
}
