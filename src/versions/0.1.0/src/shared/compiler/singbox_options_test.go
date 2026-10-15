package compiler

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
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
	if len(rules) < 4 {
		t.Fatalf("应当至少生成 1 条嗅探规则 + 1 条内建规则 + 2 条用户规则，实际 %d 条", len(rules))
	}

	// 第一条必须是嗅探规则。
	//
	// 它与内建规则谁在前这一点是**有对错**的：嗅探要在分流之前跑，
	// 否则后面按域名匹配的规则拿到的是 IP，永远匹配不上。
	// 出厂选项默认开着嗅探，所以这里能观察到它。
	if first, _ := rules[0].(map[string]any); first["action"] != "sniff" {
		t.Errorf("嗅探规则必须排在最前（否则按域名的规则匹配不到），实际第一条是 %v", rules[0])
	}

	// 内建规则紧随其后。
	if second, _ := rules[1].(map[string]any); second["ip_is_private"] != true {
		t.Errorf("内建规则（ip_is_private）应排在嗅探之后、用户规则之前，实际第二条是 %v", rules[1])
	}

	// 用户规则按输入顺序跟在后面。
	third, _ := rules[2].(map[string]any)
	if third["domain_suffix"] != "example.com" {
		t.Errorf("第 3 条应当是第一条用户规则，实际 %v", third)
	}
	fourth, _ := rules[3].(map[string]any)
	if fourth["domain_keyword"] != "ads" {
		t.Errorf("第 4 条应当是第二条用户规则，实际 %v", fourth)
	}
	// 拒绝动作用的是 action，不是 outbound——写错的话核心会把它
	// 当成一个需要解析的出站标签。
	if fourth["action"] != "reject" {
		t.Errorf("拒绝动作应当写 action=reject，实际 %v", fourth)
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

// 嗅探必须表达成**路由动作**，而且入站里绝不能出现嗅探字段。
//
// 这一条改过结论，历史值得留下：原先的实现把 sniff /
// sniff_override_destination 写进入站（当时读的是
// option/inbound.go 里那几个已废弃字段），并断言「入站里绝不出现协议键」。
//
// 真实核心（1.14.3）证明那样是错的：
//
//	legacy inbound fields are deprecated in sing-box 1.11.0 and removed
//	in sing-box 1.13.0
//
// 也就是说**每一份开了 TUN 的配置都起不来**。现在嗅探由一条
// `action: "sniff"` 的路由规则表达（option/rule_action.go 的
// RouteActionSniff），协议列表正是它的 sniffer 字段。
func TestSniffIsARouteActionAndNeverAnInboundField(t *testing.T) {
	opts := domain.DefaultOptions()
	opts.Inbound.TUN.Enabled = true
	opts.Inbound.Sniffer = domain.SniffSettings{
		Enabled:             true,
		Protocols:           []string{"tls", "http"},
		OverrideDestination: true,
	}

	req := baseRequest(trojanNode("A"))
	req.Policy = PolicyRule // 嗅探由路由动作表达，只在规则模式下存在
	req.Options = &opts
	out, report, err := CompileSingBox(req)
	if err != nil {
		t.Fatalf("CompileSingBox: %v", err)
	}
	cfg := decodeConfig(t, out)

	// 入站里**绝不能**有嗅探字段——写了核心会拒绝整份配置。
	in := tunInbound(cfg)
	if in == nil {
		t.Fatalf("TUN 已启用，应当生成 TUN 入站")
	}
	for _, k := range []string{"sniff", "sniff_override_destination", "sniff_timeout",
		"sniff_protocols", "protocols", "sniffer"} {
		if _, ok := in[k]; ok {
			t.Errorf("入站里不应出现 %s：这些是 1.13 起被移除的旧字段，"+
				"写上会让整份配置起不来", k)
		}
	}

	// 路由里必须有一条 action=sniff，且带上协议列表。
	route, _ := cfg["route"].(map[string]any)
	rules, _ := route["rules"].([]any)
	if len(rules) == 0 {
		t.Fatalf("没有生成任何路由规则")
	}
	sniff, _ := rules[0].(map[string]any)
	if sniff == nil || sniff["action"] != "sniff" {
		t.Fatalf("嗅探规则应当是第一条且 action=sniff，实际 %v", rules[0])
	}
	proto, _ := sniff["sniffer"].([]any)
	if len(proto) != 2 {
		t.Errorf("嗅探协议应当写进 sniffer 字段，实际 %v", sniff["sniffer"])
	}

	// 覆写目的地址在 1.14 的规则动作里没有对应字段，必须明确报告。
	found := false
	for _, iss := range report.Issues {
		if iss.MessageKey == "compiler.sniff_override_not_applied" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("覆写目的地址不生效时必须报告，实际没有 sniff_override_not_applied")
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

// ReadyAddress 必须给出一个能拨的地址；推不出来时返回空串。
//
// 它决定服务在接管系统网络**之前**能不能确认核心已经在监听。
// 返回空串表示不校验就绪——那正是「连上了但网页打不开」的成因，
// 因此「什么情况下返回空」也必须被钉住。
func TestInboundReadyAddress(t *testing.T) {
	cases := []struct {
		name string
		in   InboundOptions
		want string
	}{
		{
			name: "本机地址与 HTTP 端口",
			in:   InboundOptions{Listen: "127.0.0.1", MixedPort: 7890},
			want: "127.0.0.1:7890",
		},
		{
			name: "监听为空时用回环",
			in:   InboundOptions{MixedPort: 7890},
			want: "127.0.0.1:7890",
		},
		{
			name: "通配地址不能回拨自己",
			in:   InboundOptions{Listen: "0.0.0.0", MixedPort: 7890},
			want: "127.0.0.1:7890",
		},
		{
			name: "IPv6 通配同样换成回环",
			in:   InboundOptions{Listen: "::", MixedPort: 7890},
			want: "127.0.0.1:7890",
		}, {
			name: "没有 HTTP 端口时退回 SOCKS",
			in:   InboundOptions{Listen: "127.0.0.1", MixedPort: 7891},
			want: "127.0.0.1:7891",
		},
		{
			name: "端口全为 0：推不出来，必须返回空而不是瞎猜",
			in:   InboundOptions{Listen: "127.0.0.1"},
			want: "",
		},
		{
			name: "端口越界同样返回空",
			in:   InboundOptions{Listen: "127.0.0.1", MixedPort: 70000},
			want: "",
		},
		{
			name: "局域网监听保留用户填的地址",
			in:   InboundOptions{Listen: "192.168.1.10", MixedPort: 7890},
			want: "192.168.1.10:7890",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.ReadyAddress(); got != tc.want {
				t.Errorf("ReadyAddress() = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// DNS 段的几条硬性要求。这些不是风格问题——违反任何一条，
// **真实核心都会拒绝整份配置**，而我们的单元测试曾经全绿。
//
// 全部来自实测（1.14.3）：
//   - `default server cannot be fakeip`
//   - `missing domain resolver for domain server address`
//   - `missing route.default_domain_resolver`
//
// 单独写这些断言是为了让它们在**没有二进制**的机器上也能挡住回归：
// 上面的真实二进制测试默认跳过，而这几条永远在跑。
func TestDNSFieldsRequiredByTheRealCore(t *testing.T) {
	opts := domain.DefaultOptions()
	cfg := compileWithOptions(t, &opts)

	dns, _ := cfg["dns"].(map[string]any)
	if dns == nil {
		t.Fatalf("启用了 DNS 覆写却没有生成 dns 段")
	}
	servers, _ := dns["servers"].([]any)
	if len(servers) == 0 {
		t.Fatalf("dns.servers 为空")
	}

	// 1) fake-ip 服务器绝不能当默认。
	final, _ := dns["final"].(string)
	fakeipTag := ""
	for _, raw := range servers {
		s, _ := raw.(map[string]any)
		if s == nil {
			continue
		}
		if s["type"] == "fakeip" {
			fakeipTag, _ = s["tag"].(string)
		}
	}
	if fakeipTag != "" && final == fakeipTag {
		t.Errorf("dns.final 指向了 fakeip 服务器（%q）：核心会报 "+
			"`default server cannot be fakeip` 并拒绝整份配置", final)
	}

	// 2) 按域名连接的名称服务器必须声明 domain_resolver。
	byTag := map[string]map[string]any{}
	for _, raw := range servers {
		s, _ := raw.(map[string]any)
		if s == nil {
			continue
		}
		if tag, ok := s["tag"].(string); ok {
			byTag[tag] = s
		}
	}
	for _, raw := range servers {
		s, _ := raw.(map[string]any)
		if s == nil {
			continue
		}
		srv, _ := s["server"].(string)
		if srv == "" || srv == "system" {
			continue
		}
		// 只有「域名形式的地址」才需要解析器；纯 IP 不需要。
		if isIPAddress(srv) {
			continue
		}
		resolver, _ := s["domain_resolver"].(string)
		if resolver == "" {
			t.Errorf("名称服务器 %v 用的是域名地址 %q 却没有 domain_resolver："+
				"核心会报 `missing domain resolver for domain server address`", s["tag"], srv)
			continue
		}
		if _, ok := byTag[resolver]; !ok {
			t.Errorf("名称服务器 %v 引用了不存在的解析器 %q", s["tag"], resolver)
		}
	}

	// 3) 出站要能解析节点服务器的域名。
	route, _ := cfg["route"].(map[string]any)
	if route == nil {
		t.Fatalf("没有生成 route 段")
	}
	ddr, _ := route["default_domain_resolver"].(string)
	if ddr == "" {
		t.Errorf("route.default_domain_resolver 为空：核心会拒绝启动并提示 " +
			"`missing route.default_domain_resolver`")
	} else if _, ok := byTag[ddr]; !ok {
		t.Errorf("route.default_domain_resolver 引用了不存在的服务器 %q", ddr)
	}
}

// 一个名称服务器都没有、却开了 fake-ip 时，**不能**只发 fake-ip。
//
// 那种配置里 fake-ip 会成为唯一的服务器，也就是默认服务器，
// 核心直接报 `default server cannot be fakeip`。
// 这种情况下干脆不生成 fake-ip：它没有可用的真实解析器，
// 生成了也只会让整份配置起不来。
func TestFakeIPWithoutNameserversDoesNotBecomeDefault(t *testing.T) {
	opts := domain.DefaultOptions()
	opts.DNS.Nameservers = nil
	opts.DNS.FakeIP = true

	cfg := compileWithOptions(t, &opts)

	// 没有名称服务器时不应生成 dns 段引用任何 fakeip 默认。
	dns, _ := cfg["dns"].(map[string]any)
	if dns == nil {
		return // 不生成也完全可以接受
	}
	servers, _ := dns["servers"].([]any)
	final, _ := dns["final"].(string)
	for _, raw := range servers {
		s, _ := raw.(map[string]any)
		if s == nil {
			continue
		}
		if s["type"] == "fakeip" {
			tag, _ := s["tag"].(string)
			if final == tag {
				t.Errorf("没有真实名称服务器时 fakeip 成了默认（%q）："+
					"核心会报 `default server cannot be fakeip`", tag)
			}
		}
	}
}

// isIPAddress 判断字符串是不是纯 IP（不需要解析器的那种地址）。
func isIPAddress(s string) bool {
	host := s
	// 去掉可选的方括号 IPv6 与端口。
	if h, _, err := net.SplitHostPort(s); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	return net.ParseIP(host) != nil
}

// 用**真实的 sing-box 二进制**校验我们生成的配置。
// 这是本仓库里唯一能证明「生成器与核心对得上」的手段。其余测试都只断言
// 我们自己写的字段名，而字段名是否与核心一致，只有核心自己说了算——
// `dns_hijack` 那个并不存在的字段就是例子：我们的测试当时全绿，
// 真实核心却会拒绝整份配置。
//
// 默认跳过，因为它需要一个真实二进制。装上之后这样跑：
//
//	$env:SINGBOX_BIN = '<解出来的 sing-box.exe 路径>'
//	./tools/scripts/go-local.ps1 -Command test -Args ./shared/compiler/...
//
// 维护者注：**这是唯一会因核心版本变化而失败的测试。**
// 改动锁定的核心版本之后必须重跑它，否则「已验证」这句话没有依据。
func TestCompiledConfigIsAcceptedByRealSingBox(t *testing.T) {
	bin := realSingBoxPath(t)

	// 覆盖三种路由策略：它们生成的配置差别很大
	// （direct 会忽略节点，rule 才会生成规则集）。
	for _, policy := range []OutboundPolicy{PolicyRule, PolicyGlobal, PolicyDirect} {
		t.Run(string(policy), func(t *testing.T) {
			nodes := []*domain.Node{
				{
					Name: "trojan-1", Protocol: domain.ProtocolTrojan,
					Server: "trojan.example.com", Port: 443,
					Password: "pw", Security: domain.SecurityTLS,
				},
				{
					Name: "vless-1", Protocol: domain.ProtocolVLESS,
					Server: "vless.example.com", Port: 443,
					UUID:     "b831381d-6324-4d53-ad4f-8cda48b30811",
					Security: domain.SecurityTLS,
				},
				{
					Name: "ss-1", Protocol: domain.ProtocolShadowsocks,
					Server: "ss.example.com", Port: 8388,
					Password: "pw", Method: "aes-256-gcm",
				},
			}
			for _, n := range nodes {
				n.ComputeID()
			}
			checkWithRealCore(t, bin, policy, nodes, nil)
		})
	}
}

// realSingBoxPath 取出真实二进制路径；没有就让测试跳过。
func realSingBoxPath(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("SINGBOX_BIN")
	if bin == "" {
		t.Skip("SINGBOX_BIN 未设置；跳过真实二进制校验（这不代表配置一定有效）")
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("SINGBOX_BIN 指向的文件不存在：%v", err)
	}
	return bin
}

// checkWithRealCore 编译并让真实核心校验，失败即 t.Fatal。
//
// mutate 允许调用方在编译前调整选项（例如只开某个特性）。
func checkWithRealCore(t *testing.T, bin string, policy OutboundPolicy, nodes []*domain.Node, mutate func(*domain.Options)) {
	t.Helper()
	req := &Request{
		Core:   CoreSingBox,
		Nodes:  nodes,
		Policy: policy,
		Inbound: InboundOptions{
			Listen:    "127.0.0.1",
			MixedPort: 7890,
		},
		LogLevel: "warning",
	}
	if len(nodes) > 0 {
		req.SelectedNodeID = nodes[0].ID
	}
	opts := domain.DefaultOptions()
	if mutate != nil {
		mutate(&opts)
	}
	req.Options = &opts

	out, _, err := CompileSingBox(req)
	if err != nil {
		t.Fatalf("CompileSingBox: %v", err)
	}

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, out, 0o600); err != nil {
		t.Fatalf("写配置: %v", err)
	}

	// 让核心的输出直接继承到测试输出，而不是用管道捕获：
	// 本仓库的沙箱禁止通过管道捕获子进程输出（EPERM），
	// 但允许继承。这里只需要退出码。
	cmd := exec.Command(bin, "check", "-c", cfgPath)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("真实 sing-box 拒绝了这份配置：%v", err)
	}
}

// 能力表声称支持的**每一种协议**都要被真实核心接受。
//
// 为什么单独走一遍：能力表里的 protocols 是一张布尔表，而它直接决定
// 「订阅里的这个节点是被采用还是被跳过」。表里写 true、生成的配置却被
// 核心拒绝，是最糟的组合——用户看到节点在列表里，点了却起不来。
func TestEveryClaimedProtocolIsAcceptedByRealSingBox(t *testing.T) {
	bin := realSingBoxPath(t)

	// 每种协议一组「能通过校验所需的最少字段」。
	cases := []struct {
		name string
		node *domain.Node
	}{
		{"shadowsocks", &domain.Node{
			Name: "ss", Protocol: domain.ProtocolShadowsocks,
			Server: "ss.example.com", Port: 8388,
			Password: "pw", Method: "aes-256-gcm",
		}},
		{"vmess", &domain.Node{
			Name: "vmess", Protocol: domain.ProtocolVMess,
			Server: "vmess.example.com", Port: 443,
			UUID:     "b831381d-6324-4d53-ad4f-8cda48b30811",
			Security: domain.SecurityTLS,
		}},
		{"vless", &domain.Node{
			Name: "vless", Protocol: domain.ProtocolVLESS,
			Server: "vless.example.com", Port: 443,
			UUID:     "b831381d-6324-4d53-ad4f-8cda48b30811",
			Security: domain.SecurityTLS,
		}},
		{"trojan", &domain.Node{
			Name: "trojan", Protocol: domain.ProtocolTrojan,
			Server: "trojan.example.com", Port: 443,
			Password: "pw", Security: domain.SecurityTLS,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 表里说支持才需要校验；说不支持的没有配置可生成。
			if !Supports(CoreSingBox, tc.node.Protocol) {
				t.Skipf("能力表声明不支持 %s，无需校验", tc.name)
			}
			tc.node.ComputeID()
			checkWithRealCore(t, bin, PolicyRule, []*domain.Node{tc.node}, nil)
		})
	}
}

// 能力表声称支持的**每一种传输**都要被真实核心接受。
func TestEveryClaimedTransportIsAcceptedByRealSingBox(t *testing.T) {
	bin := realSingBoxPath(t)

	transports := []domain.Transport{
		domain.TransportTCP,
		domain.TransportWS,
		domain.TransportGRPC,
		domain.TransportHTTP,
		domain.TransportH2,
		domain.TransportHTTPUpgrade,
	}
	for _, tr := range transports {
		t.Run(string(tr), func(t *testing.T) {
			n := &domain.Node{
				Name: "vless-" + string(tr), Protocol: domain.ProtocolVLESS,
				Server: "vless.example.com", Port: 443,
				UUID:      "b831381d-6324-4d53-ad4f-8cda48b30811",
				Security:  domain.SecurityTLS,
				Transport: tr,
			}
			switch tr {
			case domain.TransportWS:
				n.TransportOptions.Path = "/ws"
				n.TransportOptions.Host = "ws.example.com"
			case domain.TransportGRPC:
				n.TransportOptions.ServiceName = "grpcsvc"
			case domain.TransportHTTP, domain.TransportH2:
				n.TransportOptions.Path = "/h2"
				n.TransportOptions.Host = "h2.example.com"
			case domain.TransportHTTPUpgrade:
				n.TransportOptions.Path = "/upgrade"
				n.TransportOptions.Host = "up.example.com"
			}
			n.ComputeID()
			checkWithRealCore(t, bin, PolicyRule, []*domain.Node{n}, nil)
		})
	}
}

// 出厂选项之外的几个开关也要各跑一遍真实核心。
//
// 这些开关各自会往配置里加不同的段（TUN、fake-ip、嗅探、规则集），
// 而「某个开关一开核心就起不来」正是最难在开发中发现的一类问题：
// 默认配置是好的，只有用户改了设置才出问题。
func TestOptionTogglesAreAcceptedByRealSingBox(t *testing.T) {
	bin := realSingBoxPath(t)

	n := &domain.Node{
		Name: "trojan", Protocol: domain.ProtocolTrojan,
		Server: "trojan.example.com", Port: 443,
		Password: "pw", Security: domain.SecurityTLS,
	}
	n.ComputeID()

	cases := []struct {
		name   string
		mutate func(*domain.Options)
	}{
		{"fake-ip", func(o *domain.Options) {
			o.DNS.Enabled = true
			o.DNS.FakeIP = true
			o.DNS.FakeIPRange = "198.18.0.0/15"
			o.DNS.FakeIPFilter = []string{"local.example", "time.example"}
		}},
		{"dns-without-nameservers", func(o *domain.Options) {
			o.DNS.Enabled = true
			o.DNS.Nameservers = nil
			o.DNS.FakeIP = true
		}},
		{"dns-ipv6", func(o *domain.Options) {
			o.DNS.Enabled = true
			o.DNS.IPv6 = true
		}},
		{"tun-enabled", func(o *domain.Options) {
			o.Inbound.TUN.Enabled = true
			o.Inbound.TUN.Stack = "mixed"
			o.Inbound.TUN.MTU = 1500
			o.Inbound.TUN.AutoRoute = true
			o.Inbound.TUN.DNSMode = "hijack"
			o.Inbound.TUN.DNSAddress = []string{"172.19.0.2"}
		}},
		{"rules-and-rulesets", func(o *domain.Options) {
			o.Rules = []domain.Rule{
				{Type: domain.RuleDomainSuffix, Value: "example.com", Action: domain.ActionProxy},
				{Type: domain.RuleIPCIDR, Value: "10.0.0.0/8", Action: domain.ActionDirect},
			}
			o.RuleSets = []domain.RuleSet{
				{Name: "geosite", Format: domain.RuleSetFormatBinary, URL: "https://example.com/geosite.srs"},
			}
		}},
		{"sniffer", func(o *domain.Options) {
			o.Inbound.Sniffer.Enabled = true
			o.Inbound.Sniffer.OverrideDestination = true
			o.Inbound.Sniffer.Protocols = []string{"tls", "http", "quic"}
		}},
		{"allow-lan", func(o *domain.Options) {
			o.Inbound.AllowLAN = true
		}},
		{"log-trace", func(o *domain.Options) {
			o.App.LogLevel = "trace"
		}},
		{"reality", func(o *domain.Options) {
			_ = o // reality 在节点上，见 TestClaimedNodeFeaturesAreAcceptedByRealSingBox
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checkWithRealCore(t, bin, PolicyRule, []*domain.Node{n}, tc.mutate)
		})
	}
}

// 能力表声称支持的**节点级特性**（REALITY / uTLS / VLESS flow）也要被真实核心接受。
//
// 这三项在表里各占一个布尔字段，而它们直接决定「这个节点是被采用还是被跳过」。
// 表里写 true、生成的出站却被核心拒绝，是最糟的组合。
func TestClaimedNodeFeaturesAreAcceptedByRealSingBox(t *testing.T) {
	bin := realSingBoxPath(t)

	cases := []struct {
		name string
		node *domain.Node
	}{
		{"vless-reality", &domain.Node{
			Name: "reality", Protocol: domain.ProtocolVLESS,
			Server: "reality.example.com", Port: 443,
			UUID:     "b831381d-6324-4d53-ad4f-8cda48b30811",
			Security: domain.SecurityReality,
			TLS: domain.TLSOptions{
				ServerName:       "www.example.com",
				RealityPublicKey: "jNXHt1yRo0vDuchQlIP6Z0ZvjT3KtzVI-T4E7RoLJS0",
				RealityShortID:   "0123456789abcdef",
			},
		}},
		{"vless-flow-vision", &domain.Node{
			Name: "vision", Protocol: domain.ProtocolVLESS,
			Server: "vision.example.com", Port: 443,
			UUID:     "b831381d-6324-4d53-ad4f-8cda48b30811",
			Security: domain.SecurityTLS,
			Flow:     "xtls-rprx-vision",
			TLS:      domain.TLSOptions{ServerName: "vision.example.com"},
		}},
		{"vless-utls-fingerprint", &domain.Node{
			Name: "utls", Protocol: domain.ProtocolVLESS,
			Server: "utls.example.com", Port: 443,
			UUID:     "b831381d-6324-4d53-ad4f-8cda48b30811",
			Security: domain.SecurityTLS,
			TLS: domain.TLSOptions{
				ServerName:  "utls.example.com",
				Fingerprint: "chrome",
				ALPN:        []string{"h2", "http/1.1"},
			},
		}},
		{"trojan-tls-insecure", &domain.Node{
			Name: "insecure", Protocol: domain.ProtocolTrojan,
			Server: "insecure.example.com", Port: 443,
			Password: "pw", Security: domain.SecurityTLS,
			TLS: domain.TLSOptions{ServerName: "insecure.example.com", AllowInsecure: true},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.node.ComputeID()
			checkWithRealCore(t, bin, PolicyRule, []*domain.Node{tc.node}, nil)
		})
	}
}
