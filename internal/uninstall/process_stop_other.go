//go:build !windows

package uninstall

import "context"

func stopInstallProcesses(context.Context, string, []string) error {
	return nil
}
