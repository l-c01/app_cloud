# Xboard 接口审计 · 客户端实现范围收敛

本文件确认**客户端实际需要实现的面板接口范围**。完整事实清单见 [`xboard-api-map.json`](../contracts/xboard-api-map.json)，逐条细节见 [`xboard-api-audit-part2.md`](xboard-api-audit-part2.md)。

## 结论：面板侧只需实现 3 个接口

客户端的核心链路是「登录 → 取订阅链接 → 编译配置 → 连接」。面板只承担前两步。

| 用途 | 方法 | 路径 | 认证 | 关键字段 | 源码定位 |
|---|---|---|---|---|---|
| **登录** | POST | `/api/v1/passport/auth/login` | 无 | 请求 `email`、`password`；响应 `auth_data`（Bearer 串）、`token`（订阅 token）、`is_admin` | `src/references/Xboard-master/app/Http/Controllers/V1/Passport/AuthController.php:72-85`；`app/Services/AuthService.php:18-36` |
| **取订阅链接** | GET | `/api/v1/user/getSubscribe` | Bearer | 响应 `subscribe_url`（完整订阅链接，含 token）、`u`/`d`/`transfer_enable`、`expired_at` | `src/references/Xboard-master/app/Http/Controllers/V1/User/UserController.php:131-162` |
| **校验会话 / 账户** | GET | `/api/v1/user/info` | Bearer | `email`、`transfer_enable`、`u`、`d`、`expired_at`、`banned`、`plan_id`、`uuid` | `src/references/Xboard-master/app/Http/Controllers/V1/User/UserController.php:88-114` |

`uuid`、`token`、`subscribe_url` 三者均为秘密，必须走 SecretStore，不入日志、不入导出。

## 可选：面板身份探测

| 用途 | 方法 | 路径 | 认证 | 说明 |
|---|---|---|---|---|
| 判断填写的地址是不是 Xboard，并读取站点公共配置 | GET | `/api/v1/guest/comm/config` | 无 | 无副作用、无认证，是 `discoverPanel` 的最佳探测点；插件可增删字段，客户端必须容忍未知字段 |

## 明确不做的接口

以下接口已在审计中核实存在，但**不属于客户端首轮范围**，实现时应显式不调用，避免无谓的契约维护成本：

- 工单：`ticket/fetch`、`ticket/save`、`ticket/reply`、`ticket/close`、`ticket/withdraw`
- 订单与支付：`order/*`（含 `checkout`，其响应不走标准信封）、`coupon/check`、`gift-card/*`
- 邀请与返佣：`invite/*`、`user/transfer`、`user/getStat`（其中含邀请计数）
- 公告与知识库：`notice/fetch`、`knowledge/*`（若简易模式需要展示公告，再单独纳入）
- 注册与找回密码：`passport/auth/register`、`passport/auth/forget`、`passport/comm/sendEmailVerify`
  —— 首轮按设计基线改为「打开用户自填的面板网页」完成
- 客户端自助更新：`client/app/getVersion`、`client/app/getConfig`
  —— 本项目是独立客户端，不依赖面板分发；`getConfig` 能力受限（仅 ss/vmess/trojan）

## 订阅内容获取（不走面板 JSON 信封）

拿到 `subscribe_url` 后，客户端直接请求该链接拉取节点内容。这是与面板 API 分开的第二条链路。

| 事实 | 内容 |
|---|---|
| 端点 | `GET /api/v1/client/subscribe` |
| 认证 | 订阅 token 通过 query 参数 `token` 传递（服务端按 `users.token` 查用户） |
| 可用参数 | `types`（协议过滤，`\|`、`,`、`｜` 分隔）、`filter`（关键词，长度 > 20 被忽略）、`flag`（客户端标识，也可由 `User-Agent` 提供） |
| 返回格式 | 按 `flag` 匹配协议类决定：Base64 节点列表 / Clash YAML / sing-box JSON 等。**不是 JSON 信封。** |
| 用户不可用 | 返回 **403 且 body 为空**（套餐到期、流量用尽、封禁等），客户端必须映射为具体业务错误 |
| 陷阱 | 服务端可能在前几条注入伪节点（剩余流量 / 套餐到期 / 重置天数），客户端必须过滤 |
| 源码定位 | `src/references/Xboard-master/app/Http/Controllers/V1/Client/ClientController.php:34-88`、`:193-221` |

**实现建议**：客户端按已选核心主动声明 `flag`（例如 sing-box 语境走 `sing-box`、Xray 语境走 `meta`/`v2ray`），避免服务端返回需要额外转换的格式。

## 下一步

1. 把上述接口在 `design/contracts/xboard-api-map.json` 中标注 `scope: "in_scope"`，其余标注 `scope: "out_of_scope"`。
2. 为这 4 个接口编写 DTO Schema（`design/schemas/panel/`）。
3. 生成脱敏固件样例（`src/versions/<版本>/tests/fixtures/panel/`），用于离线契约测试。
