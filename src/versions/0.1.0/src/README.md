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
| `runtime/`（其余） | 连接协调器、核心适配器、IPC | 待实现 |
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

当前 `shared/panel` **只使用标准库**，无第三方依赖，因此没有 `go.sum`。
引入任何后续依赖时必须同时更新 `deploy/targets/versions.lock.json`。
