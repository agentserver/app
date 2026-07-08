# Initializes the per-user machine identity for 星池指挥官.

param(
    [string]$MachinePath = (Join-Path $env:USERPROFILE '.agentserver-app\machine.json'),
    [string]$ComputerName = $env:COMPUTERNAME,
    [string]$ComputerNamePath = '',
    [switch]$PreserveExistingComputerName
)

$ErrorActionPreference = 'Stop'
$utf8NoBom = New-Object System.Text.UTF8Encoding $false
$explicitComputerName = $false

if ([string]::IsNullOrWhiteSpace($MachinePath)) {
    throw "Machine path cannot be empty."
}

if (-not [string]::IsNullOrWhiteSpace($ComputerNamePath)) {
    if (-not (Test-Path -LiteralPath $ComputerNamePath)) {
        throw "Computer name path does not exist: $ComputerNamePath"
    }
    $ComputerName = [System.IO.File]::ReadAllText($ComputerNamePath, $utf8NoBom)
    $explicitComputerName = $true
}

if ($null -eq $ComputerName) {
    $ComputerName = ''
}
$ComputerName = $ComputerName.Trim()

function Assert-ComputerName {
    if ([string]::IsNullOrWhiteSpace($ComputerName)) {
        throw "Computer name cannot be empty."
    }
}

function Write-MachineJson {
    param(
        [Parameter(Mandatory = $true)]
        [object]$Machine
    )
    $json = $Machine | ConvertTo-Json
    [System.IO.File]::WriteAllText($MachinePath, $json + [Environment]::NewLine, $utf8NoBom)
}

function Backup-InvalidMachineJson {
    param(
        [string]$Reason = ''
    )
    $stamp = Get-Date -Format 'yyyyMMddHHmmssffff'
    $backupPath = "$MachinePath.bad-$PID-$stamp"
    Move-Item -LiteralPath $MachinePath -Destination $backupPath -Force
    if ([string]::IsNullOrWhiteSpace($Reason)) {
        Write-Host "Backed up invalid machine identity: $backupPath"
    } else {
        Write-Host "Backed up invalid machine identity: $backupPath ($Reason)"
    }
}

if (Test-Path -LiteralPath $MachinePath) {
    try {
        $existingText = [System.IO.File]::ReadAllText($MachinePath, $utf8NoBom)
        $existing = $existingText | ConvertFrom-Json
        $existingMachineID = [string]$existing.machine_id
        $existingComputerName = [string]$existing.computer_name
        if ([string]::IsNullOrWhiteSpace($existingMachineID) -or [string]::IsNullOrWhiteSpace($existingComputerName)) {
            throw "Machine identity is incomplete: $MachinePath"
        }
        if ($PreserveExistingComputerName -and -not $explicitComputerName) {
            $ComputerName = $existingComputerName.Trim()
        }
        Assert-ComputerName
        $machine = [ordered]@{}
        foreach ($prop in $existing.PSObject.Properties) {
            $machine[$prop.Name] = $prop.Value
        }
        $machine['machine_id'] = $existingMachineID
        $machine['computer_name'] = $ComputerName
        Write-MachineJson -Machine $machine
        Write-Host "Updated machine name: $MachinePath"
        return
    } catch {
        Backup-InvalidMachineJson -Reason 'invalid JSON or incomplete machine identity'
    }
}

Assert-ComputerName

$parent = Split-Path -Parent $MachinePath
if (-not [string]::IsNullOrWhiteSpace($parent) -and -not (Test-Path -LiteralPath $parent)) {
    New-Item -ItemType Directory -Force -Path $parent | Out-Null
}

$machine = [ordered]@{
    machine_id = [guid]::NewGuid().ToString()
    computer_name = $ComputerName
}
Write-MachineJson -Machine $machine

Write-Host "Initialized machine identity: $MachinePath"
