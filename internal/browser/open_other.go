//go:build !windows

package browser

import (
	"context"
	"os/exec"
)

func openPlatform(ctx context.Context, url string) error {
	// Best-effort on dev hosts; Linux has xdg-open, macOS has open.
	for _, prog := range []string{"xdg-open", "open"} {
		if err := exec.CommandContext(ctx, prog, url).Start(); err == nil {
			return nil
		}
	}
	return nil
}
