# G0 来源登记表 / Source register

状态：G0 进行中。本文件只记录**在本地快照中实际读到**的事实；每条均附源码定位。未读到的一律标 `unresolved`。

更新方式：任何新增或替换参考源码，必须在本表追加一行并给出可复核的定位，禁止只改结论。

---

## 1. 参考源码清单 / Supplied reference sources

用户在工作区 `design/` 下提供了 5 份参考源码快照。它们均**不含 `.git` 目录**，因此无法从元数据取得 commit SHA；下表记录可复核的观测事实，SHA 列为待用户补充。

| # | 目录 | 项目 | 观测版本事实 | 源码定位 | commit SHA |
|---|---|---|---|---|---|
| S1 | `design/Xboard-master/` | 面板服务端（Laravel） | 包名 `xboard/xboard`；要求 `php ^8.2`、`laravel/framework ^12.0` | `design/Xboard-master/composer.json`（`name`、`require.php`、`require.laravel/framework`） | unresolved |
| S2 | `design/Xray-core-main/` | 代理核心 Xray | Go module `github.com/xtls/xray-core`；`go 1.27`；版本字符串在构建期注入，源码内为 `unknown` | `design/Xray-core-main/go.mod`；`design/Xray-core-main/app/version/version.go` | unresolved |
| S3 | `design/sing-box-testing/` | 代理核心 sing-box | Go module `github.com/sagernet/sing-box`；`go 1.25.5`；`constant.Version = "unknown"`（构建期注入） | `design/sing-box-testing/go.mod`；`design/sing-box-testing/constant/version.go` | unresolved |
| S4 | `design/clash-verge-rev-dev/` | 桌面 UI 参考（Clash Verge Rev） | `"name": "clash-verge"`，`"version": "2.5.9"`，`"license": "GPL-3.0-only"`；技术栈 Tauri + mihomo（`build: tauri build`） | `design/clash-verge-rev-dev/package.json` | unresolved |
| S5 | `design/ClashMetaForAndroid-main/` | Android VPN 参考（ClashMetaForAndroid） | `versionName = "2.11.35"`、`versionCode = 211035`、`minSdk = 21`、`targetSdk = 35`、`ndkVersion = "29.0.14206865"`；archive 名 `cmfa-$versionName` | `design/ClashMetaForAndroid-main/app/build.gradle.kts` | unresolved |

### 1.1 与既有设计包的重要差异（必须修正）

原设计包 `design/proxy-client-design-v0.1.1/proxy-client-plan/SOURCES.md` 中：

- **S8 已解决**：原文写「Kr328/ClashForAndroid 读取失败，需可验证快照」。实际用户提供的是 **ClashMetaForAndroid**（维护活跃的继任项目），版本 2.11.35。设计正文中凡引用「Clash for Android」之处，均应改为引用 ClashMetaForAndroid，并保留原表述作为历史记录。此变更需 ADR。
- **S2 已解决**：原文写「用户若特指原 Clash Verge，应在 G0 固定原仓库快照」。实际提供的是 **clash-verge-rev**（2.5.9），GPL-3.0-only。设计正文统一按 clash-verge-rev 引用。
- **新增事实**：原文称「未取得 Xboard 路由与认证细节」。本轮已完整审计 `app/Http/Routes/**` 与相关 Controller，契约事实见 `design/contracts/xboard-api-map.json`。

### 1.3 核心分发方式（已决定，见 ADR-0003）

用户已决定：**本项目不编译核心**。Xray / sing-box 使用官方预构建二进制随客户端分发，客户端内置按锁定版本 + SHA256 校验的自动更新。因此：

- `Xray-core-main/` 与 `sing-box-testing/` 快照在本仓库中仅作为**配置格式、CLI 契约、能力边界**的只读参考，不再作为编译来源。
- 官方预构建二进制的来源、目标版本、架构矩阵与校验和由 `versions.lock.json` 锁定。
- 参考源码的许可证核对仍须完成（分发第三方二进制时须附 NOTICE 与许可声明）。

### 1.4 许可证状态（G0 未完成项）

| 项目 | 观测到的许可证 | 需要做的事 |
|---|---|---|
| Xboard | 仓库根含 `LICENSE`（文本本轮未逐条比对） | 确认具体条款后再决定是否可参考其代码结构 |
| Xray-core | 仓库根含 `LICENSE`（本轮未读） | 固定版本后核对；MPL-2.0 与 GPL 的区分影响分发方式 |
| sing-box | 上游声明 GPLv3-or-later 且带命名/关联条件（原设计包 S10 记录） | 若以 sidecar 可执行文件形式分发，须核对并保留声明与源码义务 |
| clash-verge-rev | `GPL-3.0-only`（已在 package.json 读到） | 仅作 UI/交互参考，不得复制代码进 MIT/Apache 许可的本项目 |
| ClashMetaForAndroid | 本轮未读 LICENSE（原设计包记 GPL 系） | 同上；仅作 VPN/服务生命周期设计参考 |

> **结论（待确认）**：两个参考 UI 项目都是 GPL 系。本项目的桌面 UI（C++/wxWidgets）与 Android UI（Kotlin/Compose）必须**独立实现**，只参考交互与结构思路，不复制源代码，除非用户明确选择以 GPL 兼容方式发布整个项目。此项列为 ADR-0002。

---

## 2. 探测到的目标平台基线（来自参考快照）

这些数值**不是**本项目的承诺，只是参考项目实际使用的值，供 G1/G0 决定自己的基线时对照。

| 项目 | 事实 | 对本项目的意义 |
|---|---|---|
| ClashMetaForAndroid | `minSdk = 21`、`targetSdk = 35`、`ndkVersion 29.0.14206865` | 证明 Android 侧可低至 API 21；本设计包建议从 API 26 起评估，需在 G1 实测后冻结 |
| sing-box | `go 1.25.5` | Go 工具链下限事实；本项目共享 Go module 的 `go` 指令不得低于所引入核心的实际需求 |
| Xray-core | `go 1.27` | 高于 sing-box 的 1.25.5；**若两者在同一 Go module 内联合编译，Go 版本必须取较高者**，这是 G1 单 AAR 兼容性探针的关键输入 |
| Xboard | `php ^8.2`、`laravel/framework ^12.0` | 服务端为 Laravel 12 + Sanctum 认证栈，客户端契约据此确定（见 `design/contracts/`） |

---

## 3. 待用户补充的信息 / Requested from user

| 项 | 用途 | 优先级 |
|---|---|---|
| 5 份参考源码的准确仓库 URL 与 tag/commit | 写入 `input-sha256.txt` 与 `versions.lock.json`，使审计结果可复现 | 高（G0 收尾必需） |
| 项目正式名称、发布者、桌面 applicationId、Android 包名 | 填入 `design/client-config.json`；打包身份 | 中（G3 前） |
| Xboard 测试环境（地址 + 普通测试用户） | T03/T04 契约验证；**不要**提供管理员密钥 | 中（G2 起） |
| 项目许可证选择 | 决定 ADR-0002 的结论与参考代码复用边界 | 中（G0 收尾） |

---

## 4. 本轮证据边界 / Evidence limits

- 已做：逐文件读取 Xboard 路由、中间件、Controller、Resource、异常处理与订阅入口；采集各参考源码的版本声明。
- 未做：没有连接任何真实 Xboard 服务器，没有运行任何编译，没有在真机验证 VPN/TUN/PAC，没有核对全部许可证正文。
- 因此：本文中的「版本事实」可信；「兼容性、可编译、可运行」结论一律为**未验证**，不得写成 passed。
