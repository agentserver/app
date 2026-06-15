//go:build darwin

package vscode

import (
	"context"
	"fmt"
	"os/exec"
)

// silentInstallPlatform unzips the VS Code .zip into /Applications, verifies the
// extracted app's code signature, and clears Gatekeeper quarantine.
func silentInstallPlatform(ctx context.Context, path string, plan InstallPlan) error {
	if err := exec.CommandContext(ctx, "unzip", "-o", "-q", path, "-d", "/Applications").Run(); err != nil {
		return fmt.Errorf("unzip VS Code: %w", err)
	}
	if err := validateBootstrapperSignature(ctx, "/Applications/Visual Studio Code.app"); err != nil {
		return err
	}
	_ = exec.CommandContext(ctx, "xattr", "-dr", "com.apple.quarantine", "/Applications/Visual Studio Code.app").Run()
	return nil
}
