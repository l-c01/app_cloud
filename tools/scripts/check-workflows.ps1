<#
.SYNOPSIS
    Structural checks for GitHub Actions workflow files.

.DESCRIPTION
    Workflows are the code that actually builds and ships the product, and a
    broken one fails at the worst possible moment: after a push, in the Actions
    tab, often with a message that does not name the real cause.

    There is no YAML parser available here, so this script checks the properties
    that matter and that a plain text scan can decide reliably:

      1. no tab characters (YAML forbids tabs for indentation)
      2. required top-level keys: name, on, jobs
      3. indentation is consistent outside block scalars
         (inside run: | blocks the content is a shell script, and its
          indentation is the shell's business, not YAML's)
      4. every local reusable workflow reference (uses: ./.github/workflows/x.yml)
         points at a file that exists
      5. every file referenced by the wrappers declares on.workflow_call
      6. every workflow has at least one job

    Point 4 is the valuable one: a typo in a reusable path only fails when the
    workflow runs, and the error is "invalid workflow file", not "file not found".

    NOTE: this file is intentionally ASCII-only.
    Windows PowerShell 5.1 reads BOM-less .ps1 files using the ANSI code page.

    Exit codes: 0 = no problems, 1 = problems found, 2 = setup problem.

.EXAMPLE
    Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
    & ./tools/scripts/check-workflows.ps1
#>
[CmdletBinding()]
param(
    [string]$WorkflowDir = '.github/workflows'
)

$ErrorActionPreference = 'Stop'

$repoRoot = $PSScriptRoot
while ($repoRoot -and -not (Test-Path (Join-Path $repoRoot '.git'))) {
    $parent = Split-Path -Parent $repoRoot
    if ([string]::IsNullOrEmpty($parent) -or $parent -eq $repoRoot) { $repoRoot = $null; break }
    $repoRoot = $parent
}
if (-not $repoRoot) { throw 'Cannot locate repository root (no .git found).' }

$dir = Join-Path $repoRoot $WorkflowDir
if (-not (Test-Path $dir)) {
    Write-Host "workflow directory not found: $dir"
    exit 2
}

$files = Get-ChildItem -Path $dir -File -Filter '*.yml'
if ($files.Count -eq 0) {
    Write-Host "no workflow files under $dir"
    exit 2
}

$problems = 0

# Collect the set of files that declare on.workflow_call, so callers can be
# checked against reality rather than against a hand-maintained list.
$reusable = @{}
foreach ($file in $files) {
    $text = Get-Content -LiteralPath $file.FullName -Raw -Encoding UTF8
    if ($text -match '(?m)^\s{2}workflow_call:') { $reusable[$file.Name] = $true }
}

foreach ($file in $files) {
    $lines = @(Get-Content -LiteralPath $file.FullName -Encoding UTF8)
    $rel = $file.FullName.Substring($repoRoot.Length).TrimStart('\', '/')

    # 1) tabs
    $lineNo = 0
    foreach ($line in $lines) {
        $lineNo++
        if ($line -match "`t") {
            $problems++
            Write-Host ("TAB CHARACTER  {0}:{1}" -f $rel, $lineNo)
        }
    }

    # 2) required top-level keys
    foreach ($key in @('name:', 'on:', 'jobs:')) {
        if (-not ($lines | Where-Object { $_ -match ('^' + [regex]::Escape($key)) })) {
            $problems++
            Write-Host ("MISSING TOP-LEVEL KEY  {0}: {1}" -f $rel, $key.TrimEnd(':'))
        }
    }

    # 3) indentation outside block scalars
    #
    # A block scalar starts at a line whose value is '|' or '>' (optionally with
    # chomping/indent indicators) and continues while lines are more indented
    # than the key that introduced it.
    $inBlock = $false
    $blockIndent = 0
    $lineNo = 0
    foreach ($line in $lines) {
        $lineNo++
        if ($line -match '^\s*$') { continue }

        $indent = $line.Length - $line.TrimStart().Length

        if ($inBlock) {
            if ($indent -gt $blockIndent) { continue }
            $inBlock = $false
        }

        if ($line -match ':\s*[|>][-+]?\d*\s*$') {
            $inBlock = $true
            $blockIndent = $indent
            continue
        }

        if (($indent % 2) -ne 0) {
            $problems++
            Write-Host ("ODD INDENTATION  {0}:{1}" -f $rel, $lineNo)
            Write-Host ("                 {0}" -f $line.Trim())
        }
    }

    # 4) local reusable workflow references must exist
    $lineNo = 0
    foreach ($line in $lines) {
        $lineNo++
        if ($line -match 'uses:\s*\./\.github/workflows/([\w.-]+)') {
            $target = $Matches[1]
            if (-not (Test-Path (Join-Path $dir $target))) {
                $problems++
                Write-Host ("MISSING REUSABLE  {0}:{1}" -f $rel, $lineNo)
                Write-Host ("                  references .github/workflows/{0}, which does not exist" -f $target)
            } elseif (-not $reusable.ContainsKey($target)) {
                $problems++
                Write-Host ("NOT A REUSABLE WORKFLOW  {0}:{1}" -f $rel, $lineNo)
                Write-Host ("                         {0} has no on.workflow_call, so it cannot be called with uses:" -f $target)
            }
        }
    }

    # 6) at least one job
    $jobs = @($lines | Where-Object { $_ -match '^  [a-z][a-z0-9_-]*:\s*$' })
    # The first match after 'jobs:' is the job list; approximate by requiring
    # at least one two-space indented key at all.
    if ($jobs.Count -eq 0) {
        # Fall back to any two-space key that is not under 'on:'
        $hasAny = $false
        foreach ($line in $lines) {
            if ($line -match '^  [a-z][a-z0-9_-]*:') { $hasAny = $true; break }
        }
        if (-not $hasAny) {
            $problems++
            Write-Host ("NO JOBS  {0}" -f $rel)
        }
    }
}

# Report which files are reusable, so a new one is easy to spot.
Write-Host ''
Write-Host ("workflows : {0}" -f $files.Count)
Write-Host ("reusable  : {0}" -f (($reusable.Keys | Sort-Object) -join ', '))
Write-Host ''

if ($problems -eq 0) {
    Write-Host 'RESULT: PASS - workflow files are structurally consistent.'
    Write-Host 'NOTE: this does not prove they are valid YAML or that they run correctly.'
    exit 0
}
Write-Host ("RESULT: FAIL - {0} problem(s) found." -f $problems)
exit 1
