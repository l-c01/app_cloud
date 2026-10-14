# 公共客户端配置 / Public client configuration

填写 `client-config.json` 即可集中准备软件名称、面板地址及默认客户端设置。严格 JSON，不支持注释、尾逗号或环境变量自动展开。配套 `client-config.schema.json` 是后续实现的结构契约。目前尚无软件读取器，不会因为修改此文件立即产生安装包。

Edit client-config.json for public identity, panel addresses and client defaults. It is strict JSON with no comments, trailing commas or automatic environment expansion. The accompanying schema describes the planned loader; no application loader or installer is implemented yet.

## 最少需要填写 / Minimum setup

| 字段 / Field | 含义 / Meaning | 何时填写 / When |
|---|---|---|
| project.repositoryUrl | 源码仓库地址 / Source repository URL | 建好 GitHub 仓库后 |
| project.license | 项目许可证标识 / Project license identifier | 选定后填写；**留空前不得引入 GPL 代码**（见 ADR-0002） |
| branding.applicationName | 默认应用名 / Default application name | 命名后；当前模板值为 `default` |
| branding.displayNames.zh-CN / en | 两种语言显示名称 / Localized names | 可选；空值使用 applicationName |
| branding.publisher | 发布者 / Publisher | 正式打包前 |
| branding.applicationId | 桌面稳定身份 / Stable desktop identity | 正式打包前，反向域名格式 |
| branding.androidPackageId | Android applicationId/package identity | 正式打包前，反向域名格式；需经 Android 构建校验 |
| panel.apiBaseUrl | 面板 API 基础 URL / Panel API base URL | 接口联调前；不含密码/token |
| panel.webBaseUrl | 用户面板网页 URL / User panel web URL | 需要打开网页业务时 |

默认 API 和全部网页链接留空；空 API 只关闭面板请求，不影响独立第三方订阅。文件无需填入账号密码。品牌链接与 API 分开设置，不推测站点路径。端口为可修改默认值，后续启动必须检查冲突并返回明确错误。

All service URLs are empty by default. An empty panel URL disables panel requests while keeping independent subscriptions available. No account password is required in this file. Links are separate from the API. Runtime port collision checks remain required.

## 字段分组 / Field groups

| 分组 / Group | 内容 / Contents |
|---|---|
| project | 项目身份：源码仓库地址、许可证标识 |
| branding | 软件名、语言显示名、发布者、应用身份、品牌/帮助/隐私/条款链接、本地图标资源路径 |
| panel | API/网页基础地址、适配档案、前缀、超时、HTTP 测试开关、公共请求头和已核实的接口覆盖 |
| panelLinks | 注册、找回密码、购买、续费、文档网页；首轮可使用这些网页入口 |
| clientDefaults | 模式/记住/语言/路由、启动行为、订阅更新、监听、平台接入方式、Android 应用范围、TUN/PAC |
| testSettings | 诊断界面开关、API 编辑入口、用户自填测试目标地址 |

`apiProfile=auto` 只是计划中的适配档案选择，不承诺任何未知 Xboard 都能自动识别。`apiPathPrefix` 和 `endpointOverrides` 先留空，审计上传源码后再填写。覆盖只能引用已验证的客户端 operation，不能通过配置调用管理员或节点管理 API；接口响应 DTO 和认证语义仍由版本化 adapter 负责，JSON 不能代替代码适配。

`apiProfile=auto` is a planned adapter selector, not universal compatibility. Keep prefix and endpointOverrides empty until the supplied source is audited. Overrides are limited to verified user operations and do not replace authentication/DTO code or enable administrator/node APIs.

`customHeaders` 只放公共且非敏感参数，例如公开的客户端标识；不能存 Authorization、Cookie、密码或订阅 token。认证由 SecretStore 和 PanelAdapter 处理；后续加载器必须拒绝敏感 header 覆盖，不能靠模板文字代替运行时校验。

Public headers cannot contain Authorization, Cookie or credentials. Authentication belongs to secure runtime storage; the future loader must enforce this restriction.

`subscriptionRefreshMinutes=0` 表示默认手动更新。`androidAppScope` 的 include/exclude 互斥；包名列表在建立 VPN 前检查，改变时重新建立 VPN。`pac.geoImports` 空列表表示不加载默认外部资源；每个条目需 source/category/sha256，经实际文件格式检查后导入。不得为快速测试开启默认外部下载。

Zero subscription-refresh minutes means manual updates. Android app lists are mutually exclusive and require validation/re-establishment. Geo imports are opt-in verified resources with source/category/hash; no default download is configured.

## 配置生效与身份稳定性 / Application and identity

计划优先级：内置安全回退 → 此 JSON 的公共默认值 → 用户界面保存的个人覆盖。JSON 不覆盖已存在的个人节点/订阅/秘密，升级不重置用户配置。模板变更不会自动改变已安装应用身份。

名称、发布者、图标、应用 ID/Android 包名由打包阶段读取；包名和签名是稳定升级身份，正式发布后不能随意修改。包名留空可在开发探针阶段使用明确标注的内部测试身份，正式发布 preflight 必须要求填入。图标路径限制在项目资源目录，不允许任意文件路径。

运行期 API/网页地址和个人代理参数应可在客户端设置页编辑，保存到用户配置覆盖。API 更换必须隔离原账号凭据，不向新主机发送旧 token。系统语言默认读取设备设置，手动覆盖可选。`testTargetUrl` 留空时不主动发探测请求；选定 URL 后也不能仅凭目标能打开判定所有路由/TUN 正确。

Planned precedence: safe built-in fallbacks, public JSON defaults, then saved user overrides. Package identity/icon/publisher settings are consumed at build time; stable IDs and signing identities must remain stable after release. Runtime panel and proxy settings remain editable and persist separately. Server changes isolate credentials. Empty test-target URLs perform no automatic network probe.

## 不放在这个文件中的信息 / Exclusions

登录密码、会话 token、带 token 的订阅 URL、正式节点秘密、Android keystore/密码、GitHub token、Windows 签名密钥不进入此公共文件。发布时签名凭据使用用户自己的 GitHub Secrets；运行时秘密使用客户端安全存储。

核心/toolchain 版本、源码 commit、目标 ABI、最低 OS/SDK、签名方式由构建锁文件和目标清单定义，不能通过公共配置绕过兼容检查。用户负责实机验证，但未验证状态必须保留，不能写为 passed。

Passwords, session/subscription tokens, signing keys and repository tokens do not belong here. Build contracts and minimum OS/ABI requirements remain in locked build manifests, not public configuration switches. User-owned physical testing stays unverified until results exist.
