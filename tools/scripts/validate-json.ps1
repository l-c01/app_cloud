<#
.SYNOPSIS
    Check that every JSON file under a directory parses as valid JSON.

.DESCRIPTION
    This machine has no extra runtimes (no Node / Python / Go), so repository
    checks are implemented with PowerShell and .NET only.

    The repository root is located by walking up from this script's own directory
    until a .git directory is found, so the script does not depend on the caller's
    working directory.

    NOTE: this file is intentionally ASCII-only. Windows PowerShell reads .ps1 files
    without a BOM using the system ANSI code page, which corrupts non-ASCII text.
    Keep all repository scripts ASCII-only; put localized prose in Markdown instead.

    Exit codes: 0 = all files valid, 1 = at least one invalid file or bad path.

.PARAMETER Path
    Directory to scan, relative to the repository root or absolute.
    Default: design

.EXAMPLE
    Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
    & ./tools/scripts/validate-json.ps1

.EXAMPLE
    & ./tools/scripts/validate-json.ps1 -Path design/contracts
#>
[CmdletBinding()]
param(
    [string]$Path = 'design'
)

$ErrorActionPreference = 'Stop'

$root = $PSScriptRoot
while ($root -and -not (Test-Path (Join-Path $root '.git'))) {
    $parent = Split-Path -Parent $root
    if ([string]::IsNullOrEmpty($parent) -or $parent -eq $root) { $root = $null; break }
    $root = $parent
}
if (-not $root) { throw 'Cannot locate repository root (no .git found).' }

$target = if ([System.IO.Path]::IsPathRooted($Path)) { $Path } else { Join-Path $root $Path }
if (-not (Test-Path $target)) {
    Write-Host ("Path not found: {0}" -f $target) -ForegroundColor Red
    exit 1
}

$files = @(Get-ChildItem -Path $target -Filter '*.json' -Recurse -File -ErrorAction SilentlyContinue)
if ($files.Count -eq 0) {
    Write-Host ("No JSON files found under: {0}" -f $target)
    exit 0
}

$failed = New-Object System.Collections.Generic.List[string]
foreach ($file in $files) {
    $rel = $file.FullName.Substring($root.Length).TrimStart('\', '/')
    try {
        $null = Get-Content -LiteralPath $file.FullName -Raw -Encoding UTF8 | ConvertFrom-Json
        Write-Host ("  OK    {0}" -f $rel)
    }
    catch {
        Write-Host ("  FAIL  {0}" -f $rel) -ForegroundColor Red
        Write-Host ("        {0}" -f $_.Exception.Message) -ForegroundColor DarkRed
        $failed.Add($rel)
    }
}

Write-Host ''
Write-Host ("Checked {0} file(s); {1} failed." -f $files.Count, $failed.Count)
if ($failed.Count -gt 0) {
    Write-Host 'Failed files:'
    foreach ($f in $failed) { Write-Host ("  - {0}" -f $f) }
    exit 1
}
exit 0
