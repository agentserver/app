# agentserver-vscode v1 — portable installer (PowerShell alternative to Inno Setup)
#
# Usage:
#   1. Unzip agentserver-vscode-<ver>-portable.zip somewhere
#   2. Right-click install.ps1 → "Run with PowerShell"
#      (or: powershell -NoProfile -ExecutionPolicy Bypass -File .\install.ps1)
#
# Installs to %LOCALAPPDATA%\Programs\agentserver-vscode, creates desktop
# shortcut + folder context menu, then exits. Launch the shortcut to onboard.

param(
    [switch]$Silent,
    [switch]$Uninstall
)

$ErrorActionPreference = 'Stop'

$AppName    = 'agentserver-vscode'
$Version    = '0.1.0'
$InstallDir = Join-Path $env:LOCALAPPDATA "Programs\$AppName"
$RegKey     = "HKCU:\Software\Classes\Directory\shell\AgentserverVscode"
$RegKeyBg   = "HKCU:\Software\Classes\Directory\Background\shell\AgentserverVscode"
$DesktopLnk = Join-Path $env:USERPROFILE "Desktop\$AppName.lnk"
$UninstallKey = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\$AppName"

function Write-Step($msg) {
    Write-Host "==> $msg" -ForegroundColor Cyan
}

function Do-Uninstall {
    Write-Step "Uninstalling $AppName..."

    # Best-effort: run our own agentctl uninstall (state + secrets + registry).
    $agentctl = Join-Path $InstallDir 'agentctl.exe'
    if (Test-Path $agentctl) {
        & $agentctl uninstall --silent 2>$null
    }

    # Remove shortcut + context menu (covered by agentctl too, but be defensive)
    if (Test-Path $DesktopLnk) { Remove-Item $DesktopLnk -Force -ErrorAction SilentlyContinue }
    foreach ($k in @($RegKey, $RegKeyBg)) {
        if (Test-Path $k) { Remove-Item $k -Recurse -Force -ErrorAction SilentlyContinue }
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

Write-Step "Installing $AppName $Version to $InstallDir"

# Source files sit next to this script.
$srcDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$required = @(
    'launcher.exe',
    'onboarding-server.exe',
    'agentctl.exe',
    'open-folder.exe',
    'agentserver-vscode.vsix',
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

# Desktop shortcut
Write-Step "Creating desktop shortcut..."
$wsh = New-Object -ComObject WScript.Shell
$shortcut = $wsh.CreateShortcut($DesktopLnk)
$shortcut.TargetPath       = Join-Path $InstallDir 'launcher.exe'
$shortcut.IconLocation     = (Join-Path $InstallDir 'icon.ico') + ',0'
$shortcut.WorkingDirectory = $env:USERPROFILE
$shortcut.Description      = 'agentserver-vscode (VS Code + codex 一键启动)'
$shortcut.Save()

# Folder context menu (right-click on a folder)
Write-Step "Registering folder context menu..."
$handlerExe = Join-Path $InstallDir 'open-folder.exe'
foreach ($base in @($RegKey, $RegKeyBg)) {
    New-Item -Path $base -Force | Out-Null
    Set-ItemProperty -Path $base -Name '(default)' -Value '用 agentserver-vscode 打开'
    Set-ItemProperty -Path $base -Name 'Icon'      -Value (Join-Path $InstallDir 'icon.ico')
    $cmdKey = "$base\command"
    New-Item -Path $cmdKey -Force | Out-Null
    Set-ItemProperty -Path $cmdKey -Name '(default)' -Value "`"$handlerExe`" `"%V`""
}

# Uninstall registry entry (so it shows up in Apps & Features)
Write-Step "Registering uninstaller..."
$uninstallCmd = "powershell -NoProfile -ExecutionPolicy Bypass -File `"$(Join-Path $InstallDir 'install.ps1')`" -Uninstall -Silent"
New-Item -Path $UninstallKey -Force | Out-Null
Set-ItemProperty -Path $UninstallKey -Name 'DisplayName'     -Value $AppName
Set-ItemProperty -Path $UninstallKey -Name 'DisplayVersion'  -Value $Version
Set-ItemProperty -Path $UninstallKey -Name 'Publisher'       -Value 'agentserver'
Set-ItemProperty -Path $UninstallKey -Name 'InstallLocation' -Value $InstallDir
Set-ItemProperty -Path $UninstallKey -Name 'UninstallString' -Value $uninstallCmd
Set-ItemProperty -Path $UninstallKey -Name 'DisplayIcon'     -Value (Join-Path $InstallDir 'icon.ico')
Set-ItemProperty -Path $UninstallKey -Name 'NoModify'        -Value 1 -Type DWord
Set-ItemProperty -Path $UninstallKey -Name 'NoRepair'        -Value 1 -Type DWord

# Copy ourselves into install dir so uninstall works
Copy-Item $MyInvocation.MyCommand.Path (Join-Path $InstallDir 'install.ps1') -Force

Write-Host ""
Write-Host "Install complete." -ForegroundColor Green
Write-Host "  Install dir: $InstallDir"
Write-Host "  Desktop shortcut: $DesktopLnk"
Write-Host "  Context menu: $RegKey"
Write-Host ""
Write-Host "Double-click the '$AppName' desktop shortcut to start onboarding."

if (-not $Silent) {
    Write-Host ""
    Read-Host "Press Enter to close"
}
