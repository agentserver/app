//go:build linux

package slave

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSameExecutableMatchesDeletedProcExeByPath(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "slave-agent.exe")
	if err := os.WriteFile(exe, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := sameExecutable(exe+" (deleted)", exe)
	if err != nil {
		t.Fatalf("sameExecutable: %v", err)
	}
	if !got {
		t.Fatal("sameExecutable should trust matching deleted proc exe path after app update")
	}

	other, err := sameExecutable(filepath.Join(dir, "other-agent.exe")+" (deleted)", exe)
	if err == nil {
		t.Fatal("sameExecutable should still fail when deleted proc exe path does not match expected exe")
	}
	if !strings.Contains(err.Error(), "stat") {
		t.Fatalf("error=%q, want stat context", err)
	}
	if other {
		t.Fatal("sameExecutable matched unrelated executable")
	}
}
