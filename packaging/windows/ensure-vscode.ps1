param(
    [string]$ManifestPath = (Join-Path $PSScriptRoot 'vscode-manifest.json'),
    [string]$LocalInstallerPath = (Join-Path $PSScriptRoot 'vscode-installer.exe'),
    [int]$MaxDownloadAttempts = 80
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

function Read-VSCodeManifest([string]$Path) {
    if (-not (Test-Path $Path)) {
        throw "VS Code manifest not found: $Path"
    }
    $m = Get-Content -Raw -Path $Path | ConvertFrom-Json
    if (-not $m.version) { throw "VS Code manifest missing version" }
    if (-not $m.sha256) { throw "VS Code manifest missing sha256" }
    if (-not $m.expected_size) { throw "VS Code manifest missing expected_size" }
    if (@($m.urls).Count -eq 0) { throw "VS Code manifest missing urls" }
    if (@($m.silent_args).Count -eq 0) { throw "VS Code manifest missing silent_args" }
    return [PSCustomObject]@{
        Version      = [string]$m.version
        SHA256       = ([string]$m.sha256).ToLowerInvariant()
        ExpectedSize = [int64]$m.expected_size
        URLs         = @($m.urls | ForEach-Object { [string]$_ })
        SilentArgs   = @($m.silent_args | ForEach-Object { [string]$_ })
    }
}

function Get-FileLength([string]$Path) {
    if (-not (Test-Path $Path)) { return 0 }
    return (Get-Item $Path).Length
}

function Test-FileSHA256([string]$Path, [string]$ExpectedSHA256) {
    if (-not (Test-Path $Path)) { return $false }
    $actual = (Get-FileHash -Algorithm SHA256 -Path $Path).Hash.ToLowerInvariant()
    return $actual -eq $ExpectedSHA256.ToLowerInvariant()
}

function Test-CompleteVSCodeInstaller([string]$Path, [object]$Manifest) {
    if (-not (Test-Path $Path)) { return $false }
    $len = Get-FileLength $Path
    if ($len -ne $Manifest.ExpectedSize) { return $false }
    return Test-FileSHA256 $Path $Manifest.SHA256
}

function Invoke-ResumableDownload([string]$URL, [string]$Destination, [object]$Manifest) {
    $curl = Get-Command curl.exe -ErrorAction SilentlyContinue
    if (-not $curl) {
        throw "curl.exe not found; cannot perform resumable download"
    }

    for ($attempt = 1; $attempt -le $MaxDownloadAttempts; $attempt++) {
        $existing = Get-FileLength $Destination
        if ($existing -gt $Manifest.ExpectedSize) {
            Write-Host "  cached partial is larger than expected; restarting download" -ForegroundColor Yellow
            Remove-Item -Force $Destination -ErrorAction SilentlyContinue
            $existing = 0
        }

        if ($existing -gt 0) {
            Write-Host "  resuming download at byte $existing (attempt $attempt/$MaxDownloadAttempts)"
        } else {
            Write-Host "  starting download (attempt $attempt/$MaxDownloadAttempts)"
        }

        $args = @(
            '-fL',
            '--retry', '2',
            '--retry-delay', '2',
            '--connect-timeout', '20',
            '--speed-time', '60',
            '--speed-limit', '1024',
            '-C', '-',
            '-o', $Destination,
            $URL
        )
        & curl.exe @args
        $exit = $LASTEXITCODE
        $len = Get-FileLength $Destination

        if ($len -eq $Manifest.ExpectedSize) {
            if (Test-FileSHA256 $Destination $Manifest.SHA256) {
                return
            }
            Remove-Item -Force $Destination -ErrorAction SilentlyContinue
            throw "SHA256 mismatch for complete VS Code installer"
        }

        if ($exit -eq 0) {
            throw "download incomplete: got $len bytes, want $($Manifest.ExpectedSize)"
        }
        Write-Host "  download incomplete: got $len bytes, want $($Manifest.ExpectedSize); curl exit $exit" -ForegroundColor Yellow
    }

    $len = Get-FileLength $Destination
    throw "download incomplete after $MaxDownloadAttempts attempts: got $len bytes, want $($Manifest.ExpectedSize)"
}

function Get-VSCodeInstaller([object]$Manifest) {
    if (Test-CompleteVSCodeInstaller $LocalInstallerPath $Manifest) {
        Write-Step "Using bundled VS Code installer $LocalInstallerPath"
        return $LocalInstallerPath
    }
    if (Test-Path $LocalInstallerPath) {
        $len = Get-FileLength $LocalInstallerPath
        Write-Host "  bundled VS Code installer is invalid: got $len bytes, want $($Manifest.ExpectedSize)" -ForegroundColor Yellow
    }

    $cacheDir = Join-Path $env:LOCALAPPDATA 'agentserver-app\cache'
    if (-not (Test-Path $cacheDir)) {
        New-Item -ItemType Directory -Force -Path $cacheDir | Out-Null
    }

    $installer = Join-Path $cacheDir ("vscode-$($Manifest.Version)-win32-x64-user.exe")
    if (Test-CompleteVSCodeInstaller $installer $Manifest) {
        Write-Step "Using cached VS Code installer $installer"
        return $installer
    }
    if (Test-Path $installer) {
        Remove-Item -Force $installer
    }

    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    $tmp = "$installer.part"
    $lastError = $null
    foreach ($url in $Manifest.URLs) {
        try {
            Write-Step "Downloading VS Code $($Manifest.Version) from $url"
            Invoke-ResumableDownload $url $tmp $Manifest
            Move-Item -Force $tmp $installer
            return $installer
        } catch {
            $lastError = $_
            Write-Host "  download failed: $($_.Exception.Message)" -ForegroundColor Yellow
        }
    }
    throw "Failed to download VS Code installer: $lastError"
}

function Wait-ForVSCodeVersion([string]$ExpectedVersion, [int]$Seconds) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    do {
        $det = Get-VSCodeDetection
        if ($det.Path -and $det.Version -eq $ExpectedVersion) {
            return $det
        }
        Start-Sleep -Seconds 1
    } while ((Get-Date) -lt $deadline)
    return Get-VSCodeDetection
}

function Install-VSCode([string]$Installer, [object]$Manifest) {
    Write-Step "Running VS Code installer..."
    $proc = Start-Process -FilePath $Installer -ArgumentList $Manifest.SilentArgs -PassThru
    Wait-ProcessWithProgress $proc "Installing VS Code" "正在安装 VS Code $($Manifest.Version)，请稍候..."
    $det = Wait-ForVSCodeVersion $Manifest.Version 30
    if ($det.Path -and $det.Version -eq $Manifest.Version) {
        Write-Step "VS Code $($det.Version) installed at $($det.Path)"
        return
    }
    if ($proc.ExitCode -ne 0) {
        throw "VS Code installer exit code $($proc.ExitCode); detected path=$($det.Path) version=$($det.Version)"
    }
    throw "VS Code installer finished but VS Code $($Manifest.Version) was not detected; detected path=$($det.Path) version=$($det.Version)"
}

function Ensure-VSCodeInstalled([string]$Path) {
    Write-Step "Checking for VS Code..."
    $existing = Get-VSCodeDetection
    if ($existing.Path -and $existing.Version) {
        Write-Step "Detected existing VS Code $($existing.Version) at $($existing.Path); skipping install."
        return
    }
    if ($existing.Path) {
        Write-Host "  found VS Code command but could not read version; reinstalling from locked installer" -ForegroundColor Yellow
    }

    $manifest = Read-VSCodeManifest $Path
    $installer = Get-VSCodeInstaller $manifest
    Install-VSCode $installer $manifest
}

Ensure-VSCodeInstalled $ManifestPath
