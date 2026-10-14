<#
.SYNOPSIS
    Cross-check message keys used in Go source against the locale catalogs.

.DESCRIPTION
    The i18n contract is: every user-visible message is identified by a stable key
    such as "compiler.protocol_unsupported", and every key must exist in every
    locale catalog. Nothing else enforces that in a code base without a local Go
    toolchain, so this script performs the check from the source side.

    How keys are detected (deliberately narrow, to avoid false positives):
      - lines containing "MessageKey:"                     -> the quoted key
      - lines calling .Errorf( / .Warnf( / .Infof(         -> the quoted key argument

    Key namespace convention (this is what makes detection reliable):
      message keys are named  <package>.<topic>
      field paths are named   <field>...   (e.g. tls.reality, transport.type)

    Because a report call carries both a field path and a message key, and the
    field path also looks like a dotted lower-case identifier, the checker only
    accepts keys whose first segment is one of the known package namespaces
    listed in $messageNamespaces below. Add a namespace here when a new package
    starts emitting user-visible messages.

    Then it verifies, for every detected key:
      1. the key exists in every locale catalog under locales/;
      2. placeholder counts match across catalogs (a mismatch means one language
         will render a literal {1} or drop a value).

    NOTE: this file is intentionally ASCII-only (see design/plan/environment-constraints.md).

    Exit codes: 0 = consistent, 1 = problems found, 2 = setup problem.

.PARAMETER SourceRoot
    Go source root, relative to the repository root. Default: src/versions/0.1.0/src

.PARAMETER LocaleRoot
    Locale directory, relative to the repository root. Default: locales

.EXAMPLE
    Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
    & ./tools/scripts/check-i18n-keys.ps1
#>
[CmdletBinding()]
param(
    [string]$SourceRoot = 'src/versions/0.1.0/src',
    [string]$LocaleRoot = 'locales'
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
$locPath = Join-Path $root $LocaleRoot
if (-not (Test-Path $srcPath)) { Write-Host "Source root not found: $srcPath" -ForegroundColor Red; exit 2 }
if (-not (Test-Path $locPath)) { Write-Host "Locale root not found: $locPath" -ForegroundColor Red; exit 2 }

# ---- load catalogs ----
$catalogs = @{}
Get-ChildItem -Path $locPath -Filter '*.json' -File | ForEach-Object {
    try {
        $json = Get-Content -LiteralPath $_.FullName -Raw -Encoding UTF8 | ConvertFrom-Json
        $lang = $json.language
        if (-not $lang) { $lang = $_.BaseName }
        $map = @{}
        foreach ($p in $json.messages.PSObject.Properties) { $map[$p.Name] = [string]$p.Value }
        $catalogs[$lang] = $map
    }
    catch {
        Write-Host ("Failed to parse catalog {0}: {1}" -f $_.Name, $_.Exception.Message) -ForegroundColor Red
        exit 2
    }
}
if ($catalogs.Count -eq 0) { Write-Host "No locale catalogs found under $locPath" -ForegroundColor Red; exit 2 }

# ---- collect message keys from Go source ----

# Known message-key namespaces. A dotted identifier whose first segment is not in
# this list is treated as a field path (or something else) and ignored.
$messageNamespaces = @(
    'compiler', 'ipc', 'netmgr', 'panel', 'subscription',
    'core', 'coordinator', 'secrets', 'coreupdater', 'ui'
)

$keyPattern = '"([a-z][a-z0-9_]*\.[a-z0-9_.]+)"'
$used = @{}
$files = Get-ChildItem -Path $srcPath -Recurse -File -Filter '*.go' |
    Where-Object { $_.Name -notlike '*_test.go' }

foreach ($file in $files) {
    $lineNo = 0
    foreach ($line in (Get-Content -LiteralPath $file.FullName -Encoding UTF8)) {
        $lineNo++
        $interesting = $false
        if ($line -match 'MessageKey:') { $interesting = $true }
        elseif ($line -match '\.(Errorf|Warnf|Infof)\(') { $interesting = $true }
        if (-not $interesting) { continue }

        foreach ($m in [regex]::Matches($line, $keyPattern)) {
            $key = $m.Groups[1].Value
            $namespace = $key.Split('.')[0]
            if ($messageNamespaces -notcontains $namespace) { continue }
            if (-not $used.ContainsKey($key)) {
                $used[$key] = @()
            }
            $used[$key] += ("{0}:{1}" -f $file.Name, $lineNo)
        }
    }
}

Write-Host ''
Write-Host ("Catalogs : {0}" -f (($catalogs.Keys | Sort-Object) -join ', '))
Write-Host ("Go files : {0}" -f $files.Count)
Write-Host ("Keys used: {0}" -f $used.Count)
Write-Host ''

$problems = 0

# ---- check 1: every used key exists in every catalog ----
foreach ($key in ($used.Keys | Sort-Object)) {
    $missingIn = @()
    foreach ($lang in $catalogs.Keys) {
        if (-not $catalogs[$lang].ContainsKey($key)) { $missingIn += $lang }
    }
    if ($missingIn.Count -gt 0) {
        $problems++
        Write-Host ("MISSING  {0}" -f $key) -ForegroundColor Red
        Write-Host ("         absent from: {0}" -f ($missingIn -join ', ')) -ForegroundColor DarkRed
        Write-Host ("         used at: {0}" -f (($used[$key] | Select-Object -First 3) -join ', ')) -ForegroundColor DarkGray
    }
}

# ---- check 2: placeholder counts agree across catalogs ----
$placeholder = [regex]'\{\d+\}'
foreach ($key in ($catalogs[$catalogs.Keys[0]].Keys | Sort-Object)) {
    $counts = @{}
    foreach ($lang in $catalogs.Keys) {
        if (-not $catalogs[$lang].ContainsKey($key)) { continue }
        $counts[$lang] = ([regex]::Matches($catalogs[$lang][$key], $placeholder)).Count
    }
    if ($counts.Count -lt 2) { continue }
    $distinct = @($counts.Values | Sort-Object -Unique)
    if ($distinct.Count -gt 1) {
        $problems++
        Write-Host ("PLACEHOLDER MISMATCH  {0}" -f $key) -ForegroundColor Red
        foreach ($lang in ($counts.Keys | Sort-Object)) {
            Write-Host ("         {0}: {1}" -f $lang, $counts[$lang]) -ForegroundColor DarkRed
        }
    }
}

# ---- check 3: report unused keys as information only ----
$unused = @()
$allLocaleKeys = @{}
foreach ($lang in $catalogs.Keys) {
    foreach ($k in $catalogs[$lang].Keys) { $allLocaleKeys[$k] = $true }
}
foreach ($k in $allLocaleKeys.Keys) {
    if (-not $used.ContainsKey($k)) { $unused += $k }
}
if ($unused.Count -gt 0) {
    Write-Host ''
    Write-Host ("Note: {0} catalog key(s) are not referenced from Go source yet (informational):" -f $unused.Count) -ForegroundColor Yellow
    foreach ($k in ($unused | Sort-Object)) { Write-Host ("  {0}" -f $k) -ForegroundColor DarkGray }
}

Write-Host ''
if ($problems -eq 0) {
    Write-Host ("RESULT: PASS - {0} referenced key(s) are present in all catalogs with matching placeholders." -f $used.Count) -ForegroundColor Green
    exit 0
}
Write-Host ("RESULT: FAIL - {0} problem(s) found." -f $problems) -ForegroundColor Red
exit 1
