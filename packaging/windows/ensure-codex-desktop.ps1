param(
    [string]$LocalInstallerPath = (Join-Path $PSScriptRoot 'codex-desktop-installer.exe'),
    [int]$InstallTimeoutSeconds = 1800
)

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
            $_.PackageFamilyName -eq 'OpenAI.Codex_2p2nqsd0c76g0' -or
            $_.Name -like '*Codex*' -or
            $_.PackageFullName -like '*Codex*'
        } | Select-Object -First 1
        if ($pkg) { return $true }
    } catch {
    }
    return $false
}

function Wait-CodexDesktopInstalled([int]$TimeoutSeconds) {
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        if (Test-CodexDesktopInstalled) {
            return $true
        }
        Start-Sleep -Seconds 3
    }
    return $false
}

function Test-CodexDesktopInstallerFile([string]$Path) {
    if (-not (Test-Path -LiteralPath $Path)) {
        throw "Codex Desktop installer missing: $Path"
    }
    $item = Get-Item -LiteralPath $Path
    if ($item.Length -lt 65536) {
        throw "Codex Desktop installer is too small: $($item.Length) bytes"
    }

    $fs = [System.IO.File]::OpenRead($Path)
    try {
        $magic = New-Object byte[] 2
        $read = $fs.Read($magic, 0, 2)
        if ($read -ne 2 -or $magic[0] -ne 0x4d -or $magic[1] -ne 0x5a) {
            throw "Codex Desktop installer is not a valid MZ executable"
        }
    } finally {
        $fs.Dispose()
    }

    $sig = Get-AuthenticodeSignature -FilePath $Path
    if ($sig.Status -ne 'Valid') {
        throw "Codex Desktop installer Authenticode signature is $($sig.Status)"
    }
    if ($null -eq $sig.SignerCertificate) {
        throw "Codex Desktop installer has no signer certificate"
    }
    $subject = $sig.SignerCertificate.Subject
    if ($subject -notmatch 'O=Microsoft Corporation' -and $subject -notmatch 'Microsoft Corporation') {
        throw "Codex Desktop installer signer is not Microsoft Corporation: $subject"
    }
    $chain = New-Object System.Security.Cryptography.X509Certificates.X509Chain
    $chain.ChainPolicy.RevocationMode = [System.Security.Cryptography.X509Certificates.X509RevocationMode]::NoCheck
    if (-not $chain.Build($sig.SignerCertificate)) {
        $statuses = ($chain.ChainStatus | ForEach-Object { $_.Status }) -join ', '
        throw "Codex Desktop installer signer chain is invalid: $statuses"
    }
    $chainSubjects = @($chain.ChainElements | ForEach-Object { $_.Certificate.Subject })
    if (-not ($chainSubjects -match 'Microsoft')) {
        throw "Codex Desktop installer signer chain is not Microsoft"
    }
}

function Invoke-CodexDesktopLocalInstaller {
    if (-not (Test-Path $LocalInstallerPath)) {
        return $false
    }
    Write-Step "Running bundled Codex Desktop installer..."
    Write-Step $LocalInstallerPath
    try {
        Test-CodexDesktopInstallerFile $LocalInstallerPath
        $p = Start-Process -FilePath $LocalInstallerPath -Wait -PassThru
    } catch {
        Write-Warning "Bundled Codex Desktop installer failed verification or startup: $($_.Exception.Message); falling back to winget."
        return $false
    }
    if ($null -ne $p.ExitCode -and $p.ExitCode -ne 0) {
        Write-Warning "Bundled Codex Desktop installer failed with exit code $($p.ExitCode); falling back to winget."
        return $false
    }
    Write-Step "Waiting for Codex Desktop to become available..."
    if (-not (Wait-CodexDesktopInstalled -TimeoutSeconds $InstallTimeoutSeconds)) {
        Write-Warning "Bundled Codex Desktop installer exited, but Codex Desktop was not detected within $InstallTimeoutSeconds seconds; falling back to winget."
        return $false
    }
    return $true
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

if (-not (Invoke-CodexDesktopLocalInstaller)) {
    Invoke-CodexDesktopWingetInstall
}

Write-Step "Verifying Codex Desktop installation..."
if (-not (Test-CodexDesktopInstalled)) {
    throw "Codex Desktop 安装完成后仍未检测到。"
}
Write-Step "Codex Desktop is ready."
