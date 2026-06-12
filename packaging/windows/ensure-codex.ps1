param(
    [string]$ManifestPath = (Join-Path $PSScriptRoot 'codex-manifest.json'),
    [string]$AgentctlPath = (Join-Path $PSScriptRoot 'agentctl.exe')
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

function Write-Step([string]$Message) {
    Write-Host "==> $Message" -ForegroundColor Cyan
}

Set-ScriptOutputEncoding

if (-not (Test-Path -LiteralPath $AgentctlPath)) {
    throw "agentctl.exe not found: $AgentctlPath"
}
if (-not (Test-Path -LiteralPath $ManifestPath)) {
    throw "codex-manifest.json not found: $ManifestPath"
}

$localRoot = Join-Path $env:LOCALAPPDATA 'agentserver-app'
$cacheDir = Join-Path $localRoot 'cache\codex'
Write-Step "Ensuring Codex runtime from domestic npm mirrors..."
& $AgentctlPath install-codex --manifest $ManifestPath --dest-root $localRoot --cache-dir $cacheDir
if ($LASTEXITCODE -ne 0) {
    throw "agentctl install-codex failed with exit code $LASTEXITCODE"
}
Write-Step "Codex runtime is ready."
