# 面板接口契约（收敛版）/ Panel API contract (narrowed)

本文件是客户端实现面板功能的**唯一依据**。所有事实均逐条读取 `src/references/Xboard-master/` 源码得到，附文件路径与行号。

状态：已核实（源码级）· 未在真实服务器验证（`not_run`）
适用范围：客户端首轮。工单、订单支付、邀请返佣、礼品卡、知识库不在范围内。

---

## 0. 范围总览

客户端链路：**登录 → 取订阅链接 → 编译核心配置 → 连接**。

面板只承担前两步，因此只实现 4 个接口：

| # | 用途 | 方法 | 路径 | 认证 | 必需 |
|---|---|---|---|---|---|
| 1 | 登录 | POST | `/api/v1/passport/auth/login` | 无 | 必需 |
| 2 | 取订阅链接 | GET | `/api/v1/user/getSubscribe` | Bearer | 必需 |
| 3 | 账户信息与会话校验 | GET | `/api/v1/user/info` | Bearer | 必需 |
| 4 | 面板身份探测 | GET | `/api/v1/guest/comm/config` | 无 | 可选（推荐） |

---

## 1. 统一响应信封

所有接口（除订阅正文外）使用统一 JSON 信封。

```json
{
  "status": "success" | "fail",
  "message": "string",
  "data": <any | null>,
  "error": <any | null>
}
```

| 事实 | 说明 | 源码定位 |
|---|---|---|
| HTTP 状态码来源 | 取内部 code 的前三位，例如 `[400, ...]` → 400 | `src/references/Xboard-master/app/Helpers/ApiResponse.php:44-55` |
| 响应体不含 code | `'code'` 字段被注释掉，只有 `status` / `message` / `data` / `error` | 同上，`:50` |
| 失败也会是 HTTP 4xx/5xx | 客户端不能只看 HTTP 状态码，必须同时读 `status` 与 `message` | 同上 |
| 未登录的失败 | 抛 `ApiException('未登录或登陆已过期', 403)`，经 Handler 走 `fail` 信封 | `app/Http/Middleware/User.php:22-25`；`app/Exceptions/Handler.php:64-68` |

**实现要求**：解析层先按信封解包；`status != "success"` 时用 `message` 生成业务错误，并把 HTTP 状态码作为辅助信息保留。

---

## 2. 认证语义

| 事实 | 内容 | 源码定位 |
|---|---|---|
| 认证栈 | Laravel Sanctum，personal access token | `app/Http/Middleware/User.php:22` |
| 请求头 | `Authorization: Bearer <auth_data 去掉前缀后的串>` | `app/Services/AuthService.php:27-34` |
| 登录返回的 `auth_data` | 形如 `Bearer <plainTextToken>`（已剔除 Sanctum 的 `id\|` 前缀） | `app/Services/AuthService.php:28-34` |
| token 有效期 | 服务端创建时设为 `now()->addYear()` | `app/Services/AuthService.php:24` |
| 是否存在 refresh token | **不存在**。过期即需重新登录 | 全景审计未发现 refresh 端点 |
| 登录返回的 `token` 字段 | **不是**登录凭据，而是用户的**订阅 token**（`users.token`） | `app/Services/AuthService.php:32` |

**实现要求**：

1. `auth_data` 原样存入 SecretStore，作为 Authorization 头使用，不要自行拼接或剥离 `Bearer`。
2. `token` 与 `auth_data` 语义不同，字段命名必须区分：建议 `sessionToken`（auth_data）与 `subscriptionToken`（token）。
3. 无 refresh 机制，收到 401/403 时直接引导重新登录；不要把旧凭据发往新主机。

---

## 3. 接口明细

### 3.1 登录

```
POST /api/v1/passport/auth/login
```

| 项 | 内容 |
|---|---|
| 认证 | 无 |
| 请求体 | `email`（string，必填）、`password`（string，必填） |
| 校验规则 | 见 `app/Http/Requests/Passport/AuthLogin.php` |
| 响应 data | `token`（订阅 token）、`auth_data`（Bearer 串）、`is_admin`（boolean） |
| 失败 | 账号或密码错误等，走 `fail` 信封，`message` 为服务端文案 |
| 源码定位 | `app/Http/Controllers/V1/Passport/AuthController.php:72-85` |

**注意**：

- 登录不属于幂等操作，服务端每次登录都会新建一个 token 记录。客户端在已登录状态下重复登录前，应先确认是否需要清理旧会话。
- `is_admin` 只用于展示，不得据此开启任何管理功能（客户端不访问管理员 API）。
- 服务端可能开启人机验证；本轮登录接口未见 captcha 参数，若实际面板要求，需补充审计。

### 3.2 取订阅链接

```
GET /api/v1/user/getSubscribe
Authorization: Bearer <auth_data>
```

| 项 | 内容 |
|---|---|
| 认证 | Bearer（Sanctum） |
| 请求参数 | 无 |
| 响应 data 字段 | 见下表 |
| 失败 | `[400, "The user does not exist"]`、`[400, "Subscription plan does not exist"]` |
| 源码定位 | `app/Http/Controllers/V1/User/UserController.php:131-162` |

响应 `data` 字段：

| 字段 | 类型 | 说明 | 敏感 |
|---|---|---|---|
| `subscribe_url` | string | **完整订阅链接**，由服务端拼接，可能带查询参数 | **是** |
| `token` | string | 订阅 token | **是** |
| `uuid` | string | 协议用户标识 | **是** |
| `email` | string | 邮箱 | 是（个人信息） |
| `plan_id` | number \| null | 当前套餐 ID | 否 |
| `plan` | object \| null | 套餐对象，仅当 `plan_id` 存在时附加 | 否 |
| `expired_at` | number \| null | 到期时间（unix 秒；0 或 null 视为长期有效） | 否 |
| `u` | number | 已用上传字节 | 否 |
| `d` | number | 已用下载字节 | 否 |
| `transfer_enable` | number | 总额度字节 | 否 |
| `device_limit` | number \| null | 设备数限制 | 否 |
| `speed_limit` | number \| null | 限速值 | 否 |
| `next_reset_at` | number \| null | 下次流量重置时间 | 否 |
| `reset_day` | number \| null | 距重置天数（服务端计算） | 否 |

**实现要求**：

1. `subscribe_url` 直接作为订阅拉取地址使用，**不要**自行拼接 `/api/v1/client/subscribe?token=...`——服务端拼接方式可能含额外参数，自行拼接会在部分部署下失效。
2. `subscribe_url`、`token`、`uuid` 三者必须走 SecretStore；日志、崩溃报告、导出配置中一律脱敏。
3. `expired_at` 为 0 或 null 表示长期有效，不要显示为「1970 年过期」。

### 3.3 账户信息与会话校验

```
GET /api/v1/user/info
Authorization: Bearer <auth_data>
```

| 项 | 内容 |
|---|---|
| 认证 | Bearer（Sanctum） |
| 响应 data 字段 | `email`、`transfer_enable`、`last_login_at`、`created_at`、`banned`、`remind_expire`、`remind_traffic`、`expired_at`、`balance`、`commission_balance`、`plan_id`、`discount`、`commission_rate`、`telegram_id`、`uuid`、`avatar_url` |
| 失败 | `[400, "The user does not exist"]`、未登录 403 |
| 源码定位 | `app/Http/Controllers/V1/User/UserController.php:88-114` |

**用途**：既是账户展示的数据源，也是**会话有效性探测**的首选端点——无副作用，返回字段明确，比 `checkLogin` 信息更全。

**注意**：

- `avatar_url` 是服务端按 email 的 md5 拼出的 gravatar 地址。客户端**不得**自动请求它（会造成外联泄漏），仅在用户明确点击头像时才加载，或直接显示本地生成的首字母头像。
- `banned`、`remind_expire`、`remind_traffic` 在服务端是数值，客户端需按布尔语义解释。
- `uuid` 敏感，同 3.2。

### 3.4 面板身份探测（可选）

```
GET /api/v1/guest/comm/config
```

| 项 | 内容 |
|---|---|
| 认证 | 无 |
| 响应 data | 面板公共配置字段（14 个），可被服务端插件增删 |
| 源码定位 | `app/Http/Controllers/V1/Guest/CommController.php`；路由 `app/Http/Routes/V1/GuestRoute.php:24` |

**用途**：`discoverPanel`。用户填写 API 地址后，调用此接口判断地址是否为 Xboard，并读取站点公共配置。

**实现要求**：

- 必须容忍未知字段与字段缺失（插件可增删），解析器不得因未知字段报错。
- 探测失败不等于地址错误，需区分：DNS/连接失败、非 JSON 响应、JSON 但非 Xboard 信封。
- 探测请求**不得**携带任何凭据。

---

## 4. 订阅内容拉取（第二条链路）

`subscribe_url` 与面板 API 是两条独立链路：前者返回节点内容，后者返回 JSON。

| 事实 | 内容 | 源码定位 |
|---|---|---|
| 端点 | `GET /api/v1/client/subscribe` | `app/Http/Controllers/V1/Client/ClientController.php:34-52` |
| 认证 | 订阅 token 通过 query 参数 `token` 传递；服务端按 `users.token` 查用户 | `app/Http/Middleware/Client.php:21-28` |
| 可选参数 `types` | 协议过滤，`\|`、`,`、`｜` 分隔；空或 `all` 表示全部 | `ClientController.php:93-105` |
| 可选参数 `filter` | 关键词过滤，匹配节点名（不区分大小写）或 tags；长度 > 20 被忽略 | `ClientController.php:107-120` |
| 可选参数 `flag` | 客户端标识，也可由 `User-Agent` 提供；决定服务端输出格式 | `ClientController.php:148-191` |
| **返回不是 JSON 信封** | 按 flag 匹配到的协议类输出：Base64 节点列表 / Clash YAML / sing-box JSON 等 | `ClientController.php:66-87` |
| 用户不可用 | 返回 **403 且 body 为空**（套餐到期、流量用尽、封禁等，由 `UserService::isAvailable` 判定） | `ClientController.php:46-49` |
| 伪节点注入 | 服务端可能在前几条插入「剩余流量」「套餐到期」「距下次重置剩余」等伪节点 | `ClientController.php:193-221` |

**实现要求**：

1. 403 空 body 必须映射为**具体业务错误**（到期 / 流量用尽 / 被封禁），不能显示为「网络错误」。
2. 必须过滤伪节点：它们的特征是复用首个真实节点的字段、仅替换 `name`。建议按「服务端注入的固定前缀模式」过滤，并在解析报告中记录被过滤条数。
3. 客户端应按已选核心主动声明 `flag`，避免服务端返回需要额外转换的格式。
4. 限制响应体大小、节点数、解析深度与超时，防止恶意或异常订阅拖垮客户端。

---

## 5. 错误处理约定

| 场景 | 客户端行为 |
|---|---|
| HTTP 401 / 403 且来自面板 API | 引导重新登录；保留已有配置，不清空第三方订阅 |
| HTTP 403 且来自订阅链接（空 body） | 显示具体业务原因（到期/限额/封禁），提示前往面板处理 |
| HTTP 429 | 遵守服务端退避提示，不做高频重试 |
| HTTP 5xx | 标记面板业务不可用，保留可用配置继续工作 |
| 网络不可达 | 区分为「地址错误」「网络不通」「非 Xboard 服务」三种提示 |
| 写操作失败 | 登录外的写操作本轮不涉及；订阅拉取失败保留上一次可用快照 |

**禁止**：把服务端 `message` 当作可编程判断依据（文案会随语言与版本变化）；把未知错误统一显示为「网络错误」。

---

## 6. 未核实项

| 项 | 状态 | 下一步 |
|---|---|---|
| `AuthLogin` 的完整校验规则 | 未读取 Request 类 | 需要时补读 |
| 登录是否需要 captcha | 本轮接口未见该参数 | 有测试环境后实测 |
| `guest/comm/config` 的 14 个字段逐项含义 | 未逐条展开 | 需要时补读 |
| 真实服务器上的信封与错误码 | `not_run` | 获得测试环境后执行 T03 |
| 服务端对 `subscribe_url` 的拼接细节 | 依赖 `Helper::getSubscribeUrl`，未读取 | 需要时补读，但实现上直接使用返回值即可 |
