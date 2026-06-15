//go:build darwin

package updater

import (
	"fmt"
	"os"
	"path/filepath"
)

// replaceFile swaps a running .app bundle: a running Mach-O can't be deleted in
// place, but its bundle directory can be renamed. Old bundle → .old, new in,
// .old removed best-effort on next launch.
func replaceFile(src, dst string) error {
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("replaceFile src: %w", err)
	}
	old := dst + ".old"
	_ = os.RemoveAll(old)
	if err := os.Rename(dst, old); err != nil {
		return fmt.Errorf("rename old bundle: %w", err)
	}
	if err := os.Rename(src, dst); err != nil {
		_ = os.Rename(old, dst)
		return fmt.Errorf("rename new bundle: %w", err)
	}
	go func() { _ = os.RemoveAll(old) }()
	return nil
}

// CleanupOldBundles removes leftover *.app.old bundles next to the running app.
// Called by launcher on startup.
func CleanupOldBundles() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	bundle := filepath.Dir(filepath.Dir(filepath.Dir(exe))) // 星池指挥官.app
	dir := filepath.Dir(bundle)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".old" {
			_ = os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}
