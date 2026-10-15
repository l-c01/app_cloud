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
    [string]$VersionDir = '',
    # Optional download mirror prefix, e.g. "https://ghproxy.net".
    # Explicit opt-in: the release CDN is unreachable from some networks, but
    # switching hosts silently is not something a packaging step should decide.
    # The sha256 check still applies, so a mirror can serve a bad file but not
    # get it installed.
    [string]$Mirror = ''
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

# --- 3) pick the asset for THIS target ---------------------------------------
#
# The pin must be looked up **per architecture**. A single global hash checked
# against a download for a different architecture would pass while the wrong
# binary is being installed - the archive for another arch has a different hash,
# so the check would either fail confusingly or, worse, be loosened to "any
# pinned hash" and then prove nothing at all.
#
# There is no fallback to "latest": that would defeat the entire point of a pin.
$assets = $lockJson.sources.sing_box_core.assets
$pin = $null
if ($assets) { $pin = $assets.$TargetId }
if (-not $pin) {
    Write-Error ("target '$TargetId' has no pinned asset in versions.lock.json " +
        "(expected under sources.sing_box_core.assets.$TargetId). " +
        'Add the pin rather than downloading an unpinned binary.')
    exit 1
}
foreach ($field in @('asset', 'sha256', 'url')) {
    if ([string]::IsNullOrWhiteSpace($pin.$field)) {
        Write-Error "the pin for '$TargetId' is missing '$field' in versions.lock.json"
        exit 1
    }
}

$expected = $pin.sha256.ToLowerInvariant()

# The mirror is an explicit opt-in, never automatic. A checksum makes a mirror
# safe to USE, but it does not make silently fetching from a third party the
# right default.
$assetUrl = $pin.url
if (-not [string]::IsNullOrWhiteSpace($Mirror)) {
    $assetUrl = $Mirror.TrimEnd('/') + '/' + $pin.url
    Write-Host "  mirror    : $Mirror"
}
Write-Host "  asset     : $($pin.asset)"
Write-Host "  expected  : $expected"

$tmpRoot = Join-Path ([System.IO.Path]::GetTempPath()) ('pcx-cores-' + [guid]::NewGuid().ToString('n'))
New-Item -ItemType Directory -Force -Path $tmpRoot | Out-Null

try {
    $archive = Join-Path $tmpRoot $pin.asset

    # --- download to a temporary file, never straight into place ---
    Write-Host "  downloading..."
    $progressBackup = $ProgressPreference
    $ProgressPreference = 'SilentlyContinue'   # per-chunk progress makes this very slow
    try {
        Invoke-WebRequest -Uri $assetUrl -OutFile $archive -UseBasicParsing
    } finally {
        $ProgressPreference = $progressBackup
    }

    if (-not (Test-Path $archive)) {
        Write-Error "download produced no file: $assetUrl"
        exit 1
    }

    # --- verify BEFORE anything can use it ---
    $actual = (Get-FileHash -Path $archive -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actual -ne $expected) {
        # Delete the bad download: leaving it around invites someone to "just use
        # it" later, and the whole point of verification is that failure is final.
        Remove-Item $archive -Force
        Write-Error ("sha256 mismatch for '$($pin.asset)': expected $expected, got $actual. " +
            'The download was deleted. Do not continue with this asset.')
        exit 1
    }
    Write-Host "  sha256 ok"

    # --- extract, then place only the verified payload ---
    $extract = Join-Path $tmpRoot 'extract'
    New-Item -ItemType Directory -Force -Path $extract | Out-Null

    if ($pin.asset.EndsWith('.zip')) {
        Expand-Archive -Path $archive -DestinationPath $extract -Force
    } elseif ($pin.asset.EndsWith('.tar.gz') -or $pin.asset.EndsWith('.tgz')) {
        # tar is present on Windows 10+ and everywhere on Linux.
        & tar -xzf $archive -C $extract
        if ($LASTEXITCODE -ne 0) { Write-Error "tar failed to extract $($pin.asset)"; exit 1 }
    } else {
        Write-Error "unsupported archive type: $($pin.asset)"
        exit 1
    }

    $binary = Get-ChildItem -Path $extract -Recurse -File |
        Where-Object { $_.Name -in @('sing-box.exe', 'sing-box') } | Select-Object -First 1
    if (-not $binary) {
        Write-Error "no sing-box binary found inside $($pin.asset)"
        exit 1
    }

    New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
    Copy-Item $binary.FullName -Destination (Join-Path $OutDir $binary.Name) -Force

    # Windows builds need libcronet.dll next to the executable. Copying the whole
    # verified payload directory is simpler and less error-prone than maintaining
    # a list of which side files each release happens to ship.
    Get-ChildItem -Path $binary.Directory -File |
        Where-Object { $_.FullName -ne $binary.FullName } |
        ForEach-Object { Copy-Item $_.FullName -Destination (Join-Path $OutDir $_.Name) -Force }

    Write-Host "  placed    : $($binary.Name) -> $OutDir"
} finally {
    Remove-Item $tmpRoot -Recurse -Force -ErrorAction SilentlyContinue
}

exit 0
