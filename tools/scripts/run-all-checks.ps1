<#
.SYNOPSIS
    Run every check that can be performed without a compiler, and write a log.

.DESCRIPTION
    This machine has no C++ or Android toolchain, so those compile in CI. Go is
    installed inside the workspace (D:\workai\toolchain) and is covered by an
    optional formatting check here, plus the full build/vet/test entry point in
    tools/scripts/go-local.ps1. Everything that needs no compiler is run here,
    and this script is the single entry point for that.

    It writes the complete raw output to a log file so the result can be cited as
    evidence rather than summarised from memory.

    The script deliberately does NOT stop at the first failure: a run should show
    every problem at once, not one problem per invocation.

    A check that cannot run reports SKIP, which is shown separately from PASS.
    A check that silently passes when it never ran is worse than no check.

    NOTE: this file is intentionally ASCII-only.
    Windows PowerShell 5.1 reads BOM-less .ps1 files using the ANSI code page,
    so non-ASCII here would be mis-decoded and break parsing.

    Exit codes: 0 = everything passed, 1 = at least one check failed, 2 = setup problem.

.PARAMETER LogPath
    Where to write the full log. Defaults to output/reports/verification-run-<date>.txt

.PARAMETER Quiet
    Print only the summary, not each check's output.

.EXAMPLE
    Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
    & ./tools/scripts/run-all-checks.ps1
#>
[CmdletBinding()]
param(
    [string]$LogPath = '',
    [switch]$Quiet
)

$ErrorActionPreference = 'Continue'

$repoRoot = $PSScriptRoot
while ($repoRoot -and -not (Test-Path (Join-Path $repoRoot '.git'))) {
    $parent = Split-Path -Parent $repoRoot
    if ([string]::IsNullOrEmpty($parent) -or $parent -eq $repoRoot) { $repoRoot = $null; break }
    $repoRoot = $parent
}
if (-not $repoRoot) { throw 'Cannot locate repository root (no .git found).' }

$scripts = Join-Path $repoRoot 'tools/scripts'
if (-not (Test-Path $scripts)) { Write-Host "scripts directory not found: $scripts"; exit 2 }

if ([string]::IsNullOrEmpty($LogPath)) {
    $stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
    $LogPath = Join-Path $repoRoot "output/reports/verification-run-$stamp.txt"
}
$logDir = Split-Path -Parent $LogPath
if (-not [string]::IsNullOrEmpty($logDir)) {
    New-Item -ItemType Directory -Force -Path $logDir | Out-Null
}

# Each entry: label, script file, and named parameters.
#
# Parameters are a hashtable, not an array. An array splat passes POSITIONAL
# arguments only, so @('-Path','design') does not bind to -Path; the script
# then fails to bind and, if the exit code is not inspected carefully, the run
# still looks green. A hashtable splat passes named parameters unambiguously.
$checks = @(
    @{ Label = 'Tracked source files';         Script = 'check-tracked-files.ps1' }
    @{ Label = 'Go unused imports';            Script = 'check-go-imports.ps1' }
    @{ Label = 'Go cross-package symbols';     Script = 'check-go-symbols.ps1' }
    @{ Label = 'Go formatting (if Go present)'; Script = 'check-go-gofmt.ps1' }
    @{ Label = 'C++ structural consistency';   Script = 'check-cpp-structure.ps1' }
    @{ Label = 'Workflow structure';           Script = 'check-workflows.ps1' }
    @{ Label = 'Go <-> C++ IPC contract';      Script = 'check-ipc-contract.ps1' }
    @{ Label = 'IPC operation count agrees';   Script = 'check-ipc-operation-count.ps1' }
    @{ Label = 'i18n message keys';            Script = 'check-i18n-keys.ps1' }
    @{ Label = 'Documentation links';          Script = 'check-doc-links.ps1' }
    @{ Label = 'Design JSON syntax';           Script = 'validate-json.ps1'; Params = @{ Path = 'design' } }
    @{ Label = 'Config vs schema';             Script = 'validate-client-config.ps1' }
)

$log = New-Object System.Collections.Generic.List[string]
$log.Add('Verification run')
$log.Add("  repository : $repoRoot")
$log.Add("  started    : $(Get-Date -Format 'yyyy-MM-dd HH:mm:ss zzz')")
$log.Add("  powershell : $($PSVersionTable.PSVersion) ($($PSVersionTable.PSEdition))")
$log.Add('')

$failed = @()
$results = @()

foreach ($check in $checks) {
    $path = Join-Path $scripts $check.Script
    if (-not (Test-Path $path)) {
        $failed += $check.Label
        $results += [pscustomobject]@{ Label = $check.Label; Exit = 'missing'; Note = $check.Script }
        $log.Add("MISSING SCRIPT: $($check.Script)")
        continue
    }

    $log.Add('================================================================')
    $log.Add("$($check.Label)")
    $log.Add("  script: tools/scripts/$($check.Script)")
    $log.Add('================================================================')

    # Invoke the script and capture everything it prints.
    #
    # A hashtable splat is used so named parameters bind unambiguously. Getting
    # this wrong is dangerous rather than merely inconvenient: the script fails
    # to bind, and without careful exit-code handling the run still looks green.
    # A false PASS is worse than no check at all, so binding failures are also
    # detected from the output text below.
    $scriptParams = @{}
    if ($check.ContainsKey('Params')) { $scriptParams = $check.Params }

    # Capture EVERY stream, not just the error stream.
    #
    # The check scripts report through Write-Host, which in PowerShell 5+ goes
    # to the information stream (6). With `2>&1` those lines are not captured at
    # all: the log ends up containing exit codes and nothing else, which makes it
    # useless as evidence while still looking like a complete record.
    $output = & $path @scriptParams *>&1
    $succeeded = $?
    $exitCode = $LASTEXITCODE
    # Native exit code if present, otherwise the PowerShell success flag.
    if ($null -eq $exitCode) { $exitCode = if ($succeeded) { 0 } else { 1 } }

    # A parameter binding failure does not change $LASTEXITCODE, so check the
    # text as well. Without this the check reports PASS while doing nothing.
    $bindingFailure = $false
    foreach ($line in $output) {
        $text = $line.ToString()
        if ($text -match 'ParameterBindingException|A positional parameter cannot be found|Cannot process argument transformation') {
            $bindingFailure = $true
            break
        }
    }
    if ($bindingFailure) {
        $exitCode = 2
        $log.Add('!! the check script could not be invoked correctly (argument binding failed)')
        $log.Add('!! this is a harness bug, not a check result: the check did NOT run')
    }
    if (-not $succeeded -and $exitCode -eq 0) { $exitCode = 1 }

    foreach ($line in $output) { $log.Add($line.ToString()) }
    $log.Add("exit code: $exitCode")
    $log.Add('')

    # A check that reports SKIP did NOT verify anything. That is different from
    # PASS, and the distinction is preserved rather than flattened: a check that
    # silently passes when it never ran makes an unverified repo look verified,
    # which is the one failure mode this harness must never have.
    $skipped = $false
    foreach ($line in $output) {
        if ($line.ToString() -match '^SKIP:') { $skipped = $true; break }
    }

    if ($exitCode -ne 0) { $failed += $check.Label }
    $results += [pscustomobject]@{
        Label   = $check.Label
        Exit    = $exitCode
        Note    = ''
        Skipped = $skipped
    }

    if (-not $Quiet) {
        Write-Host ("--- {0}" -f $check.Label)
        foreach ($line in $output) { Write-Host $line }
    }
}

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
$log.Add('================================================================')
$log.Add('SUMMARY')
$log.Add('================================================================')
foreach ($r in $results) {
    $status = if ($r.Exit -ne 0) { "FAIL ($($r.Exit))" } elseif ($r.Skipped) { 'SKIP' } else { 'PASS' }
    $log.Add(("  {0,-32} {1}" -f $r.Label, $status))
}
$skippedCount = @($results | Where-Object { $_.Skipped }).Count
$log.Add('')
$log.Add("  passed: $($results.Count - $failed.Count) / $($results.Count)")
if ($skippedCount -gt 0) {
    $log.Add("  NOTE: $skippedCount check(s) reported SKIP and verified nothing.")
}
$log.Add('')
$log.Add('  These checks do not compile C++ or Kotlin. Go compilation and unit')
$log.Add('  tests run locally when the workspace toolchain is installed')
$log.Add('  (tools/scripts/go-local.ps1); otherwise they are not_run.')

$log | Set-Content -Path $LogPath -Encoding UTF8

Write-Host ''
Write-Host '================================================================'
Write-Host 'SUMMARY'
Write-Host '================================================================'
foreach ($r in $results) {
    if ($r.Exit -ne 0) { $status = "FAIL ($($r.Exit))"; $colour = 'Red' }
    elseif ($r.Skipped) { $status = 'SKIP'; $colour = 'Yellow' }
    else { $status = 'PASS'; $colour = 'Green' }
    Write-Host ("  {0,-32} {1}" -f $r.Label, $status) -ForegroundColor $colour
}
Write-Host ''
Write-Host ("  passed: {0} / {1}" -f ($results.Count - $failed.Count), $results.Count)
if ($skippedCount -gt 0) {
    Write-Host ("  skipped: {0} (verified nothing)" -f $skippedCount) -ForegroundColor Yellow
}
Write-Host ("  log   : {0}" -f (Split-Path -Leaf $LogPath))
Write-Host ''
Write-Host '  These checks do not compile C++. Kotlin is built in CI only.' -ForegroundColor Yellow
Write-Host '  Go build/vet/test: & ./tools/scripts/go-local.ps1 -Command test ./...' -ForegroundColor Yellow

if ($failed.Count -eq 0) { exit 0 }
exit 1
