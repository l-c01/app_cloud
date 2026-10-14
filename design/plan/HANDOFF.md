# 交接文档 / Handoff

> **给新会话的入口文档。** 开新会话后先读这一份，再按需读
> [`STATUS.md`](STATUS.md)（阶段状态权威来源）与
> [`../../design/architecture/ui-options.md`](../architecture/ui-options.md)（选项设计）。

更新时间：2026-10-15 · 当前阶段：**单内核收敛完成，选项实现待做**

---

## 0. 一句话现状

**三个平台的应用产物都已构建成功并核对过内容；单内核（sing-box）收敛已完成并通过 CI；
接下来要做的是「把 Clash 级别的那套配置选项真正实现出来」——这部分只完成了设计，代码尚未开始。**

---

## 1. 已完成（附证据）

### 1.1 三平台产物构建 —— 完成并核对

| 产物 | 大小 | 核对方式 |
|---|---|---|
| Android APK `app-debug.apk` | 9,457,149 字节<br>SHA256 `45ae25359264fca0a6df537327355008e5f1cf6ff19d579c8e78484c091d4e8d` | 下载后解压，确认 `lib/arm64-v8a/`、`classes*.dex`、`assets/locales/{en,zh-CN}.json`、`assets/config/client-config.json` 都在 |
| Linux `linux-amd64.tar.gz` | 2,231,607 字节<br>SHA256 `03d645616b7e32232b8a7e3f2be381f9b41572bcf84bfa0fc849c486c63db8f1` | 解压确认含 `proxyclient-desktop`、`proxyclient-service`、`locales/`、`config/`、`CORES-NOT-BUNDLED.txt` |
| Windows `windows-amd64.zip` | 2,193,163 字节<br>SHA256 `13bcd2f1600d08b87b6e1efac75d40e247e4b218aa2f5492caa7ada7035189e8` | 解压确认含 `proxyclient-desktop.exe`(505,344)、`proxyclient-service.exe`、`locales/`、`config/` |

产物下载在 `output/builds/`（已 gitignore）。

### 1.2 单内核收敛 —— 完成，CI 全绿

按 [ADR-0005](../adr/ADR-0005-单内核-sing-box.md) 把「Xray / sing-box 二选一」
收敛为「只用 sing-box」。三个批次，每批都通过 CI 7/7：

```
第一批  编译器 + 各包常量（21 文件）
第二批  文案 + 目标清单 + 能力表 + 测试夹具
第三批  遗留清理（25 文件）
```

已删除的符号：`compiler.CoreXray`、`coreupdater.CoreXray`、`core.KindXray`、
`shared/compiler/xray.go`、`sources.xray`、全部 `xrayAssetArch`、
文案键 `compiler.unknown_core` / `ui.label.core` / `ui.label.not_installed`、
`client-config.json` 与 schema 的 `core` 字段。

### 1.3 设计文档 —— 完成

- [`design/adr/ADR-0005-单内核-sing-box.md`](../adr/ADR-0005-单内核-sing-box.md)
- [`design/architecture/ui-options.md`](../architecture/ui-options.md) ← **下一阶段的主要依据**
- 中英架构文档第 6 节已由「双核心」改写为「单内核与能力表」

---

## 2. 未完成 ← 从这里继续

### 2.1 选项实现（主体工作，尚未开始）

用户的要求（原文）：

> 客户端选项比较简陋了，高级模式应该借鉴一下 clash verge 和 clash for android
> 内核选一个用吧，不要双内核了，对简易和高级模式都进行选项增补，
> 简易模式是选节点连接，搞个规则模式和分应用代理即可，
> 高级模式类似 clash 客户端，要全面完整的配置选项

**要做什么已经逐项写在 [`ui-options.md`](../architecture/ui-options.md) 里**，
含每一项对应的 sing-box 字段与默认值。建议按这个顺序做：

| 步骤 | 范围 | 说明 |
|---|---|---|
| **B1** | Go 后端：`shared/domain` 选项模型 + `shared/compiler` 生成 | **部分完成**（见 2.2）。已完成：选项模型、DNS、TUN+分应用、route 规则。**未完成：代理组、规则集、嗅探（见 2.3）、端口、启动项** |
| **B2** | IPC 契约：`runtime/ipc/protocol.go` + C++ `apps/desktop/src/core/ipc_protocol.{hpp,cpp}` | 两侧一起改 |
| **B3** | 桌面界面：`apps/desktop/src/ui/pro_panel.{hpp,cpp}`（高级）+ `simple_panel`（简易） | |
| **B4** | Android 界面：`apps/android/.../ui/ProxyApp.kt` | |
| **B5** | i18n 文案：`locales/{zh-CN,en}.json` 两侧同步 | |
| **B6** | 双语文档同步 | |

### 2.2 进度：后端配置生成层已完成三块

每一块都单独通过 CI 7/7：

| 批次 | 内容 | 提交 |
|---|---|---|
| 选项模型 | `shared/domain/options.go`——整套选项的数据结构 + `DefaultOptions()` | `674d689` |
| DNS | 生成 `dns` 段：fake-ip、名称服务器（**用稳定标签不用下标**）、过滤列表 | `cfca1cc` |
| TUN + 分应用 | TUN 入站 + `include_package`/`exclude_package`（**互斥**，走取值方法） | `dd8f752` |
| route | 用户规则 + 兜底动作；**内建规则一律在用户规则之前** | `695557d` |

`Request.Options` 是**指针且允许为 nil**：nil 表示"调用方尚未接入选项"，
此时生成的配置与接入前逐字节一致。这是让选项能**逐块接入**的关键——
不必一次性改完所有调用方与既有测试。

### 2.3 一处**故意没做**的地方（重要）

`ui-options.md` 的嗅探一节写了「嗅探协议 TLS / HTTP / QUIC」，
但 sing-box 的**入站**嗅探是 `sniff: true/false`，
协议列表未必是入站字段（新版本里它更像路由侧的东西）。

**没有真核心可以验证，因此没有实现。**
猜一个字段名会生成一份核心拒绝的配置，而错误信息来自核心深处——
这正是设计文档里批评过的「伪选项」。宁可留着不做。

**如果接手的人有条件跑真实 sing-box，请先确认这个字段，再实现。**
顺带把确认结果写进 `capability.go` 的能力表。

### 2.4 一次失败的尝试（避免重复）

曾把整个后端（B1）委派给一个子代理，**运行很多轮零代码产出**，最终取消。
后来又切小了一次（只做 DNS + TUN + 分应用），**同样零产出、无收尾报告**。

**结论：这类 Go 后端改动不要再委派**，自己做。
子代理在文档整理、跨包机械清理上表现很好（单内核收敛的三个批次都是它们做的），
但在"需要新增代码并自己验证"的任务上反复失败。

原因是推测的：它们无法编译，只能靠检查脚本判断，而检查脚本不报错时
它们可能陷入反复确认。反过来，机械性的替换/删除有明确的完成信号。

---

## 3. 工作方式与约束（重要）

### 3.1 环境

- 仓库根：`D:\workai\xbaord`，**只在这个目录内改文件**
- 本机**没有 Go / CMake / JDK / Android SDK**，无法编译
- Shell 是 Windows PowerShell 5.1；`git` 在 `C:\Program Files\Git\cmd\git.exe`（**不在 PATH**）
- **`github.com:443` 不可达**；`api.github.com` 与 `codeload.github.com` 可用
- 开工前先 `Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force`

### 3.2 推送

只能用 `tools/scripts/push-via-api.ps1`（通过 Git Data API 重放本地提交）。
不能 `git push`（`github.com` 不可达）。

```powershell
# -Base 是远端已有的提交，-From 是本地要重放区间的**父**提交
& .\tools\scripts\push-via-api.ps1 -Base "<远端HEAD>" -From "<本地父提交>"
```

推送后**必须**核对远端 tree 与本地一致：

```powershell
$rc = Invoke-RestMethod "$api/git/commits/$sha" -Headers $h
$rc.tree.sha -eq (git rev-parse 'HEAD^{tree}')
```

### 3.3 验证

```powershell
& .\tools\scripts\run-all-checks.ps1 -LogPath "output\reports\verification-run-<日期>.txt" -Quiet
```

10 项本机检查。**注意：它们只做文本级检查，不编译任何东西。**
编译与单元测试只能靠 GitHub Actions（push 会触发 `ci.yml`）。

`check-go-symbols.ps1` 是最有用的一个——它能发现「引用了已删除的符号」。

### 3.4 省上下文的做法（这个项目验证过有效）

- PowerShell 只打印**结论一行**，明细写进 `output/reports/` 文件
- 需要读大文件的判断一律交给子代理，只要它的结论
- 提交信息一律写文件再 `git commit -F`（直接 `-m` 传多行中文会被 PowerShell 拆坏）
- 子代理的报告要**当证据看，不是当结论看**：它自评失败时产出可能是好的，
  自评成功时也要自己核对
- **本机跑不了 gofmt，但 CI 会给出精确差异。**
  `.github/workflows/ci.yml` 的格式化作业做两件事：把 `gofmt -d` 的前 300 行
  打进日志（不下载任何东西就能看到要改什么），并把补丁作为 `gofmt-fixes`
  产物上传（可下载后 `git apply`）。
  差异小时照日志改更快；差异大时**下载补丁比手工算列数可靠得多**。
  手工按列数算对齐至少错过一次（`options.go` 的名字列宽算成 13，gofmt 要 12）。

---

## 4. 踩过的坑（别再踩）

| 坑 | 表现 | 教训 |
|---|---|---|
| 打包用宽通配符 `proxyclient-desktop*` | 打进去的是 `.slnx` 而不是 `.exe`，**每步都报 success** | 产物必须**下载后打开看内容** |
| 打包步骤混用相对/绝对路径 | `mkdir` 建的目录和 `tar` 写的不是一个 | 统一用绝对路径，别数 `../` |
| Kotlin 块注释**可嵌套** | KDoc 里写 `locales/*.json` 吞掉整个文件，十来个文件报未解析 | 注释里不写「斜杠加星号」 |
| 跨平台步骤没声明 `shell: bash` | Windows 上按 PowerShell 解析 bash 脚本 | 只在单平台成立时最危险 |
| MSVC `/W4 /WX` 下的 `C4996` | `getenv` 被判为不安全 | 这是厂商偏好不是缺陷，关掉 |
| `Start-Process` 复制环境变量 | 父子进程都有 `NO_PROXY`/`no_proxy` 时抛异常 | 改用 `cmd /c ... > file` 读字节 |
| git 提交信息用 `-m` 传多行中文 | PowerShell 转义拆坏消息 | 写文件 + `-F` |
| 测试断言 Windows 模式位 | `runtime/ipc` 与 `journalfile` 两个测试**时好时坏** | 平台不适用要显式 skip |

---

## 5. 仍未决的用户输入

这些不影响继续开发，但发布前需要：

- 项目许可证
- 产品名 / 发布者 / `applicationId` / `androidPackageId`
- `client-config.json` 里要填的 GitHub 仓库地址
- 参考源码的 commit SHA
- Xboard 测试环境（用于把能力表的 `verifiedAgainst` 从 `unverified` 改成实测）
- Android 签名密钥（release 构建必需；缺失时构建会明确失败）

---

## 6. 关键未验证事项（必须如实保留）

- 全部运行期行为仍是 `not_run`：从未在真机运行、从未连接真实面板、从未拉起真实核心
- **Android 的 VPN 能建立，但不会代理任何流量**（TUN 尚未交给核心）
- 能力表 `verifiedAgainst: "unverified"` —— 来自读源码，不是实测
- 生成的核心配置**从未被真实 sing-box 接受过**

**不要把上面任何一条写成「已验证」。**
