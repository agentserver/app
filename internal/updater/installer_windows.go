//go:build windows

package updater

import (
	"context"
	"os/exec"

	"github.com/agentserver/agentserver-pkg/internal/process"
)

func StartInstaller(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cmd := exec.Command(path)
	process.HideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
