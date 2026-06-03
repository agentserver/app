package shortcut

import "testing"

func TestInputValidation(t *testing.T) {
	if err := EnsureDesktopShortcut(DesktopInput{}); err == nil {
		t.Errorf("expected error on empty input")
	}
	if err := InstallContextMenu(ContextMenuInput{}); err == nil {
		t.Errorf("expected error on empty input")
	}
}
