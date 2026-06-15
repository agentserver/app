//go:build !windows && !darwin

package codexdesktop

import "context"

func installDesktopPlatform(ctx context.Context) error {
	return ErrUnsupportedPlatform
}
