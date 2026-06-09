# Initializes the per-user machine identity for 星池指挥官.

param(
    [string]$MachinePath = (Join-Path $env:USERPROFILE '.agentserver-vscode\machine.json'),
    [string]$ComputerName = $env:COMPUTERNAME
)

$ErrorActionPreference = 'Stop'

if ([string]::IsNullOrWhiteSpace($MachinePath)) {
    throw "Machine path cannot be empty."
}

if ($null -eq $ComputerName) {
    $ComputerName = ''
}
$ComputerName = $ComputerName.Trim()
if ([string]::IsNullOrWhiteSpace($ComputerName)) {
    throw "Computer name cannot be empty."
}

function Write-MachineJson {
    param(
        [Parameter(Mandatory = $true)]
        [object]$Machine
    )
    $json = $Machine | ConvertTo-Json
    $utf8NoBom = New-Object System.Text.UTF8Encoding $false
    [System.IO.File]::WriteAllText($MachinePath, $json + [Environment]::NewLine, $utf8NoBom)
}

if (Test-Path -LiteralPath $MachinePath) {
    $existing = Get-Content -Raw -LiteralPath $MachinePath | ConvertFrom-Json
    $existingMachineID = [string]$existing.machine_id
    $existingComputerName = [string]$existing.computer_name
    if ([string]::IsNullOrWhiteSpace($existingMachineID) -or [string]::IsNullOrWhiteSpace($existingComputerName)) {
        throw "Existing machine identity is incomplete: $MachinePath"
    }
    $locked = $false
    if ($null -ne $existing.PSObject.Properties['computer_name_locked']) {
        $locked = [bool]$existing.computer_name_locked
    }
    $defaultComputerName = ''
    if ($null -ne $env:COMPUTERNAME) {
        $defaultComputerName = $env:COMPUTERNAME.Trim()
    }
    $canMigrateLegacyAutoName = (-not $locked) -and
        ($existingComputerName -eq $defaultComputerName) -and
        ($ComputerName -ne $existingComputerName)
    if ($canMigrateLegacyAutoName) {
        $machine = [ordered]@{}
        foreach ($prop in $existing.PSObject.Properties) {
            $machine[$prop.Name] = $prop.Value
        }
        $machine['computer_name'] = $ComputerName
        $machine['computer_name_locked'] = $true
        Write-MachineJson -Machine $machine
        Write-Host "Migrated legacy machine name: $MachinePath"
        return
    }
    Write-Host "machine.json exists; leaving unchanged: $MachinePath"
    return
}

$parent = Split-Path -Parent $MachinePath
if (-not [string]::IsNullOrWhiteSpace($parent) -and -not (Test-Path -LiteralPath $parent)) {
    New-Item -ItemType Directory -Force -Path $parent | Out-Null
}

$machine = [ordered]@{
    machine_id = [guid]::NewGuid().ToString()
    computer_name = $ComputerName
    computer_name_locked = $true
}
Write-MachineJson -Machine $machine

Write-Host "Initialized machine identity: $MachinePath"
