//go:build darwin

package vscode

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
)

func detectPlatform() (Detected, error) {
	candidates := []string{
		"/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code",
		filepath.Join(os.Getenv("HOME"), "Applications", "Visual Studio Code.app", "Contents", "Resources", "app", "bin", "code"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			if det, err := detectAt(c); err == nil {
				return det, nil
			}
		}
	}
	if p, err := exec.LookPath("code"); err == nil {
		return detectAt(p)
	}
	return Detected{Installed: false}, errors.New("VS Code not found")
}
