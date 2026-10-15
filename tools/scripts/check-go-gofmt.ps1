<#
.SYNOPSIS
    Check Go formatting with the workspace-local toolchain, if it is installed.

.DESCRIPTION
    This check is OPTIONAL by design, and reports SKIP (not PASS) when the Go
    toolchain is absent. That distinction matters: a check that silently passes
    when it did not run is worse than no check, because it makes an unverified
    repository look verified.

    When Go IS present this performs the same test CI performs (gofmt -l), so a
    formatting problem is caught here instead of costing a CI round trip. This
    was a real cost: a batch once needed three CI runs to shake out a syntax
    error, a wrong test policy and a formatting complaint.

    NOTE: this file is intentionally ASCII-only.
    Windows PowerShell 5.1 reads BOM-less .ps1 files using the ANSI code page,
    so non-ASCII here would be mis-decoded and break parsing.

    Exit codes: 0 = formatted or toolchain absent, 1 = not formatted.

.PARAMETER ToolchainDir
    Where the Go toolchain lives. Defaults to D:\workai\toolchain.
#>
[CmdletBinding()]
param(
    [string]$ToolchainDir = 'D:\workai\toolchain'
)

$ErrorActionPreference = 'Continue'

$gofmt = Join-Path $ToolchainDir 'go\bin\gofmt.exe'
if (-not (Test-Path $gofmt)) {
    Write-Host "SKIP: no local Go toolchain at $ToolchainDir"
    Write-Host '      run this check in CI instead (job: Go formatting)'
    exit 0
}

$repoRoot = $PSScriptRoot
while ($repoRoot -and -not (Test-Path (Join-Path $repoRoot '.git'))) {
    $parent = Split-Path -Parent $repoRoot
    if ([string]::IsNullOrEmpty($parent) -or $parent -eq $repoRoot) { $repoRoot = $null; break }
    $repoRoot = $parent
}
if (-not $repoRoot) { Write-Host 'cannot locate repository root'; exit 2 }

$moduleDir = Join-Path $repoRoot 'src/versions/0.1.0/src'
if (-not (Test-Path $moduleDir)) { Write-Host "module dir not found: $moduleDir"; exit 2 }

Push-Location $moduleDir
try {
    $unformatted = & $gofmt -l . 2>&1
    if ($unformatted) {
        Write-Host 'the following files are not gofmt-formatted:'
        $unformatted | ForEach-Object { Write-Host "  $_" }
        Write-Host ''
        Write-Host '----- diff (first 200 lines) -----'
        & $gofmt -d . 2>&1 | Select-Object -First 200 | ForEach-Object { Write-Host $_ }
        Write-Host ''
        Write-Host 'fix: & ./tools/scripts/go-local.ps1 -Command fmt  (or run gofmt -w)'
        exit 1
    }
} finally {
    Pop-Location
}

Write-Host 'gofmt: OK (all files formatted)'
exit 0
