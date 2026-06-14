//go:build windows

package opencodedesktop

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"github.com/agentserver/agentserver-pkg/internal/process"
)

func detectPlatform() (Detected, error) {
	script := `
$ErrorActionPreference = 'Stop'
function Emit($installed, $path, $version) {
  [pscustomobject]@{installed=$installed; path=$path; version=$version} | ConvertTo-Json -Compress
}
function Get-OpenCodeCommandExecutable([string]$command) {
  if ([string]::IsNullOrWhiteSpace($command)) { return $null }
  $trimmed = $command.Trim()
  if ($trimmed -match '^\s*"([^"]*OpenCode\.exe)"') {
    return $matches[1]
  }
  if ($trimmed -match '^\s*([^"]*OpenCode\.exe)\b') {
    return $matches[1].Trim()
  }
  return $null
}
function Get-OpenCodeProtocolExePath([string]$scheme) {
  if (-not (Test-Path $scheme)) { return $null }
  $props = Get-ItemProperty -LiteralPath $scheme -ErrorAction SilentlyContinue
  if (-not $props) { return $null }
  $protocolExe = Get-OpenCodeCommandExecutable ([string]$props.'(default)')
  if ($protocolExe -and (Test-Path -LiteralPath $protocolExe)) {
    return (Get-Item -LiteralPath $protocolExe).FullName
  }
  return $null
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
    if ($props.DisplayName -like 'OpenCode*') {
      $installLocation = [string]$props.InstallLocation
      $exe = if ($installLocation) { Join-Path $installLocation 'OpenCode.exe' } else { '' }
      if ($exe -and (Test-Path -LiteralPath $exe)) {
        Emit $true $exe ([string]$props.DisplayVersion)
        exit 0
      }
      $uninstall = [string]$props.UninstallString
      if ($uninstall -match '"([^"]*Uninstall OpenCode\.exe)"') {
        $exe = Join-Path (Split-Path -Parent $matches[1]) 'OpenCode.exe'
        if (Test-Path -LiteralPath $exe) {
          Emit $true $exe ([string]$props.DisplayVersion)
          exit 0
        }
      }
    }
  }
}
$schemePaths = @(
  'Registry::HKEY_CURRENT_USER\Software\Classes\opencode\shell\open\command',
  'Registry::HKEY_LOCAL_MACHINE\Software\Classes\opencode\shell\open\command'
)
foreach ($scheme in $schemePaths) {
  $protocolExe = Get-OpenCodeProtocolExePath $scheme
  if ($protocolExe) {
    Emit $true $protocolExe ''
    exit 0
  }
}
Emit $false '' ''
exit 0
`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
	process.HideWindow(cmd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(string(out))
		}
		return Detected{}, fmt.Errorf("detect opencode desktop with PowerShell failed: %w; output: %s", err, msg)
	}
	return parseDetectOutput(out)
}
