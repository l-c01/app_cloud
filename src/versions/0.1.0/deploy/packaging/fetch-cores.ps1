<#
.SYNOPSIS
    Download and verify the proxy core for one Windows target.

.DESCRIPTION
    Contract (ADR-0003, design baseline 10.2):
      - the core is NEVER compiled here; only official prebuilt release binaries;
      - every asset must be pinned by version AND sha256 in versions.lock.json;
      - an asset is verified BEFORE it is placed where anything can use it;
      - if a pin is missing, this script refuses. It must not download
        "whatever is latest", and it must not skip verification.

    The refusal matters: a package that silently contains an unverified core
    looks complete to the user while carrying none of the integrity guarantees.

    NOTE: this file is intentionally ASCII-only.
    Windows PowerShell 5.1 reads BOM-less .ps1 files using the ANSI code page,
    so non-ASCII here would be mis-decoded and break parsing.
    See design/plan/environment-constraints.md section 5.2.

.PARAMETER TargetId
    Target id from targets.json, e.g. windows-amd64.

.PARAMETER OutDir
    Directory to place the verified cores into.

.PARAMETER VersionDir
    Version directory. Defaults to the repository layout relative to this script.

.EXAMPLE
    & ./fetch-cores.ps1 -TargetId windows-amd64 -OutDir stage/windows-amd64/cores
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$TargetId,
    [Parameter(Mandatory = $true)][string]$OutDir,
    [string]$VersionDir = ''
)

$ErrorActionPreference = 'Stop'

if ([string]::IsNullOrWhiteSpace($VersionDir)) {
    # deploy/packaging -> deploy/../..
    $scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
    $VersionDir = (Resolve-Path (Join-Path $scriptDir '..\..')).Path
}

$lock = Join-Path $VersionDir 'deploy/targets/versions.lock.json'
$targets = Join-Path $VersionDir 'deploy/targets/targets.json'

foreach ($path in @($lock, $targets)) {
    if (-not (Test-Path $path)) {
        Write-Error "required file not found: $path"
        exit 1
    }
}

$lockJson = Get-Content $lock -Raw -Encoding UTF8 | ConvertFrom-Json
$targetsJson = Get-Content $targets -Raw -Encoding UTF8 | ConvertFrom-Json

# --- 1) what does this target need? -----------------------------------------
$target = $targetsJson.targets | Where-Object { $_.id -eq $TargetId } | Select-Object -First 1
if (-not $target) {
    Write-Error "target '$TargetId' is not in $targets"
    exit 1
}
if ([string]::IsNullOrWhiteSpace($target.singBoxAssetArch)) {
    Write-Error "target '$TargetId' is missing its sing-box asset architecture in $targets"
    exit 1
}

# --- 2) verify the pins BEFORE downloading anything -------------------------
# Only sing-box is shipped (ADR-0005), so this list has exactly one entry. It is
# kept as a list because the refusal below must stay a per-asset check: the day a
# second asset is added, forgetting to add it here would be a silent hole.
$unresolved = @()
foreach ($name in @('sing_box_core')) {
    $pin = $lockJson.sources.$name
    if (-not $pin -or [string]::IsNullOrWhiteSpace($pin.version) -or
        [string]::IsNullOrWhiteSpace($pin.sha256)) {
        $unresolved += $name
    }
}

if ($unresolved.Count -gt 0) {
    Write-Host "REFUSING to fetch cores: these pins are unresolved in versions.lock.json: $($unresolved -join ', ')"
    Write-Host ''
    Write-Host 'A package must not bundle an unverified core. To resolve this:'
    Write-Host '  1. pick the exact release versions deliberately;'
    Write-Host '  2. record each asset filename and its sha256 in versions.lock.json;'
    Write-Host '  3. re-run the build.'
    Write-Host 'See design/adr/ADR-0003 and design/plan/STATUS.md.'
    exit 1
}

Write-Host "core pins are resolved; fetching assets for target $TargetId"
Write-Host "  sing-box arch: $($target.singBoxAssetArch)"

# The download and extraction is intentionally not implemented as a best-effort
# path: until versions.lock.json carries real asset URLs and checksums there is
# nothing correct to download.
#
# When the pins are filled in, each asset record must provide:
#   version, sha256, and one url per asset architecture.
# The fetch must then:
#   1. download to a temporary file,
#   2. compute sha256 and compare with the pin (mismatch => abort, delete),
#   3. only then move the binary into $OutDir.
Write-Error "the download step is not implemented yet: versions.lock.json carries no asset URLs. This is expected until the core versions are pinned."
exit 1
