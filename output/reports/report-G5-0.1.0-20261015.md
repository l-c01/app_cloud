# 验证报告 / Verification report

| | |
|---|---|
| **版本** | `v0.1.0` |
| **日期** | 2026-10-15 |
| **阶段** | G5 完成 |
| **结论** | 设计已完整；**代码从未被编译过** |
| **原始输出** | `output/reports/verification-run-20261015.txt` |

---

## 0. 一句话结论

**设计完整、可交付到 GitHub 手动编译；本机可做的验证全部通过（9/9），
但全部代码从未经过编译器。**

这是一个**可以开始手动构建**的状态，不是一个可以发布的状态。

---

## 1. 本机可执行的验证：9/9 通过

原始输出见同目录的 `verification-run-20261015.txt`。

| # | 检查 | 结果 | 规模 |
|---|---|---|---|
| 1 | Go 未使用 import | **PASS** | 38 个含 import 的文件，0 处未使用 |
| 2 | Go 跨包符号引用 | **PASS** | 14 个内部包，305 处引用全部可解析 |
| 3 | C++ 结构一致性 | **PASS** | 28 个文件，无结构性问题 |
| 4 | 工作流结构 | **PASS** | 13 个文件，4 个可复用工作流 |
| 5 | Go ⇄ C++ IPC 契约 | **PASS** | 版本 1 / 1.0.0 一致；10 个操作名；Go 的 10 个错误码；44 个线协议字段 |
| 6 | 文案键一致性 | **PASS** | 109 个引用键双语一致；未引用键 0 |
| 7 | 文档链接 | **PASS** | 42 个文档，110 条相对链接，0 断链 |
| 8 | 设计 JSON 语法 | **PASS** | 12 个文件 |
| 9 | 配置 vs Schema | **PASS** | 0 问题 |

复现方式：

```powershell
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
& .\tools\scripts\run-all-checks.ps1
```

---

## 2. 这些检查证明了什么

| 检查 | 证明 | **不证明** |
|---|---|---|
| Go 未使用 import | 没有「引用了却未使用」的包 | 类型错误、未定义标识符 |
| Go 跨包符号 | 每一处 `pkg.Symbol` 指向真实声明 | 参数与返回值是否匹配 |
| C++ 结构 | 文件存在、include 可解析、防重包含、括号配平 | **任何类型信息**；不代表能编译 |
| 工作流结构 | 缩进、必需键、本地复用引用存在 | 是合法 YAML，更不代表能跑通 |
| IPC 契约 | 两侧**定义**逐字一致 | 行为一致 |
| 文案键 | 键存在于所有语言表、占位符个数一致 | 翻译质量 |
| 文档链接 | 相对链接指向真实文件 | 内容正确 |

**共同的边界：这些检查一个字节的机器码都没有产生过。**

---

## 3. 只能在 CI 里执行、本机状态为 `not_run` 的检查

| 检查 | 位置 | 状态 |
|---|---|---|
| `gofmt` | `ci.yml` → format | `not_run` |
| `go build ./...` | `ci.yml` → go-checks-linux / windows | `not_run` |
| `go mod tidy` 一致性 | 同上 | `not_run` |
| `go vet ./...` | 同上 | `not_run` |
| `go test ./...` | 同上 | `not_run` |
| `go test -race` | 同上 | `not_run` |
| CMake 配置与构建 `pcx_core` | `ci.yml` → desktop-core | `not_run` |
| C++ 单元测试（`ctest`） | 同上 | `not_run` |
| 桌面 GUI 构建 | `ci.yml` → desktop-gui | `not_run` |
| 多目标交叉编译 | `verify-targets.yml` | `not_run` |
| 安装包构建 | `build-*.yml` | `not_run` |
| Android APK 构建 | `reusable-android.yml` | `not_run` |

设计基线规定：未执行的检查只有一个合法状态，就是「未执行」。
本报告据此不把任何一项写成通过。

---

## 4. 完全没有做过的验证

| 未做 | 后果 |
|---|---|
| 在任何真实设备上运行过 | 无任何运行时行为被确认 |
| 连接过真实的 Xboard 面板 | 面板字段映射全部来自对参考源码的阅读 |
| 拉起过真实的 Xray / sing-box | 命令行参数与退出码来自对核心源码的阅读 |
| 让核心检查过任何生成的配置 | 编译器输出是否被核心接受，完全未知 |
| 验证过系统网络接管的实际效果 | Windows 注册表写入与 GNOME 写入都未执行 |
| 触发过核心下载与 SHA256 校验 | 锁文件未冻结，该路径从未运行 |
| 在设备或模拟器上装过 APK | VpnService 行为完全未知 |

---

## 5. 交付物清单

### 5.1 源码规模

| 语言 | 文件 | 行数 |
|---|---|---|
| Go | 39 | 12,300 |
| C++ | 28 | 5,609 |
| Kotlin | 6 | 844 |
| PowerShell（校验脚本） | 10 | 1,897 |
| GitHub 工作流 | 13 | 1,866 |

受版本管理文件 **174** 个；提交 29 个；tag 5 个。

### 5.2 Go 包（14 个）

```
cmd/proxyclient-service     后台服务入口
shared/panel                面板 API 适配
shared/subscription         订阅拉取与节点解析
shared/domain               标准节点模型
shared/compiler             双核心配置编译器
shared/secrets              凭据存储与脱敏
shared/i18n                 文案加载、协商与格式化
runtime/coordinator         连接事务状态机
runtime/core                核心进程生命周期
runtime/coreupdater         核心下载、校验与更新
runtime/ipc                 UI ⇄ 服务协议
platform/netmgr             网络接管适配层
platform/winproxy           Windows 系统代理
platform/journalfile        持久化恢复日志
```

### 5.3 客户端

| 客户端 | 状态 |
|---|---|
| 桌面（C++20 + wxWidgets + CMake） | 骨架完整：两种界面模式、共享上下文、IPC 客户端、i18n、设置 |
| Android（Kotlin + Compose） | 骨架完整：VpnService、两种模式、共享语言资源；**不代理流量** |

### 5.4 构建入口（13 个工作流）

- `ci.yml`：7 个并行作业（静态预检 / 格式 / Linux 编译测试 / Windows 编译测试 / 桌面核心 / 桌面 GUI / 契约与翻译）
- `verify-targets.yml`：全部目标架构交叉编译
- `build-*.yml`（7 个）：手动产出安装包
- 4 个可复用工作流

### 5.5 文档（双语）

```
docs/zh-CN/  README · dev/{architecture,verification,upload-and-build} · use/README
docs/en/     同上镜像
design/      设计基线、4 份 ADR、契约、Schema、审计、阶段状态
```

---

## 6. 已知风险

### 6.1 设计级（做完了也可能有问题）

| 风险 | 说明 |
|---|---|
| 核心能力表未实测 | 标记为 `verifiedAgainst: "unverified"`，依据是对核心源码的阅读；界面会如实提示 |
| 核心版本未冻结 | `versions.lock.json` 是占位文件；release 构建被拒绝，测试构建不附带核心 |
| 契约检查只比对定义 | 两侧定义一致不代表行为一致 |
| vcpkg 首次构建耗时 | Windows GUI 首次需从源码编译 wxWidgets |
| arm64 runner 可用性 | `ubuntu-24.04-arm` 仅对公开仓库免费 |
| 单实例锁的平台差异 | `wxSingleInstanceChecker` 未实测 |

### 6.2 首次 CI 最可能的三类失败

1. **格式检查** —— 开发机没有 `gofmt`，手工对齐可能不完整。
   该作业会产出 `gofmt-fixes` 补丁产物，下载后 `git apply` 即可。
2. **编译错误或测试失败** —— 约 1.2 万行 Go 与 5.6 千行 C++ 从未编译过。
3. **契约/翻译检查** —— 会明确指出哪个键缺在哪个语言里。

---

## 7. 本次开发过程中被自动检查抓到的缺陷

记录下来是因为它们说明了「本机可做的验证」不是形式主义。
按发现顺序：

| # | 缺陷 | 由哪个检查发现 | 后果（若不修） |
|---|---|---|---|
| 1 | Xray 在 `direct` 策略下产出两个 `direct` 出站 | Go 单元测试 | 核心拒绝启动 |
| 2 | 协调器 `fail` 路径写入空日志记录，覆盖崩溃恢复记录 | Go 单元测试 | 崩溃后无法恢复网络 |
| 3 | IPC `enqueue` 非法语句 / `maxMessageBytes` 未使用 | 人工复核 | 编译失败 / 内存可被单条报文吃光 |
| 4 | `queryValue` 把「值不存在」与「读取失败」混为一谈 | 设计复核 | 读取失败返回空快照 → 永久丢失用户原配置 |
| 5 | `ParseHostPort` 接受 `7890abc` | 设计复核 | 脏数据静默通过 |
| 6 | `NoopBackend` 的解释原因被吞掉 | 设计复核 | 用户分不清「我配错了」与「平台还没做」 |
| 7 | i18n 检查器把字段路径误判为文案键 | 检查器自身的输出 | 7 个假缺失 |
| 8 | `secrets` 凭据键与文案键命名空间撞车 | 检查器输出 | 检查永远需要例外清单 |
| 9 | **C++ 里写错文案键不会被任何检查发现** | 设计复核 | 界面显示原始键名 |
| 10 | `pro_panel` 用 `"ui." + name` 拼接，绕过检查 | 检查器输出 | 任何 `ui.*` 键都显得「被引用」 |
| 11 | 检查器不认识 C++ 原始字符串 `R"(...)"` | 检查器自身 | 含 JSON 的测试文件被误报括号不平衡 |
| 12 | 桌面 preflight 通过但 build 仍是 TODO | 设计复核 | **产出假的构建成功** |
| 13 | Android preflight 中重复的 `env:` 键 | 设计复核 | release 构建以「未绑定变量」失败，与真实原因无关 |
| 14 | Android preflight 通过但 build 仍是 TODO | 设计复核 | **产出假的构建成功** |
| 15 | Kotlin 里写错文案键不会被发现 | 设计复核 | 同 #9 |
| 16 | **整个 Go 模块没有 `main` 包** | 设计复核 | GUI 连接的后台服务根本不存在 |
| 17 | `core.ProbeVersion` 不存在 | Go 符号检查 | 编译失败 |
| 18 | `h.compiler.dir` 被误判为包引用 | 检查器自身的输出 | 2 个假缺失 |
| 19 | 新写的文档有 12 个相对链接层数错误 | 文档链接检查 | 读者点开是 404 |
| 20 | 证据日志用 `2>&1` 抓不到 `Write-Host` | 设计复核 | **日志只有退出码，是空证据** |

第 12、14、16、20 项的共性值得注意：它们都是**「看起来完成了」**。
本项目的检查体系正是围绕这一类失败建立的。

---

## 8. 下一步

1. 推送到 GitHub，查看 `CI checks`（7 个并行作业）
2. 按失败信息修复（见 6.2）
3. 手动运行 `Verify target architectures`，确认全部目标架构可编译
4. 填写 `design/client-config.json` 的软件名、仓库地址、包名
5. 桌面与 Android 的功能补全（见 `README` 与各端 README 的未实现清单）
6. 冻结核心版本（`versions.lock.json`），使 release 构建可用
7. 真机验证（本报告第 4 节列出的全部项目）

---

## 9. 相关文档

- [docs/zh-CN/dev/verification.md](../../docs/zh-CN/dev/verification.md) · [English](../../docs/en/dev/verification.md)
- [docs/zh-CN/dev/architecture.md](../../docs/zh-CN/dev/architecture.md)
- [design/plan/STATUS.md](../../design/plan/STATUS.md)
- [原始输出 verification-run-20261015.txt](verification-run-20261015.txt)
