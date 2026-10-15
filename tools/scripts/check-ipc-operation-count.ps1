<#
.SYNOPSIS
    Check that the Go and C++ operation counts agree.

.DESCRIPTION
    WHY THIS EXISTS -- a real failure this would have caught.

    runtime/ipc/protocol.go lists the operations twice: once as the operation
    name constants, and once inside KnownOperations(). Two tests pin the count:

      - Go:      runtime/ipc/ipc_test.go   ("expected 18 documented operations")
      - C++:     apps/desktop/tests/test_ipc_protocol.cpp
                 (PCX_CHECK_EQ(operations.size(), std::size_t(18)))

    Adding the eight session operations updated the constants, KnownOperations()
    and the C++ mirror, but NOT either test's expected number. Everything
    checked locally passed -- including check-ipc-contract.ps1, which compares
    the two source files' operation sets and was perfectly happy -- and CI then
    failed on the C++ unit test.

    check-ipc-contract.ps1 could not catch it because the stale number lives in
    a TEST file, and that script only reads the protocol source. This check
    reads the tests too, which is the whole point of having it.

    It compares three numbers and reports any disagreement:
      1. the count known to Go  (KnownOperations())
      2. the count C++ declares  (PCX_CHECK_EQ(operations.size(), ...))
      3. the count Go's test pins (len(KnownOperations()) == N)

    NOTE: this file is intentionally ASCII-only.
    Windows PowerShell 5.1 reads BOM-less .ps1 files using the ANSI code page,
    so non-ASCII here would be mis-decoded and break parsing.

    Exit codes: 0 = all agree, 1 = disagreement, 2 = a file or pattern is missing.

.PARAMETER RepoRoot
    Repository root. Defaults to the one discovered from this script's location.
#>
[CmdletBinding()]
param(
    [string]$RepoRoot = ''
)

$ErrorActionPreference = 'Continue'

if ([string]::IsNullOrEmpty($RepoRoot)) {
    $RepoRoot = $PSScriptRoot
    while ($RepoRoot -and -not (Test-Path (Join-Path $RepoRoot '.git'))) {
        $parent = Split-Path -Parent $RepoRoot
        if ([string]::IsNullOrEmpty($parent) -or $parent -eq $RepoRoot) { $RepoRoot = $null; break }
        $RepoRoot = $parent
    }
}
if (-not $RepoRoot) { Write-Host 'cannot locate repository root'; exit 2 }

$srcRoot = Join-Path $RepoRoot 'src/versions/0.1.0/src'
$protocol = Join-Path $srcRoot 'runtime/ipc/protocol.go'
$goTest = Join-Path $srcRoot 'runtime/ipc/ipc_test.go'
$cppTest = Join-Path $srcRoot 'apps/desktop/tests/test_ipc_protocol.cpp'

foreach ($f in @($protocol, $goTest, $cppTest)) {
    if (-not (Test-Path $f)) { Write-Host "missing expected file: $f"; exit 2 }
}

function Read-Text([string]$path) {
    return [System.IO.File]::ReadAllText($path)
}

# ---------------------------------------------------------------------------
# 1) Go: count the entries inside KnownOperations().
# ---------------------------------------------------------------------------
$protoText = Read-Text $protocol
$fnMatch = [regex]::Match($protoText, 'func\s+KnownOperations\(\)\s*\[\]Operation\s*\{(?<body>[\s\S]*?)\n\}')
if (-not $fnMatch.Success) {
    Write-Host 'cannot find KnownOperations() in protocol.go; the parser needs updating'
    exit 2
}
# Entries are bare identifiers separated by commas (and possibly newlines).
$goOps = [regex]::Matches($fnMatch.Groups['body'].Value, '\bOp[A-Z][A-Za-z0-9]*\b') |
    ForEach-Object { $_.Value } | Sort-Object -Unique

# ---------------------------------------------------------------------------
# 2) Go test: the number it pins.
# ---------------------------------------------------------------------------
$goTestText = Read-Text $goTest
$goPinnedMatch = [regex]::Match($goTestText, 'len\(KnownOperations\(\)\)\s*!=\s*(?<n>\d+)')
if (-not $goPinnedMatch.Success) {
    Write-Host 'cannot find the pinned operation count in ipc_test.go'
    exit 2
}
$goPinned = [int]$goPinnedMatch.Groups['n'].Value

# ---------------------------------------------------------------------------
# 3) C++ test: the number it pins.
# ---------------------------------------------------------------------------
$cppText = Read-Text $cppTest
$cppPinnedMatch = [regex]::Match($cppText, 'operations\.size\(\)\s*,\s*std::size_t\((?<n>\d+)\)')
if (-not $cppPinnedMatch.Success) {
    Write-Host 'cannot find the pinned operation count in test_ipc_protocol.cpp'
    exit 2
}
$cppPinned = [int]$cppPinnedMatch.Groups['n'].Value

# ---------------------------------------------------------------------------
# Compare.
# ---------------------------------------------------------------------------
$goCount = $goOps.Count
Write-Host "KnownOperations() entries : $goCount"
Write-Host "Go test pins              : $goPinned"
Write-Host "C++ test pins             : $cppPinned"
Write-Host ''

$problems = 0
if ($goCount -ne $goPinned) {
    Write-Host "MISMATCH  Go test pins $goPinned but KnownOperations() has $goCount entries"
    Write-Host '          fix: update the number in runtime/ipc/ipc_test.go'
    $problems++
}
if ($goCount -ne $cppPinned) {
    Write-Host "MISMATCH  C++ test pins $cppPinned but KnownOperations() has $goCount entries"
    Write-Host '          fix: update the number in apps/desktop/tests/test_ipc_protocol.cpp'
    $problems++
}

if ($problems -gt 0) {
    Write-Host ''
    Write-Host "RESULT: FAIL - $problems operation-count mismatch(es)"
    Write-Host 'These numbers are intentionally asserted so that adding an operation'
    Write-Host 'forces the author back to both tests. Update them together.'
    exit 1
}

Write-Host 'RESULT: PASS - Go, Go test and C++ test agree on the operation count.'
exit 0
