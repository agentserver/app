package env

import (
	"strings"
	"testing"
)

func TestInjectManagedBlock(t *testing.T) {
	existing := "# my zshrc\nexport PATH=...\n"
	out := injectManagedBlock(existing, "export FOO=bar", managedStartMarker, managedEndMarker)
	if !strings.Contains(out, managedStartMarker) || !strings.Contains(out, "export FOO=bar") || !strings.Contains(out, managedEndMarker) {
		t.Errorf("block not injected:\n%s", out)
	}
}

func TestInjectManagedBlockReplacesExisting(t *testing.T) {
	existing := "header\n" + managedStartMarker + "\nexport FOO=old\n" + managedEndMarker + "\nfooter\n"
	out := injectManagedBlock(existing, "export FOO=new", managedStartMarker, managedEndMarker)
	if strings.Contains(out, "FOO=old") {
		t.Error("old block should be replaced, not duplicated")
	}
	if !strings.Contains(out, "FOO=new") {
		t.Errorf("new block missing:\n%s", out)
	}
}

func TestRemoveManagedBlock(t *testing.T) {
	existing := "header\n" + managedStartMarker + "\nexport FOO=bar\n" + managedEndMarker + "\nfooter\n"
	out := removeManagedBlock(existing, managedStartMarker, managedEndMarker)
	if strings.Contains(out, "FOO=bar") || strings.Contains(out, managedStartMarker) {
		t.Errorf("block not removed:\n%s", out)
	}
	if !strings.Contains(out, "header") || !strings.Contains(out, "footer") {
		t.Error("non-managed content should be preserved")
	}
}
