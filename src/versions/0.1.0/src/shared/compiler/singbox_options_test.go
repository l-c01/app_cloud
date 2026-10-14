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
func compileWithOptions(t *testing.T, opts *domain.Options) map[string]any {
	t.Helper()
	req := baseRequest(trojanNode("A"))
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
		if strings.Contains(iss.Message, "负载") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("跳过负载均衡组时必须写进报告，实际报告里没有提到它")
	}
}
