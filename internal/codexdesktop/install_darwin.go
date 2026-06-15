//go:build darwin

package codexdesktop

import (
	"context"
	"fmt"
	"os/exec"
)

// Codex Desktop darwin download URL (dmg/zip). NOTE (spec §10): the exact asset
// URL/format must be confirmed before release; this named constant is the single
// place to update.
const darwinCodexDesktopURL = "https://desktop.openai.com/download/codex-mac-universal.dmg"

func installDesktopPlatform(ctx context.Context) error {
	return installDesktopDarwin(ctx)
}

func installDesktopDarwin(ctx context.Context) error {
	cache, err := downloadToCache(ctx, darwinCodexDesktopURL)
	if err != nil {
		return fmt.Errorf("download codex desktop: %w", err)
	}
	if err := installDMGApp(ctx, cache, "Codex.app"); err != nil {
		return fmt.Errorf("install codex desktop dmg: %w", err)
	}
	_ = exec.CommandContext(ctx, "xattr", "-dr", "com.apple.quarantine", "/Applications/Codex.app").Run()
	return nil
}
