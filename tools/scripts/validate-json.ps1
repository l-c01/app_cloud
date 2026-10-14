<#
.SYNOPSIS
    校验设计目录下的 JSON 文件是否为合法 JSON。

.DESCRIPTION
    本机没有任何额外运行时（无 Node / Python / Go / CMake / Java），因此仓库内的检查脚本
    只用 PowerShell + .NET 实现。本脚本用于 G0 阶段的静态契约检查：
    读取目标目录下所有 .json 文件，逐个解析并报告结果。

    仓库根目录由脚本自身位置向上查找 .git 目录确定，不依赖调用时的工作目录。

    退出码：0 = 全部合法；1 = 存在非法文件或路径不存在。

.EXAMPLE
    & ./tools/scripts/validate-json.ps1
    & ./tools/scripts/validate-json.ps1 -Path design/contracts
#>
[CmdletBinding()]
param(
    [string]$Path = 'design'
)

$ErrorActionPreference = 'Stop'

# 由脚本位置向上查找仓库根（含 .git 的目录）
$root = $PSScriptRoot
while ($root -and -not (Test-Path (Join-Path $root '.git'))) {
    $parent = Split-Path -Parent $root
    if ($parent -eq $root -or [string]::IsNullOrEmpty($parent)) { $root = $null; break }
    $root = $parent
}
if (-not $root) {
    Write-Error '未能定位仓库根目录（未找到 .git）。'
    exit 1
}

$target = if ([System.IO.Path]::IsPathRooted($Path)) { $Path } else { Join-Path $root $Path }
if (-not (Test-Path $target)) {
    Write-Error "路径不存在：$target"
    exit 1
}

$files = @(Get-ChildItem -Path $target -Filter '*.json' -Recurse -File -ErrorAction SilentlyContinue)
if ($files.Count -eq 0) {
    Write-Host "未找到 JSON 文件：$target"
    exit 0
}

$failed = @()
foreach ($file in $files) {
    $rel = $file.FullName.Substring($root.Length).TrimStart('\', '/')
    try {
        $null = Get-Content -LiteralPath $file.FullName -Raw -Encoding UTF8 | ConvertFrom-Json
        Write-Host "  OK    $rel"
    }
    catch {
        Write-Host "  FAIL  $rel" -ForegroundColor Red
        Write-Host "        $($_.Exception.Message)" -ForegroundColor DarkRed
        $failed += $rel
    }
}

Write-Host ''
Write-Host "检查完成：共 $($files.Count) 个文件，失败 $($failed.Count) 个。"
if ($failed.Count -gt 0) {
    Write-Host '失败文件：'
    $failed | ForEach-Object { Write-Host "  - $_" }
    exit 1
}
exit 0
