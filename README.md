# 双核心 Xboard 客户端 / Dual-core Xboard Client

面向 Xboard 用户的轻量代理客户端。桌面（Windows / Linux）与 Android 独立实现，共享业务模型与测试样本；核心可选 Xray 或 sing-box。

工作名：ProxyClient（正式名称待定）。

---

## 目录结构

| 目录 | 用途 |
|---|---|
| [`design/`](design/) | **只放设计文件**：设计文档、接口契约、Schema、ADR、审计报告 |
| [`src/`](src/) | **源码**：`src/versions/<版本>/` 各版本源码；`src/references/` 第三方只读参考 |
| [`output/`](output/) | **对外输出**：构建产物、验证报告、导出包（不入 git） |

完整约定见 [design/architecture/repository-layout.md](design/architecture/repository-layout.md)。

---

## 产品范围（当前口径）

客户端的核心链路只有四步：

1. **登录**：用户填写面板地址与账号密码，换取会话凭据。
2. **取订阅链接**：向面板获取该用户的订阅链接（含 token）。
3. **导入并编译**：把订阅内容解析成标准节点模型，编译为所选核心的配置。
4. **建立连接**：选接入方式（系统代理 / TUN / PAC / 本地代理）并连接，退出时完整恢复。

面板业务只做与上述链路相关的最小集合：登录、账户与用量展示、订阅链接获取。工单、订单支付、邀请返佣、礼品卡等**不在首轮范围**。

第三方订阅与手工节点导入**不依赖**面板登录，始终独立可用。

---

## 双核心

| 核心 | 角色 |
|---|---|
| Xray | 可替换适配器 A，独立编译器与原生配置检查 |
| sing-box | 可替换适配器 B，独立编译器与原生配置检查 |

两者通过统一的 capability 模型描述协议、TUN、统计与热切换差异；不支持的参数必须给出字段级错误，禁止静默丢弃。

---

## 当前状态

**阶段 G0：设计收敛中。尚无任何可编译源码，也没有任何构建或运行证据。**

未完成的检查一律标为 `not_run`，不得写成通过。

| 阶段 | 内容 | 状态 |
|---|---|---|
| G0 | 设计收敛、接口契约、来源审计 | 进行中 |
| G1 | 技术探针（架构、双核心、绑定） | 未开始 |
| G2 | 标准模型、面板适配、订阅解析、i18n | 未开始 |
| G3 | Windows / Android 简易模式可用 | 未开始 |
| G4 | 专业模式与完整平台接入 | 未开始 |
| G5 | Linux 与全架构打包、手动构建 | 未开始 |
| G6 | 双语文档与收敛 | 未开始 |

---

## 版本管理

一个 git 仓，`src/versions/<版本号>/` 承载各版本源码，tag 与目录名一致（`v0.1.0`）。

```bash
git tag -l                  # 列出版本
git diff v0.1.0 v0.2.0      # 对比两个版本
```

---

## 文档

- 📘 **文档入口**：[中文](docs/zh-CN/README.md) · [English](docs/en/README.md)
- 📤 **上传源码与手动构建操作步骤**：[docs/zh-CN/dev/upload-and-build.md](docs/zh-CN/dev/upload-and-build.md) ← **从这里开始**
- ✅ **验证说明（哪些检查做过、哪些没有）**：[中文](docs/zh-CN/dev/verification.md) · [English](docs/en/dev/verification.md)
- 🏗 **架构说明**：[中文](docs/zh-CN/dev/architecture.md) · [English](docs/en/dev/architecture.md)
- 📖 **使用说明**：[中文](docs/zh-CN/use/README.md) · [English](docs/en/use/README.md)
- 设计基线：[中文](design/DESIGN.zh-CN.md) · [English](design/DESIGN.en.md)
- 公共配置填写：[CONFIGURATION.md](design/CONFIGURATION.md) · 模板 [client-config.json](design/client-config.json)
- 面板接口契约：[design/contracts/](design/contracts/)
- 多语言约定：[design/architecture/i18n-convention.md](design/architecture/i18n-convention.md)
- 打包契约：[deploy/packaging/README.md](src/versions/0.1.0/deploy/packaging/README.md) · Docker 契约：[README.md](src/versions/0.1.0/deploy/docker/README.md)
- 来源审计：[design/audit/source-register.md](design/audit/source-register.md)
- 阶段状态：[design/plan/STATUS.md](design/plan/STATUS.md)
- 环境约束（为何本地不能编译）：[design/plan/environment-constraints.md](design/plan/environment-constraints.md)

---

## 安全边界

- 源码中**不带任何生产 API 地址与账号**；全部留空由用户填写。
- 订阅链接中的 token、登录凭据、账号 UUID 一律按秘密处理：不入日志、不入导出、不入崩溃报告。
- 面板不可用或会话过期**不得**禁用第三方订阅与手工节点。
