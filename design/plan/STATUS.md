# 阶段计划与当前状态

本文件是项目进度的**唯一权威来源**。任何「已完成」的声明必须在此登记，并附可复核的证据。

更新时间：2026-10-14 · 当前阶段：**G0 → G2 过渡** · 当前版本目录：`src/versions/0.1.0/`

---

## 当前状态总览

| 项 | 值 |
|---|---|
| 阶段 | G0 设计与契约**已完成**；已开始 G2 的首个模块 |
| 版本管理 | 一个 git 仓 + `src/versions/<版本>/` + tag |
| 已受管文件 | 49（设计文档、契约、Schema、目标清单、报告、首个 Go 模块） |
| 核心分发 | **已决定**：官方预构建二进制 + 客户端内置自动更新（ADR-0003） |
| 应用源码 | 🟡 已开始：`src/versions/0.1.0/src/shared/panel`（4 个接口 + 契约测试） |
| 编译证据 | **无**（`not_run`；本机无 Go 工具链，编译与测试在 CI） |
| 运行证据 | **无**（`not_run`） |
| 未决阻塞项 | 项目许可证、项目名称与包名、Go module 正式路径、参考源码 commit |

---

## 产品范围（收敛后）

客户端核心链路四步：**登录 → 取订阅链接 → 编译配置 → 连接**。

面板侧只实现 3 个接口 + 1 个可选探测，详见 [`design/audit/xboard-api-audit-part3.md`](../audit/xboard-api-audit-part3.md)：

| 用途 | 接口 |
|---|---|
| 登录 | `POST /api/v1/passport/auth/login` |
| 取订阅链接 | `GET /api/v1/user/getSubscribe` |
| 校验会话与账户 | `GET /api/v1/user/info` |
| 可选：面板身份探测 | `GET /api/v1/guest/comm/config` |

工单、订单支付、邀请返佣、礼品卡、知识库**不在首轮范围**。

---

## 阶段计划

| 阶段 | 目标 | 交付物 | 通过条件 | 状态 |
|---|---|---|---|---|
| **G0** | 设计收敛、接口契约、来源审计 | api-map、Schema、ADR、目标清单、来源登记 | 没有未知接口被当作既定事实；必需目标都有路线 | **进行中** |
| G1 | 技术探针 | 各架构编译探针、双核心连接探针、手动工作流 | 关键架构可编译；首轮平台双核心可连 | 未开始 |
| G2 | 标准模型与业务 | 领域模型、面板适配、订阅解析、i18n | 无面板也可导入第三方订阅；空 API 安全 | 未开始 |
| G3 | 首轮可用 | Windows/Android 简易模式端到端 | 两平台代表架构可用；退出恢复通过 | 未开始 |
| G4 | 完整定制 | TUN/PAC/应用范围、专业模式、原生编辑 | 关键功能无假开关；高级配置不被破坏 | 未开始 |
| G5 | 平台与打包 | Linux 桌面、全必需架构、七个手动入口 | 必需平台构建/运行证据完整 | 未开始 |
| G6 | 文档与收敛 | 中英文开发/使用文档、AI 上下文、升级卸载 | 收敛条件全部满足 | 未开始 |

---

## G0 工作分解

| # | 工作项 | 产物 | 状态 |
|---|---|---|---|
| 1 | 目录与版本管理重构 | `.gitignore`、仓库结构说明、ADR-0001 | **完成** |
| 2 | 参考源码来源登记 | [`design/audit/source-register.md`](../audit/source-register.md) | 完成（commit 待补） |
| 3 | 面板接口契约审计 | [`design/contracts/xboard-api-map.json`](../contracts/xboard-api-map.json)（48 条） | 完成 |
| 4 | 接口范围收敛 | [`design/audit/xboard-api-audit-part3.md`](../audit/xboard-api-audit-part3.md) | 完成 |
| 5 | 首批 ADR | ADR-0001、ADR-0002 | 完成 |
| 6 | 核心分发决策 | ADR-0003（官方预构建 + 客户端自动更新） | **完成（用户决定）** |
| 7 | in-scope 接口 DTO Schema | `design/schemas/panel/panel-dto.schema.json` | **完成** |
| 8 | 公共配置 Schema 复刻与修正 | `design/schemas/client-config.schema.json`（修复 testSettings 缺失） | **完成** |
| 9 | 配置校验脚本 | `tools/scripts/*.ps1`（纯 ASCII，PowerShell+ .NET），`client-config.json` 通过校验 | **完成** |
| 10 | 0.1.0 版本骨架 | `src/versions/0.1.0/`（README + versions.lock.json 模板） | **完成** |
| 11 | 构建目标清单 | `src/versions/0.1.0/deploy/targets/targets.json`（12 目标，与既有设计包一致） | **完成** |
| 12 | 修订设计基线范围与事实错误 | `DESIGN.zh-CN.md`、`DESIGN.en.md`（接口事实、范围收敛、核心分发） | **完成** |
| 13 | G0 审计报告 | `output/reports/report-G0-0.1.0-all-20261014.md` | **完成** |
| 14 | 手动构建入口与可复用工作流 | `.github/workflows/`：7 入口 + 4 可复用 + 1 校验 = 12 个文件 | **完成（未运行）** |
| 14b | CI 静态校验与单元测试 | `.github/workflows/ci.yml`（gofmt/vet/test/race + JSON 校验） | **完成（未运行）** |
| 14c | 工作流位置的约定修正 | `design/architecture/repository-layout.md`（GitHub 只认仓库根 `.github/workflows/`） | **完成** |
| 15 | `shared/panel` 面板适配实现 | `src/versions/0.1.0/src/shared/panel/`（4 接口 + 契约测试） | **完成（未编译）** |
| 16 | Go module 骨架与说明 | `src/go.mod`、`src/README.md` | **完成** |
| 17 | `shared/domain` 标准节点模型 | `src/versions/0.1.0/src/shared/domain/node.go` | **完成（未编译）** |
| 18 | `shared/subscription` 拉取与解析 | 订阅拉取 + 格式探测 + 节点 URI 解析 + 契约测试 | **完成（未编译）** |
| 19 | 订阅格式策略决策 | ADR-0004（URI 列表为主输入；Xboard 无原生 Xray JSON） | **完成** |
| 20 | Clash YAML 导入器 | `shared/subscription` 的 YAML 解析 | 待办（G2） |
| 21 | sing-box JSON 导入器 | `shared/subscription` 的 JSON 解析 | 待办（G2） |
| 22 | 双核心配置编译器 | `shared/compiler`（Xray / sing-box 生成器 + 能力矩阵 + 契约测试） | **完成（未编译）** |
| 23 | 运行期：核心适配器与自动更新 | `runtime/core`、`runtime/coreupdater`（ADR-0003） | 待办（G3/G4） |
| 24 | 连接协调器与网络恢复 | `runtime/coordinator` | 待办（G3/G4） |
| 19 | CI 检查落地 | `gofmt` / `go vet` / `go test` 工作流 | 待办（G1） |

---

## 阻塞与未决项

| 项 | 影响 | 需要谁解决 | 最迟解决节点 |
|---|---|---|---|
| 项目许可证选择 | 决定能否复用 GPL 代码（ADR-0002） | 用户 | G0 收尾 |
| 项目正式名称、发布者、桌面 applicationId、Android 包名 | 打包身份，写入 `client-config.json` | 用户 | G3 前 |
| 5 份参考源码的准确仓库 URL 与 tag/commit | 让审计结果可复现 | 用户 | G0 收尾 |
| Xboard 测试环境（地址 + 普通测试用户） | 验证登录与订阅获取 | 用户 | G2 起 |
| Android 双核心绑定方式（单 AAR 是否可行） | 决定 Android 核心集成路线 | G1 探针结论 | G1 |

---

## 状态标记规范

| 标记 | 含义 |
|---|---|
| `not_run` | 未执行，不得写成通过 |
| `planned` | 已计划，尚未开始 |
| `in_progress` | 进行中 |
| `build_passed` | 编译通过（不等于可运行） |
| `runtime_passed` | 在目标平台实际运行并取得证据 |
| `blocked` | 有明确阻塞条件，且已记录 |
| `deferred` | 明确后置，不阻塞当前阶段 |

**禁止**：把架构编译成功写成运行可用；把未运行的测试写成 passed；为通过检查而删除目标或降低验收。
