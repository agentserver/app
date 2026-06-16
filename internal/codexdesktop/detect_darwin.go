//go:build darwin

package codexdesktop

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func detectPlatform() (Detected, error) {
	plist := "/Applications/Codex.app/Contents/Info.plist"
	if _, err := os.Stat(plist); err != nil {
		alt := filepath.Join(os.Getenv("HOME"), "Applications", "Codex.app", "Contents", "Info.plist")
		if _, err := os.Stat(alt); err != nil {
			return Detected{Installed: false}, ErrNotFound
		}
		plist = alt
	}
	return Detected{Installed: true, Version: readBundleShortVersion(plist)}, nil
}

func readBundleShortVersion(plist string) string {
	out, err := exec.Command("defaults", "read", plist, "CFBundleShortVersionString").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
