# G0 环境能力与约束

记录时间：2026-10-14 · 探测方式：本机命令探测

本文件回答两个问题：**当前机器能做什么**、**因此我们的工作方式必须怎样**。

---

## 1. 探测结果

| 工具 | 状态 | 说明 |
|---|---|---|
| `git` | **可用** · 2.56.0.windows.2 | 位于 `C:\Program Files\Git\cmd\git.exe`，**不在 PATH** |
| PowerShell | **可用** | 会话自带；但系统执行策略禁止直接运行 `.ps1` 文件，需在调用时用进程级 `Bypass` |
| Go | 未安装 | 无法本地编译共享模块、无法本地构建核心 |
| Node.js | 未安装 | 无法运行 JS 工具链 |
| Python | 未安装 | `python.exe` 仅为 Microsoft Store 占位别名，实际不可执行 |
| CMake | 未安装 | 无法本地配置桌面工程 |
| Java / JDK | 未安装 | 无法本地构建 Android |
| Cargo / Rust | 未安装 | 无备用工具链 |

## 2. 结论

**本机不具备编译任何目标的能力。**

这与设计基线一致：设计基线明确规定「编译只在 GitHub Actions 手动触发执行，不要求本地构建」。因此这不是障碍，而是必须遵守的工作方式。

## 3. 由此产生的工作约束

| 约束 | 具体要求 |
|---|---|
| 构建只能在 CI | 所有编译、打包、原生配置检查交给 GitHub Actions；本地不尝试构建 |
| 脚本只用 PowerShell | 仓库内检查脚本不得依赖 Node / Python / Go；现有脚本见 `tools/scripts/` |
| JSON 校验就地完成 | 用 PowerShell + .NET 的 `ConvertFrom-Json` 完成，已验证可用 |
| 契约先于代码 | 因为没有本地编译器，接口、字段、错误码必须在 `design/` 里先定稿，再写源码 |
| 证据不可伪造 | 未在 CI 或真机跑过的检查，一律标 `not_run`；不得因本地无法验证而写成通过 |
| 工具链版本靠 CI 锁定 | Go / CMake / JDK 等版本写入 `deploy/targets/versions.lock.json`，由 CI 校验 |

## 4. 可用的本地检查手段

| 检查 | 方式 | 状态 |
|---|---|---|
| JSON 结构合法性 | PowerShell `ConvertFrom-Json` | 已验证（`design/` 下 10 个文件全部合法） |
| 设计文档一致性 | 人工审查 + 关键词检索 | 可用 |
| 契约与源码对照 | 用检索工具核对 `src/references/` 中的行号 | 可用 |
| 版本管理操作 | `git`（全路径调用） | 可用 |
| 编译 / 单测 / 集成测试 | — | **本地不可用，必须走 CI** |

## 5. 使用提示

调用 git 时使用全路径，或先把 Git 加入 PATH：

```powershell
$git = "C:\Program Files\Git\cmd\git.exe"
& $git -C D:\workai\xbaord status
```

运行仓库内 PowerShell 脚本时，在当前进程内临时放开策略（不修改系统设置）：

```powershell
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
& .\tools\scripts\validate-json.ps1 -Path design
```

## 6. 待用户在 CI 侧准备的事项

| 项 | 何时需要 |
|---|---|
| GitHub 仓库（用于承载源码与 Actions） | 开始 G1 探针时 |
| Android 签名 keystore（放入 GitHub Secrets） | 正式发布 APK 时；前期可用 debug APK |
| Windows 代码签名证书 | 可选；无签名可生成测试安装包，但需在产物中标注签名状态 |

以上凭据**不要**发到对话中，由用户自行放入仓库 Secrets。
