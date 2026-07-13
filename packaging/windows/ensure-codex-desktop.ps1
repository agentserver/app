param(
    [string]$LocalInstallerPath = (Join-Path $PSScriptRoot 'chatgpt-desktop-installer.exe'),
    [string]$LocalInstallerManifestPath = (Join-Path $PSScriptRoot 'chatgpt-desktop-installer.manifest.json'),
    [string]$DetectionScriptPath = (Join-Path $PSScriptRoot 'codex-desktop-detect.ps1'),
    [string]$SignatureVerifierPath = (Join-Path $PSScriptRoot 'verify-chatgpt-desktop-installer.ps1'),
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

if (-not (Test-Path -LiteralPath $DetectionScriptPath -PathType Leaf)) {
    throw "ChatGPT / Codex detection script missing: $DetectionScriptPath"
}
. $DetectionScriptPath

function Write-Step([string]$Message) {
    Write-Host "==> $Message" -ForegroundColor Cyan
}

function Get-CodexDesktopStatus {
    return Get-ChatGPTCodexDetection
}

function Test-CodexDesktopInstalled {
    return (Get-CodexDesktopStatus).status -ceq 'ready'
}

function Get-CodexDesktopRepairMessage([string]$Status) {
    if ($Status -ceq 'scheme_missing') {
        return 'ChatGPT 桌面应用（含 Codex）已安装，但 codex:// 协议缺失。请在 Windows 已安装的应用 > ChatGPT > 高级选项中依次尝试 Repair、Reset；仍失败请从 Microsoft Store Reinstall。'
    }
    if ($Status -ceq 'scheme_target_invalid') {
        return 'codex:// 协议已注册，但处理器无效或不可信。请在 Windows 已安装的应用 > ChatGPT > 高级选项中依次尝试 Repair、Reset；仍失败请从 Microsoft Store Reinstall。'
    }
    return "ChatGPT / Codex 状态异常：$Status"
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

function Test-CodexDesktopInstallerFile([string]$Path, [string]$ManifestPath) {
    if (-not (Test-Path -LiteralPath $Path)) {
        throw "ChatGPT / Codex installer missing: $Path"
    }
    if (-not (Test-Path -LiteralPath $ManifestPath -PathType Leaf)) {
        throw "ChatGPT / Codex installer manifest missing: $ManifestPath"
    }
    try {
        $manifest = Get-Content -LiteralPath $ManifestPath -Raw | ConvertFrom-Json
    } catch {
        throw "ChatGPT / Codex installer manifest is invalid JSON: $($_.Exception.Message)"
    }
    $expectedProductID = '9PLM9XGG6VKS'
    $expectedSourceURL = 'https://get.microsoft.com/installer/download/9PLM9XGG6VKS?cid=website_cta_psi'
    if ([string]$manifest.product_id -cne $expectedProductID) {
        throw "ChatGPT / Codex installer manifest product_id mismatch"
    }
    if ([string]$manifest.source_url -cne $expectedSourceURL) {
        throw "ChatGPT / Codex installer manifest source_url mismatch"
    }
    $item = Get-Item -LiteralPath $Path
    if ($item.Length -lt 65536) {
        throw "ChatGPT / Codex installer is too small: $($item.Length) bytes"
    }
    if ([int64]$manifest.size -ne [int64]$item.Length) {
        throw "ChatGPT / Codex installer manifest size mismatch"
    }
    $actualHash = (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash
    if ([string]::IsNullOrWhiteSpace([string]$manifest.sha256) -or
        $actualHash -ine [string]$manifest.sha256) {
        throw "ChatGPT / Codex installer manifest SHA256 mismatch"
    }

    $fs = [System.IO.File]::OpenRead($Path)
    try {
        $magic = New-Object byte[] 2
        $read = $fs.Read($magic, 0, 2)
        if ($read -ne 2 -or $magic[0] -ne 0x4d -or $magic[1] -ne 0x5a) {
            throw "ChatGPT / Codex installer is not a valid MZ executable"
        }
    } finally {
        $fs.Dispose()
    }

    if (-not (Test-Path -LiteralPath $SignatureVerifierPath -PathType Leaf)) {
        throw 'ChatGPT / Codex installer signature verifier is missing'
    }
    & $SignatureVerifierPath -Path $Path
}

function Invoke-CodexDesktopLocalInstaller {
    if (-not (Test-Path $LocalInstallerPath)) {
        return $false
    }
    Write-Step "Running bundled ChatGPT / Codex installer..."
    Write-Step $LocalInstallerPath
    try {
        Test-CodexDesktopInstallerFile $LocalInstallerPath $LocalInstallerManifestPath
        $p = Start-Process -FilePath $LocalInstallerPath -Wait -PassThru
    } catch {
        Write-Warning "Bundled ChatGPT / Codex installer failed verification or startup: $($_.Exception.Message); falling back to winget."
        return $false
    }
    if ($null -ne $p.ExitCode -and $p.ExitCode -ne 0) {
        Write-Warning "Bundled ChatGPT / Codex installer failed with exit code $($p.ExitCode); falling back to winget."
        return $false
    }
    Write-Step "Waiting for ChatGPT / Codex to become available..."
    if (-not (Wait-CodexDesktopInstalled -TimeoutSeconds $InstallTimeoutSeconds)) {
        Write-Warning "Bundled ChatGPT / Codex installer exited, but the app was not ready within $InstallTimeoutSeconds seconds; falling back to winget."
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
        '--id=9PLM9XGG6VKS',
        '--source=msstore',
        '--exact',
        '--accept-package-agreements',
        '--accept-source-agreements',
        '--disable-interactivity'
    )
    Write-Step "Running winget install --id=9PLM9XGG6VKS --source=msstore..."
    & $winget @args
    if ($LASTEXITCODE -ne 0) {
        throw "winget install --id=9PLM9XGG6VKS --source=msstore failed with exit code $LASTEXITCODE"
    }
}

Write-Step "Checking for ChatGPT / Codex..."
$initialStatus = Get-CodexDesktopStatus
if ($initialStatus.status -ceq 'ready') {
    Write-Step "Detected existing ChatGPT / Codex; skipping install."
    exit 0
}
if ($initialStatus.status -ceq 'scheme_missing' -or $initialStatus.status -ceq 'scheme_target_invalid') {
    throw (Get-CodexDesktopRepairMessage -Status ([string]$initialStatus.status))
}
if ($initialStatus.status -cne 'not_installed') {
    throw "Unexpected ChatGPT / Codex detection status: $($initialStatus.status)"
}

if (-not (Invoke-CodexDesktopLocalInstaller)) {
    Invoke-CodexDesktopWingetInstall
}

Write-Step "Verifying ChatGPT / Codex installation..."
$finalStatus = Get-CodexDesktopStatus
if ($finalStatus.status -cne 'ready') {
    if ($finalStatus.status -ceq 'scheme_missing' -or $finalStatus.status -ceq 'scheme_target_invalid') {
        throw (Get-CodexDesktopRepairMessage -Status ([string]$finalStatus.status))
    }
    throw "ChatGPT 桌面应用（含 Codex）安装完成后仍未检测到。状态：$($finalStatus.status)"
}
Write-Step "ChatGPT / Codex is ready."
