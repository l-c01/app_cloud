<#
.SYNOPSIS
    Validate design/client-config.json against design/schemas/client-config.schema.json.

.DESCRIPTION
    This machine has no JSON Schema library (no Node / Python / Go), so this script
    implements only the subset of JSON Schema keywords that this repository's
    contracts actually use:

      type / const / enum / required / properties / additionalProperties(bool) /
      items / minLength / maxLength / minimum / maximum / pattern / $ref (local)

    NOT supported (ignored, and reported): if / then / else / anyOf / oneOf / not /
    dependentSchemas / patternProperties / unevaluatedProperties / format assertions.

    The repository root is located by walking up from this script's own directory
    until a .git directory is found, so the script does not depend on the caller's
    working directory.

    NOTE: this file is intentionally ASCII-only. Windows PowerShell reads .ps1 files
    without a BOM using the system ANSI code page, which corrupts non-ASCII text.
    Keep all repository scripts ASCII-only; put localized prose in Markdown instead.

.PARAMETER SchemaPath
    Schema file, relative to the repository root or absolute.
    Default: design/schemas/client-config.schema.json

.PARAMETER DocumentPath
    Document to validate, relative to the repository root or absolute.
    Default: design/client-config.json

.EXAMPLE
    Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
    & ./tools/scripts/validate-client-config.ps1

.EXAMPLE
    & ./tools/scripts/validate-client-config.ps1 -DocumentPath design/client-config.json
#>
[CmdletBinding()]
param(
    [string]$SchemaPath = 'design/schemas/client-config.schema.json',
    [string]$DocumentPath = 'design/client-config.json'
)

$ErrorActionPreference = 'Stop'

# ---- Locate repository root ----
$root = $PSScriptRoot
while ($root -and -not (Test-Path (Join-Path $root '.git'))) {
    $parent = Split-Path -Parent $root
    if ([string]::IsNullOrEmpty($parent) -or $parent -eq $root) { $root = $null; break }
    $root = $parent
}
if (-not $root) { throw 'Cannot locate repository root (no .git found).' }

function Resolve-RepoPath([string]$p) {
    if ([System.IO.Path]::IsPathRooted($p)) { return $p }
    return (Join-Path $root $p)
}

$schemaFile = Resolve-RepoPath $SchemaPath
$docFile = Resolve-RepoPath $DocumentPath

if (-not (Test-Path $schemaFile)) { throw "Schema not found: $schemaFile" }
if (-not (Test-Path $docFile)) { throw "Document not found: $docFile" }

$schema = Get-Content -LiteralPath $schemaFile -Raw -Encoding UTF8 | ConvertFrom-Json
$doc = Get-Content -LiteralPath $docFile -Raw -Encoding UTF8 | ConvertFrom-Json

$script:errors = New-Object System.Collections.Generic.List[string]
$script:ignoredKeywords = New-Object System.Collections.Generic.HashSet[string]
$script:unsupported = @(
    'if', 'then', 'else', 'anyOf', 'oneOf', 'not',
    'dependentSchemas', 'patternProperties', 'unevaluatedProperties'
)

function Get-JsonKind($value) {
    if ($null -eq $value) { return 'null' }
    if ($value -is [bool]) { return 'boolean' }
    if ($value -is [string]) { return 'string' }
    if ($value -is [System.Collections.IEnumerable]) { return 'array' }
    if ($value -is [int] -or $value -is [long] -or $value -is [double] -or $value -is [decimal]) { return 'number' }
    return 'object'
}

function Get-Member2($obj, [string]$name) {
    if ($null -eq $obj) { return $null }
    $p = $obj.PSObject.Properties[$name]
    if ($null -eq $p) { return $null }
    return $p.Value
}

function Test-HasMember($obj, [string]$name) {
    if ($null -eq $obj) { return $false }
    return ($null -ne $obj.PSObject.Properties[$name])
}

function Test-Schema {
    param($Value, $Sch, [string]$Path, $RootSchema)

    if ($null -eq $Sch) { return }

    if (Test-HasMember $Sch '$ref') {
        $ref = (Get-Member2 $Sch '$ref')
        if (-not $ref.StartsWith('#')) { throw "Only local `$ref is supported: $ref" }
        $node = $RootSchema
        $parts = $ref.Substring(1).Split('/') | Where-Object { $_ -ne '' }
        foreach ($seg in $parts) {
            $key = $seg -replace '~1', '/' -replace '~0', '~'
            $node = Get-Member2 $node $key
            if ($null -eq $node) { throw "Cannot resolve `$ref: $ref" }
        }
        Test-Schema -Value $Value -Sch $node -Path $Path -RootSchema $RootSchema
        return
    }

    foreach ($kw in $script:unsupported) {
        if (Test-HasMember $Sch $kw) { [void]$script:ignoredKeywords.Add($kw) }
    }

    $kind = Get-JsonKind $Value

    # ---- type ----
    if (Test-HasMember $Sch 'type') {
        $types = @(Get-Member2 $Sch 'type')
        $ok = $false
        foreach ($t in $types) {
            if ($t -eq $kind) { $ok = $true; break }
            if ($t -eq 'integer' -and $kind -eq 'number') { $ok = $true; break }
        }
        if (-not $ok) {
            $script:errors.Add(("{0} : expected type [{1}] but found {2}" -f $Path, ($types -join ', '), $kind))
            return
        }
    }

    # ---- const ----
    if (Test-HasMember $Sch 'const') {
        $expected = Get-Member2 $Sch 'const'
        if ("$Value" -ne "$expected") {
            $script:errors.Add(("{0} : expected const '{1}' but found '{2}'" -f $Path, $expected, $Value))
        }
    }

    # ---- enum ----
    if (Test-HasMember $Sch 'enum') {
        $allowed = @(Get-Member2 $Sch 'enum' | ForEach-Object { "$_" })
        if ($allowed -notcontains "$Value") {
            $script:errors.Add(("{0} : value '{1}' is not in [{2}]" -f $Path, $Value, ($allowed -join ', ')))
        }
    }

    # ---- string constraints ----
    if ($kind -eq 'string') {
        if (Test-HasMember $Sch 'minLength') {
            $min = [int](Get-Member2 $Sch 'minLength')
            if ($Value.Length -lt $min) {
                $script:errors.Add(("{0} : length {1} is below minLength {2}" -f $Path, $Value.Length, $min))
            }
        }
        if (Test-HasMember $Sch 'maxLength') {
            $max = [int](Get-Member2 $Sch 'maxLength')
            if ($Value.Length -gt $max) {
                $script:errors.Add(("{0} : length {1} exceeds maxLength {2}" -f $Path, $Value.Length, $max))
            }
        }
        if (Test-HasMember $Sch 'pattern' -and $Value.Length -gt 0) {
            $pat = Get-Member2 $Sch 'pattern'
            if ($Value -notmatch $pat) {
                $script:errors.Add(("{0} : value does not match pattern {1}" -f $Path, $pat))
            }
        }
    }

    # ---- numeric constraints ----
    if ($kind -eq 'number') {
        if (Test-HasMember $Sch 'minimum') {
            $min = [double](Get-Member2 $Sch 'minimum')
            if ([double]$Value -lt $min) {
                $script:errors.Add(("{0} : value {1} is below minimum {2}" -f $Path, $Value, $min))
            }
        }
        if (Test-HasMember $Sch 'maximum') {
            $max = [double](Get-Member2 $Sch 'maximum')
            if ([double]$Value -gt $max) {
                $script:errors.Add(("{0} : value {1} exceeds maximum {2}" -f $Path, $Value, $max))
            }
        }
    }

    # ---- array items ----
    if ($kind -eq 'array' -and (Test-HasMember $Sch 'items')) {
        $items = Get-Member2 $Sch 'items'
        $i = 0
        foreach ($item in $Value) {
            Test-Schema -Value $item -Sch $items -Path ("{0}[{1}]" -f $Path, $i) -RootSchema $RootSchema
            $i++
        }
    }

    # ---- object properties ----
    if ($kind -eq 'object') {
        $props = Get-Member2 $Sch 'properties'

        if (Test-HasMember $Sch 'required') {
            foreach ($req in @(Get-Member2 $Sch 'required')) {
                if (-not (Test-HasMember $Value $req)) {
                    $script:errors.Add(("{0} : missing required property '{1}'" -f $Path, $req))
                }
            }
        }

        $allowedExtra = $null
        if (Test-HasMember $Sch 'additionalProperties') {
            $ap = Get-Member2 $Sch 'additionalProperties'
            if ($ap -is [bool]) { $allowedExtra = $ap }
        }

        foreach ($prop in $Value.PSObject.Properties) {
            $key = $prop.Name
            $childSchema = $null
            if ($null -ne $props) { $childSchema = Get-Member2 $props $key }

            if ($null -ne $childSchema) {
                Test-Schema -Value $prop.Value -Sch $childSchema -Path ("{0}.{1}" -f $Path, $key) -RootSchema $RootSchema
            }
            elseif ($allowedExtra -eq $false) {
                $script:errors.Add(("{0} : unexpected property '{1}' (additionalProperties is false)" -f $Path, $key))
            }
        }
    }
}

Test-Schema -Value $doc -Sch $schema -Path '$' -RootSchema $schema

Write-Host ''
Write-Host ("Schema   : {0}" -f $SchemaPath)
Write-Host ("Document : {0}" -f $DocumentPath)
if ($script:ignoredKeywords.Count -gt 0) {
    $kw = (@($script:ignoredKeywords) | Sort-Object) -join ', '
    Write-Host ("Note     : unsupported schema keywords skipped: {0}" -f $kw) -ForegroundColor Yellow
}
Write-Host ''

if ($script:errors.Count -eq 0) {
    Write-Host 'RESULT: PASS (0 problems)' -ForegroundColor Green
    exit 0
}

Write-Host ("RESULT: FAIL ({0} problems)" -f $script:errors.Count) -ForegroundColor Red
foreach ($e in $script:errors) { Write-Host ("  - {0}" -f $e) -ForegroundColor DarkRed }
exit 1
