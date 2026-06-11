//go:build windows

package vscode

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
)

func detectPlatform() (Detected, error) {
	// 1. Try `where code.cmd` / `code.exe` in PATH.
	for _, name := range []string{"code.cmd", "code.exe", "code"} {
		if p, err := exec.LookPath(name); err == nil {
			if det, err := detectAt(p); err == nil {
				return det, nil
			}
		}
	}
	// 2. Microsoft Store aliases + standard install locations.
	candidates := detectCandidatesWindows(os.Getenv("LOCALAPPDATA"),
		os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"))
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			if det, err := detectAt(c); err == nil {
				return det, nil
			}
		}
	}
	return Detected{Installed: false}, errors.New("VS Code not found")
}
