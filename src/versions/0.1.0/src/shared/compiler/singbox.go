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
	// 代理组出站。
	//
	// 放在节点出站之后：组要引用节点，而 sing-box 对出站顺序没有要求，
	// 但把「组的定义」排在「它可能引用的节点」之后读起来更顺。
	cfg.Outbounds = append(cfg.Outbounds, singboxGroupOutbounds(req.Options, report)...)

	// TUN 入站（仅当选项要求时）。
	//
	// 放在这里而不是 cfg 字面量里，是因为它的存在与否取决于选项，
	// 而字面量里已经有 HTTP 与 SOCKS 两个固定入站。
	// 混在一起会让「哪些入站是固定的、哪些是可选的」看不出来。
	if tunIn := singboxTUNInbound(req.tunOptions(), req.perAppOptions(), req.sniffOptions(), report); tunIn != nil {
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
				if m := singboxRouteRule(r, req.Options, selected, report); m != nil {
					rules = append(rules, m)
					userRules++
				}
			}
			if f := singboxFinalOutbound(opts, selected, report); f != "" {
				final = f
			}
		}

		// 一条真实规则都没有时说明「规则模式」这次退化成了「全部经代理」：
		// 除了内建的私有地址直连，没有任何东西在分流。
		//
		// 这不是错误（兜底本来就是走代理，因此不存在流量泄漏），
		// 但用户以为自己选了「按规则分流」，实际却等同于全局代理，
		// 因此必须说出来，而不是让他自己从生成的配置里发现。
		if userRules == 0 {
			report.Warnf(nil, "route.rules", "compiler.no_route_rules",
				"本次没有生效的分流规则（未配置规则，或配置的规则都被跳过），"+
					"除内建的私有地址直连外将全部经代理；这不会泄漏流量，但与『按规则分流』的预期不同")
		}

		cfg.Route = &singboxRoute{Rules: rules, Final: final}
	}

	// 规则集只在规则模式下有意义：global 与 direct 都不看规则，
	// 生成一份用不上的 rule_set 只会让核心凭空多个下载任务。
	// 用户切回规则模式后它自然又会出现。
	if req.Policy == PolicyRule && cfg.Route != nil {
		cfg.Route.RuleSet = singboxRuleSets(req.Options, report)
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

// groupOutboundType 把领域的代理组类型映射到 sing-box 的出站类型。
//
// 两个「没有直接等价物」的类型都在这里显式说明，而不是悄悄降级：
//
//   - fallback（故障转移）：sing-box 没有独立的 fallback 出站。
//     用 urltest 近似，但它选的是**延迟最低**而不是**第一个可用**，
//     行为不完全相同。调用方会就此报告一条提示。
//   - balance（负载均衡）：sing-box 同样没有等价物。近似成 selector
//     会退化为「只用一个节点」，与用户对负载均衡的预期相差很远。
//     因此**不生成**，由调用方报告并跳过。
var groupOutboundType = map[domain.ProxyGroupType]string{
	domain.GroupSelect:   "selector",
	domain.GroupURLTest:  "urltest",
	domain.GroupFallback: "urltest",
}

// singboxGroupOutbounds 依据选项构造代理组出站。
//
// 返回的出站按配置顺序排列。**不生成负载均衡组**（理由见上表），
// 但会写进报告——静默丢掉一个用户配置的组，会让「规则里引用的组不存在」
// 变成一个查不出来的问题。
func singboxGroupOutbounds(opts *domain.Options, report *Report) []map[string]any {
	if opts == nil || len(opts.Groups) == 0 {
		return nil
	}

	var out []map[string]any
	for _, g := range opts.Groups {
		if g.Name == "" {
			report.Warnf(nil, "outbounds", "compiler.proxy_group_skipped",
				"忽略一个没有名字的代理组：规则要靠 tag 引用它，没有 tag 就引用不到")
			continue
		}

		typ, ok := groupOutboundType[g.Type]
		if !ok {
			report.Warnf(nil, "outbounds", "compiler.proxy_group_skipped",
				"代理组 %q 的类型 %q 在 sing-box 里没有等价物，已跳过该组", g.Name, string(g.Type))
			continue
		}
		if g.Type == domain.GroupFallback {
			report.Warnf(nil, "outbounds", "compiler.proxy_group_skipped",
				"代理组 %q 的「故障转移」已按「自动选择」生成：sing-box 没有独立的故障转移出站，"+
					"它会选延迟最低的节点而不是第一个可用的节点", g.Name)
		}

		ob := map[string]any{
			"type": typ,
			"tag":  g.Name,
		}

		// 成员列表。
		//
		// include_all 与显式成员**不能同时给**：前者表示「包含全部节点」，
		// 后者是一份具名清单。同时给出会被核心拒绝，而错误信息
		// 与「你想表达的其实是哪一个」相距很远。
		if g.IncludeAll {
			ob["outbounds"] = []string{"direct"}
			report.Warnf(nil, "outbounds", "compiler.proxy_group_skipped",
				"代理组 %q 使用了「包含全部节点」，但节点集合要到运行时才确定，"+
					"本次只放入直连出站；请改用显式成员列表", g.Name)
		} else if len(g.Members) > 0 {
			ob["outbounds"] = g.Members
		} else {
			report.Warnf(nil, "outbounds", "compiler.proxy_group_skipped",
				"代理组 %q 没有任何成员，已跳过该组", g.Name)
			continue
		}

		// url 与 interval 只对 urltest 有意义。给 selector 加上它们
		// 是无效配置。
		if typ == "urltest" {
			if g.TestURL != "" {
				ob["url"] = g.TestURL
			}
			if g.TestInterval > 0 {
				ob["interval"] = fmt.Sprintf("%ds", g.TestInterval)
			}
		}

		out = append(out, ob)
	}
	return out
}

// hasGroup 报告选项里是否定义了指定名字的代理组。
func hasGroup(opts *domain.Options, name string) bool {
	if opts == nil {
		return false
	}
	for _, g := range opts.Groups {
		if g.Name == name {
			return true
		}
	}
	return false
}

// groupNames 返回已定义组名的列表，用于把「组名写错了」变成
// 一条能直接照着改的提示，而不是让用户去翻配置。
func groupNames(opts *domain.Options) string {
	if opts == nil || len(opts.Groups) == 0 {
		return "无"
	}
	names := make([]string, 0, len(opts.Groups))
	for _, g := range opts.Groups {
		if g.Name != "" {
			names = append(names, g.Name)
		}
	}
	if len(names) == 0 {
		return "无"
	}
	return strings.Join(names, "、")
}

type singboxRoute struct {
	Rules   []map[string]any `json:"rules,omitempty"`
	RuleSet []map[string]any `json:"rule_set,omitempty"`
	Final   string           `json:"final,omitempty"`
}

// singboxRuleSets 依据选项构造 route.rule_set。
//
// 字段依据（已核实的源码事实，来自 src/references/sing-box-testing/option/rule_set.go）：
//   - _RuleSet：            type（inline/local/remote）、tag、format（source/binary）
//   - LocalRuleSet：        path
//   - RemoteRuleSet：       url、initial_path、http_client、update_interval
//   - 多标签时才要求 URL 含 {tag} 占位符；单标签不需要
//   - format 留空时由核心按 url/path 后缀推断（ruleSetDefaultFormat）
//
// **只生成 remote**：LocalOptions 要的是核心进程能读到的一条本地路径，
// 而「规则集文件放在哪」属于运行时布局（runtime 层的事），
// 不是界面选项。没有 path 就编一个 local 规则集，只会生成一份
// 核心启动时找不到文件的配置。因此本地规则集明确跳过并报告。
//
// 与代理组一样，跳过一律写进报告：静默丢掉一个用户配置的规则集，
// 会让「规则里引用的规则集不存在」变成一个查不出来的问题。
func singboxRuleSets(opts *domain.Options, report *Report) []map[string]any {
	if opts == nil || len(opts.RuleSets) == 0 {
		return nil
	}

	var out []map[string]any
	for _, rs := range opts.RuleSets {
		if rs.Name == "" {
			report.Warnf(nil, "route.rule_set", "compiler.rule_set_invalid",
				"忽略一个没有名字的规则集：规则要靠 tag 引用它，没有 tag 就引用不到")
			continue
		}

		if rs.URL == "" {
			// 内置（本地）规则集。见上面的说明：路径属于运行时布局。
			report.Warnf(nil, "route.rule_set", "compiler.rule_set_local_unsupported",
				"规则集 %q 没有 URL（内置规则集需要核心可读的本地文件路径，属于运行时布局，本层不生成），已跳过该规则集", rs.Name)
			continue
		}

		entry := map[string]any{
			"type": "remote",
			"tag":  rs.Name,
			"url":  rs.URL,
		}

		// format 的处理顺序很关键：
		//
		//   1. 用户显式给了值 → 必须是 source/binary 之一，否则报错跳过。
		//      把一个未知取值照抄进配置，核心会以 "unknown rule-set format"
		//      拒绝启动，而那句话不会告诉用户他填的是哪个值。
		//   2. 没给 → 按后缀推断；推断得出来就写死。
		//      写死而不是留给核心推断，是因为推断规则住在核心内部，
		//      写出来能让生成的配置自解释，也便于与核心行为不符时比对。
		//   3. 推断不出来 → 不写 format，报告一条警告。
		//      **不猜**：猜错的表现是核心拒绝加载该规则集，
		//      而用户会去改 URL。
		format, ok := domain.NormalizeRuleSetFormat(rs.Format)
		if !ok {
			report.Warnf(nil, "route.rule_set", "compiler.rule_set_invalid",
				"规则集 %q 的格式 %q 不受支持（只接受 %s 或 %s），已跳过该规则集",
				rs.Name, rs.Format, domain.RuleSetFormatSource, domain.RuleSetFormatBinary)
			continue
		}
		if format == "" {
			format = domain.InferRuleSetFormat(rs.URL)
		}
		if format != "" {
			entry["format"] = format
		} else {
			report.Warnf(nil, "route.rule_set", "compiler.rule_set_format_unknown",
				"规则集 %q 的地址后缀既不是 .srs 也不是 .json，无法推断格式；"+
					"已交由核心自行判断，若加载失败请显式指定格式（%s 或 %s）",
				rs.Name, domain.RuleSetFormatSource, domain.RuleSetFormatBinary)
		}

		if rs.UpdateInterval > 0 {
			// 核心的 update_interval 是时长字符串（badoption.Duration），
			// 与代理组的 interval 同一约定。
			entry["update_interval"] = fmt.Sprintf("%dh", rs.UpdateInterval)
		}

		out = append(out, entry)
	}
	return out
}

// hasRuleSet 报告选项里是否定义了指定名字的规则集。
func hasRuleSet(opts *domain.Options, name string) bool {
	if opts == nil {
		return false
	}
	for _, rs := range opts.RuleSets {
		if rs.Name == name {
			return true
		}
	}
	return false
}

// ruleSetNames 返回已定义规则集名的列表，用于把「规则集名写错了」
// 变成一条能直接照着改的提示。
func ruleSetNames(opts *domain.Options) string {
	if opts == nil || len(opts.RuleSets) == 0 {
		return "无"
	}
	names := make([]string, 0, len(opts.RuleSets))
	for _, rs := range opts.RuleSets {
		if rs.Name != "" {
			names = append(names, rs.Name)
		}
	}
	if len(names) == 0 {
		return "无"
	}
	return strings.Join(names, "、")
}

// ruleMatcherKey 把领域里的规则类型映射到 sing-box 的匹配器字段名。
//
// 两者故意不共用同一套拼写：领域的取值是界面用的（domain_suffix），
// sing-box 用的是它自己的（domain_suffix 恰好相同，但 ip_cidr 与
// process_name 在别处写法不同）。做成显式映射而不是字符串拼接，
// 是为了让「某个类型没有对应匹配器」这件事在编译期就看得见。
//
// rule_set 也在这里：它的匹配器字段就叫 rule_set，取值是规则集的 tag
// （源码依据：option/rule.go 的
// `RuleSet badoption.Listable[string] \`json:"rule_set,omitempty" reference:"rule_set"\``）。
// 它曾经不在表里，因为规则集资源尚未接入；接入之后它与其他类型
// 走同一条路径，唯一的差别是要额外核对引用的规则集存在。
var ruleMatcherKey = map[domain.RuleType]string{
	domain.RuleDomain:        "domain",
	domain.RuleDomainSuffix:  "domain_suffix",
	domain.RuleDomainKeyword: "domain_keyword",
	domain.RuleIPCIDR:        "ip_cidr",
	domain.RuleProcessName:   "process_name",
	domain.RuleRuleSet:       "rule_set",
	domain.RulePort:          "port",
}

// singboxRouteRule 把一条领域规则转成 sing-box 的路由规则。
//
// 返回 nil 表示这条规则**无法生成**，调用方应当跳过它。
// 无法生成的原因会写进报告——静默丢弃一条用户亲手写的规则，
// 会让「我明明加了规则却没生效」变成一个查不出来的问题。
func singboxRouteRule(r domain.Rule, opts *domain.Options, selected *domain.Node, report *Report) map[string]any {
	key, ok := ruleMatcherKey[r.Type]
	if !ok {
		report.Warnf(nil, "route.rules", "compiler.route_rule_skipped",
			"规则类型 %q 不受支持（没有对应的 sing-box 匹配器），该条规则已跳过", string(r.Type))
		return nil
	}
	if r.Value == "" {
		report.Warnf(nil, "route.rules", "compiler.route_rule_skipped",
			"规则类型 %q 的值为空，该条规则已跳过", string(r.Type))
		return nil
	}

	// rule_set 的取值是一个 tag，必须核对它指向的规则集真的定义了。
	//
	// 指向不存在的 tag 会让核心拒绝启动，而错误信息与「规则集名写错了」
	// 相距很远。这里提前把它变成一条能照着改的提示。
	if r.Type == domain.RuleRuleSet && !hasRuleSet(opts, r.Value) {
		report.Warnf(nil, "route.rules", "compiler.route_rule_skipped",
			"规则引用的规则集 %q 不存在（已定义的规则集：%s），该条规则已跳过",
			r.Value, ruleSetNames(opts))
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
		// 指向一个不存在的出站标签，核心会拒绝启动，
		// 而错误信息与「规则里写的组名不对」相距很远。
		// 因此先在这里核对组是否存在。
		if r.GroupName == "" {
			report.Warnf(nil, "route.rules", "compiler.route_rule_skipped",
				"规则动作 %q 需要指定代理组，但组名为空，该条规则已跳过", string(r.Action))
			return nil
		}
		if !hasGroup(opts, r.GroupName) {
			report.Warnf(nil, "route.rules", "compiler.route_rule_skipped",
				"规则引用的代理组 %q 不存在（已定义的组：%s），该条规则已跳过",
				r.GroupName, groupNames(opts))
			return nil
		}
		rule["outbound"] = r.GroupName
	default:
		report.Warnf(nil, "route.rules", "compiler.route_rule_skipped",
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
		report.Warnf(nil, "route.final", "compiler.final_action_unavailable",
			"兜底动作 %q 不可用，已按『全部经代理』处理以避免流量泄漏", string(opts.FinalAction))
		return outboundTag(selected)
	default:
		report.Warnf(nil, "route.final", "compiler.final_action_unavailable",
			"未知的兜底动作 %q，已按『全部经代理』处理以避免流量泄漏", string(opts.FinalAction))
		return outboundTag(selected)
	}
}

// singboxSniffer 把嗅探选项写进入站。
//
// 字段依据（已核实的源码事实，来自 src/references/sing-box-testing/option/inbound.go）：
//
//	SniffEnabled             bool `json:"sniff,omitempty"`
//	SniffOverrideDestination bool `json:"sniff_override_destination,omitempty"`
//	SniffTimeout             badoption.Duration `json:"sniff_timeout,omitempty"`
//
// 这三行**解决了一份设计文档里悬着的问题**。ui-options.md 2.5 节原先写
// 「嗅探协议 TLS / HTTP / QUIC」，而交接文档记录了「没有真核心可以验证，
// 因此没有实现」——不确定协议列表是不是入站字段。
//
// 读源码的结论是：**它确实不是入站字段**。入站层只有上面三个字段，
// 其中 enabled 是一个 bool。协议列表住在**路由动作**里
// （option/rule_action.go 的 RouteActionSniff：
//
//	Sniffer badoption.Listable[string] `json:"sniffer,omitempty"`
//	                                          enum:"tls,http,quic,dns,stun,..."`
//
// 而路由动作需要 `action: "sniff"` 这样一条规则才生效）。
//
// 因此本函数：
//   - 忠实生成入站层的 sniff 与 sniff_override_destination；
//   - **对协议列表明确报告「本版本不生效」**，而不是把它塞进入站
//     （塞进去会被核心当作未知字段拒绝启动，而错误信息来自核心深处）
//     或静默丢弃（用户会以为按 tls 嗅探已经生效）。
//
// 覆写目的地址的默认关（见 domain.SniffSettings 的说明）。
func singboxSniffer(sniff *domain.SniffSettings, report *Report) (enabled, override bool) {
	if sniff == nil || !sniff.Enabled {
		return false, false
	}
	if len(sniff.Protocols) > 0 {
		report.Warnf(nil, "inbounds.sniff", "compiler.sniff_protocols_not_applied",
			"嗅探协议列表（%s）在 sing-box 里属于路由动作（action: sniff 的 sniffer 字段），"+
				"不是入站字段，本版本未生成；入站层只启用嗅探本身",
			strings.Join(sniff.Protocols, "、"))
	}
	return true, sniff.OverrideDestination
}

// singboxTUNInbound 依据选项构造 TUN 入站；未启用时返回 nil
// （调用方不追加任何东西，而不是追加一个 enable:false 的入站）。
//
// 不生成「关闭状态的入站」是刻意的：核心对 enable:false 的处理
// 与「根本没有这个入站」并不总是等价，而前者会在日志里留下痕迹，
// 让排查的人以为 TUN 被配置过。
func singboxTUNInbound(tun *domain.TUNSettings, perApp *domain.PerApp, sniff *domain.SniffSettings, report *Report) map[string]any {
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
	// DNS 处理方式。
	//
	// **这里原先写的是 `dns_hijack`，那是个不存在的字段。**
	// 核实依据：option/tun.go 的 TunInboundOptions 里只有
	// `dns_mode`（enum: disabled/native/hijack）与 `dns_address`
	// （只接受 IP，不含端口）。核心对未知字段是拒绝启动的，
	// 因此原写法会让每一份开了 TUN 的配置都起不来。
	if tun.DNSMode != "" {
		in["dns_mode"] = tun.DNSMode
	}
	if len(tun.DNSAddress) > 0 {
		in["dns_address"] = tun.DNSAddress
	}

	// 嗅探。
	//
	// 只在开启时写键：核心的这两个字段都是 omitempty，写 false 与不写
	// 生成的是同一份配置，但显式写出来会让「用户关掉了它」与
	// 「这个版本还没实现它」在配置文件里长得一模一样。
	if on, override := singboxSniffer(sniff, report); on {
		in["sniff"] = true
		if override {
			in["sniff_override_destination"] = true
		}
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
