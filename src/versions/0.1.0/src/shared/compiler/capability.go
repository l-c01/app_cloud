package compiler

import (
	"proxyclient/shared/domain"
)

// 本文件定义内核能力边界。
//
// 重要说明（务必遵守）：
//   下表描述的是**依据参考源码与官方文档推断**的能力边界，
//   尚未针对锁定版本逐项实测。G4 必须用固定版本的官方二进制复核每一项，
//   并把结论回写到本文件与设计文档。在此之前，任何「sing-box 不支持 X」的判断
//   都必须在报告里以 error 呈现（宁可拒绝，也不静默产出可能是错的配置）。

// capability 描述内核的能力边界。
type capability struct {
	core Core
	// protocols 是支持的协议集合。
	protocols map[domain.Protocol]bool
	// transports 是支持的传输类型集合。
	transports map[domain.Transport]bool
	// tcpHeaderObfs 表示是否支持 TCP 上的 HTTP 伪装头。
	tcpHeaderObfs bool
	// reality 表示是否支持 REALITY。
	reality bool
	// utls 表示是否支持 uTLS 指纹伪装。
	utls bool
	// flow 表示是否支持 VLESS flow（如 xtls-rprx-vision）。
	flow bool
	// verifiedAgainst 记录本表依据的核心版本；未冻结时为 unverified。
	verifiedAgainst string
}

// capabilityTable 是当前唯一内核的能力表。
//
// 单内核（ADR-0005）：原先是「两个核心各一张表」，现在只剩 sing-box 一张。
// 这不是简化文档，而是取消了一整类缺陷——两张都标记为 unverified 的表
// 比一张更不可信，而且它会让同一个订阅在不同内核下编出不同数量的节点，
// 界面必须为此向用户解释，用户却无法自己判断该信哪一个。
var capabilityTable = capability{
	core: CoreSingBox,
	protocols: map[domain.Protocol]bool{
		domain.ProtocolShadowsocks: true,
		domain.ProtocolVMess:       true,
		domain.ProtocolVLESS:       true,
		domain.ProtocolTrojan:      true,
		// Hysteria2 是**用真实订阅验证过才加进来的**。
		//
		// 真实订阅里确实有这类节点，而此前解析器根本不认这个 scheme，
		// 它们被整条丢掉。能力表与解析器的说法必须一致：
		// 表里说 false 而解析器能解析，节点会在编译期被拒；
		// 表里说 true 而生成不出出站，用户会看到节点却连不上。
		domain.ProtocolHysteria2: true,
	},
	transports: map[domain.Transport]bool{
		domain.TransportTCP:         true,
		domain.TransportWS:          true,
		domain.TransportGRPC:        true,
		domain.TransportHTTP:        true,
		domain.TransportH2:          true,
		domain.TransportHTTPUpgrade: true,
		// 明确不支持：xhttp 在 sing-box 中无对应传输。
		// 面板只提供 xhttp 的节点会在编译时被拒绝并写进报告，
		// 而不是产出一份核心根本不认的配置。
		domain.TransportXHTTP: false,
	},
	// TCP 上的 HTTP 伪装头（headerType）是 sing-box 没有的字段。
	tcpHeaderObfs: false,
	reality:       true,
	utls:          true,
	flow:          true,
	// verifiedAgainst 记录本表实际被验证到的核心版本。
	//
	// **这次是真的验证过了，不是猜的。** 2026-10-16 用官方预构建的
	// sing-box 1.14.3（windows-amd64，sha256 与 versions.lock.json 的
	// 锁定值一致）逐项复核：把每种协议、每种传输、以及 REALITY / uTLS /
	// flow 各生成一份配置，交给 `sing-box check` 校验。
	//
	// 复核结果修掉了四个此前会让核心拒绝整份配置的缺陷：
	//   - fake-ip 被当成默认名称服务器（`default server cannot be fakeip`）
	//   - 按域名连接的 DoH 服务器没有 domain_resolver
	//   - 出站没有 route.default_domain_resolver
	//   - 嗅探写在入站上（1.13 起被移除的旧字段）
	//   - REALITY 节点缺少 uTLS 指纹（`uTLS is required by reality client`）
	//
	// 复现方式见 TestEveryClaimedProtocolIsAcceptedByRealSingBox 等用例；
	// 需要设置 SINGBOX_BIN 指向真实二进制。
	//
	// **仍未验证的部分**：以上只证明「配置被核心接受」，
	// 不证明流量真的能穿过代理。那需要真实面板与真实节点。
	verifiedAgainst: "1.14.3",
}

// Supports 返回目标核心是否支持指定协议。
func Supports(core Core, p domain.Protocol) bool {
	if core != CoreSingBox {
		// 单内核下没有第二张表可查：其它核心名一律回答「不支持」，
		// 而不是回退到 sing-box 的表——那会把「问错了核心」变成一个
		// 看起来很正常的答案。
		return false
	}
	return capabilityTable.protocols[p]
}

// CapabilitySummary 是给 UI 用的能力摘要（用于专业模式的能力矩阵展示）。
type CapabilitySummary struct {
	Core            Core
	Protocols       []string
	Transports      []string
	TCPHeaderObfs   bool
	Reality         bool
	UTLS            bool
	Flow            bool
	VerifiedAgainst string
}

// Describe 返回目标核心的能力摘要。
func Describe(core Core) (CapabilitySummary, bool) {
	if core != CoreSingBox {
		return CapabilitySummary{}, false
	}
	c := capabilityTable
	s := CapabilitySummary{
		Core:            c.core,
		TCPHeaderObfs:   c.tcpHeaderObfs,
		Reality:         c.reality,
		UTLS:            c.utls,
		Flow:            c.flow,
		VerifiedAgainst: c.verifiedAgainst,
	}
	for p, ok := range c.protocols {
		if ok {
			s.Protocols = append(s.Protocols, string(p))
		}
	}
	for t, ok := range c.transports {
		if ok {
			s.Transports = append(s.Transports, string(t))
		}
	}
	return s, true
}

// checkNode 依据能力表校验单个节点。
//
// 不再接收核心参数：单内核构建里只有一张表（ADR-0005），
// 「未知核心」也就不是一个可能的状态——Compile 与 CompileSingBox
// 已经先把别的取值拒掉了。
//
// 返回 false 表示该节点不可编译，必须排除在产物之外。
// 所有不兼容项都会以 error 级条目写入报告，携带字段路径，便于 UI 精确定位。
func checkNode(n *domain.Node, report *Report) bool {
	c := capabilityTable
	// 消息里仍然带上核心名：用户要拿它去对照版本与文档。
	core := c.core

	if c.verifiedAgainst == "unverified" {
		// 只提示一次，避免每个节点重复刷屏。
		if !hasIssue(report, "capability", "compiler.capability_unverified") {
			report.Infof(nil, "capability", "compiler.capability_unverified",
				"核心 %s 的能力表尚未针对锁定版本实测，结论可能在 G4 修正", core)
		}
	}

	ok := true

	if !c.protocols[n.Protocol] {
		report.Errorf(n, "protocol", "compiler.protocol_unsupported",
			"%s 不支持协议 %s；该节点不会进入配置", core, n.Protocol)
		return false
	}

	if !c.transports[n.Transport] {
		report.Errorf(n, "transport.type", "compiler.transport_unsupported",
			"%s 不支持传输 %s；请改用其它传输", core, n.Transport)
		ok = false
	}

	// TCP 上的 HTTP 伪装头：sing-box 没有对应字段。
	if n.Transport == domain.TransportTCP && n.TransportOptions.HeaderType != "" && !c.tcpHeaderObfs {
		report.Errorf(n, "transport.headerType", "compiler.tcp_header_obfs_unsupported",
			"%s 不支持 TCP 上的 HTTP 伪装头（headerType=%s）", core, n.TransportOptions.HeaderType)
		ok = false
	}

	if n.Security == domain.SecurityReality && !c.reality {
		report.Errorf(n, "tls.reality", "compiler.reality_unsupported",
			"%s 不支持 REALITY", core)
		ok = false
	}

	if n.TLS.Fingerprint != "" && !c.utls {
		report.Errorf(n, "tls.fingerprint", "compiler.utls_unsupported",
			"%s 不支持 uTLS 指纹伪装", core)
		ok = false
	}

	// flow 只在 VLESS 上有意义，且必须配合 TLS 或 REALITY。
	if n.Flow != "" {
		if n.Protocol != domain.ProtocolVLESS {
			report.Errorf(n, "flow", "compiler.flow_not_applicable",
				"flow 仅适用于 VLESS，当前协议为 %s", n.Protocol)
			ok = false
		} else if !c.flow {
			report.Errorf(n, "flow", "compiler.flow_unsupported",
				"%s 不支持 VLESS flow", core)
			ok = false
		} else if n.Security != domain.SecurityTLS && n.Security != domain.SecurityReality {
			report.Errorf(n, "flow", "compiler.flow_requires_tls",
				"VLESS flow（%s）必须配合 TLS 或 REALITY", n.Flow)
			ok = false
		}
	}

	// 传输必需参数缺失属于数据问题，必须显式报错而不是产出无效配置。
	switch n.Transport {
	case domain.TransportWS, domain.TransportH2, domain.TransportHTTP, domain.TransportHTTPUpgrade:
		if n.TransportOptions.Path == "" {
			report.Errorf(n, "transport.path", "compiler.transport_path_required",
				"传输 %s 缺少路径（path）", n.Transport)
			ok = false
		}
	case domain.TransportGRPC:
		if n.TransportOptions.ServiceName == "" {
			report.Errorf(n, "transport.serviceName", "compiler.transport_service_required",
				"grpc 传输缺少 serviceName")
			ok = false
		}
	}

	// 隔离字段：来源提供但本模型不认识。它们不会进入产物，必须让用户看得见。
	if len(n.Quarantine) > 0 {
		for _, key := range n.QuarantinedKeys() {
			report.Warnf(n, "quarantine."+key, "compiler.field_quarantined",
				"字段 %q 无法转换，已保留在隔离区且不会写入配置", key)
		}
	}

	if n.TLS.AllowInsecure {
		report.Warnf(n, "tls.allowInsecure", "compiler.insecure_tls_enabled",
			"该节点关闭了证书校验（allowInsecure），存在中间人风险")
	}

	return ok
}

// hasIssue 判断报告中是否已存在指定 fieldPath 与 messageKey 的条目。
func hasIssue(report *Report, fieldPath, messageKey string) bool {
	for _, i := range report.Issues {
		if i.FieldPath == fieldPath && i.MessageKey == messageKey {
			return true
		}
	}
	return false
}
