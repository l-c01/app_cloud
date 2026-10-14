# ADR-0005：单内核 —— 只保留 sing-box

- 状态：**已接受**（取代 ADR-0003 中「双内核可选」的部分）
- 日期：2026-10-15
- 取代：ADR-0003 关于「同时支持 Xray 与 sing-box」的决策

---

## 背景

原设计让用户在内核之间二选一（Xray 或 sing-box），并为此维护：

- 两套配置生成器（`shared/compiler/xray.go`、`singbox.go`）；
- 两张能力表（两者的传输、TLS、协议支持范围不同）；
- 界面上的内核选择器，以及「同一节点在 A 核可用、在 B 核不可用」的全部提示逻辑。

用户要求改为**只保留一个内核**，并把客户端选项补全到接近 Clash 客户端的程度。

## 决策

**只保留 sing-box。**

## 理由

### 1. 配置模型与目标功能一一对应

要补的选项（规则模式、分应用代理、DNS 覆写、TUN、嗅探、规则集）
在 sing-box 的配置里**都是一等公民**：

| 需要的功能 | sing-box 中的位置 |
|---|---|
| 规则分流 | `route.rules` |
| 规则集（GEOSITE/GEOIP 的对应物） | `route.rule_set` |
| DNS 覆写与 fake-ip | `dns.servers`、`dns.fakeip` |
| TUN 与自动路由 | `inbounds[type=tun]` |
| 分应用代理 | TUN 入站的 `include_package` / `exclude_package` |
| 嗅探与目的覆写 | `inbounds[].sniff`、`route.rules[].action` |

Xray 需要的是**另一套**概念（`routing.rules` + `domainStrategy` +
外部 geoip.dat/geosite.dat 文件），映射到同一套界面选项要多一层翻译，
而那一层翻译正是「界面说支持、核心其实不认」的来源。

### 2. 协议覆盖足够

Xboard 面板下发的节点协议：Shadowsocks、VMess、VLESS、Trojan，
以及部分面板的 Hysteria2、TUIC。

sing-box 全部支持，并且**同时**支持 REALITY 与 uTLS 指纹。
换句话说：保留 sing-box 不会让任何一类面板节点编不出来。

### 3. 少一个内核，少一整类缺陷

双内核带来的真实成本，在开发过程中已经出现过：

- 能力表要分别维护，而它们**都标记为 `unverified`**——
  两张没实测过的表比一张更不可信；
- 同一条节点在两个核心里可能一个能用一个不能用，
  界面必须解释清楚，否则用户看到的是「同一个订阅、换个内核就少了一半节点」；
- 打包要带两个二进制，体积翻倍，而用户只会用其中一个。

### 4. 与目标参照物一致

参照对象 Clash Verge 与 Clash for Android 都基于 mihomo（Clash.Meta），
是**单内核**。它们的选项之所以能做得那么全，
前提正是「内核的配置模型就是界面的数据模型」。
选 sing-box 让本项目能沿用同一思路。

## 后果

### 需要改的

| 位置 | 改动 |
|---|---|
| `shared/compiler/xray.go` | **删除** |
| `shared/compiler/singbox.go` | 保留，成为唯一生成器 |
| `shared/compiler/capability.go` | 从「按核心两张表」改为单表 |
| `shared/domain/node.go` | 保留；协议集合按 sing-box 的支持范围收敛 |
| 界面（桌面端 + Android） | 删除内核选择器 |
| `deploy/targets/targets.json` | 核心产物只锁 sing-box |
| 文案（`locales/*.json`） | 删除内核选择相关键，新增选项相关键 |

### 不改变的

- **核心仍然由客户端自动更新**（ADR-0003 的这一部分继续有效）；
- **仍然只从官方 Release 下载并校验 SHA256**；
- **能力表的 `verifiedAgainst` 仍然必须在实测后才能改为已验证**。

### 风险

| 风险 | 应对 |
|---|---|
| 某个面板只提供 Xray 专有传输（如 xhttp） | 编译时按能力表拒绝该节点**并在报告里说明原因**，而不是产出一个核心不认的配置。当前能力表已覆盖该判断。 |
| 用户已在用 Xray | 本项目尚未发布，没有既存用户；且节点数据来自订阅，重新编译即可。 |

## 与其它决策的关系

- **补充** ADR-0003（核心分发方式不变）
- **取代** ADR-0003 中「双内核可选」的表述
- **不影响** ADR-0004（订阅格式策略）
