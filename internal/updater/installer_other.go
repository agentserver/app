//go:build !windows && !darwin

package updater

import (
	"context"
	"fmt"
)

func StartInstaller(ctx context.Context, path string) error {
	_ = ctx
	return fmt.Errorf("installer start is unsupported on this platform: %s", path)
}
