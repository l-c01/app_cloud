# 打包契约 / Packaging contract

状态：契约已定义 · 构建步骤已接线 · **从未执行过**（`not_run`）

本目录定义「一个可交付产物由什么组成」。构建步骤在
`.github/workflows/reusable-desktop.yml` 里，本目录只放打包逻辑本身。

---

## 1. 产物的组成

每个桌面目标产出一个压缩包，内容固定为：

```
<target-id>/
├── proxyclient-desktop        图形界面
├── proxyclient-service        后台服务（GUI 的必备组件）
├── locales/                   语言资源（zh-CN.json、en.json）
├── config/client-config.json  出厂配置模板
├── cores/                     核心二进制（仅在版本已冻结时存在）
└── CORES-NOT-BUNDLED.txt      cores/ 缺失时的说明（两者必有其一）
```

**`cores/` 与 `CORES-NOT-BUNDLED.txt` 必有其一**，这是刻意的：
一个既没有核心、也没有任何说明的包，会让用户以为拿到的是完整产物。

**`proxyclient-service` 不是可选项。** 所有涉及系统网络与核心进程的操作
都在服务里；只打包 GUI 会得到一个能打开、但什么都做不了的界面。
因此构建步骤在找不到服务二进制时会明确失败，而不是产出一个残缺的包。

两个二进制的关系：GUI 通过本地 IPC 连接服务（见 `runtime/ipc`）。
设计上必须分开进程，因为修改系统代理、启动核心、恢复网络这些操作需要在
GUI 崩溃或被强杀后仍可被恢复——而 GUI 恰恰是最容易崩的那一个。

## 2. 核心分发规则（ADR-0003）

| 规则 | 原因 |
|---|---|
| 核心**不编译**，只下载官方 Release 预构建二进制 | 自编译核心无法复现官方行为，且引入一整套工具链 |
| 每个产物必须由 `versions.lock.json` 同时锁定 **version 与 sha256** | 只锁版本号不校验内容，等于信任传输过程 |
| 校验**先于**落盘 | 先放到目标位置再校验，中间会有一段「未校验但可用」的窗口 |
| 校验失败**删除**临时文件并中止 | 留下半成品迟早会被误用 |
| 锁文件未冻结时**拒绝下载** | 见下 |

### 为什么未冻结时必须拒绝

`deploy/targets/versions.lock.json` 当前是占位文件（`version` 与 `sha256` 均为 null）。
此时脚本会**明确失败**，而不是「下载最新版试试」。

理由：一个静默包含未校验核心的安装包，在用户看来和正常包完全一样，
但它不具备任何完整性保证。宁可让构建失败并说清原因。

要让构建附带核心，需要：

1. 刻意选定确切的发布版本；
2. 在 `versions.lock.json` 里记录每个资产的**文件名与 sha256**；
3. 重新构建。

## 3. 各目标的工具链

参数集中在 `deploy/targets/targets.json` 的 `desktopBuild` 字段里，
**不在工作流中另立一份映射**（设计基线 §10.2：目标矩阵只维护一处）。

| 目标 | runner | wxWidgets 来源 | 目标架构参数 |
|---|---|---|---|
| windows-amd64 | windows-latest | vcpkg `x64-windows` | `-A x64` |
| windows-386 | windows-latest | vcpkg `x86-windows` | `-A Win32` |
| windows-arm64 | windows-latest | vcpkg `arm64-windows` | `-A ARM64` |
| linux-amd64 | ubuntu-latest | apt `libwxgtk3.2-dev` | 原生 |
| linux-386 | ubuntu-latest | apt multiarch `libwxgtk3.2-dev:i386` | `-m32` |
| linux-arm64 | ubuntu-24.04-arm | apt `libwxgtk3.2-dev` | 原生 |

### 已知风险（未验证，如实记录）

| 风险 | 影响 | 说明 |
|---|---|---|
| vcpkg 首次构建 wxWidgets 耗时很长 | 三个 Windows 目标首次运行可能超时 | runner 已把 vcpkg 二进制缓存接到 Actions 缓存，后续运行会命中 |
| `ubuntu-24.04-arm` 仅对公开仓库免费 | 私有仓库的 linux-arm64 可能拿不到 runner | 失败是明确的，不会产出错误架构的产物 |
| i386 依赖需要 multiarch | linux-386 可能因依赖不完整而失败 | 失败时必须显式报错，不得产出 64 位产物冒充 |
| wxWidgets 包名随发行版变化 | apt 安装可能找不到包 | 工作流先按清单尝试，失败回退到 `libwxgtk3.0-gtk3-dev` |
| 全部构建从未执行过 | 任何一个环节都可能有错 | 第一次运行就是第一次验证 |

## 4. 产物不包含的东西

| 不含 | 原因 |
|---|---|
| 签名密钥 | 绝不进仓库；由仓库 Secrets 提供，缺失时 release 构建失败 |
| 面板账号与订阅链接 | 出厂配置里这些字段一律留空 |
| 任何凭据 | 源码与产物中不得出现 |

## 5. 本目录的文件

| 文件 | 作用 |
|---|---|
| `fetch-cores.sh` | Linux 侧核心下载与校验。锁文件未冻结时拒绝执行。 |
| `fetch-cores.ps1` | Windows 侧同上。 |
| `README.md` | 本文件。 |

> `fetch-cores.*` 当前在「下载」那一步显式报错：锁文件里还没有资产 URL。
> 这是刻意的占位失败，不是遗漏——见第 2 节。
