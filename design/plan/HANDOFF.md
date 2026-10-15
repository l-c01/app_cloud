# 交接文档 / Handoff

> **给新会话的入口文档。** 开新会话后先读这一份，再按需读
> [`STATUS.md`](STATUS.md)（阶段状态权威来源）与
> [`../../design/architecture/ui-options.md`](../architecture/ui-options.md)（选项设计）。

更新时间：2026-10-16 · 当前阶段：**两端界面已接通并各自构建通过；剩下平台接管与真机验证**

---

## 0. 一句话现状

**两个平台的界面都接通并各自构建通过了 —— 这是本项目第一次。**

| 验证 | 结果 |
|---|---|
| Go（本地 `go build/vet/test ./...`） | 通过 |
| CI checks（7 个作业，含 C++ 两个构建） | **7/7 通过** |
| `Build Android`（手动入口，arm64-v8a） | **通过**（此前从未编译过 Kotlin） |

「登录 → 取订阅 → 编译 → 连接」的后端主链在 `runtime/session` 里，
IPC 有 8 个会话/订阅操作，两端界面都已接上：桌面端有登录页、节点列表、
简易/高级七页选项；Android 有登录页、节点列表、会话面板、
简易/专业模式。文案中英各齐全（`check-i18n-keys` 237 个引用键全命中）。

**仍然不是「能用的成品」**，差的正是「真的接管流量」：

- **Windows 系统代理已接通**（`netmgr/windows.go` + wininet 刷新通知）；
  但**从未在真实 Windows 上跑过**。
- **Android 的 TUN 仍未交给核心**：方案已定（[`ADR-0006`](../adr/ADR-0006-Android-核心集成方式.md)：
  libbox/gomobile AAR + 自实现 `PlatformInterface`），**实现未开始**，
  需要 NDK 与真机，本机都还没有。**VPN 能建立但仍然不代理流量。**
- 从未连接过真实面板、从未拉起真实 sing-box。全部运行期结论仍是 `not_run`。

### 一个容易踩的坑：Android 不在 push 触发的 CI 里

`.github/workflows/build-android.yml` **只有 `workflow_dispatch`**，
而 `ci.yml` 不编译 Kotlin。也就是说**push 全绿并不代表 Android 能编译**——
本项目已经吃过这个亏：一批约 3000 行的 Kotlin 在「CI 全绿」的状态下
被提交，首次手动触发构建才暴露出 57 个编译错误。

改完 Android 后必须手动触发：

```powershell
# 用 GitHub API 触发（本机没有 gh CLI）
Invoke-WebRequest "https://api.github.com/repos/<owner>/<repo>/actions/workflows/384624012/dispatches" `
  -Method POST -Headers $headers -ContentType 'application/json' `
  -Body '{"ref":"main","inputs":{"version_dir":"src/versions/0.1.0","abi":"arm64-v8a","build_type":"test"}}'
```


### 本机现在能编译 Go 了（重要）

工具链在工作区本地 `D:\workai\toolchain\go`（不写 PATH、不写注册表，
不要了直接删目录）。用 `tools/scripts/go-local.ps1`：

```powershell
& .\tools\scripts\go-local.ps1 -Command vet  -Args ./...
& .\tools\scripts\go-local.ps1 -Command test -Args ./...
& .\tools\scripts\go-local.ps1 -Command fmt
```

**Go 改动不必再靠 CI 往返。** C++ 与 Kotlin 仍然只有 CI 能证明。

---

## 1. 已完成（附证据）

### 1.1 三平台产物构建 —— 完成并核对

| 产物 | 大小 | 核对方式 |
|---|---|---|
| Android APK `app-debug.apk` | 9,457,149 字节<br>SHA256 `45ae25359264fca0a6df537327355008e5f1cf6ff19d579c8e78484c091d4e8d` | 下载后解压，确认 `lib/arm64-v8a/`、`classes*.dex`、`assets/locales/{en,zh-CN}.json`、`assets/config/client-config.json` 都在 |
| Linux `linux-amd64.tar.gz` | 2,231,607 字节<br>SHA256 `03d645616b7e32232b8a7e3f2be381f9b41572bcf84bfa0fc849c486c63db8f1` | 解压确认含 `proxyclient-desktop`、`proxyclient-service`、`locales/`、`config/`、`CORES-NOT-BUNDLED.txt` |
| Windows `windows-amd64.zip` | 2,193,163 字节<br>SHA256 `13bcd2f1600d08b87b6e1efac75d40e247e4b218aa2f5492caa7ada7035189e8` | 解压确认含 `proxyclient-desktop.exe`(505,344)、`proxyclient-service.exe`、`locales/`、`config/` |

产物下载在 `output/builds/`（已 gitignore）。

### 1.2 单内核收敛 —— 完成，CI 全绿

按 [ADR-0005](../adr/ADR-0005-单内核-sing-box.md) 把「Xray / sing-box 二选一」
收敛为「只用 sing-box」。三个批次，每批都通过 CI 7/7：

```
第一批  编译器 + 各包常量（21 文件）
第二批  文案 + 目标清单 + 能力表 + 测试夹具
第三批  遗留清理（25 文件）
```

已删除的符号：`compiler.CoreXray`、`coreupdater.CoreXray`、`core.KindXray`、
`shared/compiler/xray.go`、`sources.xray`、全部 `xrayAssetArch`、
文案键 `compiler.unknown_core` / `ui.label.core` / `ui.label.not_installed`、
`client-config.json` 与 schema 的 `core` 字段。

### 1.3 设计文档 —— 完成

- [`design/adr/ADR-0005-单内核-sing-box.md`](../adr/ADR-0005-单内核-sing-box.md)
- [`design/architecture/ui-options.md`](../architecture/ui-options.md) ← **下一阶段的主要依据**
- 中英架构文档第 6 节已由「双核心」改写为「单内核与能力表」

---

## 2. 未完成 ← 从这里继续

### 2.1 选项实现（主体工作：后端已基本完成）

用户的要求（原文）：

> 客户端选项比较简陋了，高级模式应该借鉴一下 clash verge 和 clash for android
> 内核选一个用吧，不要双内核了，对简易和高级模式都进行选项增补，
> 简易模式是选节点连接，搞个规则模式和分应用代理即可，
> 高级模式类似 clash 客户端，要全面完整的配置选项

**要做什么已经逐项写在 [`ui-options.md`](../architecture/ui-options.md) 里**，
含每一项对应的 sing-box 字段与默认值。建议按这个顺序做：

| 步骤 | 范围 | 说明 |
|---|---|---|
| **B1** | Go 后端：`shared/domain` 选项模型 + `shared/compiler` 生成 | **基本完成**（见 2.2）。已完成：选项模型、DNS、TUN+分应用、route 规则、代理组、规则集、嗅探。**未完成：混合端口、启动项（见 2.2b）** |
| **B2** | IPC 契约：`runtime/ipc/protocol.go` + C++ `apps/desktop/src/core/ipc_protocol.{hpp,cpp}` | **完成**：新增 8 个会话/订阅操作，两侧已对齐，见 2.5 |
| **B2b** | 主链后端：`runtime/session`（凭据 → 编译请求） | **完成**，见 2.5。这是此前**完全没有**的一块 |
| **B3** | 桌面界面：`apps/desktop/src/ui/pro_panel.{hpp,cpp}`（高级）+ `simple_panel`（简易）+ 登录/节点 | **进行中** |
| **B4** | Android 界面：`apps/android/.../ui/ProxyApp.kt` | **进行中** |
| **B5** | i18n 文案：`locales/{zh-CN,en}.json` 两侧同步 | 会话/订阅的 25 个键已补；界面新增文案待补 |
| **B6** | 双语文档同步 | 待办 |
| **B7** | 平台网络接管：Windows 系统代理/TUN、Android TUN 交给核心 | **未开始**，见 2.6 |

**下一步：B3/B4 的收尾，然后 B7。** 主链后端已通，剩下的价值几乎都在
「让它真的能用」——界面接上、以及平台真的接管流量。

### 2.5 主链后端（本次新增，最重要的一块）

此前 `shared/panel`、`shared/subscription`、`shared/compiler` 各自都有实现与测试，
**但没有任何东西把它们串起来**，IPC 契约里也没有登录与取订阅。
界面只能连接一份已经构造好的配置——这不是一个客户端。

新增 `runtime/session` 包：

| 文件 | 职责 |
|---|---|
| `session.go` | 登录/登出/恢复、拉订阅、列节点、选节点、读写选项、**构造编译请求** |
| `manager.go` | 持有「当前是哪台面板的会话」，地址变化时重建 |
| `classify.go` | 把 panel 与 subscription 的错误词汇表翻译成「用户该做什么」 |
| `errors.go` | 分类与文案键；`ScrubURL` 处理路径里带 token 的订阅链接 |

几个刻意做的判断，改动前请先读它们的注释：

- **重新拉订阅后若已选节点消失，清空选择并提示**，而不是留着不存在的 ID
  让编译器悄悄回退到第一个节点——那会造成「界面显示 A、实际连 B」
- **面板地址变化时绝不复用会话**：否则新面板的凭据会写进旧面板的命名空间
- 订阅请求显式带 `flag=general`，向 Xboard 要节点 URI 列表；
  否则可能拿到尚未实现的 Clash YAML
- 凭据存储目前用 `secrets.NewUnavailableStore()`（平台安全存储尚未落地），
  它会让界面如实提示「本次可用，但不会记住登录」

IPC 新增 8 个操作：`Login` `Logout` `GetSessionState` `RefreshSubscription`
`ListNodes` `SelectNode` `GetOptions` `SetOptions`。
`SessionState` 与 `NodeView` **刻意不含任何凭据**，并有测试断言这一点。


### 2.2 进度：后端配置生成层已完成的各块

每一块都单独通过 CI 7/7：

| 批次 | 内容 | 提交 |
|---|---|---|
| 选项模型 | `shared/domain/options.go`——整套选项的数据结构 + `DefaultOptions()` | `674d689` |
| DNS | 生成 `dns` 段：fake-ip、名称服务器（**用稳定标签不用下标**）、过滤列表 | `cfca1cc` |
| TUN + 分应用 | TUN 入站 + `include_package`/`exclude_package`（**互斥**，走取值方法） | `dd8f752` |
| route | 用户规则 + 兜底动作；**内建规则一律在用户规则之前** | `695557d` |
| 代理组 | `selector` / `urltest` 出站；**负载均衡不生成**而是报告（sing-box 无等价物） | `9d57bdf` |
| 规则集 | `route.rule_set`（仅 remote）+ 规则引用规则集时的存在性核对 | 见 2.3b |
| 嗅探 | 入站层 `sniff` / `sniff_override_destination`；协议列表明确报告不生效 | 见 2.3 |
| TUN DNS 缺陷修复 | 去掉不存在的 `dns_hijack`，改用真实的 `dns_mode` / `dns_address` | 见 2.3c |

**这一批（规则集 + 嗅探 + 缺陷修复）已通过 CI 7/7**；
本机 `10/10` 只是文本级检查，不代表能编译。过程中 CI 连续暴露了三类问题，
都是**本机检查看不见**的：注释与 `func` 挤在同一行导致语法错误、
两条测试用错策略（global 而非 rule）导致断言恒空、
gofmt 要求去掉注释里成对的反引号。三次都已修掉，
每条教训都写进了第 4 节的坑表。

`Request.Options` 是**指针且允许为 nil**：nil 表示"调用方尚未接入选项"，
此时生成的配置与接入前逐字节一致。这是让选项能**逐块接入**的关键——
不必一次性改完所有调用方与既有测试。

### 2.2c 写测试时的一个坑（这次踩了两次）

规则集与用户规则**只在规则模式下生成**（global / direct 都不看规则）。
因此凡是断言 `route.*` 的测试**不能**复用那个用 global 策略的
`compileWithOptions`——否则断言看到的是空列表，看起来像「生成器坏了」，
实际是测试没走到那条路径。已加 `compileInRuleMode` 专供这类用例。

### 2.6 平台网络接管（B7，尚未开始）——「能连上」的真正门槛

**这一块不做，前面所有工作都只是「配置生成正确」。**

#### Windows

`platform/winproxy` 已经能写注册表，写入后的 wininet 刷新通知
（TODO(R05)）**已补上**：`notify_windows.go` 调 `InternetSetOptionW`
两次（SETTINGS_CHANGED 让系统重读设置、REFRESH 让已加载 wininet
的进程重建连接状态）。只调其一会表现为「新开的程序走代理、
已经开着的浏览器不走」这类很难归因的现象。

**但真正的问题不是缺通知，而是整个包没有调用点。**
`platform/winproxy` 有 444 行测试、CI 全绿、代码也写对了，
可是**没有任何代码 import 它**：`platformBackend()` 在 Windows 上
落到 `NoopBackend`，系统代理接管是**死代码**。
已新增 `platform/netmgr/windows.go` 把通用键翻译成 winproxy 的键并接通，
`platformBackend()` 加了 windows 分支。

**教训（很重要）**：一个只有测试、没有调用点的实现是最容易骗过人的状态。
该包自己的测试全过，CI 全绿，端到端功能却不存在。
判断「某功能是否可用」要顺着**调用链**查到起点，
而不是看那个包的测试是否通过。见第 4 节坑表。

#### Android：这里有一个**必须先决定**的问题（不要猜）

`apps/android/.../vpn/ProxyVpnService.kt` 目前能把 VpnService 建立起来、
能拿到 TUN 的 `ParcelFileDescriptor`，但**没有把它交给核心**，
所以**VPN 能建立，却不会代理任何流量**。

难点不在代码而在一个尚未决定的设计问题：Android 上 sing-box 拿到 TUN fd 的方式，
取决于核心以什么形式集成：

| 路线 | 含义 | 代价 |
|---|---|---|
| AAR 内嵌（gomobile） | 核心作为库跑在应用进程内，直接把 fd 交给它 | 需要写一层 Go 绑定并在 CI 里构建 AAR；Android 侧才能拿到 `startVpnService(fd)` 这类入口 |
| 预构建二进制 + 进程 | 独立进程 + `--vpn-service` / fd 继承 | 需要核心支持从 fd 接管，且要处理进程生命周期与权限 |
| tun2socks 桥接 | 应用内把 TUN 流量转成 SOCKS 交给核心 | 多一层转发、多一份依赖，性能与故障面都变差 |

**这个问题已经查清并决定了，见 [`ADR-0006`](../adr/ADR-0006-Android-核心集成方式.md)。**
要点：**采用 AAR（gomobile）内嵌**，并自己实现 sing-box 的
`adapter.PlatformInterface`——它的 `OpenInterface(options *tun.Options, ...)`
就是 fd 的入口，`AutoDetectInterfaceControl(fd)` 是回调
`VpnService.protect(fd)` 的挂载点。

两条查证出来的关键事实（此前被忽略）：
- **fd 不是配置里的键**：参考实现里它是 `LC.Tun{FileDescriptor: fd}` 这样的
  **API 参数**（`ClashMetaForAndroid/core/.../native/tun/tun.go:54-65`），
  因此「fd 怎么进去」取决于用哪个 Go API，而不是生成什么 JSON；
- **`option/tun.go` 里没有 `file_descriptor` 字段**（逐字段核对过），
  所以「在配置里加一个 fd 字段」既不可能也不必要。

**但实现仍未开始，Android 的 TUN 必须继续标记为未实现。**
ADR 里列了实现前的 4 项检查清单（其中第一项是「对着真实 SDK 确认
libbox 到底导出了哪些入口」——本仓库的参考源码里**没有** sing-box 的
Android 客户端实现，只有文档目录），以及代价：需要 gomobile 与 NDK、
CI 变慢、**本机完全没有 JDK/NDK/SDK/真机因此无法验证**。

不要把「VPN 能建立」写成「Android 能用」。

### 2.2b 后端仍缺的两项（都有明确原因，不是遗漏）

**① 混合端口。** `Options.Inbound.MixedPort` 在模型里，但生成器仍按
`Request.Inbound.HTTPPort` / `SocksPort` 生成**两个分开的入站**
（`http-in` + `socks-in`），而不是 `ui-options.md` 2.4 节要求的
一个 `type: "mixed"` 入站。

**没有顺手改掉，是因为它会强制所有调用方和既有测试一起改**
（`Request.Inbound` 的两个端口字段、IPC 契约、C++ 镜像、以及
断言这两个入站的测试）。这属于「一次性切换」，不是能增量接入的一块，
需要单独一批做完并单独验证。

**② 启动项。** `Options.App`（开机自启、启动最小化、自动连接、主题等）
**不属于配置生成**——它们不产生任何 sing-box 字段，是应用自身的行为，
要接到 `apps/desktop/src/app/settings.{hpp,cpp}` 与 Android 侧。
放在编译器里做才是错的。

`ui-options.md` 2.5 节里「覆写目的地址」对应的
`route.rules[].action: sniff` **尚未生成**，只生成了入站层的
`sniff_override_destination`（两者在 sing-box 里是不同层面的东西，
见 2.3）。

### 2.3 嗅探：原先悬着的问题**已用源码解决**（并已实现）

`ui-options.md` 的嗅探一节写了「嗅探协议 TLS / HTTP / QUIC」，
但 sing-box 的**入站**嗅探是否接受协议列表，此前无法确认，因此没有实现。

**这次直接读了参考源码，问题解决了。**
依据：`src/references/sing-box-testing/option/inbound.go`

```
SniffEnabled             bool               `json:"sniff,omitempty"`
SniffOverrideDestination bool               `json:"sniff_override_destination,omitempty"`
SniffTimeout             badoption.Duration `json:"sniff_timeout,omitempty"`
```

**结论：入站层只有上面三个字段，协议列表确实不在入站里。**
它住在**路由动作**里 —— `option/rule_action.go` 的 `RouteActionSniff`：

```
Sniffer badoption.Listable[string] `json:"sniffer,omitempty"`
        enum:"tls,http,quic,dns,stun,bittorrent,dtls,ssh,rdp,ntp"`
```

而路由动作需要单独一条 `action: "sniff"` 的规则才生效。

因此现在的做法是：**忠实生成入站层的 `sniff` 与 `sniff_override_destination`，
并对协议列表明确报告「本版本不生效」**——
既不塞进入站（会被核心当成未知字段拒绝启动），也不静默丢弃
（用户会以为按 tls 嗅探已经生效）。

需要在界面上把这一点写清楚，否则它就是设计文档批评过的「伪选项」：
用户改了协议列表，而它没有作用。

路由动作形式的 `sniffer` 列表尚未生成，已作为后续项记在下面。

### 2.3b 规则集：同样的方式解决

规则集字段依据 `option/rule_set.go`，几条容易做错的地方已按源码实现：

| 事实 | 处理 |
|---|---|
| `type` 取 `inline` / `local` / `remote` | 只生成 `remote`（见下） |
| `format` 取 `source` / `binary` | 显式值先校验，非法值报错跳过而不是照抄 |
| `format` 留空时核心按 `url` 后缀推断（`.srs`→binary，`.json`→source） | 留空时我们按同样规则推断并写死；推断不出来则**不猜**，报明确警告 |
| 多标签时才要求 URL 含 `{tag}` 占位符 | 单标签，不需要占位符 |
| `update_interval` 是时长字符串 | 写成 `"%dh"` |

**为什么只生成 `remote`**：`local` 规则集要的是核心进程能读到的一条
本地文件路径，而「规则集文件放在哪」属于运行时布局（runtime 层的事），
不是界面选项。没有 `path` 就编一个 `local` 规则集，只会生成一份
核心启动时找不到文件的配置。因此本地规则集明确跳过并写进报告。

### 2.3c 顺带发现并修掉的一个**真实缺陷**（重要教训）

在核对 TUN 字段时发现：**`dns_hijack` 这个字段在 sing-box 里根本不存在。**

- 原来 `domain.TUNSettings.DNSHijack` 默认 `["any:53"]`，生成器直接写成
  `in["dns_hijack"] = ...`，而 `ui-options.md` 里也把它当成既定事实写着。
- 核实依据：`option/tun.go` 的 `TunInboundOptions` 里只有
  `dns_mode`（enum `disabled,native,hijack`）与 `dns_address`
  （`badoption.Listable[netip.Addr]`，**只接受 IP 不含端口**）。
- **后果**：核心对未知字段是拒绝启动的，也就是**每一份开了 TUN 的配置都起不来**。
- 已改为生成 `dns_mode` / `dns_address`，原语义（劫持 DNS 到核心自己的
  DNS 模块）正好就是 `dns_mode: "hijack"`，功能没有损失。
  并加了回归测试 `TestTUNDNSModeUsesRealFields`，它同时断言
  「不该有的键不存在」——只断言新键存在是拦不住旧键被加回来的。

**教训：字段名「看起来很合理」正是它一直没被发现的原因。**
从源码里挑字段时，必须**确认那个名字真的出现过**，而不是照着一个
像命名惯例的名字推断。这类错误在文本级检查里完全看不出来——
10 项本机检查当时（以及现在）全是绿的。

**接手的人应当怀疑其他未经核对的字段。** 已经逐字段核对过的有：
出站协议字段、TLS、transport、DNS 段、TUN 入站、分应用、嗅探、
代理组、规则集、route 规则。**尚未逐一核对**的包括
`ui-options.md` 2.6 / 2.7 两节（订阅与应用设置——它们不生成 sing-box
配置，风险较低）。

### 2.4 一次失败的尝试（避免重复）

曾把整个后端（B1）委派给一个子代理，**运行很多轮零代码产出**，最终取消。
后来又切小了一次（只做 DNS + TUN + 分应用），**同样零产出、无收尾报告**。

**当时的结论是「这类 Go 后端改动不要再委派」。这条结论已部分作废**——
见下面的更新。

子代理在文档整理、跨包机械清理上表现很好（单内核收敛的三个批次都是它们做的），
但在"需要新增代码并自己验证"的任务上反复失败。

原因当时是推测的：它们无法编译，只能靠检查脚本判断，而检查脚本不报错时
它们可能陷入反复确认。反过来，机械性的替换/删除有明确的完成信号。

**2026-10-15 更新：本机装上了工作区本地的 Go 之后，这个推测成立了一半。**
那两次失败期间，检查脚本确实**不报错就代表成功**——因为它们只能做文本级检查。
现在子代理可以自己跑：

```powershell
& .\tools\scripts\go-local.ps1 -Command test ./...
```

也就是**有了明确的完成信号**。因此：

- **Go 改动的委派可以重新尝试**，但委派说明里必须明确要求
  子代理自己跑 `go-local.ps1 -Command vet/test`，并把输出作为交付证据；
- 仍然不要委派「需要判断设计取舍」的改动——那两次失败里也混着
  「不知道要交付什么」的问题，不只是编译；
- 交付报告仍要**当证据看**：自评成功时自己复核（第 3.4 节）。

---

## 3. 工作方式与约束（重要）

### 3.1 环境

- 仓库根：`D:\workai\xbaord`，**只在这个目录内改文件**
- **本机已有 Go（2026-10-15 起）**：`D:\workai\toolchain\go`，**工作区本地、不写 PATH、
  不写注册表**，不要了直接删目录。用 `tools/scripts/go-local.ps1` 调用：
  ```powershell
  & .\tools\scripts\go-local.ps1 -Command vet  ./...
  & .\tools\scripts\go-local.ps1 -Command test ./...
  & .\tools\scripts\go-local.ps1 -Command fmt            # 等价于 CI 的 gofmt 检查
  ```
  它自己设 `GOROOT`/`GOCACHE`/`GOMODCACHE`/`GOTOOLCHAIN=local`，目录都在
  `D:\workai\toolchain` 下。**`go build`/`go test` 现在能本地跑**，
  Go 侧的改动不必再靠 CI 往返。
- **本机仍然没有 CMake / JDK / Android SDK，也没有 MSVC**
  （`vswhere` 与 `C:\Program Files\Microsoft Visual Studio` 都不存在；
  此前「桌面端能编译」是 CI 的 Linux runner 做的，不是本机）。
  因此 **C++ 与 Kotlin 仍然只能靠 CI**。
- Shell 是 Windows PowerShell 5.1；`git` 在 `C:\Program Files\Git\cmd\git.exe`（**不在 PATH**）
- **`github.com:443` 与 `dl.google.com` 不可达**；`api.github.com`、
  `go.dev`、`proxy.golang.org`、`mirrors.aliyun.com` 可用
- 装 Go 时官方下载站（`go.dev/dl`、`dl.google.com/go`）**全部 404**，
  改用 `https://mirrors.aliyun.com/golang/`，并**用官方 JSON 里的 SHA256 校验**后
  才解压——镜像能用不等于字节一致，校验通过才说明一致
- 开工前先 `Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force`

### 3.2 推送

只能用 `tools/scripts/push-via-api.ps1`（通过 Git Data API 重放本地提交）。
不能 `git push`（`github.com` 不可达）。

```powershell
# -Base 是远端已有的提交，-From 是本地要重放区间的**父**提交
& .\tools\scripts\push-via-api.ps1 -Base "<远端HEAD>" -From "<本地父提交>"
```

推送后**必须**核对远端 tree 与本地一致：

```powershell
$rc = Invoke-RestMethod "$api/git/commits/$sha" -Headers $h
$rc.tree.sha -eq (git rev-parse 'HEAD^{tree}')
```

### 3.3 验证

```powershell
& .\tools\scripts\run-all-checks.ps1 -LogPath "output\reports\verification-run-<日期>.txt" -Quiet
```

11 项检查。**其中只有「Go formatting」会编译/解析 Go，且只在装了本地
Go 时运行**；它会报 `SKIP`（不是 PASS）——**SKIP 表示什么都没验证**，
不要把 SKIP 读成通过。

Go 侧真正的验证是这两条（本地可直接跑）：

```powershell
& .\tools\scripts\go-local.ps1 -Command vet  ./...
& .\tools\scripts\go-local.ps1 -Command test ./...
```

**C++、Kotlin、以及 Go 的完整构建仍然只有 CI 能证明。**
push 会触发 `ci.yml`（7 个作业）。

`check-go-symbols.ps1` 在这些纯文本检查里最有用——它能发现「引用了已删除的符号」。

**但这套检查有一个已验证的盲区：它不解析 Go 语法。**
一次改动里 `// 说明。func TestX(...)` 被拼成一行（`func` 落进注释，
函数体成了游离语句），**当时 10 项检查依然全绿**，而 CI 直接失败。
现在这条盲区已由本地 `go vet`/`go test` 覆盖，但**没装 Go 的机器上仍然存在**，
所以那台机器上还应做两件廉价的机械核对：

1. **声明是否在行首**：扫 `\S\s+(func|type)\s+\w+\(`；
2. **注释里是否有成对的反引号**：gofmt 会把它们规范成弯引号，
   于是「本机看起来没问题」的文件在 CI 的格式化作业里失败。

两条都是几行 PowerShell 就能扫的（见第 4 节坑表最后两行）。

### 3.4 省上下文的做法（这个项目验证过有效）

- PowerShell 只打印**结论一行**，明细写进 `output/reports/` 文件
- 需要读大文件的判断一律交给子代理，只要它的结论
- 提交信息一律写文件再 `git commit -F`（直接 `-m` 传多行中文会被 PowerShell 拆坏）
- 子代理的报告要**当证据看，不是当结论看**：它自评失败时产出可能是好的，
  自评成功时也要自己核对
- **本机跑不了 gofmt，但 CI 会给出精确差异。**
  `.github/workflows/ci.yml` 的格式化作业做两件事：把 `gofmt -d` 的前 300 行
  打进日志（不下载任何东西就能看到要改什么），并把补丁作为 `gofmt-fixes`
  产物上传（可下载后 `git apply`）。
  差异小时照日志改更快；差异大时**下载补丁比手工算列数可靠得多**。
  手工按列数算对齐至少错过一次（`options.go` 的名字列宽算成 13，gofmt 要 12）。

---

## 4. 踩过的坑（别再踩）

| 坑 | 表现 | 教训 |
|---|---|---|
| 打包用宽通配符 `proxyclient-desktop*` | 打进去的是 `.slnx` 而不是 `.exe`，**每步都报 success** | 产物必须**下载后打开看内容** |
| 打包步骤混用相对/绝对路径 | `mkdir` 建的目录和 `tar` 写的不是一个 | 统一用绝对路径，别数 `../` |
| Kotlin 块注释**可嵌套** | KDoc 里写 `locales/*.json` 吞掉整个文件，十来个文件报未解析 | 注释里不写「斜杠加星号」 |
| 跨平台步骤没声明 `shell: bash` | Windows 上按 PowerShell 解析 bash 脚本 | 只在单平台成立时最危险 |
| MSVC `/W4 /WX` 下的 `C4996` | `getenv` 被判为不安全 | 这是厂商偏好不是缺陷，关掉 |
| `Start-Process` 复制环境变量 | 父子进程都有 `NO_PROXY`/`no_proxy` 时抛异常 | 改用 `cmd /c ... > file` 读字节 |
| git 提交信息用 `-m` 传多行中文 | PowerShell 转义拆坏消息 | 写文件 + `-F` |
| 测试断言 Windows 模式位 | `runtime/ipc` 与 `journalfile` 两个测试**时好时坏** | 平台不适用要显式 skip |
| **注释与 `func` 挤在同一行** | 编辑时把 `// 说明。func TestX(...)` 拼到一行，`func` 落进注释，函数体成了游离语句；CI 报 `expected declaration, found opts` | **10 项本机检查全绿也发现不了**——它们只做文本级检查，不解析 Go 语法。改完 Go 后要专门扫一遍「声明是否在行首」，不能只信检查脚本 |
| **注释里成对的反引号** | gofmt 把注释中的成对反引号规范成弯引号，格式化作业因此失败 | 本机没有 gofmt 就不该在注释里用成对反引号。`adapter.go` 里那份带列表缩进的没事，别照抄它的形式 |
| **测试用了错误的策略** | 断言 `route.rule_set` 的用例复用了 global 策略的辅助函数，结果恒为空列表，看起来像生成器坏了 | 规则集与用户规则**只在规则模式下生成**。测 route 的用例必须显式用 rule 策略 |
| **写死的计数留在测试文件里** | 新增 8 个 IPC 操作后，`protocol.go`、C++ 镜像都改了，但 Go 与 C++ **测试里写死的操作数**没改。本地全套检查（含 IPC 契约检查）全绿，CI 的 C++ 单测才失败 | 契约检查只读**协议源文件**；计数在**测试文件**里它就看不到。已加 `check-ipc-operation-count.ps1` 同时读三处。**教训：检查器覆盖的是它读的那些文件，不是你以为的那一类问题** |
| **参数位置绑定被静默覆盖** | `go-local.ps1 -Command build ./...` 把 `./...` 绑到了 `-ToolchainDir`，脚本报「找不到工具链」，看起来像环境坏了 | PowerShell 的 `ValueFromRemainingArguments` 会与后面的具名参数打架。改成必须显式 `-Args ./...`，并在注释里写清为什么 |
| **有测试、没有调用点** | `platform/winproxy` 实现完整、444 行测试全过，但**没有任何代码 import 它**；Windows 落在 NoopBackend，系统代理接管是死代码。该包自己的测试永远发现不了 | 判断「功能是否可用」要顺着**调用链**查到起点，而不是看那个包的测试。对每个「我以为做完了」的功能，问一句：**谁调用它**？ |
| **push 全绿 ≠ Android 能编译** | `build-android.yml` 只有 `workflow_dispatch`，`ci.yml` 不编译 Kotlin。约 3000 行 Kotlin 在「CI 全绿」下被提交，首次手动触发才暴露 57 个编译错误 | **Android 必须在提交后手动触发 `Build Android`**。把「哪个平台由哪个入口验证」写进交接文档，否则新会话会以为 push 已经覆盖了 |
| **`receiver type mismatch` 不等于参数类型错** | Kotlin 扩展函数写成裸调用（`putIfNotEmpty(...)` 而函数是 `JSONObject.putIfNotEmpty`）时报 "Unresolved reference ... because of a receiver type mismatch"。我按字面理解成「值的类型不对」，于是去**补了一个重复定义**，反而制造出 "Conflicting overloads" | 这条消息里的 receiver 指**扩展函数的接收者**。看到它先确认函数**是否已存在**、以及调用点有没有给它接收者，别急着新建 |
| **`messages[key, *args]` 是语法错误** | 带 vararg 的 `operator get` 不能在方括号里展开数组，星号被当成乘号（报 "Unresolved reference 'times'"） | 用 `messages.get(key, *args)` |
| **wxWidgets 没有那些 wxID_\*** | 自造 `wxID_LOGOUT` / `wxID_VIEW_OPTIONS` 报 "was not declared in this scope"（它还建议 `wxID_LOWEST`） | 自定义命令 ID 从 `wxID_HIGHEST + 1` 起自己定义，不要从 1 起（1 以下被标准 ID 占用，撞上的表现是菜单触发别的命令） |
| **`wxRadioBox` 没有 `Append()`** | 先建空框再逐个 `Append` 报 "has no member named 'Append'" | 选项必须在**构造时**一次给全（`wxArrayString`），只有 `wxItemContainer` 那一族才有 Append |

---

## 5. 仍未决的用户输入

这些不影响继续开发，但发布前需要：

- 项目许可证
- 产品名 / 发布者 / `applicationId` / `androidPackageId`
- `client-config.json` 里要填的 GitHub 仓库地址
- 参考源码的 commit SHA
- Xboard 测试环境（用于把能力表的 `verifiedAgainst` 从 `unverified` 改成实测）
- Android 签名密钥（release 构建必需；缺失时构建会明确失败）

---

## 6. 关键未验证事项（必须如实保留）

- 全部运行期行为仍是 `not_run`：从未在真机运行、从未连接真实面板、从未拉起真实核心
- **Android 的 VPN 能建立，但不会代理任何流量**（TUN 尚未交给核心）
- 能力表 `verifiedAgainst: "unverified"` —— 来自读源码，不是实测
- 生成的核心配置**从未被真实 sing-box 接受过**

**不要把上面任何一条写成「已验证」。**
