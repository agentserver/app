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

$utf8NoBom = New-Object System.Text.UTF8Encoding $false
[System.IO.File]::WriteAllText($Path, $json + [Environment]::NewLine, $utf8NoBom)
Write-Host "Wrote frontend mode: $Mode"
