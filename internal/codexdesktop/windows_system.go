//go:build windows

package codexdesktop

import (
	"fmt"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func systemExecutablePath(name string) (string, error) {
	if name == "" || filepath.Base(name) != name {
		return "", fmt.Errorf("invalid system executable name %q", name)
	}
	systemDirectory, err := windows.GetSystemDirectory()
	if err != nil {
		return "", fmt.Errorf("GetSystemDirectory: %w", err)
	}
	if !filepath.IsAbs(systemDirectory) {
		return "", fmt.Errorf("system directory is not absolute: %q", systemDirectory)
	}
	var executable string
	switch name {
	case "powershell.exe":
		executable = filepath.Join(systemDirectory, "WindowsPowerShell", "v1.0", "powershell.exe")
	default:
		executable = filepath.Join(systemDirectory, name)
	}
	if !filepath.IsAbs(executable) {
		return "", fmt.Errorf("system executable path is not absolute: %q", executable)
	}
	return executable, nil
}
