# 共享业务模块（Go）

本目录是客户端的共享 Go module，供桌面 IPC 服务、Android 绑定与（可选）headless 服务复用，避免多份实现。

## 目录

| 路径 | 职责 | 状态 |
|---|---|---|
| `shared/panel/` | Xboard 面板适配：登录、取订阅链接、账户信息、面板探测 | ✅ 已实现 |
| `shared/domain/` | 规范领域模型（节点/传输/TLS/Reality） | ✅ 已实现 |
| `shared/subscription/` | 订阅拉取、格式探测、节点 URI 解析 | ✅ 已实现（URI 列表；YAML/JSON 待补） |
| `shared/compiler/` | Xray / sing-box 配置生成与能力校验 | ✅ 已实现（首轮四协议） |
| `shared/secrets/` | 系统密钥存储抽象与脱敏 | ✅ 已实现 |
| `shared/i18n/` | 多语言 | 待实现 |
| `runtime/coreupdater/` | 核心安装、SHA256 校验、原子替换与回滚 | ✅ 已实现 |
| `runtime/core/` | 核心进程生命周期、原生配置检查、日志转发 | ✅ 已实现 |
| `runtime/coordinator/` | 连接事务、状态机、网络恢复与所有权校验 | ✅ 已实现 |
| `runtime/ipc/` | UI ⇄ 服务的版本化本地 JSON RPC | ✅ 已实现 |
| `runtime/`（其余） | — | — |
| `platform/` | 平台特权操作与桥接 | 待实现 |

## 已实现：shared/panel

严格对应 [`design/contracts/panel-api-contract.md`](../../../../design/contracts/panel-api-contract.md) 与
[`design/schemas/panel/panel-dto.schema.json`](../../../../design/schemas/panel/panel-dto.schema.json)。

| 文件 | 内容 |
|---|---|
| `envelope.go` | 统一响应信封解析、错误分类（`ErrorKind`）与结构化 `Error` |
| `client.go` | HTTP 传输：URL 校验、超时、响应大小上限、认证头校验、错误映射 |
| `operations.go` | 4 个操作：`Login` / `GetSubscriptionDescriptor` / `GetAccount` / `DiscoverPanel` |
| `types.go` | DTO：`Credentials` / `Session` / `Account` / `Plan` / `SubscriptionDescriptor` / `PanelIdentity` |
| `client_test.go` | 契约测试：信封、错误分类、URL 校验、敏感头拒绝 |

### 设计要点

1. **不持有凭据**：`Client` 不保存会话，会话由调用方每次显式传入，便于集中交给 SecretStore。
2. **区分两种 token**：`Session.SessionToken`（会话，用于 Authorization）与 `Session.SubscriptionToken`（订阅 token）语义不同，禁止混用。
3. **订阅链接直接使用**：`SubscriptionDescriptor.SubscribeURL` 必须原样用于拉取订阅，不得自行拼接。
4. **错误按分类处理**：调用方依据 `Error.Kind` 决策，不得解析服务端 `message` 文案。
5. **拒绝凭据型请求头**：`WithCustomHeaders` 会拒绝 `Authorization` / `Cookie` / `Proxy-Authorization`。
6. **不自动请求头像**：`Account.AvatarURL` 仅作为字段提供，客户端不得自动加载（会造成外联泄漏）。

## 已实现：shared/compiler

把规范节点模型编译为两个核心各自的配置。生成器相互独立，不共用配置结构。

| 文件 | 内容 |
|---|---|
| `compile.go` | 请求/报告类型、统一入口 `Compile`、字段级 `Issue`、默认出口选择规则 |
| `capability.go` | 核心能力矩阵与节点校验（不兼容项产生 `transport.type` 等字段级错误） |
| `xray.go` | Xray 配置生成（含 streamSettings / realitySettings / tcpSettings） |
| `singbox.go` | sing-box 配置生成（含 transport / tls / utls / reality） |
| `compile_test.go` | 契约测试：能力差异、字段不静默丢弃、出站 tag 不重复、策略改变默认出口 |

### 核心能力差异（已按源码核实）

| 项 | Xray | sing-box | 依据 |
|---|---|---|---|
| `xhttp` 传输 | 支持 | **不支持** | Xray `infra/conf/transport_internet.go:20`；sing-box `option/v2ray_transport.go:15` 枚举无 xhttp |
| TCP 上的 HTTP 伪装头 | 支持（`tcpSettings`） | **不支持** | Xray 有 tcpSettings；sing-box 无对应字段 |
| HTTP/2 | `network: h2` 或 `http` | `transport.type: http` | Xray `:33`；sing-box 枚举只有 `http` |
| REALITY serverName 位置 | `realitySettings.serverName` | `tls.server_name` + `tls.reality` | 两核心结构不同 |

### 重要声明

`capability.go` 中的能力表标记为 `verifiedAgainst: "unverified"`：这些边界来自参考源码与官方文档，
**尚未针对锁定版本实测**。G4 必须用固定版本的官方二进制逐项复核并把结论回写。
在此之前，任何「某核心不支持 X」都按 error 处理——宁可拒绝，也不静默产出可能错误的配置。

## 已实现：shared/secrets

凭据存储契约与统一脱敏规则（R15）。

| 文件 | 内容 |
|---|---|
| `secrets.go` | `Store` 接口、固定 `Key` 集合、命名空间隔离、`Redact`/`RedactURL`、`Scrubber` |
| `secrets_test.go` | 契约测试：短值整体掩码、多字节安全、服务器隔离、不可用时拒绝降级 |

关键约定：

1. **不提供"列出全部凭据"的接口**，缩小泄漏面。
2. **平台无安全存储时报错，绝不退回明文**（`ErrUnavailable`），由界面提示用户选择仅保留会话或启用加密保险库。
3. **按服务器命名空间隔离**：`NamespacedStore` 保证切换面板后读不到旧服务器凭据，`ClearNamespace` 只清理自己的键。
4. **脱敏按字符而非字节截断**，避免产生非法 UTF-8；长度 < 12 的值整体掩码。
5. **`Scrubber` 是最后一道防线**：把已知凭据字面量从核心输出、HTTP 错误、第三方报错中清除，长字面量优先替换。

## 已实现：runtime/coreupdater

核心二进制的安装与更新（ADR-0003：官方预构建 + 客户端内置更新）。

| 文件 | 内容 |
|---|---|
| `coreupdater.go` | `Manifest`/`Release`/`Asset`、下载与校验、原子替换、回滚、版本探测 |
| `coreupdater_test.go` | 契约测试：拒绝无摘要产物、摘要不匹配不破坏现有版本、回滚、平台无匹配产物 |

关键约定：

1. **只按锁定清单升级，绝不追踪 latest**——否则不可复现、不可审计。
2. **SHA256 强制**：`Asset.Validate()` 在摘要缺失或格式错误时直接拒绝；下载后再次比对，不匹配即丢弃。
3. **先校验再替换**：验证在备份之前完成，因此失败的更新不会破坏现有可用版本（有测试守着）。
4. **临时文件与目标同目录**，保证 `rename` 是同一文件系统内的原子操作。
5. **保留上一版本**（`.previous`）供回滚；回滚时把当前版本改名为 `.failed` 而不是删除，便于诊断。
6. **不执行下载内容中的任何脚本**；版本探测只运行 `core version` 并宽松提取版本号，解析失败就报"版本未知"，不猜。

尚未实现：清单生成（由构建阶段产出）、平台安全存储实现、UI 侧的更新进度与失败提示。

## 已实现：runtime/core

核心子进程的生命周期管理。**职责边界**：只管配置检查、启动/停止、健康探测与日志转发；
不编译配置（`shared/compiler`）、不接管系统网络（`platform`）、不决定何时连接（`coordinator`）。

| 文件 | 内容 |
|---|---|
| `adapter.go` | 命令行事实表、`CheckConfig`、`Start`/`Stop`、`ConfigError`、环形日志缓冲、`WaitForListen` |
| `process_unix.go` | Unix 进程组与 `SIGTERM` 优雅终止 |
| `process_windows.go` | Windows 终止方式与其**固有限制** |
| `adapter_test.go` | 契约测试：命令行事实、环境错误与配置错误区分、环形缓冲、健康探测超时 |

命令行依据（已核实）：

| 核心 | 配置检查 | 运行 | 配置错误退出码 |
|---|---|---|---|
| Xray | `xray run -test -c <file>` | `xray run -c <file>` | **23**（`main/run.go:86`） |
| sing-box | `sing-box check -c <file>` | `sing-box run --disable-color -c <file>` | 非 0 |

关键约定：

1. **区分环境错误与配置错误**：二进制不存在不是「配置错误」，两者用户提示与重试策略不同（有测试守着）。
2. **启动前不重复做配置检查**：调用方先 `CheckConfig`，避免每次连接事务重复付出启动开销。
3. **拒绝重复 Start**：已有进程在跑时报错，而不是静默覆盖。
4. **日志走环形缓冲**：固定容量，避免高日志级别下内存无界增长（§9 轻量要求）。
5. **日志转发前脱敏**：传入 `Scrubber` 后，核心输出中的凭据会被替换。
6. **`WaitForListen` 不是连通性证明**：它只表示可以进入下一步健康检查。设计基线明确要求
   「保存配置、进程存在或端口开放都不能单独代表代理已经可用」。

**Windows 的重要限制**：Windows 没有可由普通进程发送的优雅终止信号，只能直接结束进程。
因此**不能依赖核心自身完成网络清理**，系统代理/路由/DNS 的恢复必须由客户端负责，
并由持久化恢复日志保障。这条已写入代码注释，必须同步到用户文档。

## 已实现：runtime/coordinator

把编译、核心、网络串成**一次事务**，是「单一连接事务」约束的落点。

| 文件 | 内容 |
|---|---|
| `coordinator.go` | 状态机、代次、`Connect`/`Disconnect`/`Recover`、恢复日志、网络计划 |
| `coordinator_test.go` | 25+ 组测试（假实现驱动，不需要真实核心与网络） |

状态机与设计基线 §7.2 一致：
`stopped → validating → starting_core → preparing_network → connecting → running → stopping → restoring → stopped`；
失败进入 `recovering`，恢复完成 `failed`，无法恢复 `recovery_required`。

关键约定：

1. **同一时刻只有一个事务**：并发调用返回 `ErrBusy`，不排队也不并发提交。
2. **代次保护**：每步都校验 generation，过期结果返回 `ErrSuperseded` 而不是覆盖新状态。
3. **顺序不可颠倒**：
   - 接管网络前必须**核心已就绪**（否则会断网）；
   - 断开时**先停核心再恢复网络**（否则系统会指向不存在的代理）。
   两个顺序都有专门的顺序断言测试。
4. **失败路径按需恢复**：只有**确实改过网络**时才写恢复日志并恢复。
   若编译阶段就失败却写一条空日志，会覆盖上次崩溃遗留的记录，导致真正需要恢复的设置丢失
   —— 这是实现过程中发现并修掉的真实缺陷，已有回归测试守着。
5. **恢复带所有权校验**：`Restore` 同时收到「原值」与「我们写入的值」；
   实现应逐键比较当前值，相等才写回，不等则报冲突。**冲突不强行覆盖**，交由用户处理。
6. **恢复失败不假装成功**：进入 `recovery_required` 并在状态中给出原因。
7. **`local_proxy` 不接管任何系统设置**；`planForMode` 对未知模式返回空集合，不猜。
8. **系统代理不写通配地址**：即使监听 `0.0.0.0`，写入系统代理的也必须是环回地址。

本包**不直接**调用操作系统、不启动进程、不编译配置：它通过 `Compiler`、`CoreRunner`、
`NetworkManager`、`Journal` 四个接口协作，平台实现由调用方注入。这样状态机可以在没有真实
核心与真实网络的情况下被完整测试。

## 已实现：runtime/ipc

桌面 UI 与后台服务之间的本地 IPC。**不开放公网 HTTP 管理端口**。

| 文件 | 内容 |
|---|---|
| `protocol.go` | 版本常量、操作枚举、请求/响应/事件、统一错误结构、句柄、能力与状态类型 |
| `server.go` | 监听、连接处理、令牌与版本校验、事件广播与背压、句柄文件 |
| `client.go` | 连接、并发调用与响应配对、事件分发、常用操作的类型化封装 |
| `ipc_test.go` | 20+ 组测试（本地套接字，不支持时跳过而非伪装通过） |

线协议要点：

1. **两层版本**：`ProtocolVersion`（线协议）与 `SchemaVersion`（载荷结构）独立演进，
   任一不匹配都**明确拒绝**，不做尽力解析。
2. **令牌校验在最前**：未授权请求连版本与能力信息都拿不到，也不会投递到业务层（有测试守着）。
3. **报文按行分隔并强制长度上限**：用 `ReadBytes` 而不是 `json.Decoder`，
   避免异常客户端用一条超长报文吃光内存。
4. **统一错误结构**：`code` / `messageKey` / `fieldPath` / `retryable` / `operationId` /
   `detailsRedacted`；界面文案由客户端按 `messageKey` 本地化。
5. **背压可见**：事件队列满时丢弃最旧事件并累加计数，把丢弃数附在下一条事件上；
   客户端本地队列溢出也单独计数。**两者相加才是真正丢失的总量**，不会静默少几条日志。
6. **句柄文件权限收紧**：以 `0600` 写入（含本地访问令牌），关闭时删除，
   且先写临时文件再改名，避免客户端读到写了一半的句柄。
7. **单读取协程分发**：客户端可以并发调用，响应按 ID 配对，调用方无需处理报文交叉。

尚未实现：Windows 命名管道监听器（`ListenerFactory` 已留出注入点；
默认使用 Unix 域套接字）、`WatchEvents` 的订阅语义细化。

## 本机不编译：CI 必须执行的检查

本机没有 Go 工具链（见 [`design/plan/environment-constraints.md`](../../../../design/plan/environment-constraints.md)），
因此以下检查**尚未在本地执行**，必须在 GitHub Actions 中运行：

| 检查 | 命令 | 状态 |
|---|---|---|
| 格式 | `gofmt -l .`（应无输出） | `not_run` |
| 静态检查 | `go vet ./...` | `not_run` |
| 单元与契约测试 | `go test ./...` | `not_run` |
| 竞态检查 | `go test -race ./...` | `not_run` |

> 由于本地无法运行 `gofmt`，结构体字段对齐是手工维护的；若 CI 报告格式差异，以 CI 结果为准并修正。

## 依赖

当前所有包**只使用标准库**，无第三方依赖，因此没有 `go.sum`。
引入任何后续依赖时必须同时更新 `deploy/targets/versions.lock.json`。

## module 路径与 Go 版本

`go.mod` 刻意保持最小内容（只有 `module` 与 `go` 两行）：

- **module 路径**：正式仓库地址尚未确定，暂用占位名 `proxyclient`。
  待 GitHub 仓库确定后应改为真实路径（例如 `github.com/<owner>/<repo>/src/versions/0.1.0/src`），
  并同步更新所有 import。**改名前不应发布正式构建。**
- **Go 版本**：核心（Xray / sing-box）按 ADR-0003 以官方预构建二进制分发，
  不参与本 module 编译，因此这里不受核心自身 Go 版本要求约束。
  实际使用的版本以 `deploy/targets/versions.lock.json` 的冻结值为准，由 CI 校验。
- 之所以不在 `go.mod` 里写注释：`go mod tidy` 会重写该文件，
  注释可能被丢弃，从而让 CI 的 tidy 检查产生无意义的 diff。说明放在这里。
