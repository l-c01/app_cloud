# 验证说明 / Verification

本页回答两个问题：**做了哪些检查**，以及**这些检查没有证明什么**。

第二个问题比第一个重要。一份只列「全部通过」的报告是没有信息量的——
它没有告诉读者哪些结论**不能**从中得出。

> **最重要的一句话：代码已编译通过、测试全绿，但从未在真实设备上运行过。**
>
> 本机没有 Go / C++ / JDK / Android SDK 工具链，因此编译与单元测试全部在
> GitHub Actions 里进行。**2026-10-15 的 CI 运行 7 个作业全部通过**
> （Linux 与 Windows 双平台）。
>
> 「编译通过」与「能用」之间仍有距离：未连接过真实面板、
> 未让核心检查过生成的配置、未在真机上验证过网络接管。

---

## 1. 可以本机执行的检查

这些检查不需要编译工具链，因此在本机与 CI 中都会运行。

| 检查 | 脚本 | 证明什么 | **不证明什么** |
|---|---|---|---|
| Go 未使用 import | `check-go-imports.ps1` | 没有「引用了却未使用」的包（Go 会因此拒绝编译） | 不证明能编译：类型错误、未定义标识符都查不出 |
| Go 跨包符号引用 | `check-go-symbols.ps1` | 每一处 `pkg.Symbol` 都指向真实存在的声明 | 不检查参数类型与返回值匹配 |
| C++ 结构一致性 | `check-cpp-structure.ps1` | CMake 引用的文件存在、`#include` 可解析、头文件有防重包含、括号配平 | 完全不涉及类型与模板；**不代表能编译** |
| 工作流结构 | `check-workflows.ps1` | 无制表符、必需顶层键、块标量外的缩进、本地复用引用存在且声明了 `on.workflow_call` | 不证明是合法 YAML，更不证明能跑通 |
| IPC 契约一致性 | `check-ipc-contract.ps1` | Go 与 C++ 的协议版本、操作名、错误码、线协议字段名逐字一致 | 不检查行为一致，只检查定义一致 |
| 文案键一致性 | `check-i18n-keys.ps1` | 代码引用的键存在于**所有**语言表，且占位符个数跨语言一致 | 不检查翻译质量 |
| 设计 JSON 语法 | `validate-json.ps1` | 12 个设计 JSON 是合法 JSON | 不检查语义 |
| 配置 vs Schema | `validate-client-config.ps1` | 出厂配置符合它的 Schema | 不检查取值是否合理 |

一条统一的原则：**这些脚本宁可漏报，不可误报**。
误报会让使用者开始忽略输出，那时检查就彻底失效了。

---

## 2. 最近一次本机检查的结果

> 时间：2026-10-15 · 版本：`v0.1.0-alpha.4`

| 检查 | 结果 |
|---|---|
| Go 未使用 import | **通过** — 38 个含 import 的文件，0 处未使用 |
| Go 跨包符号引用 | **通过** — 14 个内部包，305 处引用全部可解析 |
| C++ 结构一致性 | **通过** — 28 个文件无结构性问题 |
| 工作流结构 | **通过** — 13 个文件，识别出 4 个可复用工作流 |
| IPC 契约 | **通过** — 版本 1 / 1.0.0 一致；10 个操作名；Go 的 10 个错误码都在 C++ 侧；Go 的 44 个线协议字段全部被 C++ 解析器覆盖 |
| 文案键 | **通过** — 109 个引用键在两种语言里都存在且占位符一致；未引用键 0 |
| 设计 JSON 语法 | **通过** — 12 个文件 |
| 配置 vs Schema | **通过** — 0 问题 |
| PowerShell 纯 ASCII | **通过** — 全部 `.ps1` 无非 ASCII 字节 |

规模：Go 39 文件 / 12300 行 · C++ 28 文件 / 5609 行 · Kotlin 6 文件 / 844 行 ·
受版本管理文件 163 个。

---

## 3. CI 里的编译与测试结果

> **2026-10-15 · CI 运行 #10 · 提交 `a034b88` · 7/7 通过**

| 检查 | 位置 | 状态 |
|---|---|---|
| `gofmt` 格式 | `ci.yml` → format | ✅ **PASS** |
| `go build ./...` | `ci.yml` → go-checks-linux / windows | ✅ **PASS**（两个平台） |
| `go mod tidy` 一致性 | 同上 | ✅ **PASS** |
| `go vet ./...` | 同上 | ✅ **PASS** |
| `go test ./...` | 同上 | ✅ **PASS**（14 个包全部通过） |
| `go test -race` | 同上 | ✅ **PASS** |
| CMake 配置与构建 `pcx_core` | `ci.yml` → desktop-core | ✅ **PASS** |
| C++ 单元测试（`ctest`） | 同上 | ✅ **PASS** |
| 桌面 GUI 构建 | `ci.yml` → desktop-gui | ✅ **PASS**（wxWidgets 3.2，`-Werror`） |
| 多目标交叉编译 | `verify-targets.yml` | ⬜ 未运行 |
| 安装包构建 | `build-*.yml` | ⬜ 未运行 |
| Android APK 构建 | `reusable-android.yml` | ⬜ 未运行 |

**「未运行」一律不得写成「通过」。** 这是设计基线的硬要求：
未执行的检查只有一个合法状态，就是「未执行」。上表后三行仍然如此。

从首次推送到全绿共用了 10 轮 CI，每轮都暴露出成批、彼此无关的缺陷。
它们有一个共同特征：**本机的检查全部读磁盘、做静态匹配，
而这些都是「运行或编译时才会暴露」的问题**——
路径解析、类型转换、算法语义、IPv6 解析、事件类型绑定。

能在本机发现的，都已经补成了检查（跟踪文件、CMake 相对路径、
YAML 块标量、文档链接、工作流结构）。其余只能靠 CI。

---

## 4. 完全没有做过的验证

| 未做的事 | 后果 |
|---|---|
| 在任何真实设备上运行过 | 无法确认任何运行时行为 |
| 连接过任何真实的 Xboard 面板 | 面板适配层的字段映射全部来自对参考源码的阅读 |
| 拉起过真实的 Xray / sing-box 进程 | 命令行参数与退出码来自对核心源码的阅读 |
| 让核心检查过任何生成的配置 | 编译器输出是否被核心接受，完全未知 |
| 验证过系统网络接管在真机上生效 | Windows 注册表写入与 GNOME 设置写入都未执行过 |
| 验证过核心下载与 SHA256 校验 | 锁文件未冻结，该路径从未被触发 |
| 验证过 Android 的 VpnService | 未在设备或模拟器上安装过 APK |

简言之：**这是一份设计完整、但尚未与现实接触过的代码库。**

---

## 5. 已知的设计级风险

这些不是「还没做」，而是**做完了也可能有问题**的地方：

| 风险 | 说明 |
|---|---|
| 能力表未实测 | `shared/compiler` 的能力表标记为 `verifiedAgainst: "unverified"`，依据是对核心源码的阅读而非实测。界面会如实提示。 |
| 核心版本未冻结 | `versions.lock.json` 是占位文件。发布构建会被拒绝，测试构建不附带核心。 |
| vcpkg 首次构建耗时 | Windows GUI 首次构建需要从源码编译 wxWidgets，可能超出默认超时。 |
| arm64 runner 可用性 | `ubuntu-24.04-arm` 仅对公开仓库免费。 |
| 交叉语言契约只比对定义 | 协议两侧的**定义**一致，不代表**行为**一致。 |
| 单实例锁的平台差异 | `wxSingleInstanceChecker` 在各平台的行为未实测。 |

---

## 6. 如何自己复现这些检查

```powershell
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
cd <仓库根>

& .\tools\scripts\check-go-imports.ps1
& .\tools\scripts\check-go-symbols.ps1
& .\tools\scripts\check-cpp-structure.ps1
& .\tools\scripts\check-workflows.ps1
& .\tools\scripts\check-ipc-contract.ps1
& .\tools\scripts\check-i18n-keys.ps1
& .\tools\scripts\validate-json.ps1 -Path design
& .\tools\scripts\validate-client-config.ps1
```

编译与测试请推送到 GitHub 后查看 Actions。

---

## 7. 相关文档

- [架构说明](architecture.md)
- [上传与构建](upload-and-build.md)
- [design/plan/STATUS.md](../../../design/plan/STATUS.md)：阶段与完成度
- [design/plan/environment-constraints.md](../../../design/plan/environment-constraints.md)：为什么本机不能编译
