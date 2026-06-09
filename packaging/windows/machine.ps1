# Initializes the per-user machine identity for 星池指挥官.

param(
    [string]$MachinePath = (Join-Path $env:USERPROFILE '.agentserver-vscode\machine.json'),
    [string]$ComputerName = $env:COMPUTERNAME
)

$ErrorActionPreference = 'Stop'

if ([string]::IsNullOrWhiteSpace($MachinePath)) {
    throw "Machine path cannot be empty."
}

if (Test-Path -LiteralPath $MachinePath) {
    Write-Host "machine.json exists; leaving unchanged: $MachinePath"
    return
}

if ($null -eq $ComputerName) {
    $ComputerName = ''
}
$ComputerName = $ComputerName.Trim()
if ([string]::IsNullOrWhiteSpace($ComputerName)) {
    throw "Computer name cannot be empty."
}

$parent = Split-Path -Parent $MachinePath
if (-not [string]::IsNullOrWhiteSpace($parent) -and -not (Test-Path -LiteralPath $parent)) {
    New-Item -ItemType Directory -Force -Path $parent | Out-Null
}

$machine = [ordered]@{
    machine_id = [guid]::NewGuid().ToString()
    computer_name = $ComputerName
}
$json = $machine | ConvertTo-Json
$utf8NoBom = New-Object System.Text.UTF8Encoding $false
[System.IO.File]::WriteAllText($MachinePath, $json + [Environment]::NewLine, $utf8NoBom)

Write-Host "Initialized machine identity: $MachinePath"
