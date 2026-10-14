<#
.SYNOPSIS
    Detect unused imports in Go files.

.DESCRIPTION
    Go refuses to compile a file that imports a package and never uses it.
    That is the single most common compile error in freshly written code that has
    never been built, and it is detectable statically without a Go toolchain.

    This script parses the import block of each .go file, derives the identifier
    each import introduces (alias, or the last path segment), and checks whether
    that identifier appears anywhere in the rest of the file.

    Deliberate limitations:
      - it works on text, not on the AST, so it cannot follow shadowing;
      - blank (_) and dot (.) imports are skipped, since they introduce no name;
      - a match inside a comment or string counts as a use, which can produce a
        false NEGATIVE (missed error) but never a false positive. For a pre-check
        that is the right trade-off: it will not send you chasing a non-problem.

    NOTE: this file is intentionally ASCII-only (see design/plan/environment-constraints.md).

    Exit codes: 0 = no unused imports found, 1 = problems found, 2 = setup problem.

.PARAMETER SourceRoot
    Go source root, relative to the repository root. Default: src/versions/0.1.0/src

.EXAMPLE
    Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
    & ./tools/scripts/check-go-imports.ps1
#>
[CmdletBinding()]
param(
    [string]$SourceRoot = 'src/versions/0.1.0/src'
)

$ErrorActionPreference = 'Stop'

# ---- locate repository root ----
$root = $PSScriptRoot
while ($root -and -not (Test-Path (Join-Path $root '.git'))) {
    $parent = Split-Path -Parent $root
    if ([string]::IsNullOrEmpty($parent) -or $parent -eq $root) { $root = $null; break }
    $root = $parent
}
if (-not $root) { throw 'Cannot locate repository root (no .git found).' }

$srcPath = Join-Path $root $SourceRoot
if (-not (Test-Path $srcPath)) { Write-Host "Source root not found: $srcPath" -ForegroundColor Red; exit 2 }

$files = Get-ChildItem -Path $srcPath -Recurse -File -Filter '*.go'
if ($files.Count -eq 0) { Write-Host "No Go files under $srcPath" -ForegroundColor Red; exit 2 }

$problems = 0
$checked = 0

foreach ($file in $files) {
    $lines = @(Get-Content -LiteralPath $file.FullName -Encoding UTF8)
    if ($lines.Count -eq 0) { continue }

    # ---- locate the import block ----
    $imports = @()   # each entry: @{ Line = n; Path = '...'; Alias = '...' }
    $importStart = -1
    $importEnd = -1

    for ($i = 0; $i -lt $lines.Count; $i++) {
        $line = $lines[$i]
        if ($line -match '^\s*import\s*\(\s*$') { $importStart = $i; break }
        if ($line -match '^\s*import\s+"([^"]+)"') {
            $imports += @{ Line = $i + 1; Path = $Matches[1]; Alias = '' }
            $importStart = $i
            $importEnd = $i
            break
        }
        if ($line -match '^\s*import\s+(\S+)\s+"([^"]+)"') {
            $imports += @{ Line = $i + 1; Path = $Matches[2]; Alias = $Matches[1] }
            $importStart = $i
            $importEnd = $i
            break
        }
    }

    if ($importStart -ge 0 -and $importEnd -lt 0) {
        # multi-line import block
        for ($i = $importStart + 1; $i -lt $lines.Count; $i++) {
            if ($lines[$i] -match '^\s*\)\s*$') { $importEnd = $i; break }
            if ($lines[$i] -match '^\s*(?:(\S+)\s+)?"([^"]+)"') {
                $alias = $Matches[1]
                if (-not $alias) { $alias = '' }
                $imports += @{ Line = $i + 1; Path = $Matches[2]; Alias = $alias }
            }
        }
    }

    if ($imports.Count -eq 0) { continue }
    $checked++

    # ---- build the body text (everything outside the import block) ----
    $bodyLines = @()
    for ($i = 0; $i -lt $lines.Count; $i++) {
        if ($importStart -ge 0 -and $i -ge $importStart -and $i -le $importEnd) { continue }
        $bodyLines += $lines[$i]
    }
    $body = ($bodyLines -join "`n")

    # ---- check each import ----
    foreach ($imp in $imports) {
        $alias = $imp.Alias
        if ($alias -eq '_' -or $alias -eq '.') { continue }

        $name = $alias
        if (-not $name) {
            $segments = $imp.Path -split '/'
            $name = $segments[$segments.Count - 1]
            # Strip a major-version suffix such as /v2.
            if ($name -match '^v\d+$' -and $segments.Count -ge 2) { $name = $segments[$segments.Count - 2] }
        }
        # A hyphen is legal in a path but not in an identifier; the package name
        # replaces it. Fall back to the last alpha segment.
        if ($name -match '-') { $name = ($name -replace '-', '') }

        # Word-boundary search across the body.
        if ($body -notmatch ('\b' + [regex]::Escape($name) + '\b')) {
            $problems++
            $rel = $file.FullName.Substring($root.Length).TrimStart('\', '/')
            Write-Host ("UNUSED IMPORT  {0}:{1}" -f $rel, $imp.Line) -ForegroundColor Red
            Write-Host ("               import {0}  -> identifier '{1}' never used" -f $imp.Path, $name) -ForegroundColor DarkRed
        }
    }
}

Write-Host ''
Write-Host ("Checked {0} Go file(s) with imports out of {1} file(s)." -f $checked, $files.Count)
if ($problems -eq 0) {
    Write-Host 'RESULT: PASS - no unused imports detected.' -ForegroundColor Green
    exit 0
}
Write-Host ("RESULT: FAIL - {0} unused import(s). Go will not compile these files." -f $problems) -ForegroundColor Red
exit 1
