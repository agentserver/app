//go:build darwin

package vscode

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const macSignatureTimeout = 60 * time.Second

// validateBootstrapperSignature verifies the extracted app's code signature and
// Gatekeeper assessment. Replaces the Windows Authenticode path on macOS.
// (Called from silentInstallPlatform on /Applications/Visual Studio Code.app.)
func validateBootstrapperSignature(ctx context.Context, appPath string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	c, cancel := context.WithTimeout(ctx, macSignatureTimeout)
	defer cancel()
	if out, err := exec.CommandContext(c, "codesign", "--verify", "--deep", "--strict", appPath).CombinedOutput(); err != nil {
		return fmt.Errorf("codesign verify %s: %w: %s", appPath, err, strings.TrimSpace(string(out)))
	}
	// spctl assess: unsigned/unnotarized builds fail; v1 accepts that (user
	// right-click-opens), so only log, do not fail.
	_, _ = exec.CommandContext(c, "spctl", "--assess", "--type", "execute", "--verbose", appPath).CombinedOutput()
	return nil
}
