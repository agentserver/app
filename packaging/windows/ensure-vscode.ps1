param(
    [string]$BootstrapperURL = 'https://get.microsoft.com/installer/download/XP9KHM4BK9FZ7Q?cid=website_cta_psi',
    [string]$LocalBootstrapperPath = (Join-Path $env:LOCALAPPDATA 'agentserver-app\cache\vscode\vscode-store-bootstrapper.exe'),
    [int]$InstallTimeoutSeconds = 600
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

function Write-Step($msg) {
    Write-Host "==> $msg" -ForegroundColor Cyan
}

function Wait-ProcessWithProgress([System.Diagnostics.Process]$Process, [string]$Activity, [string]$Status) {
    $percent = 0
    try {
        while (-not $Process.HasExited) {
            Write-Progress -Activity $Activity -Status $Status -PercentComplete $percent
            Start-Sleep -Milliseconds 500
            $percent += 3
            if ($percent -gt 95) { $percent = 5 }
            $Process.Refresh()
        }
    } finally {
        Write-Progress -Activity $Activity -Completed
    }
}

function Get-VSCodeCommandPath {
    $candidates = @()

    $cmd = Get-Command code.cmd -ErrorAction SilentlyContinue
    if ($cmd) { $candidates += $cmd.Source }

    $cmdExe = Get-Command code.exe -ErrorAction SilentlyContinue
    if ($cmdExe) { $candidates += $cmdExe.Source }

    if ($env:LOCALAPPDATA) {
        $candidates += (Join-Path $env:LOCALAPPDATA 'Microsoft\WindowsApps\code.exe')
        $candidates += (Join-Path $env:LOCALAPPDATA 'Microsoft\WindowsApps\code.cmd')
        $candidates += (Join-Path $env:LOCALAPPDATA 'Programs\Microsoft VS Code\bin\code.cmd')
    }
    if ($env:ProgramFiles) {
        $candidates += (Join-Path $env:ProgramFiles 'Microsoft VS Code\bin\code.cmd')
    }
    if (${env:ProgramFiles(x86)}) {
        $candidates += (Join-Path ${env:ProgramFiles(x86)} 'Microsoft VS Code\bin\code.cmd')
    }

    foreach ($p in ($candidates | Where-Object { $_ } | Select-Object -Unique)) {
        if (Test-Path $p) { return $p }
    }
    return $null
}

function Get-VSCodeVersion([string]$CodePath) {
    if (-not $CodePath) { return $null }
    try {
        $out = & $CodePath --version 2>$null
        foreach ($line in $out) {
            $v = "$line".Trim()
            if ($v) { return $v }
        }
    } catch {
        return $null
    }
    return $null
}

function Get-VSCodeDetection {
    $path = Get-VSCodeCommandPath
    $version = Get-VSCodeVersion $path
    return [PSCustomObject]@{ Path = $path; Version = $version }
}

function DownloadBootstrapper {
    $dir = Split-Path -Parent $LocalBootstrapperPath
    if (-not (Test-Path $dir)) {
        New-Item -ItemType Directory -Force -Path $dir | Out-Null
    }
    Write-Step "Downloading VS Code Microsoft Store bootstrapper..."
    $curl = Get-Command curl.exe -ErrorAction SilentlyContinue
    if ($curl) {
        & curl.exe -fL --retry 2 --retry-delay 2 --connect-timeout 20 -o $LocalBootstrapperPath $BootstrapperURL
        if ($LASTEXITCODE -eq 0 -and (Test-Path $LocalBootstrapperPath)) { return }
    }
    Invoke-WebRequest -Uri $BootstrapperURL -OutFile $LocalBootstrapperPath -UseBasicParsing
}

function Wait-ForVSCode([int]$Seconds) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    do {
        $det = Get-VSCodeDetection
        if ($det.Path -and $det.Version) {
            return $det
        }
        Start-Sleep -Seconds 2
    } while ((Get-Date) -lt $deadline)
    return Get-VSCodeDetection
}

Set-ScriptOutputEncoding
Write-Step "Checking for VS Code..."
$existing = Get-VSCodeDetection
if ($existing.Path -and $existing.Version) {
    Write-Step "Detected existing VS Code $($existing.Version) at $($existing.Path); skipping install."
    exit 0
}

DownloadBootstrapper
Write-Step "Running VS Code Microsoft Store bootstrapper..."
$proc = Start-Process -FilePath $LocalBootstrapperPath -PassThru
Wait-ProcessWithProgress $proc "Installing VS Code" "正在通过微软商店引导器安装 VS Code，请稍候..."
if ($proc.ExitCode -ne 0) {
    throw "VS Code 微软商店引导器安装失败，退出码 $($proc.ExitCode)"
}
$det = Wait-ForVSCode $InstallTimeoutSeconds
if (-not ($det.Path -and $det.Version)) {
    throw "VS Code 微软商店引导器已退出，但未检测到 code 命令。已检查 WindowsApps 与常规安装目录。"
}
Write-Step "VS Code $($det.Version) installed at $($det.Path)"
