# 阶段计划与当前状态

本文件是项目进度的**唯一权威来源**。任何「已完成」的声明必须在此登记，并附可复核的证据。

更新时间：2026-10-16 · 当前阶段：**两端界面已接通并各自构建通过；生成的配置已用真实核心校验** · 当前版本目录：`src/versions/0.1.0/`

---

## 当前状态总览

| 项 | 值 |
|---|---|
| 阶段 | **配置生成已对真实核心验证**：锁定的 sing-box 1.14.3 接受我们生成的每一份配置 |
| 版本管理 | 一个 git 仓 + `src/versions/<版本>/` + tag（`v0.1.0-alpha.1` … `alpha.4`） |
| 核心分发 | **已决定并落地**：官方预构建 + SHA256 校验 + 原子替换（ADR-0003） |
| 核心版本锁定 | 🟢 **已填实**：sing-box **1.14.3**，12 个目标各自带 sha256 与 URL；Go 锁定 **1.27.2** |
| 应用源码 | 🟢 桌面端与 Android 界面均已接通（不是骨架） |
| 本机可做验证 | 🟢 **12/12 通过**；Go 侧 `build` / `vet` / `gofmt` / `test ./...` 全绿 |
| 编译证据 | 🟢 **CI 8 个作业全绿**（新增 `Real core accepts generated config`），提交 `7ef1945` |
| 编译证据（Android） | 🟢 **`Build Android` 手动入口通过**（arm64-v8a）。**它不在 push 触发的 CI 里** |
| 配置正确性证据 | 🟢 **真实核心实测**：`sing-box check` 接受全部生成的配置（协议/传输/REALITY/uTLS/flow/各选项开关） |
| 运行证据 | 🔴 **仍为 `not_run`**：从未在任何真实设备上运行过，未连接过真实面板，未拉起过真实核心代理流量 |
| 未决阻塞项 | 平台网络接管（Android TUN 交给核心）、项目许可证、软件名与包名 |

### 一句话

**配置生成已经被真实核心验证过；但流量从未真的穿过代理。**

2026-10-16 用锁定的官方预构建 sing-box 1.14.3 逐项复核，查出并修掉
**五个会让核心拒绝整份配置的缺陷**（fake-ip 当默认名称服务器、
缺少 domain_resolver、嗅探用了 1.13 已移除的入站字段等）——
在那之前我们自己的测试全是绿的。现在这批校验由 CI 的
`core-conformance` 作业在每次 push 时重跑。

**仍然不是「能用的成品」**：Windows 系统代理从未在真机跑过，
Android 的 TUN **仍未交给核心**（方案见 [ADR-0006](../adr/ADR-0006-Android-核心集成方式.md)），
也从未连接过真实面板。「配置被核心接受」与「流量能穿过代理」是两件事。


### CI 全绿（2026-10-15，run #10）

| 作业 | 结果 | 证明了什么 |
|---|---|---|
| Go formatting | PASS | 全部 Go 源码符合 gofmt |
| Go static pre-checks | PASS | 跟踪文件、跨包符号、C++ 结构、工作流、文档链接、i18n 键 |
| Go build / vet / test (linux) | PASS | Go 编译通过、`vet` 无问题、全部单元测试通过 |
| Go build / test (windows) | PASS | Windows 专属文件（`//go:build windows`）也编译并测试通过 |
| Desktop core library and tests | PASS | C++ `pcx_core` 编译通过，`ctest` 全部用例通过 |
| Desktop GUI build (linux) | PASS | wxWidgets 可执行文件构建成功（`-Werror`） |
| Design JSON, config schema and i18n | PASS | 设计 JSON、出厂配置、翻译键一致性 |

**仍未证明的**：连接真实 Xboard 面板、生成的核心配置是否被 sing-box 接受、
系统网络接管在真机上是否生效、Android 的 VPN 是否代理流量。
这些都需要运行期验证，见 [验证说明](../../docs/zh-CN/dev/verification.md)。

### 如何自行复核

```powershell
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
& .\tools\scripts\run-all-checks.ps1
```

原始输出会自动写入 `output/reports/verification-run-<时间戳>.txt`。

### 首次 CI 的预期

| 可能失败 | 处理 |
|---|---|
| 格式检查 | 开发机无 `gofmt`，手工对齐可能不完整。下载 `gofmt-fixes` 补丁产物后 `git apply` |
| 编译错误或测试失败 | 约 1.8 万行代码从未编译过，属预期风险 |
| 契约/翻译检查 | 会明确指出哪个键缺在哪个语言里 |

**未执行的检查一律记 `not_run`，不得写成「通过」。**

---

## 产品范围（收敛后）

客户端核心链路四步：**登录 → 取订阅链接 → 编译配置 → 连接**。

面板侧只实现 3 个接口 + 1 个可选探测，详见 [`design/audit/xboard-api-audit-part3.md`](../audit/xboard-api-audit-part3.md)：

| 用途 | 接口 |
|---|---|
| 登录 | `POST /api/v1/passport/auth/login` |
| 取订阅链接 | `GET /api/v1/user/getSubscribe` |
| 校验会话与账户 | `GET /api/v1/user/info` |
| 可选：面板身份探测 | `GET /api/v1/guest/comm/config` |

工单、订单支付、邀请返佣、礼品卡、知识库**不在首轮范围**。

---

## 阶段计划

| 阶段 | 目标 | 交付物 | 通过条件 | 状态 |
|---|---|---|---|---|
| **G0** | 设计收敛、接口契约、来源审计 | api-map、Schema、ADR、目标清单、来源登记 | 没有未知接口被当作既定事实；必需目标都有路线 | **进行中** |
| G1 | 技术探针 | 各架构编译探针、sing-box 连接探针、手动工作流 | 关键架构可编译；首轮平台可连 | 未开始 |
| G2 | 标准模型与业务 | 领域模型、面板适配、订阅解析、i18n | 无面板也可导入第三方订阅；空 API 安全 | 未开始 |
| G3 | 首轮可用 | Windows/Android 简易模式端到端 | 两平台代表架构可用；退出恢复通过 | 未开始 |
| G4 | 完整定制 | TUN/PAC/应用范围、专业模式、原生编辑 | 关键功能无假开关；高级配置不被破坏 | 未开始 |
| G5 | 平台与打包 | Linux 桌面、全必需架构、七个手动入口 | 必需平台构建/运行证据完整 | 未开始 |
| G6 | 文档与收敛 | 中英文开发/使用文档、AI 上下文、升级卸载 | 收敛条件全部满足 | 未开始 |

---

## G0 工作分解

| # | 工作项 | 产物 | 状态 |
|---|---|---|---|
| 1 | 目录与版本管理重构 | `.gitignore`、仓库结构说明、ADR-0001 | **完成** |
| 2 | 参考源码来源登记 | [`design/audit/source-register.md`](../audit/source-register.md) | 完成（commit 待补） |
| 3 | 面板接口契约审计 | [`design/contracts/xboard-api-map.json`](../contracts/xboard-api-map.json)（48 条） | 完成 |
| 4 | 接口范围收敛 | [`design/audit/xboard-api-audit-part3.md`](../audit/xboard-api-audit-part3.md) | 完成 |
| 5 | 首批 ADR | ADR-0001、ADR-0002 | 完成 |
| 6 | 核心分发决策 | ADR-0003（官方预构建 + 客户端自动更新） | **完成（用户决定）** |
| 7 | in-scope 接口 DTO Schema | `design/schemas/panel/panel-dto.schema.json` | **完成** |
| 8 | 公共配置 Schema 复刻与修正 | `design/schemas/client-config.schema.json`（修复 testSettings 缺失） | **完成** |
| 9 | 配置校验脚本 | `tools/scripts/*.ps1`（纯 ASCII，PowerShell+ .NET），`client-config.json` 通过校验 | **完成** |
| 10 | 0.1.0 版本骨架 | `src/versions/0.1.0/`（README + versions.lock.json 模板） | **完成** |
| 11 | 构建目标清单 | `src/versions/0.1.0/deploy/targets/targets.json`（12 目标，与既有设计包一致） | **完成** |
| 12 | 修订设计基线范围与事实错误 | `DESIGN.zh-CN.md`、`DESIGN.en.md`（接口事实、范围收敛、核心分发） | **完成** |
| 13 | G0 审计报告 | `output/reports/report-G0-0.1.0-all-20261014.md` | **完成** |
| 14 | 手动构建入口与可复用工作流 | `.github/workflows/`：7 入口 + 4 可复用 + 1 校验 = 12 个文件 | **完成（未运行）** |
| 14b | CI 静态校验与单元测试 | `.github/workflows/ci.yml`（gofmt/vet/test/race + JSON 校验） | **完成（未运行）** |
| 14c | 工作流位置的约定修正 | `design/architecture/repository-layout.md`（GitHub 只认仓库根 `.github/workflows/`） | **完成** |
| 15 | `shared/panel` 面板适配实现 | `src/versions/0.1.0/src/shared/panel/`（4 接口 + 契约测试） | **完成（未编译）** |
| 16 | Go module 骨架与说明 | `src/go.mod`、`src/README.md` | **完成** |
| 17 | `shared/domain` 标准节点模型 | `src/versions/0.1.0/src/shared/domain/node.go` | **完成（未编译）** |
| 18 | `shared/subscription` 拉取与解析 | 订阅拉取 + 格式探测 + 节点 URI 解析 + 契约测试 | **完成（未编译）** |
| 19 | 订阅格式策略决策 | ADR-0004（URI 列表为主输入；Xboard 无原生 Xray JSON） | **完成** |
| 20 | Clash YAML 导入器 | `shared/subscription` 的 YAML 解析 | 待办（G2） |
| 21 | sing-box JSON 导入器 | `shared/subscription` 的 JSON 解析 | 待办（G2） |
| 22 | sing-box 配置编译器 | `shared/compiler`（sing-box 生成器 + 能力矩阵 + 契约测试） | **完成（未编译）** |
| 23 | 凭据存储与脱敏 | `shared/secrets`（Store 契约、命名空间隔离、Redact、Scrubber + 测试） | **完成（未编译）** |
| 24 | 核心安装与自动更新 | `runtime/coreupdater`（ADR-0003 落地：校验/原子替换/回滚 + 测试） | **完成（未编译）** |
| 25 | 运行期：核心适配器 | `runtime/core`（配置检查、进程生命周期、日志转发 + 测试） | **完成（未编译）** |
| 26 | 连接协调器 | `runtime/coordinator`（状态机、代次、恢复日志、所有权校验 + 25 组测试） | **完成（未编译）** |
| 26b | 平台网络接管实现 | `platform/`（系统代理 / TUN / PAC / VPN） | 待办（G4） |
| 27 | 平台安全存储实现 | Windows DPAPI / Linux Secret Service / Android Keystore | 待办（G4） |
| 28 | UI ⇄ 服务 IPC | `runtime/ipc`（版本化本地 JSON RPC + 令牌 + 背压 + 20 组测试） | **完成（未编译）** |
| 29 | 多语言资源与 i18n 包 | `locales/zh-CN.json`、`locales/en.json`（各 62 键）、`shared/i18n` | **完成** |
| 29b | i18n 约定与自动校验 | `design/architecture/i18n-convention.md`、`tools/scripts/check-i18n-keys.ps1`（已接入 CI） | **完成** |
| 29c | 关闭 i18n 缺口 | 62 个键全部被代码引用；7 个包改为返回文案键（原缺口 35 个键已归零） | **完成（未编译）** |
| 30 | 平台网络接管适配层 | `platform/netmgr`（Backend + Manager + GNOME 后端 + 40 组测试） | **完成（未编译）** |
| 31 | 持久化恢复日志 | `platform/journalfile`（原子写入 + 权限收紧 + 测试） | **完成（未编译）** |
| 32 | Windows 系统代理 | `platform/winproxy`（reg.exe + 注入防护 + 所有权校验；InternetSetOption TODO） | **部分完成** |
| 33 | 桌面端 IPC 协议（Go 的 C++ 镜像） | `apps/desktop/src/core/ipc_protocol.{hpp,cpp}` + 30 余组测试 | **完成（未编译）** |
| 33b | 跨语言 IPC 契约自动校验 | `tools/scripts/check-ipc-contract.ps1`（已接入 CI） | **完成** |
| 34 | 桌面端 IPC 传输层与客户端 | `ipc_transport`、`ipc_client`（后台读取线程 + 事件排队 + 20 余组测试） | **完成（未编译）** |
| 35 | 桌面端界面骨架 | `apps/desktop/src/{main.cpp,ui,app}`（两种模式共用一份上下文） | **完成（未编译）** |
| 35b | 出厂配置与用户偏好分离 | `apps/desktop/src/app/settings.{hpp,cpp}` + 20 组测试 | **完成（未编译）** |
| 36 | Android 骨架 | `apps/android`（Kotlin + Compose + VpnService；**不代理流量**） | **部分完成（未编译）** |
| 37 | 后台服务入口 | `src/cmd/proxyclient-service/main.go`（此前整个模块没有 main 包） | **完成（未编译）** |
| 38 | 打包契约与构建接线 | `deploy/packaging/`、`reusable-desktop.yml` 真实构建（含服务二进制） | **完成（未运行）** |
| 39 | Docker 契约 | `deploy/docker/README.md`（**刻意不创建 Dockerfile**，理由见文档） | **完成（deferred）** |
| 40 | 双语文档 | `docs/zh-CN/`、`docs/en/`：README、架构、验证、上传构建、使用说明 | **完成** |
| 41 | 本机可执行验证全套 | `tools/scripts/`：9 个检查 + `run-all-checks.ps1` | **完成（9/9 通过）** |
| 42 | G5 验证报告 | `output/reports/report-G5-0.1.0-20261015.md` + 原始输出 | **完成** |
| — | Clash YAML 导入器 | `shared/subscription` 的 YAML 解析 | 待办 |
| — | sing-box JSON 导入器 | `shared/subscription` 的 JSON 解析 | 待办 |
| — | 平台安全存储实现 | Windows DPAPI / Linux Secret Service / Android Keystore | 待办 |
| — | TUN / PAC 平台实现 | `platform/` 的 TUN 与 PAC 接管 | 待办 |
| — | Windows 注册表刷新 | `wininet` 的 `InternetSetOption` 调用 | 待办 |
| — | Android 核心绑定 | 决定 AAR 内嵌还是预构建二进制 | 待办（G1 探针） |
| — | 核心版本冻结 | 填写 `versions.lock.json` 的版本与 SHA256 | 待办（用户决定） |
| — | 真机运行验证 | 全部运行期结论目前为 `not_run` | 待办 |

---

## 交付状态（G5 结束时）

| 维度 | 状态 |
|---|---|
| 设计 | **完整**：设计基线、4 份 ADR、接口契约、Schema、目标清单、多语言与打包约定 |
| 源码 | **骨架完整**：Go 14 包（含服务入口）、C++ 桌面端、Kotlin Android 端 |
| 构建接线 | **已接线**：13 个工作流，含真实构建与安装包产出步骤 |
| 文档 | **双语完整**：架构、验证、上传构建、使用说明，中英各一套 |
| 本机验证 | **9/9 通过**（静态预检、契约一致性、JSON/Schema、链接） |
| 编译验证 | **无**（`not_run`） |
| 运行验证 | **无**（`not_run`） |
| 可交付性 | **可以开始手动构建**；不是可发布状态 |

---

## 阻塞与未决项

| 项 | 影响 | 需要谁解决 | 最迟解决节点 |
|---|---|---|---|
| 项目许可证选择 | 决定能否复用 GPL 代码（ADR-0002） | 用户 | G0 收尾 |
| 项目正式名称、发布者、桌面 applicationId、Android 包名 | 打包身份，写入 `client-config.json` | 用户 | G3 前 |
| 5 份参考源码的准确仓库 URL 与 tag/commit | 让审计结果可复现 | 用户 | G0 收尾 |
| Xboard 测试环境（地址 + 普通测试用户） | 验证登录与订阅获取 | 用户 | G2 起 |
| Android 核心集成方式（预构建二进制还是 AAR 内嵌） | 决定 Android 核心集成路线 | G1 探针结论 | G1 |

---

## 状态标记规范

| 标记 | 含义 |
|---|---|
| `not_run` | 未执行，不得写成通过 |
| `planned` | 已计划，尚未开始 |
| `in_progress` | 进行中 |
| `build_passed` | 编译通过（不等于可运行） |
| `runtime_passed` | 在目标平台实际运行并取得证据 |
| `blocked` | 有明确阻塞条件，且已记录 |
| `deferred` | 明确后置，不阻塞当前阶段 |

**禁止**：把架构编译成功写成运行可用；把未运行的测试写成 passed；为通过检查而删除目标或降低验收。
