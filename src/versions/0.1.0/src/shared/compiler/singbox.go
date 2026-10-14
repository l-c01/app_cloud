package compiler

import (
	"encoding/json"
	"fmt"
	"strings"

	"proxyclient/shared/domain"
)

// 本文件实现 sing-box 配置生成器。
//
// 字段依据（已核实的源码事实，来自 src/references/sing-box-testing/option/）：
//   - ServerOptions：         server、server_port
//   - ShadowsocksOutbound：   method、password、plugin、plugin_opts、udp_over_tcp、multiplex
//   - VMessOutbound：         uuid、security、alter_id、packet_encoding、transport、tls
//   - VLESSOutbound：         uuid、flow、packet_encoding、transport、tls
//   - TrojanOutbound：        password、transport、tls
//   - TLS 容器：              tls{enabled, server_name, insecure, alpn, utls{enabled,fingerprint},
//                             reality{enabled, public_key, short_id}}
//   - transport.type 枚举：   http、ws、quic、grpc、httpupgrade（**没有 h2，也没有 xhttp**）
//     HTTP/2 通过 type=http 表达；grpc 用 service_name；ws/httpupgrade 用 path/host/headers。

// CompileSingBox 生成 sing-box 配置。
//
// 本构建唯一的内核（ADR-0005）：能力校验、字段级错误、报告与
// 默认出口选择规则都在这里实现，不再有第二套语义需要对齐。
func CompileSingBox(req *Request) ([]byte, *Report, error) {
	if req == nil {
		return nil, nil, fmt.Errorf("nil request")
	}
	if req.Core != CoreSingBox {
		return nil, nil, fmt.Errorf("CompileSingBox called with core %q", req.Core)
	}
	if err := req.validate(); err != nil {
		return nil, nil, err
	}

	report := &Report{Core: CoreSingBox}

	usable := make([]*domain.Node, 0, len(req.Nodes))
	for _, n := range req.Nodes {
		if n == nil {
			continue
		}
		if err := n.Validate(); err != nil {
			report.Errorf(n, "", "compiler.node_invalid", "%v", err)
			continue
		}
		if checkNode(n, report) {
			usable = append(usable, n)
		}
	}
	// 去掉会产生重复出站 tag 的节点：
	// sing-box 也不接受重复的 outbound tag。
	usable = dedupeByOutboundTag(usable, report)

	if len(usable) == 0 {
		sortIssues(report.Issues)
		return nil, report, fmt.Errorf("no compilable node for core %s", CoreSingBox)
	}
	report.CompiledNodes = len(usable)

	listen := req.listenAddress()
	cfg := singboxConfig{
		Log: map[string]any{"level": req.logLevel()},
		DNS: singboxDNSSection(req.dnsOptions()),
		Inbounds: []map[string]any{
			{
				"type":        "http",
				"tag":         "http-in",
				"listen":      listen,
				"listen_port": req.Inbound.HTTPPort,
			},
			{
				"type":        "socks",
				"tag":         "socks-in",
				"listen":      listen,
				"listen_port": req.Inbound.SocksPort,
			},
		},
	}
	report.InboundPorts = map[string]int{"socks": req.Inbound.SocksPort, "http": req.Inbound.HTTPPort}

	selected := selectNode(req, report, usable)

	// sing-box 用 route.final 指定默认出口，因此出站顺序不影响默认行为。
	for _, n := range usable {
		if ob := singboxNodeOutbound(n, report); ob != nil {
			cfg.Outbounds = append(cfg.Outbounds, ob)
		}
	}
	// TUN 入站（仅当选项要求时）。
	//
	// 放在这里而不是 cfg 字面量里，是因为它的存在与否取决于选项，
	// 而字面量里已经有 HTTP 与 SOCKS 两个固定入站。
	// 混在一起会让「哪些入站是固定的、哪些是可选的」看不出来。
	if tunIn := singboxTUNInbound(req.tunOptions(), req.perAppOptions()); tunIn != nil {
		cfg.Inbounds = append(cfg.Inbounds, tunIn)
	}

	cfg.Outbounds = append(cfg.Outbounds, map[string]any{"type": "direct", "tag": "direct"})

	switch req.Policy {
	case PolicyGlobal:
		cfg.Route = &singboxRoute{Final: outboundTag(selected)}
	case PolicyDirect:
		cfg.Route = &singboxRoute{Final: "direct"}
	case PolicyRule:
		rules := []map[string]any{
			// ip_is_private 是 sing-box 内建匹配器，不依赖任何外部数据文件。
			{"ip_is_private": true, "outbound": "direct"},
		}
		final := outboundTag(selected)

		// 用户配置的规则（仅当已接入选项模型）。
		//
		// **顺序即优先级，且内建规则一律在前。**
		// 用户规则排在后面，这样很难用一条宽泛的规则意外绕开
		// 「私有地址直连」——那条一旦被绕开，局域网设备会试图走代理，
		// 症状是「打印机、投屏、NAS 忽然不能用了」，
		// 而用户不会想到是自己加的一条域名规则导致的。
		userRules := 0
		if opts := req.Options; opts != nil {
			for _, r := range opts.Rules {
				if m := singboxRouteRule(r, selected, report); m != nil {
					rules = append(rules, m)
					userRules++
				}
			}
			if f := singboxFinalOutbound(opts, selected, report); f != "" {
				final = f
			}
		}

		// 只有一条真实规则都没有时才提示规则资源缺失。
		// 用户已经配了规则却仍报「尚未接入」，会让人以为自己的配置没生效。
		if userRules == 0 {
			report.Warnf(nil, "route.rules", "compiler.rule_resources_missing",
				"规则路由所需的规则集尚未接入，本次编译按『全部经代理』处理以避免流量泄漏；接入规则资源后此行为会改变")
		}

		cfg.Route = &singboxRoute{Rules: rules, Final: final}
	}

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		sortIssues(report.Issues)
		return nil, report, fmt.Errorf("marshal sing-box config: %w", err)
	}
	sortIssues(report.Issues)
	return out, report, nil
}

type singboxConfig struct {
	Log       map[string]any   `json:"log"`
	DNS       *singboxDNS      `json:"dns,omitempty"`
	Inbounds  []map[string]any `json:"inbounds"`
	Outbounds []map[string]any `json:"outbounds"`
	Route     *singboxRoute    `json:"route,omitempty"`
}

// singboxDNS 是 sing-box 的 dns 段。
//
// 字段顺序与 sing-box 文档一致，便于人工比对生成的配置。
type singboxDNS struct {
	Servers []map[string]any `json:"servers,omitempty"`
	Rules   []map[string]any `json:"rules,omitempty"`
	// Final 指定兜底的名称服务器标签。
	Final string `json:"final,omitempty"`
	// Strategy 取 prefer_ipv4 / prefer_ipv6 / ipv4_only / ipv6_only。
	Strategy string `json:"strategy,omitempty"`
	// IndependentCache 让不同的名称服务器各用独立的缓存。
	IndependentCache bool `json:"independent_cache,omitempty"`
}

// dnsServerTag 是默认名称服务器的标签前缀。
//
// 给名称服务器起稳定的标签而不是依赖下标，是因为分流规则要按标签引用它
// （国内域名走国内 DNS）。用下标的话，用户调整服务器顺序会让所有规则
// 静默指向别的服务器——而症状只是「解析变慢了」，很难追到顺序上。
const (
	dnsTagDefault = "default"
	dnsTagFakeIP  = "fakeip"
)

// singboxDNSSection 依据选项构造 dns 段。
//
// **关闭时不返回任何东西**（调用方不写 dns 键），而不是返回一个空段。
// 空段会让核心按自己的默认值解析，而用户以为自己关掉了覆写——
// 「关了但行为变了」比「关不了」更难排查。
//
// opts 为 nil 时同样不生成：这样尚未接入选项的调用方
// （以及大量既有测试）行为完全不变。
func singboxDNSSection(opts *domain.DNSSettings) *singboxDNS {
	if opts == nil || !opts.Enabled {
		return nil
	}

	section := &singboxDNS{
		IndependentCache: true,
		// 默认只解析 IPv4。多数代理链路对 IPv6 支持不完整，
		// 开着会表现为「某些网站慢或超时」，而用户很难联想到 IPv6。
		Strategy: "prefer_ipv4",
	}
	if opts.IPv6 {
		section.Strategy = ""
	}

	// fake-ip 服务器排在最前：命中过滤列表的域名会落到后面的常规服务器。
	if opts.FakeIP {
		fakeip := map[string]any{
			"type": "fakeip",
			"tag":  dnsTagFakeIP,
		}
		if opts.FakeIPRange != "" {
			fakeip["inet4_range"] = opts.FakeIPRange
		}
		section.Servers = append(section.Servers, fakeip)
		section.Final = dnsTagFakeIP
	}

	for i, ns := range opts.Nameservers {
		if ns == "" {
			continue
		}
		tag := dnsTagDefault
		if i > 0 {
			// 首个之外的需要各自的标签，否则同名标签会被核心拒绝。
			tag = fmt.Sprintf("%s-%d", dnsTagDefault, i)
		}
		srv := map[string]any{"tag": tag}
		switch {
		case ns == "system":
			srv["type"] = "local"
		case strings.HasPrefix(ns, "https://"):
			srv["type"] = "https"
			srv["server"] = strings.TrimPrefix(ns, "https://")
		case strings.HasPrefix(ns, "tls://"):
			srv["type"] = "tls"
			srv["server"] = strings.TrimPrefix(ns, "tls://")
		default:
			srv["type"] = "udp"
			srv["server"] = ns
		}
		section.Servers = append(section.Servers, srv)
	}

	// 非 fake-ip 模式下没有 fakeip 服务器，兜底指向第一个常规服务器；
	// 一个都没有时留空，让核心用它自己的默认值，而不是我们编一个。
	if section.Final == "" && len(section.Servers) > 0 {
		if tag, ok := section.Servers[0]["tag"].(string); ok {
			section.Final = tag
		}
	}

	// 不使用 fake-ip 的域名。
	//
	// 局域网设备发现、时间同步、以及微信与 QQ 的回调域名必须在这里：
	// 它们拿到假地址就再也连不上，而症状是「某些应用莫名其妙打不开」，
	// 与 DNS 的关系很难联想。
	if opts.FakeIP && len(opts.FakeIPFilter) > 0 {
		section.Rules = append(section.Rules, map[string]any{
			"domain":   opts.FakeIPFilter,
			"server":   dnsTagDefault,
			"strategy": "prefer_ipv4",
		})
	}

	return section
}

type singboxRoute struct {
	Rules []map[string]any `json:"rules,omitempty"`
	Final string           `json:"final,omitempty"`
}

// ruleMatcherKey 把领域里的规则类型映射到 sing-box 的匹配器字段名。
//
// 两者故意不共用同一套拼写：领域的取值是界面用的（domain_suffix），
// sing-box 用的是它自己的（domain_suffix 恰好相同，但 ip_cidr 与
// process_name 在别处写法不同）。做成显式映射而不是字符串拼接，
// 是为了让「某个类型没有对应匹配器」这件事在编译期就看得见。
var ruleMatcherKey = map[domain.RuleType]string{
	domain.RuleDomain:        "domain",
	domain.RuleDomainSuffix:  "domain_suffix",
	domain.RuleDomainKeyword: "domain_keyword",
	domain.RuleIPCIDR:        "ip_cidr",
	domain.RuleProcessName:   "process_name",
	domain.RulePort:          "port",
}

// singboxRouteRule 把一条领域规则转成 sing-box 的路由规则。
//
// 返回 nil 表示这条规则**无法生成**，调用方应当跳过它。
// 无法生成的原因会写进报告——静默丢弃一条用户亲手写的规则，
// 会让「我明明加了规则却没生效」变成一个查不出来的问题。
func singboxRouteRule(r domain.Rule, selected *domain.Node, report *Report) map[string]any {
	key, ok := ruleMatcherKey[r.Type]
	if !ok {
		// rule_set 不在上面的映射里：它需要规则集资源，而资源尚未接入。
		// 这正是 rule_resources_missing 那条警告描述的缺口。
		report.Warnf(nil, "route.rules", "compiler.rule_resources_missing",
			"规则类型 %q 所需的资源尚未接入，该条规则已跳过", string(r.Type))
		return nil
	}
	if r.Value == "" {
		report.Warnf(nil, "route.rules", "compiler.rule_resources_missing",
			"规则类型 %q 的值为空，该条规则已跳过", string(r.Type))
		return nil
	}

	rule := map[string]any{key: r.Value}

	switch r.Action {
	case domain.ActionDirect:
		rule["outbound"] = "direct"
	case domain.ActionReject:
		rule["action"] = "reject"
	case domain.ActionProxy:
		rule["outbound"] = outboundTag(selected)
	case domain.ActionGroup:
		// 代理组尚未接入生成器。指向一个不存在的出站标签，
		// 核心会拒绝启动，而错误信息与「组还没做」相距很远。
		report.Warnf(nil, "route.rules", "compiler.rule_resources_missing",
			"规则动作 %q 请指定代理组，但代理组尚未接入，该条规则已跳过", string(r.Action))
		return nil
	default:
		report.Warnf(nil, "route.rules", "compiler.rule_resources_missing",
			"未知的规则动作 %q，该条规则已跳过", string(r.Action))
		return nil
	}

	return rule
}

// singboxFinalOutbound 依据选项决定兜底出口。
//
// 返回空串表示「不要改兜底」，调用方保留原值。
//
// **兜底默认走代理而不是直连**：回落直连会在规则没覆盖时
// **静默泄漏真实流量**——用户以为在走代理，实际上没有。
// 拒绝与代理组做兜底都没有意义（一个会断掉全部未匹配流量，
// 一个是尚未接入的资源），两者都明确报告并保留原值，而不是静默采用。
func singboxFinalOutbound(opts *domain.Options, selected *domain.Node, report *Report) string {
	switch opts.FinalAction {
	case domain.ActionDirect:
		return "direct"
	case domain.ActionProxy, "":
		return outboundTag(selected)
	case domain.ActionReject, domain.ActionGroup:
		report.Warnf(nil, "route.final", "compiler.rule_resources_missing",
			"兜底动作 %q 不可用，已按『全部经代理』处理以避免流量泄漏", string(opts.FinalAction))
		return outboundTag(selected)
	default:
		report.Warnf(nil, "route.final", "compiler.rule_resources_missing",
			"未知的兜底动作 %q，已按『全部经代理』处理以避免流量泄漏", string(opts.FinalAction))
		return outboundTag(selected)
	}
}

// singboxTUNInbound 依据选项构造 TUN 入站；未启用时返回 nil
// （调用方不追加任何东西，而不是追加一个 enable:false 的入站）。
//
// 不生成「关闭状态的入站」是刻意的：核心对 enable:false 的处理
// 与「根本没有这个入站」并不总是等价，而前者会在日志里留下痕迹，
// 让排查的人以为 TUN 被配置过。
func singboxTUNInbound(tun *domain.TUNSettings, perApp *domain.PerApp) map[string]any {
	if tun == nil || !tun.Enabled {
		return nil
	}

	in := map[string]any{
		"type": "tun",
		"tag":  "tun-in",
	}
	if tun.Stack != "" {
		in["stack"] = tun.Stack
	}
	if tun.Device != "" {
		in["interface_name"] = tun.Device
	}
	if tun.MTU > 0 {
		in["mtu"] = tun.MTU
	}
	if tun.AutoRoute {
		in["auto_route"] = true
	}
	if tun.StrictRoute {
		in["strict_route"] = true
	}
	if len(tun.DNSHijack) > 0 {
		in["dns_hijack"] = tun.DNSHijack
	}

	// 分应用代理。
	//
	// include_package 与 exclude_package 在 sing-box 里**互斥**：
	// 同时给出会被核心拒绝，而错误信息来自核心深处。
	// 因此这里通过 PerApp 的两个取值方法拿列表——它们保证同一时刻
	// 只有一个非空（见 domain.PerApp 的说明）。
	if perApp != nil {
		if pkgs := perApp.IncludePackages(); len(pkgs) > 0 {
			in["include_package"] = pkgs
		} else if pkgs := perApp.ExcludePackages(); len(pkgs) > 0 {
			in["exclude_package"] = pkgs
		}
		// 模式为「全部应用」或列表为空时不设过滤，即全部走代理。
		// 这与「设了一个空列表」不同：空列表在核心那里可能被解释为
		// 「不代理任何应用」，正好与用户的选择相反。
	}

	return in
}

// singboxNodeOutbound 依据协议构造 sing-box 出站。
func singboxNodeOutbound(n *domain.Node, report *Report) map[string]any {
	if n == nil {
		return nil
	}

	ob := map[string]any{
		"type":        string(n.Protocol),
		"tag":         outboundTag(n),
		"server":      n.Server,
		"server_port": n.Port,
	}

	switch n.Protocol {
	case domain.ProtocolShadowsocks:
		ob["method"] = n.Method
		ob["password"] = n.Password
		if p, ok := n.Quarantine["plugin"].(string); ok && p != "" {
			// sing-box 原生支持 SS 插件（plugin / plugin_opts）。
			ob["plugin"] = p
			report.Infof(n, "quarantine.plugin", "compiler.plugin_mapped",
				"SS 插件参数已映射到 sing-box 的 plugin 字段")
		}

	case domain.ProtocolVMess:
		ob["uuid"] = n.UUID
		ob["security"] = "auto"
		if n.AlterID != 0 {
			ob["alter_id"] = n.AlterID
		}

	case domain.ProtocolVLESS:
		ob["uuid"] = n.UUID
		if n.Flow != "" {
			ob["flow"] = n.Flow
		}

	case domain.ProtocolTrojan:
		ob["password"] = n.Password

	default:
		report.Errorf(n, "protocol", "compiler.protocol_unsupported",
			"sing-box 生成器遇到未处理的协议 %s", n.Protocol)
		return nil
	}

	if t := singboxTransport(n); t != nil {
		ob["transport"] = t
	}
	if tls := singboxTLS(n); tls != nil {
		ob["tls"] = tls
	}
	return ob
}

// singboxTransport 构造 transport 块。
//
// 注意映射关系：h2 与 http 都映射为 type="http"，因为 sing-box 的枚举里没有 h2。
func singboxTransport(n *domain.Node) map[string]any {
	switch n.Transport {
	case domain.TransportWS:
		t := map[string]any{"type": "ws"}
		if n.TransportOptions.Path != "" {
			t["path"] = n.TransportOptions.Path
		}
		if n.TransportOptions.Host != "" {
			t["headers"] = map[string]any{"Host": n.TransportOptions.Host}
		}
		return t

	case domain.TransportGRPC:
		t := map[string]any{"type": "grpc"}
		if n.TransportOptions.ServiceName != "" {
			t["service_name"] = n.TransportOptions.ServiceName
		}
		return t

	case domain.TransportHTTP, domain.TransportH2:
		t := map[string]any{"type": "http"}
		if n.TransportOptions.Path != "" {
			t["path"] = n.TransportOptions.Path
		}
		if n.TransportOptions.Host != "" {
			// host 在 sing-box 中是列表。
			t["host"] = []string{n.TransportOptions.Host}
		}
		return t

	case domain.TransportHTTPUpgrade:
		t := map[string]any{"type": "httpupgrade"}
		if n.TransportOptions.Path != "" {
			t["path"] = n.TransportOptions.Path
		}
		if n.TransportOptions.Host != "" {
			t["host"] = n.TransportOptions.Host
		}
		return t
	}
	// tcp 是默认传输，不需要 transport 块。
	return nil
}

// singboxTLS 构造 tls 块。
func singboxTLS(n *domain.Node) map[string]any {
	if n.Security != domain.SecurityTLS && n.Security != domain.SecurityReality {
		return nil
	}

	tls := map[string]any{"enabled": true}
	if n.TLS.ServerName != "" {
		tls["server_name"] = n.TLS.ServerName
	}
	if n.TLS.AllowInsecure {
		tls["insecure"] = true
	}
	if len(n.TLS.ALPN) > 0 {
		tls["alpn"] = n.TLS.ALPN
	}
	if n.TLS.Fingerprint != "" {
		tls["utls"] = map[string]any{
			"enabled":     true,
			"fingerprint": n.TLS.Fingerprint,
		}
	}
	if n.Security == domain.SecurityReality {
		tls["reality"] = map[string]any{
			"enabled":    true,
			"public_key": n.TLS.RealityPublicKey,
			"short_id":   n.TLS.RealityShortID,
		}
	}
	return tls
}
