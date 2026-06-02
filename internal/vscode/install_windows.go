//go:build windows

package vscode

import (
	"context"
	"fmt"
	"os/exec"
)

func silentInstallPlatform(ctx context.Context, path string, plan InstallPlan) error {
	cmd := exec.CommandContext(ctx, path, plan.SilentArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("vscode installer %s %v: %w (%s)", path, plan.SilentArgs, err, out)
	}
	return nil
}
