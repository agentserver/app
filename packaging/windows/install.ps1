# 星池指挥官 v1 — portable installer (PowerShell alternative to Inno Setup)
#
# Usage:
#   1. Unzip agentserver-vscode-<ver>-portable.zip somewhere
#   2. Right-click install.ps1 → "Run with PowerShell"
#      (or: powershell -NoProfile -ExecutionPolicy Bypass -File .\install.ps1)
#
# Installs to %LOCALAPPDATA%\Programs\agentserver-vscode, creates desktop
# shortcut + folder context menu, and installs the selected frontend.
# Default frontend is Codex Desktop; use -MinimalVSCode for simplified VS Code.
# Launch the shortcut to onboard.

param(
    [switch]$Silent,
    [switch]$Uninstall,
    [switch]$MinimalVSCode
)

$ErrorActionPreference = 'Stop'

function Set-ScriptOutputEncoding {
    try {
        $utf8 = New-Object System.Text.UTF8Encoding $false
        [Console]::OutputEncoding = $utf8
        $script:OutputEncoding = $utf8
        & chcp.com 65001 > $null 2>$null
    } catch {
        # Best-effort only; installation must still work if the host forbids it.
    }
}

Set-ScriptOutputEncoding

$AppName    = 'agentserver-vscode'
$AppDisplayName = '星池指挥官'
$ContextMenuLabel = '用星池指挥官打开'
$Version    = '0.1.0'
$InstallDir = Join-Path $env:LOCALAPPDATA "Programs\$AppName"
$RegSubKeyFile = "Software\Classes\*\shell\AgentserverVscode"
$RegSubKeyDir  = "Software\Classes\Directory\shell\AgentserverVscode"
$RegSubKeyBg   = "Software\Classes\Directory\Background\shell\AgentserverVscode"
$RegKey     = "HKCU:\Software\Classes\Directory\shell\AgentserverVscode"
$RegKeyBg   = "HKCU:\Software\Classes\Directory\Background\shell\AgentserverVscode"
$DesktopLnk = Join-Path $env:USERPROFILE "Desktop\$AppDisplayName.lnk"
$LegacyDesktopLnk = Join-Path $env:USERPROFILE "Desktop\$AppName.lnk"
$UninstallKey = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\$AppName"

function Write-Step($msg) {
    Write-Host "==> $msg" -ForegroundColor Cyan
}

function Refresh-ShellIconCache {
    try {
        $signature = @'
using System;
using System.Runtime.InteropServices;

public static class ShellIconCacheNotify {
    [DllImport("shell32.dll")]
    public static extern void SHChangeNotify(int wEventId, uint uFlags, IntPtr dwItem1, IntPtr dwItem2);
}
'@
        if (-not ("ShellIconCacheNotify" -as [type])) {
            Add-Type -TypeDefinition $signature
        }
        # SHCNE_ASSOCCHANGED tells Explorer to drop stale icon associations.
        [ShellIconCacheNotify]::SHChangeNotify(0x08000000, 0, [IntPtr]::Zero, [IntPtr]::Zero)
        $ie4uinit = Join-Path $env:WINDIR 'System32\ie4uinit.exe'
        if (Test-Path $ie4uinit) {
            & $ie4uinit -show 2>$null
        }
    } catch {
        # Best-effort only; the shortcut still points at a cache-busted icon path.
    }
}

function Set-RegistryStringValue([string]$SubKey, [string]$Name, [string]$Value) {
    $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey($SubKey)
    if (-not $key) {
        throw "Failed to create HKCU:\$SubKey"
    }
    try {
        if ($Name -eq '') {
            $key.SetValue($null, $Value, [Microsoft.Win32.RegistryValueKind]::String)
        } else {
            $key.SetValue($Name, $Value, [Microsoft.Win32.RegistryValueKind]::String)
        }
    } finally {
        $key.Close()
    }
}

function Remove-RegistrySubKeyTree([string]$SubKey) {
    try {
        [Microsoft.Win32.Registry]::CurrentUser.DeleteSubKeyTree($SubKey, $false)
    } catch {
        # Missing keys are fine during upgrade/uninstall.
    }
}

function Do-Uninstall {
    Write-Step "Uninstalling $AppDisplayName..."

    # Prefer the dedicated uninstaller; it also schedules removal of this
    # installation directory after the process exits.
    $uninstaller = Join-Path $InstallDir 'uninstall.exe'
    if (Test-Path $uninstaller) {
        & $uninstaller --silent
        if ($LASTEXITCODE -ne 0) {
            throw "uninstall.exe failed with exit code $LASTEXITCODE"
        }
        Write-Host "Uninstall complete." -ForegroundColor Green
        return
    }

    # Fallback for older installs: run agentctl uninstall (state + secrets + registry).
    $agentctl = Join-Path $InstallDir 'agentctl.exe'
    if (Test-Path $agentctl) {
        & $agentctl uninstall --silent 2>$null
    }

    # Remove shortcut + context menu (covered by agentctl too, but be defensive)
    if (Test-Path $DesktopLnk) { Remove-Item $DesktopLnk -Force -ErrorAction SilentlyContinue }
    if (Test-Path $LegacyDesktopLnk) { Remove-Item $LegacyDesktopLnk -Force -ErrorAction SilentlyContinue }
    foreach ($k in @($RegSubKeyFile, $RegSubKeyDir, $RegSubKeyBg)) {
        Remove-RegistrySubKeyTree $k
    }

    # Remove install dir
    if (Test-Path $InstallDir) {
        Remove-Item $InstallDir -Recurse -Force -ErrorAction SilentlyContinue
    }

    # Remove uninstall registry entry
    if (Test-Path $UninstallKey) { Remove-Item $UninstallKey -Recurse -Force }

    Write-Host "Uninstall complete." -ForegroundColor Green
}

if ($Uninstall) {
    Do-Uninstall
    exit 0
}

# --- Install -----------------------------------------------------------------

Write-Step "Installing $AppDisplayName $Version to $InstallDir"

# Source files sit next to this script.
$srcDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$required = @(
    'launcher.exe',
    'onboarding-server.exe',
    'agentctl.exe',
    'open-folder.exe',
    'uninstall.exe',
    'token-refresher.exe',
    'driver-agent.exe',
    'slave-agent.exe',
    'codex-desktop-installer.exe',
    'agentserver-vscode.vsix',
    'ensure-vscode.ps1',
    'ensure-codex-desktop.ps1',
    'write-install-mode.ps1',
    'machine.ps1',
    'vscode-manifest.json',
    'icon.ico'
)
foreach ($f in $required) {
    if (-not (Test-Path (Join-Path $srcDir $f))) {
        throw "Missing payload file: $f (expected in $srcDir)"
    }
}

# Mkdir + copy
if (-not (Test-Path $InstallDir)) {
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
}
foreach ($f in $required) {
    Copy-Item (Join-Path $srcDir $f) (Join-Path $InstallDir $f) -Force
}
Write-Step "Copied $($required.Count) files."

$IconPath = Join-Path $InstallDir 'icon.ico'
$ShellIconPath = $IconPath
try {
    $iconHash = (Get-FileHash -Algorithm SHA256 $IconPath).Hash.Substring(0, 12).ToLowerInvariant()
    $ShellIconPath = Join-Path $InstallDir "icon-$iconHash.ico"
    Copy-Item $IconPath $ShellIconPath -Force
    Get-ChildItem -Path $InstallDir -Filter 'icon-*.ico' -File -ErrorAction SilentlyContinue |
        Where-Object { $_.FullName -ne $ShellIconPath } |
        Remove-Item -Force -ErrorAction SilentlyContinue
} catch {
    Write-Host "Note: failed to create cache-busting icon path; using icon.ico."
    $ShellIconPath = $IconPath
}

$MachinePath = Join-Path $env:USERPROFILE '.agentserver-vscode\machine.json'
$InitialComputerName = $env:COMPUTERNAME
$ShouldPromptComputerName = $false
if (-not (Test-Path -LiteralPath $MachinePath)) {
    $ShouldPromptComputerName = $true
} else {
    try {
        $existing = Get-Content -Raw -LiteralPath $MachinePath | ConvertFrom-Json
        $locked = $false
        if ($null -ne $existing.PSObject.Properties['computer_name_locked']) {
            $locked = [bool]$existing.computer_name_locked
        }
        if ((-not $locked) -and ($existing.computer_name -eq $env:COMPUTERNAME)) {
            $ShouldPromptComputerName = $true
        }
    } catch {
        Write-Host "Note: failed to inspect existing machine.json; leaving it unchanged unless machine.ps1 reports an error."
    }
}
if ((-not $Silent) -and $ShouldPromptComputerName) {
    $machinePrompt = "Computer name [$InitialComputerName]"
    $machineInput = Read-Host $machinePrompt
    if (-not [string]::IsNullOrWhiteSpace($machineInput)) {
        $InitialComputerName = $machineInput.Trim()
    }
}

Write-Step "Initializing computer name..."
& (Join-Path $InstallDir 'machine.ps1') -MachinePath $MachinePath -ComputerName $InitialComputerName

if ($MinimalVSCode) {
    # Bundled codex.exe — copy into the expected per-user bin dir so
    # ConfigureVSCode finds it and skips the 246MB GitHub download.
    $codexSrc = Join-Path $srcDir 'codex.exe'
    $codexBinDir = Join-Path $env:LOCALAPPDATA "agentserver-vscode\bin"
    $codexDst = Join-Path $codexBinDir 'codex.exe'
    if (Test-Path $codexSrc) {
        if (-not (Test-Path $codexBinDir)) {
            New-Item -ItemType Directory -Force -Path $codexBinDir | Out-Null
        }
        Write-Step "Staging bundled codex.exe to $codexDst ..."
        Copy-Item $codexSrc $codexDst -Force
        $sz = (Get-Item $codexDst).Length
        Write-Step ("codex.exe copied ({0:N0} bytes, {1:N1} MB)" -f $sz, ($sz / 1MB))
    } else {
        Write-Host "Note: codex.exe NOT bundled in this zip; first launch will fetch from GitHub."
    }

    Write-Step "Writing install mode minimal_vscode..."
    & (Join-Path $InstallDir 'write-install-mode.ps1') -Mode 'minimal_vscode' -Path (Join-Path $InstallDir 'install-mode.json')
    Write-Step "Ensuring VS Code is installed..."
    & (Join-Path $InstallDir 'ensure-vscode.ps1') -ManifestPath (Join-Path $InstallDir 'vscode-manifest.json') -LocalInstallerPath (Join-Path $srcDir 'vscode-installer.exe')
} else {
    Write-Step "Writing install mode codex_desktop..."
    & (Join-Path $InstallDir 'write-install-mode.ps1') -Mode 'codex_desktop' -Path (Join-Path $InstallDir 'install-mode.json')
    Write-Step "Ensuring Codex Desktop is installed..."
    & (Join-Path $InstallDir 'ensure-codex-desktop.ps1') -LocalInstallerPath (Join-Path $srcDir 'codex-desktop-installer.exe')
}

# Desktop shortcut
Write-Step "Creating desktop shortcut..."
$wsh = New-Object -ComObject WScript.Shell
$shortcut = $wsh.CreateShortcut($DesktopLnk)
$shortcut.TargetPath       = Join-Path $InstallDir 'launcher.exe'
$shortcut.IconLocation     = $ShellIconPath + ',0'
$shortcut.WorkingDirectory = $env:USERPROFILE
$shortcut.Description      = '星池指挥官一键启动'
$shortcut.Save()
if (Test-Path $LegacyDesktopLnk) { Remove-Item $LegacyDesktopLnk -Force -ErrorAction SilentlyContinue }

# File/folder context menu (right-click on a file, folder, or folder background)
Write-Step "Registering file and folder context menus..."
$handlerExe = Join-Path $InstallDir 'open-folder.exe'
foreach ($entry in @(
    @{ Key = $RegSubKeyFile; Arg = '%1' },
    @{ Key = $RegSubKeyDir;  Arg = '%V' },
    @{ Key = $RegSubKeyBg;   Arg = '%V' }
)) {
    Set-RegistryStringValue $entry.Key '' $ContextMenuLabel
    Set-RegistryStringValue $entry.Key 'Icon' $ShellIconPath
    $cmdKey = "$($entry.Key)\command"
    Set-RegistryStringValue $cmdKey '' "`"$handlerExe`" `"$($entry.Arg)`""
}

# Uninstall registry entry (so it shows up in Apps & Features)
Write-Step "Registering uninstaller..."
$uninstallCmd = "`"$(Join-Path $InstallDir 'uninstall.exe')`" --silent"
New-Item -Path $UninstallKey -Force | Out-Null
Set-ItemProperty -Path $UninstallKey -Name 'DisplayName'     -Value $AppDisplayName
Set-ItemProperty -Path $UninstallKey -Name 'DisplayVersion'  -Value $Version
Set-ItemProperty -Path $UninstallKey -Name 'Publisher'       -Value 'agentserver'
Set-ItemProperty -Path $UninstallKey -Name 'InstallLocation' -Value $InstallDir
Set-ItemProperty -Path $UninstallKey -Name 'UninstallString' -Value $uninstallCmd
Set-ItemProperty -Path $UninstallKey -Name 'DisplayIcon'     -Value $ShellIconPath
Set-ItemProperty -Path $UninstallKey -Name 'NoModify'        -Value 1 -Type DWord
Set-ItemProperty -Path $UninstallKey -Name 'NoRepair'        -Value 1 -Type DWord

# Copy ourselves into install dir so uninstall works
Copy-Item $MyInvocation.MyCommand.Path (Join-Path $InstallDir 'install.ps1') -Force
Refresh-ShellIconCache

Write-Host ""
Write-Host "Install complete." -ForegroundColor Green
Write-Host "  Install dir: $InstallDir"
Write-Host "  Desktop shortcut: $DesktopLnk"
Write-Host "  Context menus: files, folders, folder background"
Write-Host ""
Write-Host "Double-click the '$AppDisplayName' desktop shortcut to start onboarding."

if (-not $Silent) {
    Write-Host ""
    Read-Host "Press Enter to close"
}
