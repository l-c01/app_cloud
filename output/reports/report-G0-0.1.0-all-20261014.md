# G0 审计报告 / G0 audit report

- 用途：阶段关卡证据归档
- 版本：0.1.0
- 目标：全部（设计阶段，无构建目标）
- 日期：2026-10-14
- 状态：G0 进行中（本报告记录截至本日的可核实证据）

> 本报告只记录**实际执行并取得结果**的检查。未执行的检查一律标 `not_run`，不得写成通过。

---

## 1. 本阶段结论摘要

| 项 | 结论 |
|---|---|
| 目录与版本管理 | ✅ 完成（design / src / output 三目录；一仓 + 版本目录 + tag） |
| 参考源码来源登记 | ✅ 完成（5 份快照的版本事实与许可观测） |
| 面板接口契约审计 | ✅ 完成（48 条接口事实，逐条附源码定位） |
| 接口范围收敛 | ✅ 完成（收敛为 3 必需 + 1 可选） |
| 数据契约 Schema | ✅ 完成（面板 DTO + 公共配置） |
| 架构决策记录 | ✅ 4 份（ADR-0001/0002/0003 + 索引） |
| 目标清单 | ✅ 完成（12 目标，与既有设计包一致） |
| 版本锁文件 | 🟡 模板就位，数值未冻结（不伪造） |
| 核心分发方式 | ✅ 已决定（官方预构建 + 客户端自动更新） |
| 应用源码 | ❌ 尚未开始（G1/G2） |
| 构建证据 | `not_run` |
| 运行证据 | `not_run` |

---

## 2. 已执行的检查与结果

所有检查均在本机执行，工具与方法记录如下。

| # | 检查 | 方法 | 结果 |
|---|---|---|---|
| 1 | 设计目录 JSON 结构合法性 | `tools/scripts/validate-json.ps1 -Path design` | ✅ 12 个文件全部合法，0 失败 |
| 2 | 公共配置是否符合其 Schema | `tools/scripts/validate-client-config.ps1` | ✅ PASS，0 问题 |
| 3 | 版本目录 JSON 合法性 | `tools/scripts/validate-json.ps1 -Path src\versions\0.1.0` | ✅ 2 个文件合法 |
| 4 | 目标清单与既有设计包一致性 | 对照 `project-spec.json` 目标 id 集合 | ✅ 12 对 12，无差异 |
| 5 | 必需/可后置目标划分 | 统计 `required` 字段 | ✅ 9 必需 + 3 可后置 |
| 6 | 第三方快照未进入版本历史 | `git ls-files` 与 `.gitignore` 规则核对 | ✅ 受管 47 文件，无 vendor 代码 |
| 7 | 本机工具链能力 | 命令探测 | 见 §4，无编译工具链 |

### 2.1 校验器能力与限制

本机无 Node / Python / Go / CMake / Java，因此 `tools/scripts/` 下的校验器用 PowerShell + .NET 手写，只支持本仓库契约实际用到的 JSON Schema 子集：

- 支持：`type` / `const` / `enum` / `required` / `properties` / `additionalProperties(bool)` / `items` / `minLength` / `maxLength` / `minimum` / `maximum` / `pattern` / 本地 `$ref`
- 不支持（会跳过并打印提示）：`if` / `then` / `else` / `anyOf` / `oneOf` / `not` / `dependentSchemas` / `patternProperties` / `format` 语义断言

**这是一处已知的校验盲区**：`panel-dto.schema.json` 中使用了 `oneOf`（`plan` 字段）与 `allOf`（敏感字段包装），这两处不会被本机校验器验证。计划在 G1 的 CI 中引入完整 JSON Schema 校验器复核。

---

## 3. 面板接口契约（事实摘要）

完整清单见 [`design/contracts/xboard-api-map.json`](../../design/contracts/xboard-api-map.json)，逐条说明见 [`design/contracts/panel-api-contract.md`](../../design/contracts/panel-api-contract.md)。

### 3.1 客户端实现范围

| 用途 | 方法 | 路径 | 认证 | 必需 |
|---|---|---|---|---|
| 登录 | POST | `/api/v1/passport/auth/login` | 无 | 必需 |
| 取订阅链接 | GET | `/api/v1/user/getSubscribe` | Bearer | 必需 |
| 账户与会话校验 | GET | `/api/v1/user/info` | Bearer | 必需 |
| 面板身份探测 | GET | `/api/v1/guest/comm/config` | 无 | 可选 |
| 拉取订阅内容 | GET | `/api/v1/client/subscribe`（由 `subscribe_url` 提供） | query token | 必需 |

### 3.2 已核实的关键事实

| 事实 | 证据来源 |
|---|---|
| 认证为 Laravel Sanctum Bearer，**无 refresh token** | `src/references/Xboard-master/app/Http/Middleware/User.php:22`；`app/Services/AuthService.php` |
| 会话有效期一年（`now()->addYear()`） | `app/Services/AuthService.php:24` |
| 登录返回的 `token` 是**订阅 token**，`auth_data` 才是会话凭据 | `app/Services/AuthService.php:31-35` |
| 统一响应信封为 `{status,message,data,error}`，HTTP 码取内部 code 前三位 | `app/Helpers/ApiResponse.php:44-55` |
| `subscribe_url` 由服务端拼接，客户端不得自行拼接 | `app/Utils/Helper.php::getSubscribeUrl` |
| 订阅接口用户不可用时返回 403 且 body 为空 | `app/Http/Controllers/V1/Client/ClientController.php:46-49` |
| 订阅响应可能含服务端注入的伪节点 | `app/Http/Controllers/V1/Client/ClientController.php:193-221` |
| 节点列表接口**不含**连接所需的 host/port/password | `app/Http/Resources/NodeResource.php:17-27` |

### 3.3 服务端不一致与陷阱（实现时必须处理）

1. `order/checkout` 与 `gift-card/history` 不使用标准信封（本轮不在实现范围，仅记录）。
2. `gift-card/detail` 明文返回兑换码，`history` 却打码 —— 客户端若实现须统一按秘密处理。
3. `plan/fetch` 对「不存在」与「不可用」返回同一 400 与同一文案，无法区分原因。
4. 多处参数无服务端校验，客户端必须自行校验。
5. `user/notice/fetch` 每页固定 5 条，不可配置。

---

## 4. 环境与工具链事实

| 工具 | 状态 | 影响 |
|---|---|---|
| git | ✅ 2.56.0.windows.2（不在 PATH） | 用全路径调用 |
| PowerShell | ✅ 可用（执行策略需进程级 Bypass） | 校验脚本运行方式 |
| Go / CMake / Java / Node / Python | ❌ 均不可用 | **本机无法编译任何目标** |

**结论**：本机不具备编译能力，这与设计基线「构建只在 GitHub Actions 手动触发」一致，不是阻塞项。契约与 Schema 的校验已用现有工具完成。

详细记录见 [`design/plan/environment-constraints.md`](../../design/plan/environment-constraints.md)。

---

## 5. 未执行 / 未核实项（`not_run`）

| 项 | 状态 | 何时可解 |
|---|---|---|
| 与真实 Xboard 服务器的联调（T03/T04） | `not_run` | 用户提供测试环境后（G2 起） |
| 任何目标的编译 | `not_run` | G1，GitHub Actions |
| 任何平台的运行验证 | `not_run` | G1/G3，用户实机 |
| 核心二进制的版本与 SHA256 冻结 | 未冻结 | G0 收尾 / G1 |
| 全量 JSON Schema 校验（引入完整校验器） | `not_run` | G1 CI |
| 许可证正文逐条核对 | 未完成 | G0 收尾（依赖用户选择项目许可证） |
| Android 核心集成方式（AAR vs 预构建） | 未定 | G1 绑定探针 |

---

## 6. 阶段收尾所需的用户输入

| # | 需要的信息 | 影响 | 阻塞程度 |
|---|---|---|---|
| 1 | 项目许可证选择 | 决定 ADR-0002 结论与代码复用边界 | 阻塞 G2 起的源码编写 |
| 2 | 软件正式名称、发布者、桌面 applicationId、Android 包名 | 打包身份、`client-config.json` 填充 | 不阻塞 G1，阻塞 G3 |
| 3 | 5 份参考源码的仓库 URL 与 tag/commit | 锁文件与审计可复现性 | 不阻塞推进 |
| 4 | Xboard 测试环境（地址 + 普通测试用户） | 端到端契约验证 | 不阻塞 G1，阻塞 G2 联调 |

**注意**：不要提供面板管理员密钥、签名私钥或 GitHub token；这些应由用户放入 GitHub Secrets。

---

## 7. 下一步（按顺序）

1. 补齐设计基线中英文文档的接口事实章节（`DESIGN.zh-CN.md` / `DESIGN.en.md`）。
2. 建立 `deploy/workflows/` 七个手动入口与三个可复用工作流的设计骨架（G1 实现）。
3. 开始 `src/versions/0.1.0/src/shared/panel` 与 `shared/domain` 的接口设计与实现（G2）。
4. 建立脱敏固件与契约测试框架（G2）。

---

## 8. 证据归档

| 证据 | 位置 |
|---|---|
| 接口事实清单（机器可读） | `design/contracts/xboard-api-map.json` |
| 接口契约说明 | `design/contracts/panel-api-contract.md` |
| 面板 DTO Schema | `design/schemas/panel/panel-dto.schema.json` |
| 公共配置 Schema | `design/schemas/client-config.schema.json` |
| 来源与许可登记 | `design/audit/source-register.md` |
| 接口审计细节 | `design/audit/xboard-api-audit-part2.md`、`xboard-api-audit-part3.md` |
| 架构决策 | `design/adr/` |
| 目标清单与版本锁 | `src/versions/0.1.0/deploy/targets/` |
| 阶段状态 | `design/plan/STATUS.md` |
| 环境约束 | `design/plan/environment-constraints.md` |

---

*本报告由 G0 阶段工作流生成；任何结论变更须同步更新 `design/plan/STATUS.md` 与相关设计文件。*
