<#
.SYNOPSIS
    Push local commits to GitHub through the REST API instead of the git transport.

.DESCRIPTION
    In some networks `github.com:443` is unreachable while `api.github.com:443`
    works. The git transport has no alternative endpoint, so `git push` fails
    with "Failed to connect to github.com:443" even though the network is fine
    for everything else -- including the API used to read CI logs.

    This script replays local commits onto the remote branch using the Git Data
    API (blobs -> tree -> commit -> update ref). Commit history, messages,
    authors and dates are preserved: it creates one API commit per local commit.

    It is not a general replacement for `git push`: it does not handle merges,
    does not update tags unless asked, and cannot be used when the remote has
    commits the local branch does not have. It exists to get work onto GitHub
    when the transport is blocked.

    The token is read from Git Credential Manager via `git credential fill` and
    is never printed.

    NOTE: this file is intentionally ASCII-only.
    Windows PowerShell 5.1 reads BOM-less .ps1 files using the ANSI code page.

    Exit codes: 0 = pushed, 1 = failed, 2 = setup problem.

.PARAMETER Remote
    Remote name whose URL identifies the repository. Default: origin.

.PARAMETER Branch
    Branch to update. Default: the current branch.

.PARAMETER From
    First local commit to push. Default: the remote branch tip.

.PARAMETER IncludeTags
    Also create any local tag that points at one of the pushed commits and does
    not exist remotely.

.EXAMPLE
    Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
    & ./tools/scripts/push-via-api.ps1
#>
[CmdletBinding()]
param(
    [string]$Remote = 'origin',
    [string]$Branch = '',
    [string]$From = '',
    [string]$Base = '',
    [string[]]$Repair = @(),
    [switch]$IncludeTags
)

$ErrorActionPreference = 'Stop'

function Get-GitPath {
    $cmd = Get-Command git -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    $candidate = 'C:\Program Files\Git\cmd\git.exe'
    if (Test-Path $candidate) { return $candidate }
    throw 'git was not found'
}

$git = Get-GitPath

$repoRoot = $PSScriptRoot
while ($repoRoot -and -not (Test-Path (Join-Path $repoRoot '.git'))) {
    $parent = Split-Path -Parent $repoRoot
    if ([string]::IsNullOrEmpty($parent) -or $parent -eq $repoRoot) { $repoRoot = $null; break }
    $repoRoot = $parent
}
if (-not $repoRoot) { throw 'Cannot locate repository root (no .git found).' }

function Get-BlobBytes {
    param([string]$Rev, [string]$Path)
    # Read the blob straight from the object database.
    #
    # Reading from a worktree checkout would be wrong: checking out applies the
    # .gitattributes filters, so a file declared `text eol=crlf` (which is what
    # this repository says about *.ps1) comes back with CRLF even though the
    # stored blob has LF. Uploading those bytes silently rewrites the file's
    # line endings in the repository.
    #
    # `git cat-file blob` bypasses every filter, and Start-Process's
    # -RedirectStandardOutput writes the bytes without going through
    # PowerShell's text pipeline.
    $ls = (& $git -C $repoRoot ls-tree $Rev -- $Path) -join ''
    if ($ls -notmatch '^\d+ blob ([0-9a-f]+)') { throw "no blob for $Path at $Rev" }
    $blob = $Matches[1]

    $tmp = Join-Path $repoRoot 'output/_push-api-blob.bin'
    try {
        $proc = Start-Process -FilePath $git `
            -ArgumentList @('-C', $repoRoot, 'cat-file', 'blob', $blob) `
            -RedirectStandardOutput $tmp -NoNewWindow -Wait -PassThru
        if ($proc.ExitCode -ne 0) { throw "git cat-file failed for $Path at $Rev" }
        return [System.IO.File]::ReadAllBytes($tmp)
    } finally {
        Remove-Item $tmp -Force -ErrorAction SilentlyContinue
    }
}

function New-BlobEntry {
    param([string]$Rev, [string]$Path)
    $bytes = Get-BlobBytes -Rev $Rev -Path $Path
    $b64 = [Convert]::ToBase64String($bytes)
    $blob = Invoke-Api -Method POST -Path '/git/blobs' -Body @{ content = $b64; encoding = 'base64' }
    return @{ path = $Path; mode = '100644'; type = 'blob'; sha = $blob.sha }
}

$repoRoot = $repoRoot

function Git {
    param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Args)
    # Native command stderr is handled carefully here.
    #
    # In Windows PowerShell 5.1 a native command's stderr output is turned into
    # an ErrorRecord, and under $ErrorActionPreference = 'Stop' that terminates
    # the script -- even when the command succeeded. `git worktree add` prints
    # its progress on stderr, so the first version of this script "failed" on a
    # command that had actually worked.
    #
    # The preference is therefore relaxed just around the call, and stderr is
    # discarded so it cannot pollute $out (callers parse it).
    $saved = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $out = & $git -C $repoRoot @Args 2>$null
        $code = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $saved
    }
    if ($code -ne 0) { throw ("git {0} failed" -f ($Args -join ' ')) }
    return $out
}

# ---------------------------------------------------------------------------
# Identify the repository
# ---------------------------------------------------------------------------
$remoteUrl = (Git remote get-url $Remote | Select-Object -First 1)
if ($remoteUrl -notmatch 'github\.com[:/](?<owner>[^/]+)/(?<repo>[^/]+?)(?:\.git)?$') {
    throw "cannot parse a GitHub owner/repo from the remote URL: $remoteUrl"
}
$owner = $Matches['owner']
$repo = $Matches['repo']
Write-Host ("repository : {0}/{1}" -f $owner, $repo)

if ([string]::IsNullOrEmpty($Branch)) {
    $Branch = (Git branch --show-current | Select-Object -First 1)
}
if ([string]::IsNullOrEmpty($Branch)) { throw 'cannot determine the branch to push' }

# ---------------------------------------------------------------------------
# Token
# ---------------------------------------------------------------------------
$fill = "protocol=https`nhost=github.com`n" | & $git credential fill 2>&1
$token = ($fill | Where-Object { $_ -match '^password=' }) -replace '^password=',''
if ([string]::IsNullOrWhiteSpace($token)) {
    throw 'no credential available; run a normal git push once to authenticate'
}
$headers = @{
    'Authorization' = "Bearer $token"
    'User-Agent'    = 'proxyclient-push-via-api'
    'Accept'        = 'application/vnd.github+json'
}
$api = "https://api.github.com/repos/$owner/$repo"

function Invoke-Api {
    param([string]$Method, [string]$Path, $Body)
    $params = @{ Method = $Method; Uri = "$api$Path"; Headers = $headers; TimeoutSec = 60 }
    if ($null -ne $Body) {
        $params['Body'] = ($Body | ConvertTo-Json -Depth 20 -Compress)
        $params['ContentType'] = 'application/json; charset=utf-8'
    }
    return Invoke-RestMethod @params
}

# Confirm the API is actually reachable before doing anything.
try {
    $null = Invoke-Api -Method GET -Path ''
} catch {
    throw "the GitHub API is not reachable either: $($_.Exception.Message)"
}

# ---------------------------------------------------------------------------
# Work out what to push
# ---------------------------------------------------------------------------
$localTip = (Git rev-parse $Branch | Select-Object -First 1).Trim()

$remoteTip = $null
try {
    $ref = Invoke-Api -Method GET -Path "/git/ref/heads/$Branch"
    $remoteTip = $ref.object.sha
} catch {
    Write-Host 'remote branch does not exist yet; it will be created'
}

if ([string]::IsNullOrEmpty($From)) {
    if ([string]::IsNullOrEmpty($remoteTip)) {
        throw 'the remote branch does not exist; pass -From with the first commit to push'
    }
    $From = $remoteTip
}

# -Base lets the new commits build on a remote commit that does not exist in the
# local object database. That happens whenever a previous run of this script
# created the remote commits: their SHAs differ from the local ones, so the
# local branch and the remote branch hold the same content under different ids.
# Without -Base the ancestry check below would refuse to push anything ever
# again, which is safe but useless.
$newBase = $From
$skipAncestry = $false
if (-not [string]::IsNullOrEmpty($Base)) {
    $newBase = $Base
    $skipAncestry = $true
    Write-Host ("base       : {0} (building on a remote commit)" -f $Base.Substring(0, 7))
}

if ($From -eq $localTip -and -not $skipAncestry) {
    Write-Host 'nothing to push: the remote tip already matches the local tip'
    exit 0
}

# The base used for the first replayed commit must be an ancestor of the local
# tip unless it was supplied explicitly, otherwise this script would silently
# drop remote commits.
if (-not $skipAncestry) {
    & $git -C $repoRoot merge-base --is-ancestor $From $localTip 2>&1 | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw "the remote tip ($From) is not an ancestor of $localTip; refusing to replay (would drop remote commits). Pass -Base if the remote was written by an earlier run of this script."
    }
}

# Oldest first.
$commits = @(Git rev-list --reverse "$From..$localTip")
if ($commits.Count -eq 0) {
    if ($Repair.Count -eq 0) {
        Write-Host 'nothing to push'
        exit 0
    }
    # A repair-only push still needs one commit to carry the corrected blobs.
    $commits = @($localTip)
}
Write-Host ("commits    : {0} to replay" -f $commits.Count)
Write-Host ''

# ---------------------------------------------------------------------------
# File content is read from the object database by Get-BlobBytes, not from a
# worktree: checking out a worktree applies the .gitattributes filters and would
# rewrite line endings on the way into the repository.
# ---------------------------------------------------------------------------
try {
    $parentSha = $newBase
    $pushed = 0

    foreach ($commit in $commits) {
        $sha = $commit.Trim()
        $short = $sha.Substring(0, 7)

        # Metadata, including the original author and date.
        #
        # Fields are separated with the ASCII unit separator rather than NUL:
        # PowerShell may truncate a captured string at an embedded NUL.
        $meta = (Git show -s --format='%an%x1f%ae%x1f%aI%x1f%cn%x1f%ce%x1f%cI%x1f%s' $sha) -join ''
        $parts = $meta -split [char]0x1f
        $an = $parts[0]; $ae = $parts[1]; $ad = $parts[2]
        $cn = $parts[3]; $ce = $parts[4]; $cd = $parts[5]; $subject = $parts[6]
        $fullMessage = (Git log -1 --format='%B' $sha) -join "`n"

        # Changed paths, relative to the repository root.
        $nameStatus = @(Git diff-tree --no-commit-id --name-status -r -M $sha)
        if ($nameStatus.Count -eq 0) {
            Write-Host ("  {0}  (no file changes; replaying as an empty commit)" -f $short)
        } else {
            Write-Host ("  {0}  {1}" -f $short, $subject)
        }

        $entries = @()
        $seen = @{}
        foreach ($line in $nameStatus) {
            if ([string]::IsNullOrWhiteSpace($line)) { continue }
            $fields = $line -split "`t"
            if ($fields.Count -lt 2) { continue }
            $status = $fields[0]
            $path = $fields[1]
            # Renames carry an extra path column; take the destination.
            if ($status -like 'R*' -and $fields.Count -ge 3) { $path = $fields[2] }

            if ($status -eq 'D') {
                $entries += @{ path = $path; mode = '100644'; type = 'blob'; sha = $null }
                $seen[$path] = $true
                continue
            }

            $entries += (New-BlobEntry -Rev $sha -Path $path)
            $seen[$path] = $true
        }

        # Repair paths are re-uploaded from the commit being replayed even when
        # that commit does not touch them. Used to correct content that a
        # previous push wrote incorrectly (for example line endings rewritten by
        # a checkout filter), where the local file is right but unchanged.
        foreach ($rp in $Repair) {
            if ([string]::IsNullOrWhiteSpace($rp)) { continue }
            if ($seen.ContainsKey($rp)) { continue }
            Write-Host ("             repairing {0}" -f $rp)
            $entries += (New-BlobEntry -Rev $sha -Path $rp)
            $seen[$rp] = $true
        }

        # The new tree is layered on the parent's tree, so unchanged files are
        # reused rather than re-uploaded.
        $parentTree = (Invoke-Api -Method GET -Path "/git/commits/$parentSha").tree.sha
        $tree = Invoke-Api -Method POST -Path '/git/trees' -Body @{ tree = $entries; base_tree = $parentTree }

        $commitBody = @{
            message   = $fullMessage
            tree      = $tree.sha
            parents   = @($parentSha)
            author    = @{ name = $an; email = $ae; date = $ad }
            committer = @{ name = $cn; email = $ce; date = $cd }
        }
        $newCommit = Invoke-Api -Method POST -Path '/git/commits' -Body $commitBody

        # Advance the parent for the next commit. The ref is only updated at the
        # very end, so a failure part-way leaves the remote untouched.
        if ($pushed -eq 0 -and [string]::IsNullOrEmpty($remoteTip)) {
            # Branch does not exist: create it on the first commit.
            $null = Invoke-Api -Method POST -Path '/git/refs' -Body @{ ref = "refs/heads/$Branch"; sha = $newCommit.sha }
            $remoteTip = $newCommit.sha
        }
        $parentSha = $newCommit.sha
        $pushed++
    }

    # -----------------------------------------------------------------------
    # Move the branch
    # -----------------------------------------------------------------------
    if (-not [string]::IsNullOrEmpty($remoteTip) -and $pushed -gt 0) {
        $null = Invoke-Api -Method PATCH -Path "/git/refs/heads/$Branch" -Body @{ sha = $parentSha; force = $false }
    }
    Write-Host ''
    Write-Host ("pushed     : {0} commit(s); {1} is now {2}" -f $pushed, $Branch, $parentSha.Substring(0, 7))

    # -----------------------------------------------------------------------
    # Tags (optional)
    # -----------------------------------------------------------------------
    if ($IncludeTags) {
        $localTags = @(Git tag --points-at $parentSha)
        foreach ($tag in $localTags) {
            $t = $tag.Trim()
            if ([string]::IsNullOrEmpty($t)) { continue }
            try {
                $null = Invoke-Api -Method GET -Path "/git/ref/tags/$t"
                continue  # already exists
            } catch { }
            $objType = (Git cat-file -t $t | Select-Object -First 1).Trim()
            if ($objType -eq 'tag') {
                $tagMsg = (Git tag -l --format='%(contents)' $t) -join "`n"
                $target = (Git rev-parse "$t^{}" | Select-Object -First 1).Trim()
                $taggerName = (Git for-each-ref --format='%(taggername)' "refs/tags/$t" | Select-Object -First 1).Trim()
                $taggerEmail = (Git for-each-ref --format='%(taggeremail)' "refs/tags/$t" | Select-Object -First 1).Trim()
                $taggerDate = (Git for-each-ref --format='%(taggerdate:iso-strict)' "refs/tags/$t" | Select-Object -First 1).Trim()
                $tagObj = Invoke-Api -Method POST -Path '/git/tags' -Body @{
                    tag     = $t
                    message = $tagMsg
                    object  = $target
                    type    = 'commit'
                    tagger  = @{ name = $taggerName; email = $taggerEmail; date = $taggerDate }
                }
                $null = Invoke-Api -Method POST -Path '/git/refs' -Body @{ ref = "refs/tags/$t"; sha = $tagObj.sha }
            } else {
                $null = Invoke-Api -Method POST -Path '/git/refs' -Body @{ ref = "refs/tags/$t"; sha = $t }
            }
            Write-Host ("tag        : {0} -> {1}" -f $t, $target.Substring(0, 7))
        }
    }
} catch {
    Write-Host ''
    Write-Host ("FAILED: {0}" -f $_.Exception.Message)
    throw
}

Write-Host ''
Write-Host 'RESULT: pushed through the API.'
exit 0
