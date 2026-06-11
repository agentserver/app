param(
    [string]$InstallDir = (Split-Path -Parent $MyInvocation.MyCommand.Path)
)

$ErrorActionPreference = 'Stop'

function Write-Step($msg) {
    Write-Host "==> $msg" -ForegroundColor Cyan
}

function Write-TextFileUtf8NoBom([string]$Path, [string]$Content) {
    $dir = Split-Path -Parent $Path
    if (-not [string]::IsNullOrWhiteSpace($dir) -and -not (Test-Path $dir)) {
        New-Item -ItemType Directory -Force -Path $dir | Out-Null
    }
    $utf8 = New-Object System.Text.UTF8Encoding $false
    [System.IO.File]::WriteAllText($Path, $Content, $utf8)
}

function Expand-SkillsArchive([string]$ArchivePath, [string]$DestRoot) {
    if (-not (Test-Path $ArchivePath)) {
        return
    }
    if (-not (Get-Command tar.exe -ErrorAction SilentlyContinue)) {
        throw "tar.exe is required to install driver skills"
    }
    if (-not (Test-Path $DestRoot)) {
        New-Item -ItemType Directory -Force -Path $DestRoot | Out-Null
    }
    $tmp = Join-Path $env:TEMP ("agentserver-skills-" + [guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Force -Path $tmp | Out-Null
    try {
        & tar.exe -xzf $ArchivePath -C $tmp
        if ($LASTEXITCODE -ne 0) {
            throw "tar.exe failed to extract $ArchivePath with exit code $LASTEXITCODE"
        }
        $sourceRoot = $tmp
        $skillsRoot = Join-Path $tmp 'skills'
        if (Test-Path $skillsRoot) {
            $sourceRoot = $skillsRoot
        }
        $sourceRootFull = [System.IO.Path]::GetFullPath($sourceRoot).TrimEnd('\')
        $items = Get-ChildItem -Path $sourceRoot -Force -Recurse
        foreach ($item in $items) {
            $full = [System.IO.Path]::GetFullPath($item.FullName)
            $rel = $full.Substring($sourceRootFull.Length).TrimStart('\')
            if ([string]::IsNullOrWhiteSpace($rel)) {
                continue
            }
            $dest = Join-Path $DestRoot $rel
            if ($item.PSIsContainer) {
                if (-not (Test-Path $dest)) {
                    New-Item -ItemType Directory -Force -Path $dest | Out-Null
                }
                continue
            }
            if (Test-Path $dest) { continue }
            $parent = Split-Path -Parent $dest
            if (-not [string]::IsNullOrWhiteSpace($parent) -and -not (Test-Path $parent)) {
                New-Item -ItemType Directory -Force -Path $parent | Out-Null
            }
            Copy-Item -LiteralPath $item.FullName -Destination $dest
        }
    } finally {
        Remove-Item $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }
}

function Read-DriverCodexPrompt([string]$ArchivePath) {
    if (-not (Test-Path $ArchivePath)) {
        return $null
    }
    if (-not (Get-Command tar.exe -ErrorAction SilentlyContinue)) {
        throw "tar.exe is required to install driver Codex prompt"
    }
    $tmp = Join-Path $env:TEMP ("agentserver-codex-prompt-" + [guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Force -Path $tmp | Out-Null
    try {
        & tar.exe -xzf $ArchivePath -C $tmp
        if ($LASTEXITCODE -ne 0) {
            throw "tar.exe failed to extract $ArchivePath with exit code $LASTEXITCODE"
        }
        $promptPath = Join-Path $tmp 'prompts-codex\AGENTS.md'
        if (-not (Test-Path $promptPath)) {
            return $null
        }
        return [System.IO.File]::ReadAllText($promptPath)
    } finally {
        Remove-Item $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }
}

$LoomPromptStartMarker = '<!-- agentserver-app loom driver prompt:start -->'
$LoomPromptEndMarker = '<!-- agentserver-app loom driver prompt:end -->'

function Merge-DriverCodexAgentsPrompt([string]$AgentsPath, [string]$Prompt) {
    $prompt = $Prompt.TrimEnd()
    $block = $LoomPromptStartMarker + "`n" + $prompt + "`n" + $LoomPromptEndMarker
    $existing = ''
    if (Test-Path $AgentsPath) {
        $existing = [System.IO.File]::ReadAllText($AgentsPath)
    }
    if ([string]::IsNullOrWhiteSpace($existing)) {
        Write-TextFileUtf8NoBom $AgentsPath ($block + "`n")
        return
    }
    $start = $existing.IndexOf($LoomPromptStartMarker, [System.StringComparison]::Ordinal)
    $end = $existing.IndexOf($LoomPromptEndMarker, [System.StringComparison]::Ordinal)
    if ($start -ge 0 -or $end -ge 0) {
        if ($start -lt 0 -or $end -lt 0 -or $end -lt $start) {
            throw "Existing Codex AGENTS.md has malformed Loom managed block"
        }
        $end = $end + $LoomPromptEndMarker.Length
        $next = $existing.Substring(0, $start) + $block + $existing.Substring($end)
        Write-TextFileUtf8NoBom $AgentsPath $next
        return
    }
    Write-TextFileUtf8NoBom $AgentsPath ($existing.TrimEnd("`r", "`n") + "`n`n" + $block + "`n")
}

Write-Step "Installing driver skills and Codex instructions..."
$agentsSkills = Join-Path $env:USERPROFILE '.agents\skills'
$codexSkills = Join-Path $env:USERPROFILE '.codex\skills'
foreach ($archive in @(
    (Join-Path $InstallDir 'driver-skills.tar.gz'),
    (Join-Path $InstallDir 'driver-superpower-skills.tar.gz')
)) {
    Expand-SkillsArchive $archive $agentsSkills
    Expand-SkillsArchive $archive $codexSkills
}
if (Test-Path (Join-Path $InstallDir 'driver-codex-prompts.tar.gz')) {
    $driverCodexPrompt = Read-DriverCodexPrompt (Join-Path $InstallDir 'driver-codex-prompts.tar.gz')
    if (-not [string]::IsNullOrWhiteSpace($driverCodexPrompt)) {
        Merge-DriverCodexAgentsPrompt (Join-Path $env:USERPROFILE '.codex\AGENTS.md') $driverCodexPrompt
    }
}
