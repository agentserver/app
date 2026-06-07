param(
    [Parameter(Mandatory=$true)]
    [ValidateSet('codex_desktop', 'minimal_vscode')]
    [string]$Mode,

    [string]$Path = (Join-Path $PSScriptRoot 'install-mode.json')
)

$ErrorActionPreference = 'Stop'

$dir = Split-Path -Parent $Path
if ($dir -and -not (Test-Path $dir)) {
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
}

$json = @{
    frontend_mode = $Mode
} | ConvertTo-Json -Depth 2

Set-Content -Path $Path -Value $json -Encoding UTF8
Write-Host "Wrote frontend mode: $Mode"
