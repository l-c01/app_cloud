package compiler

import (
	"encoding/json"
	"fmt"

	"proxyclient/shared/domain"
)

// 本文件实现 Xray 配置生成器。
//
// 字段依据（已核实的源码事实）：
//   - network 允许值：src/references/Xray-core-main/infra/conf/transport_internet.go:18-41
//     即 raw/tcp、ws/websocket、grpc、httpupgrade、h2/h3/http、xhttp/splithttp、quic 等。
//   - security 允许值：同文件 :92-119，即 ""/none、tls、reality、xtls。
//   - 出站结构按协议分别构造：shadowsocks/trojan 用 servers，vmess/vless 用 vnext+users。

// CompileXray 生成 Xray 配置。
//
// 返回值：
//   - 第一个值为配置 JSON 字节（缩进美化，便于专业模式预览与差异对比）；
//   - 第二个值为编译报告，即使出错也可能非空，调用方必须读取它。
//
// 当报告中存在 error 级条目时，产物仅包含可用节点；若没有任何可用节点，
// 则返回错误并且不产出配置。
func CompileXray(req *Request) ([]byte, *Report, error) {
	if req == nil {
		return nil, nil, fmt.Errorf("nil request")
	}
	if req.Core != CoreXray {
		return nil, nil, fmt.Errorf("CompileXray called with core %q", req.Core)
	}
	if err := req.validate(); err != nil {
		return nil, nil, err
	}

	report := &Report{Core: CoreXray}

	// 1) 能力校验，筛出可编译节点。
	usable := make([]*domain.Node, 0, len(req.Nodes))
	for _, n := range req.Nodes {
		if n == nil {
			continue
		}
		if err := n.Validate(); err != nil {
			report.Errorf(n, "", "compiler.node_invalid", "%v", err)
			continue
		}
		if checkNode(CoreXray, n, report) {
			usable = append(usable, n)
		}
	}
	if len(usable) == 0 {
		sortIssues(report.Issues)
		return nil, report, fmt.Errorf("no compilable node for core %s", CoreXray)
	}
	report.CompiledNodes = len(usable)

	// 2) 入口：本地 HTTP 与 SOCKS。
	cfg := xrayConfig{
		Log: xrayLog{Loglevel: req.logLevel()},
		Inbounds: []map[string]any{
			{
				"tag":      "socks-in",
				"listen":   req.listenAddress(),
				"port":     req.Inbound.SocksPort,
				"protocol": "socks",
				"settings": map[string]any{"udp": true},
			},
			{
				"tag":      "http-in",
				"listen":   req.listenAddress(),
				"port":     req.Inbound.HTTPPort,
				"protocol": "http",
			},
		},
	}
	report.InboundPorts = map[string]int{"socks": req.Inbound.SocksPort, "http": req.Inbound.HTTPPort}

	// 3) 出站：每个可用节点一个，另有 direct 与 block。
	//
	// Xray 没有 sing-box 那样的 route.final，**第一个出站即默认出口**。
	// 因此默认出口必须排在首位，且 tag 不能重复（重复 tag 会导致核心拒绝启动）。
	selected := selectNode(req, report, usable)

	cfg.Outbounds = make([]map[string]any, 0, len(usable)+2)
	for _, n := range usable {
		cfg.Outbounds = append(cfg.Outbounds, xrayNodeOutbound(n, report))
	}

	if req.Policy == PolicyDirect {
		// 直连策略：把 direct 放到首位作为默认出口，不再重复追加。
		cfg.Outbounds = append([]map[string]any{directOutbound()}, cfg.Outbounds...)
		cfg.Outbounds = append(cfg.Outbounds, blockOutbound())
	} else {
		if req.Policy == PolicyRule {
			// 规则资源（Geo / rule-set）尚未接入，见 R08。
			// 此时把流量交给代理出口，而不是回落直连——回落直连可能造成真实流量泄漏。
			report.Warnf(nil, "routing.policy", "compiler.rule_resources_missing",
				"规则路由所需的 Geo/规则集尚未接入，本次编译按『全部经代理』处理以避免流量泄漏；接入规则资源后此行为会改变")
		}
		// 把选定节点提到首位作为默认出口。
		reordered := make([]map[string]any, 0, len(cfg.Outbounds))
		for _, ob := range cfg.Outbounds {
			if ob["tag"] == outboundTag(selected) {
				reordered = append(reordered, ob)
			}
		}
		for _, ob := range cfg.Outbounds {
			if ob["tag"] != outboundTag(selected) {
				reordered = append(reordered, ob)
			}
		}
		cfg.Outbounds = append(reordered, directOutbound(), blockOutbound())
	}

	// 4) 路由：直连链路里显式把内网与保留地址排除，避免把本地流量送进代理。
	//
	// 这里刻意使用**显式 CIDR**而不是 geoip:private：后者依赖核心数据文件，
	// 数据文件缺失时行为不确定。显式网段不依赖任何外部资源。
	if req.Policy != PolicyDirect {
		cfg.Routing = &xrayRouting{
			DomainStrategy: "AsIs",
			Rules: []map[string]any{
				{
					"type":        "field",
					"outboundTag": "direct",
					"ip":          privateCIDRs(),
				},
			},
		}
	}

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		sortIssues(report.Issues)
		return nil, report, fmt.Errorf("marshal xray config: %w", err)
	}
	sortIssues(report.Issues)
	return out, report, nil
}

// xrayConfig 是 Xray 顶层配置。
type xrayConfig struct {
	Log       xrayLog          `json:"log"`
	Inbounds  []map[string]any `json:"inbounds"`
	Outbounds []map[string]any `json:"outbounds"`
	Routing   *xrayRouting     `json:"routing,omitempty"`
}

type xrayLog struct {
	Loglevel string `json:"loglevel"`
}

type xrayRouting struct {
	DomainStrategy string           `json:"domainStrategy,omitempty"`
	Rules          []map[string]any `json:"rules,omitempty"`
}

// outboundTag 由节点 ID 派生，保证同一节点在多次编译中得到相同的 tag，
// 便于运行期热切换与日志对照。
func outboundTag(n *domain.Node) string {
	if n == nil {
		return "proxy"
	}
	id := n.ID
	if len(id) > 8 {
		id = id[:8]
	}
	if id == "" {
		return "proxy"
	}
	return "proxy-" + id
}

// xrayNodeOutbound 依据协议构造 Xray 出站。
func xrayNodeOutbound(n *domain.Node, report *Report) map[string]any {
	if n == nil {
		return directOutbound()
	}

	ob := map[string]any{
		"tag":      outboundTag(n),
		"protocol": string(n.Protocol),
	}

	switch n.Protocol {
	case domain.ProtocolShadowsocks:
		ob["settings"] = map[string]any{
			"servers": []map[string]any{{
				"address":  n.Server,
				"port":     n.Port,
				"method":   n.Method,
				"password": n.Password,
			}},
		}

	case domain.ProtocolVMess:
		ob["settings"] = map[string]any{
			"vnext": []map[string]any{{
				"address": n.Server,
				"port":    n.Port,
				"users": []map[string]any{{
					"id":       n.UUID,
					"alterId":  n.AlterID,
					"security": "auto",
				}},
			}},
		}

	case domain.ProtocolVLESS:
		user := map[string]any{
			"id":         n.UUID,
			"encryption": "none",
		}
		if n.Flow != "" {
			user["flow"] = n.Flow
		}
		ob["settings"] = map[string]any{
			"vnext": []map[string]any{{
				"address": n.Server,
				"port":    n.Port,
				"users":   []map[string]any{user},
			}},
		}

	case domain.ProtocolTrojan:
		ob["settings"] = map[string]any{
			"servers": []map[string]any{{
				"address":  n.Server,
				"port":     n.Port,
				"password": n.Password,
			}},
		}

	default:
		// checkNode 已拦截不支持的协议；此处是防御性兜底。
		report.Errorf(n, "protocol", "compiler.protocol_unsupported",
			"Xray 生成器遇到未处理的协议 %s", n.Protocol)
	}

	if ss := xrayStreamSettings(n, report); ss != nil {
		ob["streamSettings"] = ss
	}
	return ob
}

// xrayStreamSettings 构造 streamSettings。
func xrayStreamSettings(n *domain.Node, report *Report) map[string]any {
	ss := map[string]any{
		"network":  string(n.Transport),
		"security": string(n.Security),
	}

	switch n.Transport {
	case domain.TransportWS:
		ws := map[string]any{}
		if n.TransportOptions.Path != "" {
			ws["path"] = n.TransportOptions.Path
		}
		if n.TransportOptions.Host != "" {
			ws["headers"] = map[string]any{"Host": n.TransportOptions.Host}
		}
		if len(ws) > 0 {
			ss["wsSettings"] = ws
		}

	case domain.TransportGRPC:
		ss["grpcSettings"] = map[string]any{
			"serviceName": n.TransportOptions.ServiceName,
		}

	case domain.TransportHTTP, domain.TransportH2:
		// Xray 把 h2/h3/http 都解析为 HTTP 传输（transport_internet.go:33）。
		h2 := map[string]any{}
		if n.TransportOptions.Path != "" {
			h2["path"] = n.TransportOptions.Path
		}
		if n.TransportOptions.Host != "" {
			h2["host"] = []string{n.TransportOptions.Host}
		}
		if len(h2) > 0 {
			ss["httpSettings"] = h2
		}

	case domain.TransportHTTPUpgrade:
		hu := map[string]any{}
		if n.TransportOptions.Path != "" {
			hu["path"] = n.TransportOptions.Path
		}
		if n.TransportOptions.Host != "" {
			hu["host"] = n.TransportOptions.Host
		}
		if len(hu) > 0 {
			ss["httpupgradeSettings"] = hu
		}

	case domain.TransportXHTTP:
		xh := map[string]any{}
		if n.TransportOptions.Path != "" {
			xh["path"] = n.TransportOptions.Path
		}
		if n.TransportOptions.Host != "" {
			xh["host"] = n.TransportOptions.Host
		}
		if n.TransportOptions.Mode != "" {
			xh["mode"] = n.TransportOptions.Mode
		}
		if len(xh) > 0 {
			ss["xhttpSettings"] = xh
		}

	case domain.TransportTCP:
		// TCP 上的 HTTP 伪装头。
		if n.TransportOptions.HeaderType != "" {
			tcp := map[string]any{
				"header": map[string]any{
					"type": n.TransportOptions.HeaderType,
				},
			}
			if n.TransportOptions.Path != "" || n.TransportOptions.Host != "" {
				req := map[string]any{}
				if n.TransportOptions.Path != "" {
					req["path"] = []string{n.TransportOptions.Path}
				}
				if n.TransportOptions.Host != "" {
					req["headers"] = map[string]any{"Host": []string{n.TransportOptions.Host}}
				}
				tcp["header"].(map[string]any)["request"] = req
			}
			ss["tcpSettings"] = tcp
		}
	}

	// TLS / REALITY 细节。
	if n.Security == domain.SecurityTLS || n.Security == domain.SecurityReality {
		tls := map[string]any{}
		if n.TLS.ServerName != "" {
			tls["serverName"] = n.TLS.ServerName
		}
		if n.TLS.AllowInsecure {
			tls["allowInsecure"] = true
		}
		if len(n.TLS.ALPN) > 0 {
			tls["alpn"] = n.TLS.ALPN
		}
		if n.TLS.Fingerprint != "" {
			tls["fingerprint"] = n.TLS.Fingerprint
		}

		if n.Security == domain.SecurityReality {
			reality := map[string]any{
				"publicKey": n.TLS.RealityPublicKey,
			}
			if n.TLS.RealityShortID != "" {
				reality["shortId"] = n.TLS.RealityShortID
			}
			// REALITY 下 serverName 与 fingerprint 都属于 realitySettings，
			// 不是 tlsSettings：
			//
			//   realitySettings 与 tlsSettings 是互斥的两种 TLS 呈现方式，
			//   REALITY 连接不上时会去找 tlsSettings，找不到就报
			//   「TLS 配置缺失」——一个和真实原因（字段放错了地方）无关的错误。
			if v, ok := tls["serverName"]; ok {
				reality["serverName"] = v
				delete(tls, "serverName")
			}
			if v, ok := tls["fingerprint"]; ok {
				reality["fingerprint"] = v
				delete(tls, "fingerprint")
			}
			ss["realitySettings"] = reality
		}

		// 只有还剩真正的 TLS 字段时才发出 tlsSettings。
		// REALITY 节点把字段都移走后这里自然为空，于是不会发出——
		// 这正是想要的：一个只有 REALITY 的节点不该带一份空的 tlsSettings。
		if len(tls) > 0 {
			ss["tlsSettings"] = tls
		}
	}

	return ss
}

// directOutbound 返回直连出站。
func directOutbound() map[string]any {
	return map[string]any{
		"tag":      "direct",
		"protocol": "freedom",
	}
}

// blockOutbound 返回阻断出站。
func blockOutbound() map[string]any {
	return map[string]any{
		"tag":      "block",
		"protocol": "blackhole",
		"settings": map[string]any{},
	}
}

// privateCIDRs 返回内网与保留地址网段。
//
// 这些地址必须直连，不能进入代理，否则会造成本地服务不可达与无谓的流量绕行。
// 使用显式网段而非 geoip 数据文件，避免引入外部资源依赖。
func privateCIDRs() []string {
	return []string{
		"127.0.0.0/8",
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"169.254.0.0/16",
		"100.64.0.0/10",
		"::1/128",
		"fc00::/7",
		"fe80::/10",
	}
}
