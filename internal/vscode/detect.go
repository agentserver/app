// Package vscode covers detection, install, configuration, and extension
// management for the VS Code editor.
package vscode

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type Detected struct {
	Installed bool
	Path      string
	Version   string
}

// Detect tries to locate a usable `code` command and parse its version.
// On Windows checks standard install locations + PATH.
func Detect() (Detected, error) {
	return detectPlatform()
}

func detectAt(path string) (Detected, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return Detected{}, fmt.Errorf("%s --version: %w", path, err)
	}
	v := parseVersion(string(out))
	if v == "" {
		return Detected{}, fmt.Errorf("could not parse version from: %q", out)
	}
	return Detected{Installed: true, Path: path, Version: v}, nil
}

func parseVersion(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// First non-empty line is the version.
		return line
	}
	return ""
}
