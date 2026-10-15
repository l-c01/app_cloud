<#
.SYNOPSIS
    Run the workspace-local Go toolchain without touching PATH.

.DESCRIPTION
    This machine has no Go on PATH, and the toolchain is deliberately kept
    inside the workspace (D:\workai\toolchain) so that installing it does not
    modify the host: no PATH entry, no registry key, no %LOCALAPPDATA% writes.
    Everything it needs lives in one directory that can simply be deleted.

    The cost of that choice is that every invocation needs the absolute path and
    an environment, which is exactly what this script provides. Use it instead
    of calling go.exe directly so that GOROOT/GOCACHE/GOPROXY stay consistent.

    GOTOOLCHAIN=local is set on purpose. go.mod says "go 1.24"; if the toolchain
    directive were left at auto, a toolchain mismatch would try to download
    another Go from the network, which is slow and can fail.

    NOTE: this file is intentionally ASCII-only.
    Windows PowerShell 5.1 reads BOM-less .ps1 files using the ANSI code page,
    so non-ASCII here would be mis-decoded and break parsing.

    Exit codes: the exit code of the Go command itself; 2 = toolchain missing.

.PARAMETER Command
    build, test, vet or fmt. Defaults to 'test'.

.PARAMETER Args
    Extra arguments passed through to the Go command. Pass them by NAME:
    `-Args ./...`. Passing them positionally after -Command binds them to
    this parameter only when -ToolchainDir is not also supplied, which makes
    the invocation fail confusingly (the toolchain path gets overwritten by
    the first package pattern).

.EXAMPLE
    & ./tools/scripts/go-local.ps1 -Command test -Args ./...

.EXAMPLE
    & ./tools/scripts/go-local.ps1 -Command vet -Args ./...
#>
[CmdletBinding()]
param(
    [ValidateSet('build', 'test', 'vet', 'fmt')]
    [string]$Command = 'test',
    [string[]]$Args = @(),
    [string]$ToolchainDir = 'D:\workai\toolchain'
)

$ErrorActionPreference = 'Stop'

$goExe = Join-Path $ToolchainDir 'go\bin\go.exe'
if (-not (Test-Path $goExe)) {
    Write-Host "Go toolchain not found at $goExe"
    Write-Host 'Install it, or pass -ToolchainDir. See design/plan/HANDOFF.md section 3.1.'
    exit 2
}

$repoRoot = $PSScriptRoot
while ($repoRoot -and -not (Test-Path (Join-Path $repoRoot '.git'))) {
    $parent = Split-Path -Parent $repoRoot
    if ([string]::IsNullOrEmpty($parent) -or $parent -eq $repoRoot) { $repoRoot = $null; break }
    $repoRoot = $parent
}
if (-not $repoRoot) { throw 'Cannot locate repository root (no .git found).' }

$moduleDir = Join-Path $repoRoot 'src/versions/0.1.0/src'
if (-not (Test-Path $moduleDir)) { throw "Go module directory not found: $moduleDir" }

# Keep every scrap of toolchain state inside the workspace directory.
$env:GOROOT = Join-Path $ToolchainDir 'go'
$env:GOTOOLCHAIN = 'local'
$env:GOCACHE = Join-Path $ToolchainDir 'gocache'
$env:GOMODCACHE = Join-Path $ToolchainDir 'gomodcache'
# The official proxy is not reachable from this network; the module has no
# external dependencies today, but keep this set for when it does.
$env:GOPROXY = 'https://mirrors.aliyun.com/goproxy/,direct'
$env:GOFLAGS = ''

Push-Location $moduleDir
try {
    if ($Command -eq 'fmt') {
        # Report unformatted files; exit 1 if there are any, mirroring CI.
        $gofmt = Join-Path $ToolchainDir 'go\bin\gofmt.exe'
        $unformatted = & $gofmt -l . 2>&1
        if ($unformatted) {
            Write-Host 'not gofmt-formatted:'
            $unformatted | ForEach-Object { Write-Host "  $_" }
            Write-Host ''
            Write-Host 'diff:'
            & $gofmt -d . 2>&1 | Select-Object -First 200 | ForEach-Object { Write-Host $_ }
            exit 1
        }
        Write-Host 'gofmt: OK (all files formatted)'
        exit 0
    }

    $goArgs = @($Command) + $Args
    & $goExe @goArgs
    exit $LASTEXITCODE
} finally {
    Pop-Location
}
