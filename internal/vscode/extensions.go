package vscode

import (
	"context"
	"fmt"
	"os/exec"
)

type Installer struct {
	CodeExe       string
	UserDataDir   string
	ExtensionsDir string
	Extensions    []string // ids ("publisher.name") or absolute .vsix paths
}

func InstallExtensions(ctx context.Context, in Installer) error {
	if in.CodeExe == "" || in.UserDataDir == "" || in.ExtensionsDir == "" {
		return fmt.Errorf("InstallExtensions: CodeExe/UserDataDir/ExtensionsDir required")
	}
	for _, ext := range in.Extensions {
		args := []string{
			"--user-data-dir", in.UserDataDir,
			"--extensions-dir", in.ExtensionsDir,
			"--install-extension", ext,
			"--force",
		}
		cmd := exec.CommandContext(ctx, in.CodeExe, args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("install %s: %w (%s)", ext, err, out)
		}
	}
	return nil
}
