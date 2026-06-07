param()

$ErrorActionPreference = 'Stop'

function Set-ScriptOutputEncoding {
    try {
        $utf8 = New-Object System.Text.UTF8Encoding $false
        [Console]::OutputEncoding = $utf8
        $script:OutputEncoding = $utf8
        & chcp.com 65001 > $null 2>$null
    } catch {
    }
}

Set-ScriptOutputEncoding

function Write-Step([string]$Message) {
    Write-Host "==> $Message" -ForegroundColor Cyan
}

function Test-CodexDesktopInstalled {
    $schemePaths = @(
        'Registry::HKEY_CURRENT_USER\Software\Classes\codex\shell\open\command',
        'Registry::HKEY_LOCAL_MACHINE\Software\Classes\codex\shell\open\command'
    )
    foreach ($p in $schemePaths) {
        if (Test-Path $p) { return $true }
    }
    try {
        $pkg = Get-AppxPackage | Where-Object {
            $_.Name -like '*Codex*' -or $_.PackageFullName -like '*Codex*'
        } | Select-Object -First 1
        if ($pkg) { return $true }
    } catch {
    }
    return $false
}

function Get-WingetPath {
    $cmd = Get-Command winget.exe -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    return $null
}

function Invoke-CodexDesktopWingetInstall {
    $winget = Get-WingetPath
    if (-not $winget) {
        throw "未找到 winget；请安装或更新 Windows App Installer / Windows Package Manager 后重试。"
    }
    $args = @(
        'install',
        'Codex',
        '-s',
        'msstore',
        '--accept-source-agreements',
        '--accept-package-agreements',
        '--disable-interactivity'
    )
    Write-Step "Running winget install Codex -s msstore..."
    & $winget @args
    if ($LASTEXITCODE -ne 0) {
        throw "winget install Codex -s msstore failed with exit code $LASTEXITCODE"
    }
}

Write-Step "Checking for Codex Desktop..."
if (Test-CodexDesktopInstalled) {
    Write-Step "Detected existing Codex Desktop; skipping install."
    exit 0
}

Invoke-CodexDesktopWingetInstall

Write-Step "Verifying Codex Desktop installation..."
if (-not (Test-CodexDesktopInstalled)) {
    throw "Codex Desktop 安装完成后仍未检测到。"
}
Write-Step "Codex Desktop is ready."
