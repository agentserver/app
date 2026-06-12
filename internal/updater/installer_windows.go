//go:build windows

package updater

import (
	"context"
	"os/exec"

	"github.com/agentserver/agentserver-pkg/internal/process"
)

func StartInstaller(ctx context.Context, path string) error {
	cmd := exec.CommandContext(ctx, path)
	process.HideWindow(cmd)
	return cmd.Start()
}
