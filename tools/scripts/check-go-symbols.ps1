<#
.SYNOPSIS
    Verify that every symbol referenced across internal Go packages exists.

.DESCRIPTION
    The module uses internal package imports (proxyclient/...). A typo or a
    renamed identifier in such a reference is a compile error that a Go toolchain
    catches instantly -- but there is no Go toolchain on this machine.

    This script builds a package-level symbol index for every internal package,
    then checks each qualified reference of the form <pkg>.<Symbol> against it.

    Scope and limitations:
      - only references through internal imports (proxyclient/...) are checked;
        standard library and builtin identifiers are not;
      - only package-level declarations are indexed (functions, methods, types,
        vars, consts), which is exactly what a qualified reference can reach;
      - selector chains beyond two levels (pkg.A.B) only check pkg.A;
      - text-based, so a name inside a comment counts as present (false negative,
        never a false positive).

    NOTE: this file is intentionally ASCII-only (see design/plan/environment-constraints.md).

    Exit codes: 0 = all references resolve, 1 = unresolved references, 2 = setup problem.

.PARAMETER SourceRoot
    Go source root, relative to the repository root. Default: src/versions/0.1.0/src

.EXAMPLE
    Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
    & ./tools/scripts/check-go-symbols.ps1
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

# ---- build the package symbol index ----
# Key: relative package path (e.g. shared/domain) -> set of exported+unexported names.

$symbols = @{}
$packageDirs = Get-ChildItem -Path $srcPath -Recurse -Directory |
    Where-Object { (Get-ChildItem $_.FullName -Filter '*.go' -File).Count -gt 0 }

foreach ($dir in $packageDirs) {
    $rel = $dir.FullName.Substring($srcPath.Length).TrimStart('\', '/') -replace '\\', '/'
    $set = @{}

    $goFiles = Get-ChildItem $dir.FullName -Filter '*.go' -File
    foreach ($f in $goFiles) {
        $lines = @(Get-Content -LiteralPath $f.FullName -Encoding UTF8)
        $blockKind = ''
        foreach ($line in $lines) {
            # block open / close for const, var, type
            if ($line -match '^\s*(const|var|type)\s*\(\s*$') { $blockKind = $Matches[1]; continue }
            if ($line -match '^\s*\)\s*$') { $blockKind = ''; continue }

            if ($line -match '^\s*func\s+(?:\([^)]*\)\s+)?([A-Za-z_]\w*)\s*\(') {
                $set[$Matches[1]] = $true
                continue
            }
            if ($line -match '^\s*func\s+(?:\([^)]*\)\s+)?([A-Za-z_]\w*)\s*\[?.*?(?:\{|$)') {
                # generic declaration without a parenthesis right after the name
                $set[$Matches[1]] = $true
                continue
            }
            if ($line -match '^\s*(?:const|var)\s+([A-Za-z_]\w*)\s') {
                $set[$Matches[1]] = $true
                continue
            }
            if ($line -match '^\s*type\s+([A-Za-z_]\w*)\s') {
                $set[$Matches[1]] = $true
                continue
            }
            if ($blockKind -eq 'const' -or $blockKind -eq 'var') {
                if ($line -match '^\s*([A-Za-z_]\w*)\s*(?:[A-Za-z_\[\]\*\.\|]+\s*)?=') {
                    $set[$Matches[1]] = $true
                    continue
                }
                if ($line -match '^\s*([A-Za-z_]\w*)\s*$') {
                    # continued const/var list
                    $set[$Matches[1]] = $true
                    continue
                }
            }
            if ($blockKind -eq 'type') {
                if ($line -match '^\s*([A-Za-z_]\w*)\s') {
                    $set[$Matches[1]] = $true
                    continue
                }
            }
        }
    }
    $symbols[$rel] = $set
}

Write-Host ("Indexed {0} internal package(s)." -f $symbols.Count) -ForegroundColor DarkGray

# ---- check references ----
$problems = 0
$checkedRefs = 0
$files = Get-ChildItem -Path $srcPath -Recurse -File -Filter '*.go'

foreach ($file in $files) {
    $lines = @(Get-Content -LiteralPath $file.FullName -Encoding UTF8)
    $fileDir = $file.DirectoryName.Substring($srcPath.Length).TrimStart('\', '/') -replace '\\', '/'

    # map identifier -> target package path
    $imports = @{}
    $inBlock = $false
    foreach ($line in $lines) {
        if ($line -match '^\s*import\s*\(\s*$') { $inBlock = $true; continue }
        if ($inBlock -and $line -match '^\s*\)\s*$') { $inBlock = $false; continue }
        if ($inBlock -or $line -match '^\s*import\s') {
            if ($line -match '^\s*(?:(\S+)\s+)?"(proxyclient/[^"]+)"') {
                $alias = $Matches[1]
                $path = $Matches[2]
                $target = $path -replace '^proxyclient/', ''
                $name = $alias
                if (-not $name) {
                    $segs = $target -split '/'
                    $name = $segs[$segs.Count - 1]
                }
                if ($name -ne '_' -and $name -ne '.') { $imports[$name] = $target }
            }
        }
    }
    if ($imports.Count -eq 0) { continue }

    for ($i = 0; $i -lt $lines.Count; $i++) {
        $line = $lines[$i]
        # skip comment-only lines to reduce noise
        if ($line -match '^\s*//') { continue }

        # Strip string literals before matching.
        #
        # Without this, a quoted message key such as "compiler.protocol_unsupported"
        # looks exactly like a qualified Go reference and is reported as unresolved.
        # Names inside strings are never package references, so removing them is
        # both correct and the only way to keep the check free of false positives.
        $clean = $line -replace '"[^"]*"', '""'
        $clean = $clean -replace '`[^`]*`', '``'

        foreach ($pkgName in $imports.Keys) {
            $target = $imports[$pkgName]
            $pattern = '\b' + [regex]::Escape($pkgName) + '\.([A-Za-z_]\w*)'
            foreach ($m in [regex]::Matches($clean, $pattern)) {
                $symbol = $m.Groups[1].Value
                $checkedRefs++
                if (-not $symbols.ContainsKey($target)) { continue }
                if (-not $symbols[$target].ContainsKey($symbol)) {
                    $problems++
                    $rel = $file.FullName.Substring($root.Length).TrimStart('\', '/')
                    Write-Host ("UNRESOLVED  {0}:{1}" -f $rel, ($i + 1)) -ForegroundColor Red
                    Write-Host ("            {0}.{1}  not declared in package '{2}'" -f $pkgName, $symbol, $target) -ForegroundColor DarkRed
                }
            }
        }
    }
}

Write-Host ''
Write-Host ("Checked {0} internal reference(s)." -f $checkedRefs)
if ($problems -eq 0) {
    Write-Host 'RESULT: PASS - all internal package references resolve.' -ForegroundColor Green
    exit 0
}
Write-Host ("RESULT: FAIL - {0} unresolved reference(s)." -f $problems) -ForegroundColor Red
exit 1
