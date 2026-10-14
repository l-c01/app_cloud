# 双核心 Xboard 客户端：正式开发设计包
# Dual-core Xboard Client: Development Planning Package

版本 / Version: 0.1.1 · 日期 / Date: 2026-10-14

本包是设计基线，供下一阶段生成源码、GitHub Actions 和正式文档使用。没有客户端实现、可运行安装包或已验证的构建工作流；所有构建与运行状态均为待验证。软件工作名称暂用 `ProxyClient`，正式名称、包名和签名身份在 G0 阶段确定。

This package defines the baseline for implementation, GitHub Actions and final documentation. It contains no application implementation, runnable installer or validated build workflow. Build and runtime status remain unverified. `ProxyClient` is a temporary working name.

## 阅读顺序 / Reading order

1. `DESIGN.zh-CN.md`：完整中文工作流、需求、架构、构建矩阵、验收和收敛标准。
2. `DESIGN.en.md`：英文设计基线。
3. `project-spec.json`：供 AI/脚本读取的需求、目标平台、阶段和约束，不能代替文字说明。
4. `AGENTS.md`：后续 AI 修改纪律。
5. `templates/`：任务、变更记录、决策记录、验证证据和交接格式。
6. `SOURCES.md`：已核对资料、未核实事项和正式开发的源码审计清单。

Read the Chinese or English design, then the machine-readable spec, AI rules, templates and source register. Requirements have stable IDs. Future source code, tests, documentation and change records must reference those IDs.

## 当前结论 / Current decisions

- 桌面端候选：C++/wxWidgets 原生 GUI + Go 公共业务服务 + Xray/sing-box 独立核心进程。Android 候选：Kotlin/Compose + VpnService + Go/AAR 核心包装。候选必须先通过多架构技术验证。
- Windows、Android 为首轮重点；Linux 桌面为下一交付阶段；Docker 为可选无界面代理服务，单独验收。
- 不使用面板管理员或服务端节点密钥；空 API 地址不妨碍第三方订阅模式。
- 简易/专业模式只是展示与操作入口，不维护两套配置。UI 模式、流量接入方式、路由策略、Android 应用范围为四个独立维度。
- 所有架构保留目标位置；任何构建失败或无法运行的平台必须明确标记，不能从“一键全部”中悄悄剔除。

Desktop candidate: native C++/wxWidgets UI, shared Go business service and separate core processes. Android candidate: Kotlin/Compose, VpnService and Go/AAR bindings. Windows and Android lead delivery; Linux follows; Docker is an optional headless service. All candidates require architecture probes before commitment. UI mode, traffic entry, routing policy and Android app scope are independent settings.

## 下一阶段起点 / Next implementation step

先执行 G0（版本、API、许可和目标系统冻结）与 G1（编译及双核心网络探针），再生成正式仓库。G1 失败时形成 ADR，调整技术路线，保持用户需求 ID 和目标矩阵不变。

Start with G0 version/API/license/platform discovery and G1 build/network probes. Only then generate the application repository. Failed probes require a recorded architecture decision rather than silent scope reduction.

## 0.1.1 更新 / Update

新增 client-config.json、client-config.schema.json 与 CONFIGURATION.md。软件名称与面板信息由用户填写；Windows/Android 实机测试由用户执行。源码附件本次未能读取，审计待收到文件。

Public branding/panel/default settings are centralized; physical-device validation belongs to the user. Uploaded source archives were not accessible during this update.
