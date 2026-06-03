//go:build !windows

package shortcut

import "errors"

func ensureDesktopShortcutPlatform(DesktopInput) error {
	return errors.New("shortcut: only Windows is supported in v1")
}
func installContextMenuPlatform(ContextMenuInput) error {
	return errors.New("shortcut: only Windows is supported in v1")
}
func uninstallAllPlatform(ContextMenuInput, string) error { return nil }
