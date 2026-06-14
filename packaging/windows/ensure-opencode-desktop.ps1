param(
    [string]$InstallerURL = 'https://opencode.ai/download/stable/windows-x64-nsis',
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

function Test-OpenCodeDesktopExecutable([string]$Path) {
    if ([string]::IsNullOrWhiteSpace($Path) -or -not (Test-Path -LiteralPath $Path)) {
        return $false
    }
    try {
        $proc = Start-Process -FilePath $Path -ArgumentList '--version' -WindowStyle Hidden -PassThru -ErrorAction Stop
        if (-not $proc.WaitForExit(10000)) {
            try {
                Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
            } catch {
            }
            return $false
        }
        return ($proc.ExitCode -eq 0)
    } catch {
        return $false
    }
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
        if (Test-OpenCodeDesktopExecutable $p) {
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
            if (-not ($props.DisplayName -like 'OpenCode*')) { continue }
            $installLocation = [string]$props.InstallLocation
            if (-not [string]::IsNullOrWhiteSpace($installLocation)) {
                $exe = Join-Path $installLocation 'OpenCode.exe'
                if (Test-OpenCodeDesktopExecutable $exe) {
                    return $exe
                }
            }
            $uninstallString = [string]$props.UninstallString
            if ($uninstallString -match '"([^"]*Uninstall OpenCode\.exe)"') {
                $exe = Join-Path (Split-Path -Parent $matches[1]) 'OpenCode.exe'
                if (Test-OpenCodeDesktopExecutable $exe) {
                    return $exe
                }
            }
        }
    }

    $schemePaths = @(
        'Registry::HKEY_CURRENT_USER\Software\Classes\opencode\shell\open\command',
        'Registry::HKEY_LOCAL_MACHINE\Software\Classes\opencode\shell\open\command'
    )
    foreach ($p in $schemePaths) {
        $protocolExe = Get-OpenCodeProtocolExePath $p
        if ($protocolExe) {
            return $protocolExe
        }
    }
    return $null
}

function Get-OpenCodeCommandExecutable([string]$Command) {
    if ([string]::IsNullOrWhiteSpace($Command)) {
        return $null
    }
    $trimmed = $Command.Trim()
    if ($trimmed -match '^\s*"([^"]*OpenCode\.exe)"') {
        return $matches[1]
    }
    if ($trimmed -match '^\s*([^"]*OpenCode\.exe)\b') {
        return $matches[1].Trim()
    }
    return $null
}

function Get-OpenCodeProtocolExePath([string]$Path) {
    if (-not (Test-Path $Path)) {
        return $null
    }
    $props = Get-ItemProperty -LiteralPath $Path -ErrorAction SilentlyContinue
    if ($null -eq $props) {
        return $null
    }
    $protocolExe = Get-OpenCodeCommandExecutable ([string]$props.'(default)')
    if ($protocolExe -and (Test-OpenCodeDesktopExecutable $protocolExe)) {
        return (Get-Item -LiteralPath $protocolExe).FullName
    }
    return $null
}

function Test-OpenCodeDesktopInstalled {
    if (Get-OpenCodeDesktopExePath) {
        return $true
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
        $subject = if ($sig.SignerCertificate) { $sig.SignerCertificate.Subject } else { '<none>' }
        throw "OpenCode Desktop installer Authenticode signature is $($sig.Status); signer subject: $subject"
    }
    if ($null -eq $sig.SignerCertificate) {
        throw "OpenCode Desktop installer has no signer certificate"
    }
}

function Invoke-OpenCodeDesktopInstallerDownload {
    $cacheRoot = Join-Path ([System.IO.Path]::GetTempPath()) 'agentserver-opencode-desktop'
    if (-not (Test-Path -LiteralPath $cacheRoot)) {
        New-Item -ItemType Directory -Force -Path $cacheRoot | Out-Null
    }
    $installerPath = Join-Path $cacheRoot 'opencode-desktop-installer.exe'
    $partialPath = $installerPath + '.part'
    Remove-Item -LiteralPath $installerPath,$partialPath -Force -ErrorAction SilentlyContinue

    Write-Step "Downloading latest OpenCode Desktop installer..."
    Write-Step $InstallerURL
    try {
        try {
            [System.Net.ServicePointManager]::SecurityProtocol = [System.Net.ServicePointManager]::SecurityProtocol -bor [System.Net.SecurityProtocolType]::Tls12
        } catch {
        }
        $curl = Get-Command 'curl.exe' -ErrorAction SilentlyContinue
        if ($curl) {
            & $curl.Source -fL --retry 2 --connect-timeout 15 --max-time 1200 --speed-time 30 --speed-limit 1024 -o $partialPath $InstallerURL
            if ($LASTEXITCODE -ne 0) {
                throw "curl.exe failed with exit code $LASTEXITCODE"
            }
        } else {
            Invoke-WebRequest -Uri $InstallerURL -OutFile $partialPath -UseBasicParsing -TimeoutSec 1200
        }
        Move-Item -LiteralPath $partialPath -Destination $installerPath -Force
        Test-OpenCodeDesktopInstallerFile $installerPath
        return $installerPath
    } catch {
        Remove-Item -LiteralPath $partialPath -Force -ErrorAction SilentlyContinue
        throw "OpenCode Desktop installer download or verification failed: $($_.Exception.Message)"
    }
}

function Invoke-OpenCodeDesktopDownloadedInstaller {
    $installerPath = Invoke-OpenCodeDesktopInstallerDownload
    Write-Step "Running downloaded OpenCode Desktop installer silently..."
    Write-Step $installerPath
    try {
        $proc = Start-Process -FilePath $installerPath -ArgumentList '/S' -Wait -PassThru
    } catch {
        throw "OpenCode Desktop installer failed to start: $($_.Exception.Message)"
    }
    if ($null -ne $proc.ExitCode -and $proc.ExitCode -ne 0) {
        throw "OpenCode Desktop installer failed with exit code $($proc.ExitCode)."
    }
    Write-Step "Checking OpenCode Desktop installation result..."
    return (Test-OpenCodeDesktopInstalled)
}

function Invoke-OpenCodeDesktopManualFallback([string]$Reason) {
    Write-Warning "Unable to install OpenCode Desktop automatically: $Reason"
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

try {
    if (-not (Invoke-OpenCodeDesktopDownloadedInstaller)) {
        Invoke-OpenCodeDesktopManualFallback "downloaded installer exited, but OpenCode Desktop was not detected within $InstallTimeoutSeconds seconds"
    }
} catch {
    Invoke-OpenCodeDesktopManualFallback $_.Exception.Message
}

Write-Step "Verifying OpenCode Desktop installation..."
if (-not (Test-OpenCodeDesktopInstalled)) {
    throw "OpenCode Desktop 安装完成后仍未检测到。"
}
Write-Step "OpenCode Desktop is ready."
