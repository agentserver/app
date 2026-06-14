//go:build !windows

package opencodedesktop

import "context"

func validateInstallerSignature(context.Context, string) error {
	return nil
}
