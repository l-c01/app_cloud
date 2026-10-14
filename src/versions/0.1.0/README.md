# 版本 0.1.0 骨架说明

本目录是客户端 **0.1.0** 版本的源码根。当前阶段（G0）尚未产生可编译源码，本目录先约定结构。

版本号规则：语义化版本；tag 与目录名一致（`v0.1.0`）。历史版本目录只读，新版本从上一版本复制后修改。

## 规划结构

```
src/versions/0.1.0/
├── src/
│   ├── apps/
│   │   ├── desktop/        C++20 + wxWidgets 桌面 UI（CMake）
│   │   ├── android/        Kotlin + Compose Android UI（后续）
│   │   └── headless/       Go 无界面入口（Docker 用，可选）
│   ├── shared/
│   │   ├── domain/         Profile/Node/Rule/DNS 标准模型
│   │   ├── panel/          Xboard 面板适配与 DTO（登录/取订阅链接/账户）
│   │   ├── subscription/   订阅拉取与格式解析
│   │   ├── compiler/       Xray / sing-box 配置生成与能力校验
│   │   ├── rules/          Geo / PAC 规则（后续）
│   │   ├── secrets/        系统密钥存储与脱敏
│   │   └── i18n/           多语言（后续）
│   ├── runtime/
│   │   ├── coordinator/    连接协调器
│   │   ├── core/           Xray / sing-box 适配器（sidecar 进程）
│   │   ├── coreupdater/    核心自动更新（ADR-0003）
│   │   └── ipc/            桌面 UI <-> 服务 IPC
│   └── platform/           Windows/Linux 特权代理、Android 桥接（后续）
├── deploy/
│   ├── targets/            targets.json、versions.lock.json
│   ├── workflows/          GitHub Actions 手动入口
│   ├── packaging/          打包、安装、卸载、签名
│   └── docker/             Dockerfile 与 compose（可选后置）
├── locales/                中英文翻译源（后续）
└── tests/
    ├── fixtures/           脱敏固件（面板响应、订阅样本）
    └── contract/           契约测试
```

## 技术基线

| 项 | 值 |
|---|---|
| 共享业务模块 | Go（单一 module） |
| 桌面 UI | C++20 + wxWidgets + CMake |
| 核心 | Xray / sing-box **官方预构建二进制** sidecar + 自动更新（ADR-0003） |
| 本机构建 | 不进行；构建在 GitHub Actions 手动触发 |

## 约定

1. 先契约后实现：接口、字段、错误码先在 `design/` 定稿（当前参考 `design/contracts/panel-api-contract.md` 与 `design/schemas/panel/panel-dto.schema.json`）。
2. 禁止重复造轮子：新增能力优先复用 `src/shared/` 既有接口。
3. 秘密（登录凭据、订阅链接、uuid）一律走 `shared/secrets`，不入日志、不入导出。
4. 未运行测试标 `not_run`，禁止写成通过。

## 本版本首个实现任务（建议 G1/G2 起）

1. 建立 `src/shared/domain` 领域模型 + `src/shared/panel` 面板适配（登录 → 取订阅链接 → 账户）。
2. 建立 `src/runtime/core` 双核心 sidecar 适配器与 `coreupdater` 自动更新。
3. 建立 `tests/fixtures` 脱敏固件与契约测试。
