# 文档

语言：**简体中文** · [English](../en/README.md)

---

## 从这里开始

| 你想做什么 | 读这个 |
|---|---|
| **自己构建一份** | [dev/upload-and-build.md](dev/upload-and-build.md) |
| **知道到底验证过什么** | [dev/verification.md](dev/verification.md) |
| **看懂各部件怎么拼起来** | [dev/architecture.md](dev/architecture.md) |
| **使用它** | [use/README.md](use/README.md) |

---

## 首先要知道的一件事

**全部代码从未被编译过。**

开发机上没有 Go、C++、JDK 或 Android SDK 工具链。所有编译与单元测试都在
GitHub Actions 里进行，那里的结果才是唯一依据。

[验证说明](dev/verification.md)逐项列出了：哪些检查已在本机做过、
哪些只能在 CI 里做、哪些**完全没有做过**。未执行的检查一律记为
`not_run`，绝不写成「通过」。

---

## 项目文档

| 文档 | 内容 |
|---|---|
| [design/DESIGN.zh-CN.md](../../design/DESIGN.zh-CN.md) | 设计基线：需求与约束的权威来源 |
| [design/adr/](../../design/adr/) | 架构决策记录 |
| [design/plan/STATUS.md](../../design/plan/STATUS.md) | 阶段与完成度 |
| [design/CONFIGURATION.md](../../design/CONFIGURATION.md) | 出厂配置文件 |
| [design/architecture/i18n-convention.md](../../design/architecture/i18n-convention.md) | 多语言约定 |
| [design/plan/environment-constraints.md](../../design/plan/environment-constraints.md) | 为什么本机不能编译 |
| [deploy/packaging/README.md](../../src/versions/0.1.0/deploy/packaging/README.md) | 打包契约 |
| [deploy/docker/README.md](../../src/versions/0.1.0/deploy/docker/README.md) | Docker 契约（刻意未实现） |

---

## 这是什么

Xboard 面板的双核心（Xray / sing-box）代理客户端，含桌面端
（Windows、Linux）与 Android 前端。

它是**客户端**，本身不提供任何代理服务——你需要有自己的面板账号或订阅链接。
