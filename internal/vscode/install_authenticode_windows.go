//go:build windows

package vscode

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const bootstrapperSignatureTimeout = 30 * time.Second

func validateBootstrapperSignature(ctx context.Context, path string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	sigCtx, cancel := context.WithTimeout(ctx, bootstrapperSignatureTimeout)
	defer cancel()

	const script = `
$ErrorActionPreference = 'Stop'
$ExpectedBootstrapperPublisherPattern = '(^|,\s*)(CN|O)=Microsoft (Corporation|Windows)(,|$)'
$sig = Get-AuthenticodeSignature -FilePath $args[0]
if ($sig.Status -ne 'Valid') {
    throw "VS Code Microsoft Store bootstrapper Authenticode signature is $($sig.Status)"
}
if ($null -eq $sig.SignerCertificate) {
    throw "VS Code Microsoft Store bootstrapper has no signer certificate"
}
$subject = $sig.SignerCertificate.Subject
if ($subject -notmatch $ExpectedBootstrapperPublisherPattern -and $subject -notmatch 'O=Microsoft Corporation') {
    throw "VS Code Microsoft Store bootstrapper signer is not Microsoft: $subject"
}
$chain = New-Object System.Security.Cryptography.X509Certificates.X509Chain
$chain.ChainPolicy.RevocationMode = [System.Security.Cryptography.X509Certificates.X509RevocationMode]::NoCheck
if (-not $chain.Build($sig.SignerCertificate)) {
    $statuses = ($chain.ChainStatus | ForEach-Object { $_.Status }) -join ', '
    throw "VS Code Microsoft Store bootstrapper signer chain is invalid: $statuses"
}
$chainSubjects = @($chain.ChainElements | ForEach-Object { $_.Certificate.Subject })
if (-not ($chainSubjects -match 'Microsoft')) {
    throw "VS Code Microsoft Store bootstrapper signer chain is not Microsoft"
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
		return fmt.Errorf("validate VS Code Microsoft Store bootstrapper signature: %w: %s", err, msg)
	}
	return nil
}
