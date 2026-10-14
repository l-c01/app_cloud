<#
.SYNOPSIS
    Detect broken relative links between Markdown documents.

.DESCRIPTION
    The documentation cross-references itself heavily, and a relative link is
    resolved against the directory of the file that contains it. Get the number
    of "../" wrong by one and the link silently points somewhere that does not
    exist -- it renders fine in an editor and 404s when clicked.

    Nothing else checks this. A broken link is a documentation defect that a
    reader discovers at the worst moment: while following the one instruction
    they needed.

    What is checked:
      - relative links between files: [text](path) and [text](path#anchor)
      - links to directories (reported, since a directory link is usually a
        mistake in a document)

    What is ignored on purpose:
      - absolute URLs (http, https, mailto)
      - pure anchors (#section)
      - links inside fenced code blocks

    NOTE: this file is intentionally ASCII-only.
    Windows PowerShell 5.1 reads BOM-less .ps1 files using the ANSI code page.

    Exit codes: 0 = no broken links, 1 = broken links found, 2 = setup problem.

.PARAMETER Roots
    Directories to scan, relative to the repository root.

.EXAMPLE
    Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
    & ./tools/scripts/check-doc-links.ps1
#>
[CmdletBinding()]
param(
    [string[]]$Roots = @('docs', 'design', 'output', 'README.md', 'src', '.github')
)

$ErrorActionPreference = 'Stop'

$repoRoot = $PSScriptRoot
while ($repoRoot -and -not (Test-Path (Join-Path $repoRoot '.git'))) {
    $parent = Split-Path -Parent $repoRoot
    if ([string]::IsNullOrEmpty($parent) -or $parent -eq $repoRoot) { $repoRoot = $null; break }
    $repoRoot = $parent
}
if (-not $repoRoot) { throw 'Cannot locate repository root (no .git found).' }

# Collect markdown files. Reference snapshots are excluded: they are
# third-party material and their links are not ours to fix.
$files = @()
foreach ($root in $Roots) {
    $path = Join-Path $repoRoot $root
    if (-not (Test-Path $path)) { continue }
    $item = Get-Item $path
    if ($item.PSIsContainer) {
        $files += Get-ChildItem -Path $path -Recurse -File -Filter '*.md' |
            Where-Object { $_.FullName -notmatch '\\references\\' }
    } elseif ($item.Extension -eq '.md') {
        $files += $item
    }
}

if ($files.Count -eq 0) {
    Write-Host 'no markdown files found'
    exit 2
}

$broken = 0
$checked = 0
$dirLinks = 0

foreach ($file in $files) {
    $lines = @(Get-Content -LiteralPath $file.FullName -Encoding UTF8)
    $rel = $file.FullName.Substring($repoRoot.Length).TrimStart('\', '/')
    $fileDir = $file.DirectoryName

    $inFence = $false
    $lineNo = 0
    foreach ($line in $lines) {
        $lineNo++

        if ($line -match '^\s*```') {
            $inFence = -not $inFence
            continue
        }
        if ($inFence) { continue }

        foreach ($m in [regex]::Matches($line, '\[[^\]]*\]\(([^)\s]+)\)')) {
            $target = $m.Groups[1].Value

            # Skip absolute URLs, protocol-relative URLs and pure anchors.
            if ($target -match '^(https?:|mailto:|ftp:|#|data:)') { continue }
            # Skip links that are only an anchor on the same page.
            if ($target.StartsWith('#')) { continue }

            # Strip the anchor part.
            $pathPart = $target.Split('#')[0]
            if ([string]::IsNullOrEmpty($pathPart)) { continue }

            # URLs with a scheme we did not list (for example a custom one).
            if ($pathPart -match '^[a-zA-Z][a-zA-Z0-9+.-]*:') { continue }

            $checked++
            $resolved = Join-Path $fileDir $pathPart
            # Normalise ../ segments before testing.
            try {
                $full = [System.IO.Path]::GetFullPath($resolved)
            } catch {
                $broken++
                Write-Host ("BROKEN  {0}:{1}" -f $rel, $lineNo)
                Write-Host ("        invalid path: {0}" -f $target)
                continue
            }

            if (-not (Test-Path -LiteralPath $full)) {
                $broken++
                Write-Host ("BROKEN  {0}:{1}" -f $rel, $lineNo)
                Write-Host ("        {0}" -f $target)
                Write-Host ("        resolved to: {0}" -f $full)
            } elseif (Test-Path -LiteralPath $full -PathType Container) {
                # A directory link is usually a mistake in a document: it gives
                # the reader a listing instead of the file they wanted.
                $dirLinks++
                Write-Host ("DIR LINK  {0}:{1}  {2}" -f $rel, $lineNo, $target)
            }
        }
    }
}

Write-Host ''
Write-Host ("documents : {0}" -f $files.Count)
Write-Host ("links     : {0} relative link(s) checked" -f $checked)
Write-Host ''

if ($broken -eq 0) {
    Write-Host ("RESULT: PASS - no broken relative links ({0} directory link(s) noted)." -f $dirLinks)
    exit 0
}
Write-Host ("RESULT: FAIL - {0} broken link(s)." -f $broken)
exit 1
