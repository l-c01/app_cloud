<#
.SYNOPSIS
    Verify that authored source files are actually tracked by git.

.DESCRIPTION
    Every other check in this directory reads files from disk. None of them can
    tell the difference between "the file exists" and "the file is in the
    repository". That gap shipped a real defect:

        .gitignore contained the rule  secrets/
        which matches a directory named "secrets" at ANY depth, including the
        Go package  src/versions/0.1.0/src/shared/secrets/
        The whole package was therefore never committed. Locally every check
        passed; CI reported:
            package proxyclient/shared/secrets is not in std

    The failure is invisible on the machine where the code was written, which is
    exactly why it needs its own check.

    What is reported:
      IGNORED    an authored file is excluded by .gitignore  -> failure
      UNTRACKED  an authored file exists but was never added -> failure

    Only authored trees are inspected. Third-party reference snapshots and
    generated output are expected to be ignored and are skipped.

    NOTE: this file is intentionally ASCII-only.
    Windows PowerShell 5.1 reads BOM-less .ps1 files using the ANSI code page.

    Exit codes: 0 = clean, 1 = problems found, 2 = setup problem.

.PARAMETER Roots
    Authored directories to inspect, relative to the repository root.

.EXAMPLE
    Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
    & ./tools/scripts/check-tracked-files.ps1
#>
[CmdletBinding()]
param(
    [string[]]$Roots = @(
        'src/versions',
        'src/README.md',
        'design',
        'docs',
        'locales',
        'tools',
        '.github/workflows'
    )
)

$ErrorActionPreference = 'Stop'

$repoRoot = $PSScriptRoot
while ($repoRoot -and -not (Test-Path (Join-Path $repoRoot '.git'))) {
    $parent = Split-Path -Parent $repoRoot
    if ([string]::IsNullOrEmpty($parent) -or $parent -eq $repoRoot) { $repoRoot = $null; break }
    $repoRoot = $parent
}
if (-not $repoRoot) { throw 'Cannot locate repository root (no .git found).' }

$git = Get-Command git -ErrorAction SilentlyContinue
if (-not $git) {
    # Git is not always on PATH (it certainly is not on the machine this was
    # written on), so fall back to the usual install location.
    $candidate = 'C:\Program Files\Git\cmd\git.exe'
    if (Test-Path $candidate) { $git = Get-Item $candidate } else {
        Write-Host 'git was not found; this check cannot run'
        exit 2
    }
} else {
    $git = Get-Item $git.Source
}

# Ask git for everything it considers ignored or untracked under the given roots.
# One invocation, rather than one per file.
$existing = @()
foreach ($root in $Roots) {
    if (Test-Path (Join-Path $repoRoot $root)) { $existing += $root }
}
if ($existing.Count -eq 0) {
    Write-Host 'none of the requested roots exist'
    exit 2
}

$raw = & $git.FullName -C $repoRoot status --ignored --porcelain --untracked-files=all -- $existing 2>&1
if ($LASTEXITCODE -ne 0) {
    Write-Host 'git status failed:'
    $raw | ForEach-Object { Write-Host "  $_" }
    exit 2
}

$ignored = @()
$untracked = @()
foreach ($line in $raw) {
    $text = $line.ToString()
    if ($text.Length -lt 4) { continue }
    $marker = $text.Substring(0, 2)
    $path = $text.Substring(3).Trim('"')
    if ($marker -eq '!!') { $ignored += $path }
    elseif ($marker -eq '??') { $untracked += $path }
}

# Directories reported as ignored may contain thousands of files; collapse them
# so the message stays readable.
function Show-Paths($paths) {
    $shown = @{}
    foreach ($p in $paths) {
        $dir = Split-Path -Parent $p
        if ($dir -and -not $shown.ContainsKey($dir)) { $shown[$dir] = 0 }
        if ($dir) { $shown[$dir]++ } 
    }
    return $shown
}

$problems = 0

if ($ignored.Count -gt 0) {
    Write-Host 'IGNORED - authored files excluded by .gitignore:'
    foreach ($p in ($ignored | Sort-Object)) {
        Write-Host ("  {0}" -f $p)
        $problems++
    }
    Write-Host ''
    Write-Host '  A rule that matches too broadly will silently drop a whole source'
    Write-Host '  directory. Anchor such rules to the repository root, e.g.'
    Write-Host '  "/secrets/" instead of "secrets/".'
    Write-Host ''
}

if ($untracked.Count -gt 0) {
    Write-Host 'UNTRACKED - authored files never added to git:'
    foreach ($p in ($untracked | Sort-Object)) {
        Write-Host ("  {0}" -f $p)
        $problems++
    }
    Write-Host ''
    Write-Host '  These exist on this machine but would be missing from a clone.'
    Write-Host ''
}

Write-Host ("roots     : {0}" -f ($existing -join ', '))
Write-Host ("ignored   : {0}" -f $ignored.Count)
Write-Host ("untracked : {0}" -f $untracked.Count)
Write-Host ''

if ($problems -eq 0) {
    Write-Host 'RESULT: PASS - every authored file is tracked by git.'
    exit 0
}
Write-Host ("RESULT: FAIL - {0} problem(s) found." -f $problems)
exit 1
