//go:build windows

package codexdesktop

import "context"

func installDesktopPlatform(ctx context.Context) error {
	out, err := runWinget(ctx, WingetInstallArgs())
	if err != nil {
		return ClassifyWingetError(err, out)
	}
	return nil
}
