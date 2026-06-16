//go:build !windows && !darwin

package uninstall

import "context"

func stopInstallProcesses(context.Context, string, []string) error {
	return nil
}
