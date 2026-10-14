# Android 客户端 / Android client

状态：**骨架**（`build_passed` 未达成，`runtime_passed` 未达成）

本模块是 Kotlin + Jetpack Compose 的 Android 前端。它是**骨架**而不是可用实现，
本文档如实说明已完成与未完成的部分——设计基线要求两者必须分开陈述。

---

## 1. 已实现 / Implemented

| 部分 | 说明 |
|---|---|
| Gradle 工程 | 版本全部固定，不做 latest 跟踪；见 `settings.gradle.kts` 的说明 |
| 前台服务 | `ProxyVpnService` 的通知、生命周期、`onRevoke` 处理 |
| VpnService 建立 | TUN 地址、默认路由、排除自身应用 |
| 应用范围 | 通过 `addDisallowedApplication` 表达；界面层尚未暴露选择器 |
| 界面骨架 | Compose 的简单/专业两种模式，共用同一份状态 |
| 语言资源 | 读取 `assets/locales/*.json`，**与桌面端和 Go 端共用同一份文件** |
| 出厂配置 | 读取 `assets/config/client-config.json`，与桌面端同一个文件 |
| 偏好持久化 | 界面模式与「记住模式」，只在勾选时写盘 |
| 打包与签名约束 | release 缺密钥时构建失败，绝不生成随机 release key |

## 2. 未实现 / Not implemented

| 缺口 | 影响 | 前置条件 |
|---|---|---|
| **把 TUN 流量交给核心** | 当前连接后**流量不会经过代理** | 核心的 Android 绑定方式，由 G1 探针决定（AAR 内嵌还是预构建二进制，见 ADR-0003 第 5 条） |
| 面板登录与订阅获取 | 无法从面板取得节点 | 复用共享 Go 层的等价实现，或走客户端本地实现 |
| 节点列表与切换 | 无法选择节点 | 同上 |
| 应用范围选择器 | 只能全部应用 | 需要包管理器列表与界面 |
| 核心下载与更新 | 首次运行无法自动获取核心 | 与桌面端共用锁定清单 |
| 真机运行验证 | 全部运行期结论为 `not_run` | 需要真机或模拟器 |

**因此：现在产出的 APK 可以安装、可以打开界面、可以建立 VPN 设备，
但不会代理任何流量。** 这一点在构建日志与产物里都会明确标注。

## 3. 构建 / Building

依赖：JDK 17、Android SDK（`ANDROID_HOME`）、Gradle 8.11.1。

```bash
cd src/versions/0.1.0/src/apps/android
gradle --no-daemon :app:assembleDebug -Ppcx.abiFilter=arm64-v8a
```

**不使用 Gradle wrapper**：wrapper 的 jar 是二进制文件，不入版本控制。
CI 由 `gradle/actions/setup-gradle` 提供 Gradle 并接管缓存。

`-Ppcx.abiFilter` 收窄到单个 ABI。不带该参数时会打包全部三个目标 ABI，
这与「一个目标一份产物」的约定不符，因此 CI 总是显式传入。

### 三个 ABI

| ABI | 对应目标 |
|---|---|
| `arm64-v8a` | android-arm64-v8a |
| `armeabi-v7a` | android-armeabi-v7a |
| `x86` | android-x86 |

映射直接在 `targets.json` 的 `abi` 字段里给出，**不做二次映射**——
二次映射是 ABI 错位的常见来源。

### release 签名

必须通过环境变量提供：

```
ANDROID_KEYSTORE_PATH / ANDROID_KEYSTORE_BASE64
ANDROID_KEYSTORE_PASSWORD
ANDROID_KEY_ALIAS
ANDROID_KEY_PASSWORD
```

缺少任何一项时 `assembleRelease` **显式失败**。这条约束是有意的：
没有它时 Gradle 会产出一个未签名的 APK，文件名看起来完全正常，
用户拿到后无法安装，而错误信息与真正的原因相距很远。
**绝不生成随机 release key**：那会让后续版本无法覆盖安装。

## 4. 包名 / applicationId

来自 `design/client-config.json` 的 `branding.androidPackageId`。

该字段默认留空，留空时使用 **`dev.proxyclient.internaltest`** ——
一个明确标注为内部测试的身份，而不是猜一个看起来正式的名字。

正式包名一旦发布就无法更改，猜错等于把用户永久绑定在一个占位身份上。
**正式发布前必须填写该字段。**

## 5. 权限 / Permissions

清单里每一项权限都能说清用途，这是设计基线的要求。

| 权限 | 用途 |
|---|---|
| `INTERNET` | 代理客户端的基本前提 |
| `ACCESS_NETWORK_STATE` | 判断网络类型，用于提示与诊断 |
| `FOREGROUND_SERVICE` | 代理必须在应用退到后台后继续运行 |
| `FOREGROUND_SERVICE_SPECIAL_USE` | Android 14 起前台服务必须声明用途类型 |
| `POST_NOTIFICATIONS` | 常驻通知；没有它用户看不到「正在代理」 |

**刻意不声明 `RECEIVE_BOOT_COMPLETED`**：开机自启属于用户偏好，
默认开启会让人以为手机被装了什么。需要时由用户显式开启再声明。

`BIND_VPN_SERVICE` 由系统在用户授权后绑定，**应用不能自行声明**；
清单里的 `android:permission` 是要求调用方持有该权限，
否则任何应用都能绑定我们的服务并借此建立 VPN 隧道。

## 6. 数据与隐私 / Data and privacy

- `android:allowBackup="false"`，且 `data_extraction_rules.xml` 排除全部域。
  应用保存的订阅链接、会话凭据与节点信息都算凭据——**订阅链接本身就是凭据**，
  拿到它就能使用该账号的流量。云备份会把它们复制到用户未必意识到的位置。
- 应用图标是矢量绘制，不含二进制资源。
- 界面文案不含中文硬编码：全部来自共享语言资源。

## 7. 与其它组件的共用关系

| 共用项 | 位置 | 理由 |
|---|---|---|
| 语言资源 | `locales/*.json`（仓库根） | 两份翻译必然漂移 |
| 出厂配置 | `design/client-config.json` | 三个客户端必须对「这是什么产品」有同一认知 |
| 目标清单 | `deploy/targets/targets.json` | 构建矩阵只维护一处 |
| 构件约定 | `deploy/packaging/README.md` | 同上 |

键集合与占位符的一致性由 `tools/scripts/check-i18n-keys.ps1` 统一校验，
该脚本同时扫描 Go、C++ 与 Kotlin 三种源码。
