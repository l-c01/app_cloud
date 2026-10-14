<#
.SYNOPSIS
    Detect import problems in Go files, in both directions.

.DESCRIPTION
    Go refuses to compile a file that imports a package and never uses it, and it
    equally refuses to compile one that uses a package it never imported
    ("undefined: strings"). Both are common in freshly written code that has
    never been built, and both are detectable statically without a Go toolchain.

    Direction 1 (unused): parses the import block of each .go file, derives the
    identifier each import introduces (alias, or the last path segment), and
    checks whether that identifier appears anywhere in the rest of the file.

    Direction 2 (missing): checks whether any package in a conservative
    allowlist is used as "pkg.Something" without being imported. The allowlist is
    deliberately narrow - see the comment at the check itself for why ambiguous
    short names are excluded.

    Deliberate limitations:
      - it works on text, not on the AST, so it cannot follow shadowing;
      - blank (_) and dot (.) imports are skipped, since they introduce no name;
      - a match inside a comment or string counts as a use, which can produce a
        false NEGATIVE (missed error) but never a false positive. For a pre-check
        that is the right trade-off: it will not send you chasing a non-problem.

    NOTE: this file is intentionally ASCII-only (see design/plan/environment-constraints.md).

    Exit codes: 0 = no import problems, 1 = problems found, 2 = setup problem.

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

    # ---- check for MISSING stdlib imports ----
    #
    # The unused-import check above only catches one direction. The mirror image
    # is a file that USES a package it never imported, which Go also refuses to
    # compile ("undefined: strings"). That gap was real: a change added
    # strings.HasPrefix/TrimPrefix to a file whose import block had no strings,
    # and this script reported PASS.
    #
    # Why an explicit list instead of "every stdlib package": a static text
    # scanner cannot tell a package qualifier from a variable or field of the
    # same name. `path.Join` is a package call, but a struct with a field named
    # `path` would look identical. Inventing a false positive here would break
    # the property this script promises in its header - it never sends you
    # chasing a non-problem.
    #
    # So the list below is deliberately conservative: names that are practically
    # never used as identifiers in this codebase, and that are common enough to
    # be worth guarding. Ambiguous short names (io, log, net, os, time, path,
    # url, rand) are left out on purpose. Missing those is a false negative,
    # which is the acceptable direction.
    $guardedPackages = @(
        'strings', 'strconv', 'filepath', 'regexp', 'bufio',
        'unicode', 'reflect', 'bytes', 'sha256', 'base64', 'utf8',
        'errors', 'sort', 'json', 'exec', 'signal', 'atomic'
    )

    $importedNames = @{}
    foreach ($imp in $imports) {
        $alias = $imp.Alias
        if ($alias -eq '_' -or $alias -eq '.') { continue }
        $nm = $alias
        if (-not $nm) {
            $segments = $imp.Path -split '/'
            $nm = $segments[$segments.Count - 1]
            if ($nm -match '^v\d+$' -and $segments.Count -ge 2) { $nm = $segments[$segments.Count - 2] }
        }
        if ($nm -match '-') { $nm = ($nm -replace '-', '') }
        $importedNames[$nm] = $true
    }

    foreach ($pkg in $guardedPackages) {
        if ($importedNames.ContainsKey($pkg)) { continue }
        # Usage is "pkg.Something", and not "something.pkg.Something" - the
        # negative lookbehind keeps field access out of the match.
        if ($body -match ('(?<![\w.])' + [regex]::Escape($pkg) + '\s*\.')) {
            $problems++
            $rel = $file.FullName.Substring($root.Length).TrimStart('\', '/')
            Write-Host ("MISSING IMPORT {0}" -f $rel) -ForegroundColor Red
            Write-Host ("               uses '{0}.' but does not import it; Go will report 'undefined: {0}'" -f $pkg) -ForegroundColor DarkRed
        }
    }

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
Write-Host 'Checked both directions: imports that are never used, and packages used but never imported.'
if ($problems -eq 0) {
    Write-Host 'RESULT: PASS - every import is used, and no guarded stdlib package is used without an import.' -ForegroundColor Green
    exit 0
}
Write-Host ("RESULT: FAIL - {0} import problem(s). Go will not compile these files." -f $problems) -ForegroundColor Red
exit 1
