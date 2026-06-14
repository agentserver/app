//go:build windows

package opencodedesktop

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const installerSignatureTimeout = 30 * time.Second

func validateInstallerSignature(ctx context.Context, path string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	sigCtx, cancel := context.WithTimeout(ctx, installerSignatureTimeout)
	defer cancel()

	const script = `
$ErrorActionPreference = 'Stop'
$sig = Get-AuthenticodeSignature -FilePath $args[0]
$subject = if ($sig.SignerCertificate) { $sig.SignerCertificate.Subject } else { '<none>' }
if ($sig.Status -ne 'Valid') {
    throw "OpenCode Desktop installer Authenticode signature is $($sig.Status); signer subject: $subject"
}
if ($null -eq $sig.SignerCertificate) {
    throw "OpenCode Desktop installer has no signer certificate; signer subject: $subject"
}
`
	cmd := exec.CommandContext(sigCtx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script, path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" && sigCtx.Err() != nil {
			msg = sigCtx.Err().Error()
		}
		if msg == "" {
			msg = "powershell signature validation command failed"
		}
		return fmt.Errorf("validate OpenCode Desktop installer signature: %w: %s", err, msg)
	}
	return nil
}
