<#
.SYNOPSIS
    Cross-check the IPC contract between the Go service and the C++ desktop UI.

.DESCRIPTION
    The IPC protocol is implemented twice: once in Go (the service) and once in
    C++ (the UI). The two must agree exactly on:

      - the wire protocol version
      - the payload schema version
      - the operation names
      - the stable error codes

    Drift here does not fail loudly. The typical symptom is "the service said it
    succeeded but the UI shows nothing", or a handshake rejection that reads like
    a random failure. Nothing in either language's compiler can catch it, so this
    script compares the two definitions directly.

    Sources of truth:
      Go   : runtime/ipc/protocol.go
      C++  : apps/desktop/src/core/ipc_protocol.hpp
             plus the version numbers, which live in CMakeLists.txt as compile
             definitions so that both the header and the build agree.

    NOTE: this file is intentionally ASCII-only.
    Windows PowerShell 5.1 reads BOM-less .ps1 files using the ANSI code page,
    so non-ASCII here would be mis-decoded and break parsing.

    Exit codes: 0 = contracts agree, 1 = drift found, 2 = setup problem.

.PARAMETER VersionDir
    Version directory, relative to the repository root. Default: src/versions/0.1.0

.EXAMPLE
    Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
    & ./tools/scripts/check-ipc-contract.ps1
#>
[CmdletBinding()]
param(
    [string]$VersionDir = 'src/versions/0.1.0'
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

$goProtocol = Join-Path $repoRoot "$VersionDir/src/runtime/ipc/protocol.go"
$cppProtocol = Join-Path $repoRoot "$VersionDir/src/apps/desktop/src/core/ipc_protocol.hpp"
$cppProtocolImpl = Join-Path $repoRoot "$VersionDir/src/apps/desktop/src/core/ipc_protocol.cpp"
$cppCmake = Join-Path $repoRoot "$VersionDir/src/apps/desktop/CMakeLists.txt"

foreach ($path in @($goProtocol, $cppProtocol, $cppProtocolImpl, $cppCmake)) {
    if (-not (Test-Path $path)) {
        Write-Host "Required file not found: $path"
        exit 2
    }
}

$problems = 0

function Report-Drift {
    param([string]$What, [string]$Go, [string]$Cpp)
    $script:problems++
    Write-Host ("DRIFT  {0}" -f $What)
    Write-Host ("       go : {0}" -f $Go)
    Write-Host ("       c++: {0}" -f $Cpp)
}

# ---------------------------------------------------------------------------
# 1) Versions
# ---------------------------------------------------------------------------
$goLines = @(Get-Content -LiteralPath $goProtocol -Encoding UTF8)
$cppLines = @(Get-Content -LiteralPath $cppProtocol -Encoding UTF8)
$cmakeLines = @(Get-Content -LiteralPath $cppCmake -Encoding UTF8)

$goProtocolVersion = $null
$goSchemaVersion = $null
foreach ($line in $goLines) {
    if ($line -match '^const\s+ProtocolVersion\s*=\s*(\d+)') { $goProtocolVersion = $Matches[1] }
    if ($line -match '^const\s+SchemaVersion\s*=\s*"([^"]+)"') { $goSchemaVersion = $Matches[1] }
}

$cppProtocolVersion = $null
$cppSchemaVersion = $null
foreach ($line in $cmakeLines) {
    if ($line -match 'PCX_IPC_PROTOCOL_VERSION=(\d+)') { $cppProtocolVersion = $Matches[1] }
    if ($line -match 'PCX_IPC_SCHEMA_VERSION="([^"]+)"') { $cppSchemaVersion = $Matches[1] }
}

if (-not $goProtocolVersion) { Write-Host 'Could not read ProtocolVersion from the Go source.'; exit 2 }
if (-not $goSchemaVersion) { Write-Host 'Could not read SchemaVersion from the Go source.'; exit 2 }
if (-not $cppProtocolVersion) { Write-Host 'Could not read PCX_IPC_PROTOCOL_VERSION from CMakeLists.txt.'; exit 2 }
if (-not $cppSchemaVersion) { Write-Host 'Could not read PCX_IPC_SCHEMA_VERSION from CMakeLists.txt.'; exit 2 }

if ($goProtocolVersion -ne $cppProtocolVersion) {
    Report-Drift 'wire protocol version' $goProtocolVersion $cppProtocolVersion
}
if ($goSchemaVersion -ne $cppSchemaVersion) {
    Report-Drift 'payload schema version' $goSchemaVersion $cppSchemaVersion
}
Write-Host ("protocol version : go={0} c++={1}" -f $goProtocolVersion, $cppProtocolVersion)
Write-Host ("schema version   : go={0} c++={1}" -f $goSchemaVersion, $cppSchemaVersion)

# ---------------------------------------------------------------------------
# 2) Operation names
# ---------------------------------------------------------------------------
$goOperations = @()
foreach ($line in $goLines) {
    if ($line -match '^\s*Op\w+\s+Operation\s*=\s*"([^"]+)"') { $goOperations += $Matches[1] }
}

# Only the op namespace block declares operations; stop at its closing brace.
$cppOperations = @()
$inOpNamespace = $false
foreach ($line in $cppLines) {
    if ($line -match '^namespace op \{') { $inOpNamespace = $true; continue }
    if ($inOpNamespace) {
        if ($line -match '^\}') { $inOpNamespace = $false; continue }
        if ($line -match 'k\w+\s*=\s*"([^"]+)"') { $cppOperations += $Matches[1] }
    }
}

Write-Host ("operation names  : go={0} c++={1}" -f $goOperations.Count, $cppOperations.Count)

foreach ($name in $goOperations) {
    if ($cppOperations -notcontains $name) {
        Report-Drift "operation '$name' missing in C++" $name '<absent>'
    }
}
foreach ($name in $cppOperations) {
    if ($goOperations -notcontains $name) {
        Report-Drift "operation '$name' exists only in C++" '<absent>' $name
    }
}

# ---------------------------------------------------------------------------
# 3) Error codes
# ---------------------------------------------------------------------------
$goCodes = @()
foreach ($line in $goLines) {
    if ($line -match '^\s*Code\w+\s*=\s*"([^"]+)"') { $goCodes += $Matches[1] }
}

$cppCodes = @()
$inCodeNamespace = $false
foreach ($line in $cppLines) {
    if ($line -match '^namespace code \{') { $inCodeNamespace = $true; continue }
    if ($inCodeNamespace) {
        if ($line -match '^\}') { $inCodeNamespace = $false; continue }
        if ($line -match 'k\w+\s*=\s*"([^"]+)"') { $cppCodes += $Matches[1] }
    }
}

Write-Host ("error codes      : go={0} c++={1} (C++ may define extra client-local codes)" -f $goCodes.Count, $cppCodes.Count)

# Every Go code must exist in C++: the UI has to recognise everything the
# service can send. The reverse is not required: the C++ side adds codes for
# failures that only the client can produce (transport, incompatible client).
foreach ($name in $goCodes) {
    if ($cppCodes -notcontains $name) {
        Report-Drift "error code '$name' missing in C++" $name '<absent>'
    }
}
foreach ($name in $cppCodes) {
    if ($goCodes -notcontains $name) {
        Write-Host ("info: C++ defines a client-local error code not present in Go: {0}" -f $name)
    }
}

# ---------------------------------------------------------------------------
# 4) Field names used by both sides
#
# A cheap but useful check: every JSON field name that appears as a json tag in
# the Go structs should be mentioned in the C++ parser, otherwise a field is
# being silently dropped by the UI.
# ---------------------------------------------------------------------------
$goJsonTags = @()
foreach ($line in $goLines) {
    foreach ($m in [regex]::Matches($line, 'json:"([a-zA-Z][a-zA-Z0-9]*)')) {
        $goJsonTags += $m.Groups[1].Value
    }
}
$goJsonTags = @($goJsonTags | Sort-Object -Unique)

$cppText = ($cppLines -join "`n") + "`n" +
           (@(Get-Content -LiteralPath $cppProtocolImpl -Encoding UTF8) -join "`n")
$untouched = @()
foreach ($tag in $goJsonTags) {
    # The C++ parsers read fields by their wire name, so the name must appear
    # as a quoted literal somewhere in the header or the implementation.
    if ($cppText -notmatch ('"' + [regex]::Escape($tag) + '"')) {
        $untouched += $tag
    }
}
Write-Host ("wire field names : {0} in Go, {1} not mentioned in the C++ parser" -f $goJsonTags.Count, $untouched.Count)
foreach ($tag in $untouched) {
    Write-Host ("info: field '{0}' is not parsed by the C++ side" -f $tag)
}

# ---------------------------------------------------------------------------
Write-Host ''
if ($problems -eq 0) {
    Write-Host 'RESULT: PASS - the Go and C++ IPC definitions agree.'
    exit 0
}
Write-Host ("RESULT: FAIL - {0} contract difference(s) found." -f $problems)
exit 1
