<#
.SYNOPSIS
    Static structural checks for the C++ desktop sources.

.DESCRIPTION
    There is no C++ toolchain on this machine, so the desktop code cannot be
    compiled here. This script performs the checks that are still possible
    without one, and that catch the most common breakages in hand-written code:

      1. every source file listed in CMakeLists.txt exists
      2. every quoted #include "..." resolves to an existing file under src/
      3. every header has a #pragma once guard
      4. braces, parentheses and brackets are balanced per file
         (a truncated edit usually shows up here first)

    Limitations, stated plainly: this cannot check types, overload resolution,
    template instantiation, or anything that needs a real parser. A green result
    means "no obvious structural breakage", NOT "it compiles".

    NOTE: this file is intentionally ASCII-only.
    Windows PowerShell 5.1 reads BOM-less .ps1 files using the ANSI code page,
    so non-ASCII text here is decoded incorrectly and produces parse errors.
    See design/plan/environment-constraints.md section 5.2.

    Exit codes: 0 = no problems, 1 = problems found, 2 = setup problem.

.PARAMETER DesktopRoot
    Desktop app root, relative to the repository root.
    Default: src/versions/0.1.0/src/apps/desktop

.EXAMPLE
    Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
    & ./tools/scripts/check-cpp-structure.ps1
#>
[CmdletBinding()]
param(
    [string]$DesktopRoot = 'src/versions/0.1.0/src/apps/desktop'
)

$ErrorActionPreference = 'Stop'

# ---- locate repository root ----
$repoRoot = $PSScriptRoot
while ($repoRoot -and -not (Test-Path (Join-Path $repoRoot '.git'))) {
    $parent = Split-Path -Parent $repoRoot
    if ([string]::IsNullOrEmpty($parent) -or $parent -eq $repoRoot) { $repoRoot = $null; break }
    $repoRoot = $parent
}
if (-not $repoRoot) { throw 'Cannot locate repository root (no .git found).' }

$desktopPath = Join-Path $repoRoot $DesktopRoot
if (-not (Test-Path $desktopPath)) {
    Write-Host "Desktop root not found: $desktopPath"
    Write-Host 'Nothing to check yet.'
    exit 0
}

$srcPath = Join-Path $desktopPath 'src'
$problems = 0

# ---------------------------------------------------------------------------
# 1) Every source file referenced by CMakeLists.txt must exist.
# ---------------------------------------------------------------------------
Write-Host 'Checking CMakeLists.txt source references...'
$cmakeFile = Join-Path $desktopPath 'CMakeLists.txt'
if (-not (Test-Path $cmakeFile)) {
    Write-Host 'CMakeLists.txt is missing.'
    exit 1
}

$cmakeLines = @(Get-Content -LiteralPath $cmakeFile -Encoding UTF8)

# Source lists appear either inline in add_library/add_executable, or in the
# set(PCX_GUI_SOURCES ...) block. Walk the file once and track which list we are
# inside; a line containing ')' closes it.
#
# Tracking the kind matters: GUI sources are allowed to be missing (the GUI
# target is skipped until they exist), anything else is a hard problem.
$listKind = ''
foreach ($line in $cmakeLines) {
    $trimmed = $line.Trim()

    if ($listKind -eq '') {
        if ($trimmed -match '^add_(library|executable)\(') {
            $listKind = 'required'
        } elseif ($trimmed -match '^set\(PCX_GUI_SOURCES') {
            $listKind = 'gui'
        } else {
            continue
        }
    }

    foreach ($m in [regex]::Matches($trimmed, '(?:^|\s)((?:src|tests)/[\w./-]+)')) {
        $relSource = $m.Groups[1].Value
        if (Test-Path -LiteralPath (Join-Path $desktopPath $relSource)) { continue }
        if ($listKind -eq 'gui') {
            Write-Host ("pending GUI source  {0}  (GUI target will be skipped)" -f $relSource)
        } else {
            $problems++
            Write-Host ("MISSING SOURCE  {0}" -f $relSource)
            Write-Host '                referenced by CMakeLists.txt but does not exist'
        }
    }

    if ($trimmed.Contains(')')) { $listKind = '' }
}

# ---------------------------------------------------------------------------
# 2) to 4) Per-file checks
# ---------------------------------------------------------------------------
$sources = @()
if (Test-Path $srcPath) {
    $sources += Get-ChildItem -Path $srcPath -Recurse -File -Include '*.cpp', '*.hpp'
}
$testsPath = Join-Path $desktopPath 'tests'
if (Test-Path $testsPath) {
    $sources += Get-ChildItem -Path $testsPath -Recurse -File -Include '*.cpp', '*.hpp'
}

Write-Host ("Checking {0} C++ file(s)..." -f $sources.Count)

foreach ($file in $sources) {
    # Replace C++ raw string literals with an empty string literal before scanning.
    #
    # The scanner below is line-based and cannot track a raw string's delimiter
    # across lines, and the contents of R"(...)" must not affect bracket balance.
    # Without this, every raw string containing braces or quotes is mis-parsed.
    $raw_text = [System.IO.File]::ReadAllText($file.FullName, [System.Text.Encoding]::UTF8)
    $raw_text = [regex]::Replace($raw_text, '(?s)R"([^()\\\s]{0,16})\(.*?\)\1"', '""')
    $lines = @($raw_text -split "`r?`n")

    $rel = $file.FullName.Substring($repoRoot.Length).TrimStart('\', '/')

    # 2) Quoted includes must resolve.
    #
    # Search order matters: a header next to the including file comes first
    # (tests/test_framework.hpp is included as "test_framework.hpp" from tests/),
    # then the shared src/ tree.
    foreach ($line in $lines) {
        if ($line -match '^\s*#\s*include\s+"([^"]+)"') {
            $target = $Matches[1]
            $candidates = @(
                (Join-Path $file.DirectoryName $target),
                (Join-Path $srcPath $target),
                (Join-Path $desktopPath $target)
            )
            $found = $false
            foreach ($candidate in $candidates) {
                if (Test-Path -LiteralPath $candidate) { $found = $true; break }
            }
            if (-not $found) {
                $problems++
                Write-Host ("UNRESOLVED INCLUDE  {0}" -f $rel)
                Write-Host ("                    quoted include not found: {0}" -f $target)
            }
        }
    }

    # 3) Headers must have an include guard.
    if ($file.Extension -eq '.hpp') {
        $hasGuard = $false
        foreach ($line in $lines) {
            if ($line -match '^\s*#\s*pragma\s+once') { $hasGuard = $true; break }
            if ($line -match '^\s*#\s*ifndef\s') { $hasGuard = $true; break }
        }
        if (-not $hasGuard) {
            $problems++
            Write-Host ("NO INCLUDE GUARD  {0}" -f $rel)
        }
    }

    # 4) Balance braces, parentheses and brackets.
    #
    # A small hand-rolled scanner rather than a regex: a regex cannot track
    # "am I inside a string literal", and that state is exactly what matters
    # here, because a brace inside a string must not be counted.
    $depthCurly = 0
    $depthParen = 0
    $depthSquare = 0
    $inBlockComment = $false
    $lineNo = 0

    foreach ($line in $lines) {
        $lineNo++
        $i = 0
        $inString = $false
        $inChar = $false

        while ($i -lt $line.Length) {
            $ch = $line[$i]

            if ($inBlockComment) {
                if ($ch -eq '*' -and ($i + 1) -lt $line.Length -and $line[$i + 1] -eq '/') {
                    $inBlockComment = $false
                    $i += 2
                    continue
                }
                $i++
                continue
            }

            if ($inString) {
                if ($ch -eq '\') { $i += 2; continue }
                if ($ch -eq '"') { $inString = $false }
                $i++
                continue
            }

            if ($inChar) {
                if ($ch -eq '\') { $i += 2; continue }
                if ($ch -eq "'") { $inChar = $false }
                $i++
                continue
            }

            if ($ch -eq '/' -and ($i + 1) -lt $line.Length -and $line[$i + 1] -eq '*') {
                $inBlockComment = $true
                $i += 2
                continue
            }
            if ($ch -eq '/' -and ($i + 1) -lt $line.Length -and $line[$i + 1] -eq '/') {
                break   # the rest of the line is a comment
            }
            if ($ch -eq '"') { $inString = $true; $i++; continue }
            if ($ch -eq "'") { $inChar = $true; $i++; continue }

            if ($ch -eq '{') { $depthCurly++ }
            elseif ($ch -eq '}') { $depthCurly-- }
            elseif ($ch -eq '(') { $depthParen++ }
            elseif ($ch -eq ')') { $depthParen-- }
            elseif ($ch -eq '[') { $depthSquare++ }
            elseif ($ch -eq ']') { $depthSquare-- }

            if ($depthCurly -lt 0 -or $depthParen -lt 0 -or $depthSquare -lt 0) {
                $problems++
                Write-Host ("UNBALANCED  {0}:{1}" -f $rel, $lineNo)
                Write-Host '            a closing bracket has no opener'
                # Reset, so one problem is not reported many times.
                $depthCurly = 0
                $depthParen = 0
                $depthSquare = 0
                break
            }
            $i++
        }
    }

    if ($depthCurly -ne 0 -or $depthParen -ne 0 -or $depthSquare -ne 0) {
        $problems++
        Write-Host ("UNBALANCED AT EOF  {0}" -f $rel)
        Write-Host ("                   braces={0} parens={1} brackets={2} (all should be 0)" -f $depthCurly, $depthParen, $depthSquare)
    }
}

Write-Host ''
if ($problems -eq 0) {
    Write-Host ("RESULT: PASS - {0} file(s) checked, no structural problems found." -f $sources.Count)
    Write-Host 'NOTE: this does not mean the code compiles; it has never been compiled.'
    exit 0
}
Write-Host ("RESULT: FAIL - {0} problem(s) found." -f $problems)
exit 1
