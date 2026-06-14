param(
    [string]$LocalInstallerPath = (Join-Path $PSScriptRoot 'opencode-desktop-installer.exe'),
    [int]$InstallTimeoutSeconds = 1200,
    [Int64]$MinInstallerSize = 65536
)

$ErrorActionPreference = 'Stop'
$OfficialDownloadURL = 'https://opencode.ai/download'

function Set-ScriptOutputEncoding {
    try {
        $utf8 = New-Object System.Text.UTF8Encoding $false
        [Console]::OutputEncoding = $utf8
        $script:OutputEncoding = $utf8
        & chcp.com 65001 > $null 2>$null
    } catch {
    }
}

function Write-Step([string]$Message) {
    Write-Host "==> $Message" -ForegroundColor Cyan
}

function Get-OpenCodeDesktopExePath {
    $candidates = @()
    if ($env:LOCALAPPDATA) {
        $candidates += (Join-Path $env:LOCALAPPDATA 'Programs\OpenCode\OpenCode.exe')
    }
    if ($env:USERPROFILE) {
        $candidates += (Join-Path $env:USERPROFILE 'AppData\Local\Programs\OpenCode\OpenCode.exe')
    }
    foreach ($p in ($candidates | Where-Object { $_ } | Select-Object -Unique)) {
        if (Test-Path -LiteralPath $p) {
            return $p
        }
    }

    $uninstallRoots = @(
        'Registry::HKEY_CURRENT_USER\Software\Microsoft\Windows\CurrentVersion\Uninstall',
        'Registry::HKEY_LOCAL_MACHINE\Software\Microsoft\Windows\CurrentVersion\Uninstall',
        'Registry::HKEY_LOCAL_MACHINE\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall'
    )
    foreach ($root in $uninstallRoots) {
        if (-not (Test-Path $root)) { continue }
        foreach ($child in Get-ChildItem $root -ErrorAction SilentlyContinue) {
            $props = Get-ItemProperty -LiteralPath $child.PSPath -ErrorAction SilentlyContinue
            if ($props.DisplayName -ne 'OpenCode') { continue }
            $installLocation = [string]$props.InstallLocation
            if (-not [string]::IsNullOrWhiteSpace($installLocation)) {
                $exe = Join-Path $installLocation 'OpenCode.exe'
                if (Test-Path -LiteralPath $exe) {
                    return $exe
                }
            }
            return 'registry'
        }
    }
    return $null
}

function Test-OpenCodeDesktopInstalled {
    if (Get-OpenCodeDesktopExePath) {
        return $true
    }
    $schemePaths = @(
        'Registry::HKEY_CURRENT_USER\Software\Classes\opencode\shell\open\command',
        'Registry::HKEY_LOCAL_MACHINE\Software\Classes\opencode\shell\open\command'
    )
    foreach ($p in $schemePaths) {
        if (Test-Path $p) { return $true }
    }
    return $false
}

function Wait-OpenCodeDesktopInstalled([int]$TimeoutSeconds) {
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        if (Test-OpenCodeDesktopInstalled) {
            return $true
        }
        Start-Sleep -Seconds 3
    }
    return $false
}

function Test-OpenCodeDesktopInstallerFile([string]$Path) {
    if (-not (Test-Path -LiteralPath $Path)) {
        throw "OpenCode Desktop installer missing: $Path"
    }
    $item = Get-Item -LiteralPath $Path
    if ($item.Length -lt $MinInstallerSize) {
        throw "OpenCode Desktop installer is too small: $($item.Length) bytes"
    }

    $fs = [System.IO.File]::OpenRead($Path)
    try {
        $magic = New-Object byte[] 2
        $read = $fs.Read($magic, 0, 2)
        if ($read -ne 2 -or $magic[0] -ne 0x4d -or $magic[1] -ne 0x5a) {
            throw "OpenCode Desktop installer is not a valid MZ executable"
        }
    } finally {
        $fs.Dispose()
    }

    $sig = Get-AuthenticodeSignature -FilePath $Path
    if ($sig.Status -ne 'Valid') {
        throw "OpenCode Desktop installer Authenticode signature is $($sig.Status)"
    }
    if ($null -eq $sig.SignerCertificate) {
        throw "OpenCode Desktop installer has no signer certificate"
    }
}

function Invoke-OpenCodeDesktopLocalInstaller {
    if (-not (Test-Path -LiteralPath $LocalInstallerPath)) {
        Write-Warning "Bundled OpenCode Desktop installer not found: $LocalInstallerPath"
        return $false
    }
    Write-Step "Running bundled OpenCode Desktop installer..."
    Write-Step $LocalInstallerPath
    try {
        Test-OpenCodeDesktopInstallerFile $LocalInstallerPath
        $proc = Start-Process -FilePath $LocalInstallerPath -Wait -PassThru
    } catch {
        Write-Warning "Bundled OpenCode Desktop installer failed verification or startup: $($_.Exception.Message)"
        return $false
    }
    if ($null -ne $proc.ExitCode -and $proc.ExitCode -ne 0) {
        Write-Warning "Bundled OpenCode Desktop installer failed with exit code $($proc.ExitCode)."
        return $false
    }
    Write-Step "Waiting for OpenCode Desktop to become available..."
    return (Wait-OpenCodeDesktopInstalled -TimeoutSeconds $InstallTimeoutSeconds)
}

function Invoke-OpenCodeDesktopManualFallback {
    Write-Warning "Unable to install OpenCode Desktop from the bundled installer."
    Write-Warning "Opening the official OpenCode download page: $OfficialDownloadURL"
    try {
        Start-Process $OfficialDownloadURL | Out-Null
    } catch {
        Write-Warning "Failed to open browser: $($_.Exception.Message)"
    }
    throw "OpenCode Desktop 安装失败。请从 $OfficialDownloadURL 下载并安装后重新运行安装程序。"
}

Set-ScriptOutputEncoding
Write-Step "Checking for OpenCode Desktop..."
if (Test-OpenCodeDesktopInstalled) {
    Write-Step "Detected existing OpenCode Desktop; skipping install."
    exit 0
}

if (-not (Invoke-OpenCodeDesktopLocalInstaller)) {
    Invoke-OpenCodeDesktopManualFallback
}

Write-Step "Verifying OpenCode Desktop installation..."
if (-not (Test-OpenCodeDesktopInstalled)) {
    throw "OpenCode Desktop 安装完成后仍未检测到。"
}
Write-Step "OpenCode Desktop is ready."
