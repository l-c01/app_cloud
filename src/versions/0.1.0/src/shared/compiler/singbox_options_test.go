package compiler

import (
	"encoding/json"
	"strings"
	"testing"

	"proxyclient/shared/domain"
)

// decodeConfig 把生成的配置解回 map，便于按字段断言。
//
// 断言「某个键**不存在**」必须能表达出来。只断言「存在且等于某值」
// 会让「关闭时不应生成该段」这类要求悄悄失效——
// 而那正是本批选项里最容易做错、也最难在运行期发现的一类。
func decodeConfig(t *testing.T, out []byte) map[string]any {
	t.Helper()
	var cfg map[string]any
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("生成的配置不是合法 JSON: %v", err)
	}
	return cfg
}

// compileWithOptions 用一份选项编译单个节点，返回解开的配置。
//
// 策略固定为 rule，而不是 baseRequest 的 global：规则集与规则只在
// 规则模式下生成（global / direct 都不看规则，生成一份用不上的
// rule_set 只会让核心凭空多出下载任务）。用 global 的话，
// 所有针对 route 段的断言都会看到空结果，而原因与被测的选项无关。
func compileWithOptions(t *testing.T, opts *domain.Options) map[string]any {
	t.Helper()
	req := baseRequest(trojanNode("A"))
	req.Policy = PolicyRule
	req.Options = opts
	out, _, err := CompileSingBox(req)
	if err != nil {
		t.Fatalf("CompileSingBox: %v", err)
	}
	return decodeConfig(t, out)
}

// tunInbound 在配置里找 TUN 入站；找不到返回 nil。
func tunInbound(cfg map[string]any) map[string]any {
	inbounds, _ := cfg["inbounds"].([]any)
	for _, raw := range inbounds {
		in, _ := raw.(map[string]any)
		if in["type"] == "tun" {
			return in
		}
	}
	return nil
}

// 关闭 DNS 时必须**不生成 dns 键**，而不是生成一个空的。
//
// 空段会让核心按自己的默认值解析，而用户以为自己关掉了覆写。
// 「关了但行为变了」比「关不了」更难排查。
func TestDNSDisabledOmitsSection(t *testing.T) {
	opts := domain.DefaultOptions()
	opts.DNS.Enabled = false

	cfg := compileWithOptions(t, &opts)
	if _, ok := cfg["dns"]; ok {
		t.Errorf("DNS 关闭时不应生成 dns 段，实际生成了：%v", cfg["dns"])
	}
}

// DNS 开启时应当生成，且默认只解析 IPv4。
func TestDNSDefaultsDisableIPv6(t *testing.T) {
	opts := domain.DefaultOptions()

	cfg := compileWithOptions(t, &opts)
	dns, ok := cfg["dns"].(map[string]any)
	if !ok {
		t.Fatalf("DNS 开启时应当生成 dns 段，实际没有")
	}
	if dns["strategy"] != "prefer_ipv4" {
		t.Errorf("IPv6 默认关闭时 strategy 应为 prefer_ipv4，实际 %v", dns["strategy"])
	}
}

// 未启用 TUN 时不应生成 TUN 入站。
//
// 生成一个 enable:false 的入站与「根本没有这个入站」并不等价：
// 前者会在日志里留下痕迹，让排查的人以为 TUN 被配置过。
func TestTUNDisabledOmitsInbound(t *testing.T) {
	opts := domain.DefaultOptions()
	opts.Inbound.TUN.Enabled = false

	cfg := compileWithOptions(t, &opts)
	if in := tunInbound(cfg); in != nil {
		t.Errorf("TUN 未启用时不应生成 TUN 入站，实际生成了：%v", in)
	}
}

// 分应用代理的两个列表在 sing-box 里互斥，不能同时出现。
func TestPerAppIncludeAndExcludeAreExclusive(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mode    domain.PerAppMode
		wantKey string
		goneKey string
	}{
		{"仅代理所选", domain.PerAppInclude, "include_package", "exclude_package"},
		{"排除所选", domain.PerAppExclude, "exclude_package", "include_package"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := domain.DefaultOptions()
			opts.Inbound.TUN.Enabled = true
			opts.Inbound.PerApp = domain.PerApp{
				Mode:     tc.mode,
				Packages: []string{"com.example.app"},
			}

			in := tunInbound(compileWithOptions(t, &opts))
			if in == nil {
				t.Fatalf("TUN 已启用，应当生成 TUN 入站")
			}
			if _, ok := in[tc.wantKey]; !ok {
				t.Errorf("模式 %s 应当生成 %s", tc.mode, tc.wantKey)
			}
			if _, ok := in[tc.goneKey]; ok {
				t.Errorf("模式 %s 不应同时生成 %s：两者互斥，同时给出会被核心拒绝", tc.mode, tc.goneKey)
			}
		})
	}
}

// 「全部应用」模式不应生成任何包名过滤。
//
// 设一个**空列表**与不设过滤不同：空列表在核心那里可能被解释为
// 「不代理任何应用」，正好与用户的选择相反。
func TestPerAppAllOmitsFilters(t *testing.T) {
	opts := domain.DefaultOptions()
	opts.Inbound.TUN.Enabled = true
	opts.Inbound.PerApp = domain.PerApp{Mode: domain.PerAppAll}

	in := tunInbound(compileWithOptions(t, &opts))
	if in == nil {
		t.Fatalf("TUN 已启用，应当生成 TUN 入站")
	}
	for _, k := range []string{"include_package", "exclude_package"} {
		if _, ok := in[k]; ok {
			t.Errorf("「全部应用」模式不应生成 %s", k)
		}
	}
}

// 用户规则的顺序必须原样保留，且内建规则在它们之前。
//
// 顺序即优先级。内建规则（私有地址直连）排在最前，
// 这样很难用一条宽泛的规则意外绕开它——绕开之后当局域网设备
// 试图走代理时会失败，而用户不会想到是自己加的域名规则导致的。
func TestUserRulesKeepOrderAfterBuiltins(t *testing.T) {
	opts := domain.DefaultOptions()
	opts.Rules = []domain.Rule{
		{Type: domain.RuleDomainSuffix, Value: "example.com", Action: domain.ActionDirect},
		{Type: domain.RuleDomainKeyword, Value: "ads", Action: domain.ActionReject},
	}

	req := baseRequest(trojanNode("A"))
	req.Policy = PolicyRule
	req.Options = &opts
	out, _, err := CompileSingBox(req)
	if err != nil {
		t.Fatalf("CompileSingBox: %v", err)
	}
	cfg := decodeConfig(t, out)

	route, _ := cfg["route"].(map[string]any)
	rules, _ := route["rules"].([]any)
	if len(rules) < 3 {
		t.Fatalf("应当至少生成 1 条内建规则 + 2 条用户规则，实际 %d 条", len(rules))
	}

	// 第一条必须是内建规则。
	if first, _ := rules[0].(map[string]any); first["ip_is_private"] != true {
		t.Errorf("内建规则（ip_is_private）必须排在最前，实际第一条是 %v", rules[0])
	}

	// 用户规则按输入顺序跟在后面。
	second, _ := rules[1].(map[string]any)
	if second["domain_suffix"] != "example.com" {
		t.Errorf("第 2 条应当是第一条用户规则，实际 %v", second)
	}
	third, _ := rules[2].(map[string]any)
	if third["domain_keyword"] != "ads" {
		t.Errorf("第 3 条应当是第二条用户规则，实际 %v", third)
	}
	// 拒绝动作用的是 action，不是 outbound——写错的话核心会把它
	// 当成一个需要解析的出站标签。
	if third["action"] != "reject" {
		t.Errorf("拒绝动作应当写 action=reject，实际 %v", third)
	}
}

// 负载均衡在 sing-box 里没有等价物，必须**不生成**并报告，
// 而不是悄悄降级成一个只用一个节点的 selector。
func TestBalanceGroupIsSkippedNotDowngraded(t *testing.T) {
	opts := domain.DefaultOptions()
	opts.Groups = []domain.ProxyGroup{
		{Name: "负载", Type: domain.GroupBalance, Members: []string{"A"}},
	}

	req := baseRequest(trojanNode("A"))
	req.Options = &opts
	out, report, err := CompileSingBox(req)
	if err != nil {
		t.Fatalf("CompileSingBox: %v", err)
	}
	cfg := decodeConfig(t, out)

	outbounds, _ := cfg["outbounds"].([]any)
	for _, raw := range outbounds {
		ob, _ := raw.(map[string]any)
		if ob["tag"] == "负载" {
			t.Errorf("负载均衡组没有等价物，不应生成，实际生成了 %v", ob)
		}
	}

	// 必须留下可见的记录，否则用户只会看到「组不见了」。
	found := false
	for _, iss := range report.Issues {
		if iss.MessageKey == "compiler.proxy_group_skipped" && strings.Contains(iss.Message, "负载") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("跳过负载均衡组时必须写进报告，实际报告里没有提到它")
	}
}

// routeSection 取出配置里的 route 段。
func routeSection(t *testing.T, cfg map[string]any) map[string]any {
	t.Helper()
	route, ok := cfg["route"].(map[string]any)
	if !ok {
		t.Fatalf("应当生成 route 段，实际没有")
	}
	return route
}

// compileInRuleMode 用规则模式编译，返回配置与报告。
//
// 规则集与用户规则**只在规则模式下生成**（global / direct 都不看规则，
// 生成一份用不上的 rule_set 只会让核心凭空多出下载任务）。
// 因此凡是断言 route.rule_set 的用例都必须走这里，而不是
// compileWithOptions——后者用的是 baseRequest 的 global 策略，
// 会稳定地产生空结果，让断言看起来像「生成器坏了」。
func compileInRuleMode(t *testing.T, opts *domain.Options) (map[string]any, *Report) {
	t.Helper()
	req := baseRequest(trojanNode("A"))
	req.Policy = PolicyRule
	req.Options = opts
	out, report, err := CompileSingBox(req)
	if err != nil {
		t.Fatalf("CompileSingBox: %v", err)
	}
	return decodeConfig(t, out), report
}

// ruleSetsIn 取出 route.rule_set 列表。
func ruleSetsIn(t *testing.T, cfg map[string]any) []map[string]any {
	t.Helper()
	raw, _ := routeSection(t, cfg)["rule_set"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		if m, ok := r.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// 规则集必须在 route 段里生成，且格式按 URL 后缀推断后**写死**。
//
// 写死而不是留给核心推断：推断规则住在核心内部，写出来能让生成的
// 配置自解释，也便于日后与核心行为不符时比对。
func TestRuleSetRemoteWithInferredFormat(t *testing.T) {
	opts := domain.DefaultOptions()
	opts.RuleSets = []domain.RuleSet{
		{Name: "geosite-cn", URL: "https://example.com/geosite-cn.srs", UpdateInterval: 24},
		{Name: "geoip-cn", URL: "https://example.com/geoip-cn.json"},
	}

	cfg := compileWithOptions(t, &opts)
	sets := ruleSetsIn(t, cfg)
	if len(sets) != 2 {
		t.Fatalf("应当生成 2 个规则集，实际 %d 个：%v", len(sets), sets)
	}

	if sets[0]["type"] != "remote" {
		t.Errorf("规则集应当生成 type=remote，实际 %v", sets[0]["type"])
	}
	if sets[0]["tag"] != "geosite-cn" {
		t.Errorf("规则集 tag 应当用名字，实际 %v", sets[0]["tag"])
	}
	if sets[0]["format"] != "binary" {
		t.Errorf(".srs 后缀应当推断为 binary，实际 %v", sets[0]["format"])
	}
	if sets[0]["update_interval"] != "24h" {
		t.Errorf("更新间隔应当写成时长字符串 24h，实际 %v", sets[0]["update_interval"])
	}
	if sets[1]["format"] != "source" {
		t.Errorf(".json 后缀应当推断为 source，实际 %v", sets[1]["format"])
	}
}

// 后缀推断不出来时**不能猜**，必须报告。
//
// 猜错的表现是核心拒绝加载该规则集，而用户会去改 URL——
// 方向完全错了。
func TestRuleSetUnknownFormatIsReportedNotGuessed(t *testing.T) {
	opts := domain.DefaultOptions()
	opts.RuleSets = []domain.RuleSet{
		{Name: "odd", URL: "https://example.com/download?id=7"},
	}

	cfg, report := compileInRuleMode(t, &opts)

	sets := ruleSetsIn(t, cfg)
	if len(sets) == 0 {
		t.Fatalf("应当为该规则集生成条目，实际一个都没有")
	}
	set := sets[0]
	if _, ok := set["format"]; ok {
		t.Errorf("推断不出格式时不应写 format（那是猜的），实际写了 %v", set["format"])
	}

	found := false
	for _, iss := range report.Issues {
		if iss.MessageKey == "compiler.rule_set_format_unknown" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("推断不出格式时必须报告，实际报告里没有 rule_set_format_unknown")
	}
}

// 非法的 format 必须**报错跳过**，不能照抄进配置。
//
// 照抄之后核心会以 "unknown rule-set format" 拒绝启动，
// 而那句话不会告诉用户他填的是哪个值。
func TestRuleSetInvalidFormatIsSkipped(t *testing.T) {
	opts := domain.DefaultOptions()
	opts.RuleSets = []domain.RuleSet{
		{Name: "bad", URL: "https://example.com/x.srs", Format: "mrs"},
	}

	cfg, _ := compileInRuleMode(t, &opts)
	if sets := ruleSetsIn(t, cfg); len(sets) != 0 {
		t.Errorf("非法 format 的规则集不应生成，实际生成了 %v", sets)
	}
}

// 内置（无 URL）规则集不生成：它需要核心可读的本地文件路径，
// 属于运行时布局，编一个出来只会让核心启动时找不到文件。
// 但必须报告，否则规则引用了它时用户查不出原因。
func TestLocalRuleSetSkippedWithReport(t *testing.T) {
	opts := domain.DefaultOptions()
	opts.RuleSets = []domain.RuleSet{{Name: "builtin"}}

	cfg, report := compileInRuleMode(t, &opts)

	if sets := ruleSetsIn(t, cfg); len(sets) != 0 {
		t.Errorf("无 URL 的规则集不应生成，实际生成了 %v", sets)
	}
	found := false
	for _, iss := range report.Issues {
		if iss.MessageKey == "compiler.rule_set_local_unsupported" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("跳过本地规则集时必须报告，实际没有 rule_set_local_unsupported")
	}
}

// 规则引用规则集时，生成的匹配器字段是 rule_set，取值是 tag。
func TestRuleSetRuleCompiles(t *testing.T) {
	opts := domain.DefaultOptions()
	opts.RuleSets = []domain.RuleSet{
		{Name: "geosite-cn", URL: "https://example.com/geosite-cn.srs"},
	}
	opts.Rules = []domain.Rule{
		{Type: domain.RuleRuleSet, Value: "geosite-cn", Action: domain.ActionDirect},
	}

	req := baseRequest(trojanNode("A"))
	req.Policy = PolicyRule
	req.Options = &opts
	out, _, err := CompileSingBox(req)
	if err != nil {
		t.Fatalf("CompileSingBox: %v", err)
	}

	rules, _ := routeSection(t, decodeConfig(t, out))["rules"].([]any)
	var hit map[string]any
	for _, raw := range rules {
		if m, ok := raw.(map[string]any); ok && m["rule_set"] == "geosite-cn" {
			hit = m
			break
		}
	}
	if hit == nil {
		t.Fatalf("引用已定义规则集的规则应当生成，实际规则列表为 %v", rules)
	}
	if hit["outbound"] != "direct" {
		t.Errorf("直连动作应当写 outbound=direct，实际 %v", hit["outbound"])
	}
}

// 引用不存在的规则集会生成一个核心无法解析的 tag，必须提前拦下。
func TestRuleSetRuleWithUnknownSetIsSkipped(t *testing.T) {
	opts := domain.DefaultOptions()
	opts.Rules = []domain.Rule{
		{Type: domain.RuleRuleSet, Value: "nope", Action: domain.ActionDirect},
	}

	cfg := compileWithOptions(t, &opts)
	rules, _ := routeSection(t, cfg)["rules"].([]any)
	for _, raw := range rules {
		if m, ok := raw.(map[string]any); ok && m["rule_set"] != nil {
			t.Errorf("引用不存在规则集的规则不应生成，实际生成了 %v", m)
		}
	}
}

// global / direct 模式不看规则，因此不应生成用不上的 rule_set——
// 那只会让核心凭空多出下载任务。
func TestRuleSetsOmittedOutsideRuleMode(t *testing.T) {
	for _, policy := range []OutboundPolicy{PolicyGlobal, PolicyDirect} {
		opts := domain.DefaultOptions()
		opts.RuleSets = []domain.RuleSet{
			{Name: "geosite-cn", URL: "https://example.com/geosite-cn.srs"},
		}

		req := baseRequest(trojanNode("A"))
		req.Policy = policy
		req.Options = &opts
		out, _, err := CompileSingBox(req)
		if err != nil {
			t.Fatalf("CompileSingBox(%s): %v", policy, err)
		}

		if sets := ruleSetsIn(t, decodeConfig(t, out)); len(sets) != 0 {
			t.Errorf("策略 %s 下不应生成规则集，实际生成了 %v", policy, sets)
		}
	}
}

// 嗅探开启时写进入站；入站层只有 sniff 与 sniff_override_destination，
// **协议列表不是入站字段**（源码依据：option/inbound.go）。
//
// 把它塞进入站会被核心当作未知字段拒绝启动，因此这里断言
// 入站里绝不出现协议相关的键。
func TestSniffWritesOnlyInboundLevelFields(t *testing.T) {
	opts := domain.DefaultOptions()
	opts.Inbound.TUN.Enabled = true
	opts.Inbound.Sniffer = domain.SniffSettings{
		Enabled:             true,
		Protocols:           []string{"tls", "http"},
		OverrideDestination: true,
	}

	req := baseRequest(trojanNode("A"))
	req.Options = &opts
	out, report, err := CompileSingBox(req)
	if err != nil {
		t.Fatalf("CompileSingBox: %v", err)
	}
	in := tunInbound(decodeConfig(t, out))
	if in == nil {
		t.Fatalf("TUN 已启用，应当生成 TUN 入站")
	}

	if in["sniff"] != true {
		t.Errorf("嗅探开启时入站应当有 sniff=true，实际 %v", in["sniff"])
	}
	if in["sniff_override_destination"] != true {
		t.Errorf("覆写开启时应当有 sniff_override_destination=true，实际 %v", in["sniff_override_destination"])
	}
	for _, k := range []string{"sniff_protocols", "protocols", "sniffer"} {
		if _, ok := in[k]; ok {
			t.Errorf("入站里不应出现 %s：协议列表属于路由动作，不是入站字段", k)
		}
	}

	// 协议列表没有生效，必须明确说出来，否则它就是「改了没反应」的伪选项。
	found := false
	for _, iss := range report.Issues {
		if iss.MessageKey == "compiler.sniff_protocols_not_applied" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("协议列表未生效时必须报告，实际没有 sniff_protocols_not_applied")
	}
}

// 嗅探关闭时不应写入站，也不应报告协议问题。
func TestSniffDisabledOmitsFields(t *testing.T) {
	opts := domain.DefaultOptions()
	opts.Inbound.TUN.Enabled = true
	opts.Inbound.Sniffer.Enabled = false

	req := baseRequest(trojanNode("A"))
	req.Options = &opts
	out, report, err := CompileSingBox(req)
	if err != nil {
		t.Fatalf("CompileSingBox: %v", err)
	}

	in := tunInbound(decodeConfig(t, out))
	if _, ok := in["sniff"]; ok {
		t.Errorf("嗅探关闭时不应写入 sniff 键，实际 %v", in["sniff"])
	}
	for _, iss := range report.Issues {
		if iss.MessageKey == "compiler.sniff_protocols_not_applied" {
			t.Errorf("嗅探关闭时不应报告协议列表问题")
		}
	}
}

// 规则模式下一旦没有生效的规则，必须说明「这次等同于全局代理」。
//
// 不泄漏流量，但与「按规则分流」的预期不同——用户需要知道。
func TestRuleModeWithoutRulesWarns(t *testing.T) {
	opts := domain.DefaultOptions()

	req := baseRequest(trojanNode("A"))
	req.Policy = PolicyRule
	req.Options = &opts
	_, report, err := CompileSingBox(req)
	if err != nil {
		t.Fatalf("CompileSingBox: %v", err)
	}

	found := false
	for _, iss := range report.Issues {
		if iss.MessageKey == "compiler.no_route_rules" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("规则模式下没有生效规则时必须报告，实际没有 no_route_rules")
	}
}

// TUN 的 DNS 处理必须用真实字段 dns_mode / dns_address。
//
// **这是一条回归测试。** 这里曾经生成 dns_hijack，
// 而 option/tun.go 的 TunInboundOptions 里根本没有这个字段——
// 核心对未知字段是拒绝启动的，也就是说每一份开了 TUN 的配置
// 都起不来。字段名写得「看起来很合理」正是它一直没被发现的原因。
//
// 断言里同时包含「不该有的键不存在」，因为只断言 dns_mode 存在
// 无法阻止 dns_hijack 被重新加回来。
func TestTUNDNSModeUsesRealFields(t *testing.T) {
	opts := domain.DefaultOptions()
	opts.Inbound.TUN.Enabled = true

	req := baseRequest(trojanNode("A"))
	req.Options = &opts
	out, _, err := CompileSingBox(req)
	if err != nil {
		t.Fatalf("CompileSingBox: %v", err)
	}
	in := tunInbound(decodeConfig(t, out))
	if in == nil {
		t.Fatalf("TUN 已启用，应当生成 TUN 入站")
	}

	if in["dns_mode"] != "hijack" {
		t.Errorf("默认应当生成 dns_mode=hijack，实际 %v", in["dns_mode"])
	}
	for _, k := range []string{"dns_hijack", "dns_hijack_address", "dns_hijack_any"} {
		if _, ok := in[k]; ok {
			t.Errorf("TUN 入站里不应出现 %s：sing-box 的 TunInboundOptions 没有这个字段，"+
				"核心会因未知字段拒绝启动", k)
		}
	}
}

// dns_address 只在给出时生成，且不含端口（核心的字段是 netip.Addr）。
func TestTUNDNSAddressOnlyWhenSet(t *testing.T) {
	opts := domain.DefaultOptions()
	opts.Inbound.TUN.Enabled = true

	req := baseRequest(trojanNode("A"))
	req.Options = &opts
	out, _, err := CompileSingBox(req)
	if err != nil {
		t.Fatalf("CompileSingBox: %v", err)
	}
	if in := tunInbound(decodeConfig(t, out)); in != nil {
		if _, ok := in["dns_address"]; ok {
			t.Errorf("未设置 DNS 地址时不应生成 dns_address，实际 %v", in["dns_address"])
		}
	}

	opts.Inbound.TUN.DNSAddress = []string{"172.19.0.2"}
	req.Options = &opts
	out, _, err = CompileSingBox(req)
	if err != nil {
		t.Fatalf("CompileSingBox: %v", err)
	}
	in := tunInbound(decodeConfig(t, out))
	addr, ok := in["dns_address"].([]any)
	if !ok || len(addr) != 1 || addr[0] != "172.19.0.2" {
		t.Errorf("设置了 DNS 地址时应当生成 dns_address，实际 %v", in["dns_address"])
	}
}
