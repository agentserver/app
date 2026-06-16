//go:build darwin

package shortcut

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// appBundleFromExe derives the .app bundle root from an executable path inside
// Contents/MacOS/. e.g. .../星池指挥官.app/Contents/MacOS/launcher -> .../星池指挥官.app
func appBundleFromExe(exe string) (string, error) {
	macOS := filepath.Dir(exe)       // .../Contents/MacOS
	contents := filepath.Dir(macOS)  // .../Contents
	bundle := filepath.Dir(contents) // .../星池指挥官.app
	if filepath.Ext(bundle) != ".app" {
		return "", fmt.Errorf("shortcut: %q is not inside an .app bundle", exe)
	}
	return bundle, nil
}

func ensureDesktopShortcutPlatform(in DesktopInput) error {
	bundle, err := appBundleFromExe(in.TargetExe)
	if err != nil {
		return err
	}
	desktop, err := filepath.Abs(filepath.Join(os.Getenv("HOME"), "Desktop"))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(desktop, 0o755); err != nil {
		return err
	}
	script := fmt.Sprintf(`tell application "Finder" to make alias file to (POSIX file %q) to (POSIX file %q)`, bundle, desktop)
	if err := exec.Command("osascript", "-e", script).Run(); err != nil {
		// 回退 symlink（丢失图标，但保证有入口）。
		return os.Symlink(bundle, filepath.Join(desktop, in.Name))
	}
	return nil
}

func installContextMenuPlatform(in ContextMenuInput) error {
	// 1) 记录 open-folder 真实路径，供 Quick Action 间接读取（位置无关）。
	dataRoot := filepath.Join(os.Getenv("HOME"), ".agentserver-app")
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dataRoot, "open-folder-path.txt"), []byte(in.HandlerExe+"\n"), 0o644); err != nil {
		return fmt.Errorf("write open-folder-path.txt: %w", err)
	}
	// 2) 拷贝随包 workflow 模板到 ~/Library/Services/。
	bundle, err := appBundleFromExe(in.HandlerExe)
	if err != nil {
		return err
	}
	src := filepath.Join(bundle, "Contents", "Resources", in.MenuLabel+".workflow")
	dstDir := filepath.Join(os.Getenv("HOME"), "Library", "Services")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	dst := filepath.Join(dstDir, in.MenuLabel+".workflow")
	_ = os.RemoveAll(dst)
	return copyAll(src, dst)
}

func uninstallAllPlatform(in ContextMenuInput, desktopName string) error {
	if desktopName != "" {
		_ = os.Remove(filepath.Join(os.Getenv("HOME"), "Desktop", desktopName))
	}
	_ = os.RemoveAll(filepath.Join(os.Getenv("HOME"), "Library", "Services", in.MenuLabel+".workflow"))
	_ = os.Remove(filepath.Join(os.Getenv("HOME"), ".agentserver-app", "open-folder-path.txt"))
	return nil
}

// copyAll recursively copies a directory tree (used for the .workflow bundle).
func copyAll(src, dst string) error {
	return exec.Command("cp", "-R", src, dst).Run()
}
