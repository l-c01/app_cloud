# 仓库目录结构 / Repository structure

本文件是仓库结构的唯一权威说明。任何新增目录先在此登记，再落地文件。

Version: 0.1 · 阶段：重构后基线

---

## 三个顶层目录 / Three top-level directories

仓库只有三个顶层目录，各司其职，互不混杂。

| 目录 | 用途 | 放什么 | 不放什么 |
|---|---|---|---|
| `design/` | **只放设计文件** | 设计文档、接口契约、Schema、架构决策记录（ADR）、审计报告、目标清单 | 源码、第三方快照、构建产物 |
| `src/` | **源码** | `src/versions/` 各版本源码；`src/references/` 第三方只读参考 | 设计文档、交付产物 |
| `output/` | **对外输出** | 构建产物、验证报告、导出包、交付归档 | 源码、设计文档 |

---

## `design/` — 设计文件

| 路径 | 内容 |
|---|---|
| `design/DESIGN.zh-CN.md`、`design/DESIGN.en.md` | 设计基线（中英同步，需求编号 R01–R16） |
| `design/CONFIGURATION.md`、`design/client-config.json` | 公共客户端配置模板与填写说明 |
| `design/architecture/` | 架构拆解：目录约定、模块职责、数据流、状态机 |
| `design/contracts/` | 外部契约：Xboard 面板接口映射、订阅格式、IPC 契约、错误码 |
| `design/schemas/` | 机器可读 Schema：公共配置、面板 DTO、IPC、迁移 |
| `design/adr/` | 架构决策记录，一项决策一个文件 |
| `design/plan/` | 阶段计划、目标清单、工作分解与当前状态 |
| `design/audit/` | 对参考源码的事实性审计报告与来源登记 |
| `design/proxy-client-design-v0.1.1/` | 历史设计包（原始交付，只读归档） |

---

## `src/` — 源码

```
src/
├── references/          第三方只读参考源码（不入 git）
│   ├── Xboard-master/           面板服务端，接口契约的事实来源
│   ├── Xray-core-main/          代理核心 A
│   ├── sing-box-testing/        代理核心 B
│   ├── clash-verge-rev-dev/     桌面 UI 交互参考
│   └── ClashMetaForAndroid-main/ Android VPN/服务生命周期参考
└── versions/            本项目源码，每个版本一个目录
    └── <version>/       例如 0.1.0/
```

### 版本管理约定

- **一个 git 仓**，`src/versions/<版本号>/` 承载每个版本的完整源码快照。
- 版本号遵循语义化版本：`0.1.0`、`0.2.0`、`1.0.0`。
- 每个版本发布时打 tag：`v0.1.0`、`v0.2.0`，与目录名一致。
- 版本之间可直接 `git diff v0.1.0 v0.2.0` 对比。
- 新版本从上一版本复制后再改，**不得就地重写历史版本目录**（历史版本是证据）。
- 只有需要长期保留的里程碑才新开版本目录；日常开发在 `src/versions/<当前版本>/` 内提交。

### `src/references/` 规则

- 只读：不编译、不修改、不纳入版本历史。
- 可复现性由 `design/audit/source-register.md` 记录的版本事实与摘要保证。
- 引用参考代码必须遵守 `design/adr/` 中的许可证决策。

### 单个版本目录内的规划结构

```
src/versions/<version>/
├── src/
│   ├── apps/desktop/        C++20 + wxWidgets 桌面 UI（CMake）
│   ├── apps/android/        Kotlin + Compose Android UI
│   ├── apps/headless/       Go 无界面入口（Docker 用）
│   ├── shared/              Go 共享模块
│   │   ├── domain/            Profile/Node/Rule/DNS 标准模型
│   │   ├── panel/             Xboard 接口适配与 DTO
│   │   ├── subscription/      订阅解析与格式探测
│   │   ├── compiler/          Xray / sing-box 配置生成
│   │   ├── rules/             Geo 与 PAC 规则
│   │   ├── secrets/           系统密钥存储与脱敏
│   │   └── i18n/              多语言
│   ├── runtime/               连接协调器、核心适配器、IPC
│   └── platform/              特权代理与平台桥接
├── deploy/
│   ├── targets/               targets.json、versions.lock.json
│   ├── packaging/             打包、安装、卸载、签名
│   └── docker/                Dockerfile 与 compose
├── locales/                   中英文翻译源
└── tests/                     契约测试、固件、端到端场景
```

### GitHub Actions 工作流的位置（重要例外）

**GitHub 只会在仓库根目录 `.github/workflows/` 下查找工作流**，放在版本目录里不会被触发。

因此约定如下：

| 内容 | 位置 | 说明 |
|---|---|---|
| 可运行的工作流定义 | `.github/workflows/`（仓库根） | GitHub 唯一识别的位置 |
| 版本相关的构建清单 | `src/versions/<version>/deploy/targets/` | `targets.json`、`versions.lock.json` |
| 打包与签名脚本 | `src/versions/<version>/deploy/packaging/` | 由工作流调用 |

工作流通过相对路径**读取目标清单**，把「构建什么」与「怎么构建」分开：清单随版本走，工作流不硬编码目标矩阵。切换版本时只需改清单路径。

---

## `output/` — 对外输出

| 路径 | 内容 |
|---|---|
| `output/builds/` | 各目标架构的构建产物与校验和 |
| `output/reports/` | 验证报告、审计结论、性能基线 |
| `output/exports/` | 交付归档、给用户的导出包 |

命名规范：`<用途>-<版本>-<目标>-<日期>.<扩展名>`，例如 `report-0.1.0-windows-amd64-20261014.md`。

---

## 命名与提交流程

- 文件与目录：小写，单词间用 `-`；Go 包名用单数小写单词。
- 需求编号 `R01`–`R16`，阶段 `G0`–`G6`，测试 `T01`–`T17`，决策 `ADR-0001` 起。
- 提交信息：`<阶段>/<需求编号>: <动词短语>`，例如 `G2/R03: 实现登录与订阅链接获取`。
- 未运行的测试不得写成 passed；架构编译通过不等于运行可用。
- 结构或约定变更时，同步更新本文件与 `design/plan/` 状态文件。
