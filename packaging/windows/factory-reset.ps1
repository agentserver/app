# factory-reset.ps1 — full wipe of 星池指挥官 + VS Code + all our state.
#
# Use BEFORE a clean re-install test to make sure no leftover state biases
# the result.
#
# Removes:
#   - 星池指挥官 install dir + .lnk + registry + Apps&features entry
#   - codex.exe + bin dir
#   - %USERPROFILE%\.agentserver-app\ (state, vscode-data, vscode-extensions, cache)
#   - %USERPROFILE%\.codex\config.toml + .bak files
#   - HKCU\Environment OPENAI_API_KEY
#   - VS Code (via its own uninstaller, both per-user and system installs)
#   - All running Code.exe / launcher.exe / agentctl.exe / onboarding-server.exe
#
# Does NOT touch: modelserver/agentserver accounts (those are server-side),
# Windows credential manager entries (added in future v2).

param([switch]$KeepVSCode, [switch]$Silent)

$ErrorActionPreference = 'SilentlyContinue'

function Set-ScriptOutputEncoding {
    try {
        $utf8 = New-Object System.Text.UTF8Encoding $false
        [Console]::OutputEncoding = $utf8
        $script:OutputEncoding = $utf8
        & chcp.com 65001 > $null 2>$null
    } catch {
        # Best-effort only; reset must still work if the host forbids it.
    }
}

Set-ScriptOutputEncoding

function Write-Step($msg) { Write-Host "==> $msg" -ForegroundColor Cyan }

function Remove-RegistrySubKeyTree([string]$SubKey) {
    try {
        [Microsoft.Win32.Registry]::CurrentUser.DeleteSubKeyTree($SubKey, $false)
    } catch {
        # Missing keys are fine during reset.
    }
}

if (-not $Silent) {
    Write-Host ""
    Write-Host "This will permanently remove 星池指挥官, all its state," -ForegroundColor Yellow
    if (-not $KeepVSCode) {
        Write-Host "AND uninstall VS Code itself." -ForegroundColor Yellow
    } else {
        Write-Host "but keep VS Code (--KeepVSCode)." -ForegroundColor Yellow
    }
    $ans = Read-Host "Type 'reset' to confirm"
    if ($ans -ne 'reset') { Write-Host "aborted"; exit 1 }
}

# --- 1. Kill any running processes ---
Write-Step "Killing running processes..."
Get-Process Code,launcher,agentctl,onboarding-server,'open-folder' -ErrorAction SilentlyContinue |
    Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Seconds 2

# --- 2. Run 星池指挥官's own uninstaller if present ---
$ourUninstall = "$env:LOCALAPPDATA\Programs\agentserver-app\install.ps1"
if (Test-Path $ourUninstall) {
    Write-Step "Running 星池指挥官 uninstaller..."
    & powershell -NoProfile -ExecutionPolicy Bypass -File $ourUninstall -Uninstall -Silent
}

# --- 3. Belt-and-suspenders: nuke any leftovers ---
Write-Step "Removing residual files..."
$paths = @(
    "$env:LOCALAPPDATA\Programs\agentserver-app",       # install dir
    "$env:LOCALAPPDATA\agentserver-app",                 # codex bin + state cache
    "$env:USERPROFILE\.agentserver-app",                 # state / vscode-data / extensions
    "$env:USERPROFILE\Desktop\星池指挥官.lnk",               # desktop shortcut
    "$env:USERPROFILE\Desktop\agentserver-app.lnk"       # desktop shortcut
)
foreach ($p in $paths) {
    if (Test-Path $p) {
        Remove-Item -Recurse -Force $p -ErrorAction SilentlyContinue
        Write-Host "  removed $p"
    }
}

# Codex user config — back up the original if it has non-modelserver providers
$codexConfig = "$env:USERPROFILE\.codex\config.toml"
if (Test-Path $codexConfig) {
    $backup = "$codexConfig.factory-reset-backup-$([int][double]::Parse((Get-Date -UFormat %s)))"
    Move-Item $codexConfig $backup -Force
    Write-Host "  backed up ~/.codex/config.toml → $backup"
}

# Registry
$regs = @(
    "Software\Classes\*\shell\AgentserverApp",
    "Software\Classes\Directory\shell\AgentserverApp",
    "Software\Classes\Directory\Background\shell\AgentserverApp"
)
foreach ($r in $regs) {
    Remove-RegistrySubKeyTree $r
    Write-Host "  removed HKCU:\$r"
}
$uninstallReg = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\agentserver-app"
if (Test-Path $uninstallReg) {
    Remove-Item -Recurse -Force $uninstallReg -ErrorAction SilentlyContinue
    Write-Host "  removed $uninstallReg"
}

# Env var
$envKey = [Environment]::GetEnvironmentVariable("OPENAI_API_KEY", "User")
if ($envKey) {
    [Environment]::SetEnvironmentVariable("OPENAI_API_KEY", $null, "User")
    Write-Host "  removed HKCU\Environment\OPENAI_API_KEY (was '$($envKey.Substring(0,[Math]::Min(10,$envKey.Length)))...')"
}

# --- 4. Uninstall VS Code (unless -KeepVSCode) ---
if (-not $KeepVSCode) {
    Write-Step "Uninstalling VS Code..."
    # User-scope install (recommended path; uses unins000.exe inside install dir)
    $vsCodeUserDir = "$env:LOCALAPPDATA\Programs\Microsoft VS Code"
    if (Test-Path $vsCodeUserDir) {
        $vsUnins = Join-Path $vsCodeUserDir 'unins000.exe'
        if (Test-Path $vsUnins) {
            Write-Host "  user-scope: running $vsUnins /VERYSILENT /SUPPRESSMSGBOXES /NORESTART"
            Start-Process -FilePath $vsUnins -ArgumentList '/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART' -Wait
        } else {
            Write-Host "  user-scope: no unins000.exe; removing dir directly"
            Remove-Item -Recurse -Force $vsCodeUserDir -ErrorAction SilentlyContinue
        }
    }
    # System-scope install (rarer; under Program Files)
    foreach ($sysDir in @("${env:ProgramFiles}\Microsoft VS Code", "${env:ProgramFiles(x86)}\Microsoft VS Code")) {
        if (Test-Path $sysDir) {
            $vsUnins = Join-Path $sysDir 'unins000.exe'
            if (Test-Path $vsUnins) {
                Write-Host "  system-scope: running $vsUnins /VERYSILENT"
                Start-Process -FilePath $vsUnins -ArgumentList '/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART' -Wait
            }
        }
    }
    # Per-user data + extensions (these are NOT touched by the uninstaller)
    Remove-Item -Recurse -Force "$env:APPDATA\Code" -ErrorAction SilentlyContinue
    Remove-Item -Recurse -Force "$env:USERPROFILE\.vscode" -ErrorAction SilentlyContinue
}

# --- 5. Report ---
Write-Step "Verification:"
$checks = @(
    @{name="星池指挥官 install"; path="$env:LOCALAPPDATA\Programs\agentserver-app"},
    @{name="codex bin dir"; path="$env:LOCALAPPDATA\agentserver-app"},
    @{name="state dir"; path="$env:USERPROFILE\.agentserver-app"},
    @{name="desktop .lnk"; path="$env:USERPROFILE\Desktop\星池指挥官.lnk"},
    @{name="legacy desktop .lnk"; path="$env:USERPROFILE\Desktop\agentserver-app.lnk"},
    @{name="reg file shell"; path="Registry::HKEY_CURRENT_USER\Software\Classes\*\shell\AgentserverApp"},
    @{name="reg shell"; path="HKCU:\Software\Classes\Directory\shell\AgentserverApp"},
    @{name="reg apps&features"; path="HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\agentserver-app"},
    @{name="VS Code (user)"; path="$env:LOCALAPPDATA\Programs\Microsoft VS Code"},
    @{name="VS Code (sys)"; path="${env:ProgramFiles}\Microsoft VS Code"}
)
foreach ($c in $checks) {
    $present = Test-Path -LiteralPath $c.path
    $mark = if ($present) { "STILL HERE" } else { "gone" }
    $clr  = if ($present) { 'Yellow' } else { 'Green' }
    Write-Host ("  {0,-30}  {1}" -f $c.name, $mark) -ForegroundColor $clr
}

Write-Host ""
Write-Host "factory-reset complete." -ForegroundColor Green
