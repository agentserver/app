//go:build windows

package codexdesktop

import (
	"os/exec"
	"strings"
)

func detectPlatform() (Detected, error) {
	script := `$paths = @('Registry::HKEY_CURRENT_USER\Software\Classes\codex\shell\open\command','Registry::HKEY_LOCAL_MACHINE\Software\Classes\codex\shell\open\command'); foreach ($p in $paths) { if (Test-Path $p) { Write-Output 'url-scheme'; exit 0 } }; $pkg = Get-AppxPackage | Where-Object { $_.Name -like '*Codex*' -or $_.PackageFullName -like '*Codex*' } | Select-Object -First 1; if ($pkg) { Write-Output $pkg.Version; exit 0 }; exit 1`
	out, err := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script).CombinedOutput()
	if err != nil {
		return Detected{Installed: false}, ErrNotFound
	}
	version := strings.TrimSpace(string(out))
	if version == "url-scheme" {
		version = ""
	}
	return Detected{Installed: true, Version: version}, nil
}
