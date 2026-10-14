<#
.SYNOPSIS
    Run every check that can be performed without a compiler, and write a log.

.DESCRIPTION
    This machine has no Go, C++, JDK or Android SDK toolchain, so compilation and
    unit tests happen in CI. Everything else can and should be run here, and this
    script is the single entry point for that.

    It writes the complete raw output to a log file so the result can be cited as
    evidence rather than summarised from memory.

    The script deliberately does NOT stop at the first failure: a run should show
    every problem at once, not one problem per invocation.

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
    @{ Label = 'Go unused imports';            Script = 'check-go-imports.ps1' }
    @{ Label = 'Go cross-package symbols';     Script = 'check-go-symbols.ps1' }
    @{ Label = 'C++ structural consistency';   Script = 'check-cpp-structure.ps1' }
    @{ Label = 'Workflow structure';           Script = 'check-workflows.ps1' }
    @{ Label = 'Go <-> C++ IPC contract';      Script = 'check-ipc-contract.ps1' }
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

    if ($exitCode -ne 0) { $failed += $check.Label }
    $results += [pscustomobject]@{ Label = $check.Label; Exit = $exitCode; Note = '' }

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
    $log.Add(("  {0,-32} {1}" -f $r.Label, $(if ($r.Exit -eq 0) { 'PASS' } else { "FAIL ($($r.Exit))" })))
}
$log.Add('')
$log.Add("  passed: $($results.Count - $failed.Count) / $($results.Count)")
$log.Add('')
$log.Add('  NOTE: none of this compiles anything. The C++ and Go sources have')
$log.Add('        never been through a compiler on this machine. Compilation and')
$log.Add('        unit tests run in GitHub Actions; their status is not_run locally.')

$log | Set-Content -Path $LogPath -Encoding UTF8

Write-Host ''
Write-Host '================================================================'
Write-Host 'SUMMARY'
Write-Host '================================================================'
foreach ($r in $results) {
    $status = if ($r.Exit -eq 0) { 'PASS' } else { "FAIL ($($r.Exit))" }
    $colour = if ($r.Exit -eq 0) { 'Green' } else { 'Red' }
    Write-Host ("  {0,-32} {1}" -f $r.Label, $status) -ForegroundColor $colour
}
Write-Host ''
Write-Host ("  passed: {0} / {1}" -f ($results.Count - $failed.Count), $results.Count)
Write-Host ("  log   : {0}" -f (Split-Path -Leaf $LogPath))
Write-Host ''
Write-Host '  NOTE: none of this compiles anything. Compilation and unit tests run'
Write-Host '        in GitHub Actions and are not_run locally.' -ForegroundColor Yellow

if ($failed.Count -eq 0) { exit 0 }
exit 1
