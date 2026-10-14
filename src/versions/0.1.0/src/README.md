# 共享业务模块（Go）

本目录是客户端的共享 Go module，供桌面 IPC 服务、Android 绑定与（可选）headless 服务复用，避免多份实现。

## 目录

| 路径 | 职责 |
|---|---|
| `shared/panel/` | Xboard 面板适配：登录、取订阅链接、账户信息、面板探测 |
| `shared/domain/` | 标准领域模型（Profile / Node / Rule / DNS），待实现 |
| `shared/subscription/` | 订阅拉取与格式解析，待实现 |
| `shared/compiler/` | Xray / sing-box 配置生成与能力校验，待实现 |
| `shared/secrets/` | 系统密钥存储抽象与脱敏，待实现 |
| `shared/i18n/` | 多语言，待实现 |
| `runtime/` | 连接协调器、核心适配器、核心自动更新、IPC，待实现 |
| `platform/` | 平台特权操作与桥接，待实现 |

当前已实现：**`shared/panel`**。

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
