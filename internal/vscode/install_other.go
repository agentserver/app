//go:build !windows

package vscode

import (
	"context"
	"errors"
)

func silentInstallPlatform(ctx context.Context, path string, plan InstallPlan) error {
	return errors.New("vscode.SilentInstall: only Windows is supported in v1")
}
