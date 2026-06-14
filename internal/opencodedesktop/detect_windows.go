//go:build windows

package opencodedesktop

import (
	"fmt"
	"os/exec"

	"github.com/agentserver/agentserver-pkg/internal/process"
)

func detectPlatform() (Detected, error) {
	script := `
$ErrorActionPreference = 'Stop'
function Emit($installed, $path, $version) {
  [pscustomobject]@{installed=$installed; path=$path; version=$version} | ConvertTo-Json -Compress
}
$paths = @()
if ($env:LOCALAPPDATA) {
  $paths += (Join-Path $env:LOCALAPPDATA 'Programs\OpenCode\OpenCode.exe')
}
$paths += (Join-Path $env:USERPROFILE 'AppData\Local\Programs\OpenCode\OpenCode.exe')
foreach ($candidate in $paths) {
  if ($candidate -and (Test-Path -LiteralPath $candidate)) {
    $item = Get-Item -LiteralPath $candidate
    $version = $item.VersionInfo.ProductVersion
    Emit $true $item.FullName $version
    exit 0
  }
}
$uninstallRoots = @(
  'Registry::HKEY_CURRENT_USER\Software\Microsoft\Windows\CurrentVersion\Uninstall',
  'Registry::HKEY_LOCAL_MACHINE\Software\Microsoft\Windows\CurrentVersion\Uninstall'
)
foreach ($root in $uninstallRoots) {
  if (-not (Test-Path $root)) { continue }
  foreach ($key in Get-ChildItem $root -ErrorAction SilentlyContinue) {
    $props = Get-ItemProperty $key.PSPath -ErrorAction SilentlyContinue
    if ($props.DisplayName -eq 'OpenCode') {
      $installLocation = [string]$props.InstallLocation
      $exe = if ($installLocation) { Join-Path $installLocation 'OpenCode.exe' } else { '' }
      if ($exe -and (Test-Path -LiteralPath $exe)) {
        Emit $true $exe ([string]$props.DisplayVersion)
        exit 0
      }
    }
  }
}
$schemePaths = @(
  'Registry::HKEY_CURRENT_USER\Software\Classes\opencode\shell\open\command',
  'Registry::HKEY_LOCAL_MACHINE\Software\Classes\opencode\shell\open\command'
)
foreach ($scheme in $schemePaths) {
  if (Test-Path $scheme) {
    Emit $true '' ''
    exit 0
  }
}
Emit $false '' ''
exit 0
`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
	process.HideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Detected{}, fmt.Errorf("detect opencode desktop with PowerShell failed: %w; output: %s", err, string(out))
	}
	return parseDetectOutput(out)
}
