package shortcut

import (
	"os"
	"strings"
	"testing"
)

func TestInputValidation(t *testing.T) {
	if err := EnsureDesktopShortcut(DesktopInput{}); err == nil {
		t.Errorf("expected error on empty input")
	}
	if err := InstallContextMenu(ContextMenuInput{}); err == nil {
		t.Errorf("expected error on empty input")
	}
}

func TestWindowsShortcutSourceRegistersFileAndFolderMenus(t *testing.T) {
	body, err := os.ReadFile("shortcut_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		`Software\Classes\*\shell\`,
		`%1`,
		`%V`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("shortcut_windows.go missing %q", want)
		}
	}
}
