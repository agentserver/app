//go:build !windows

package vscode

import (
	"errors"
	"os/exec"
)

func detectPlatform() (Detected, error) {
	if p, err := exec.LookPath("code"); err == nil {
		return detectAt(p)
	}
	return Detected{Installed: false}, errors.New("VS Code not found")
}
