package compiler

import (
	"encoding/json"
	"strings"
	"testing"

	"proxyclient/shared/domain"
)

// 这些测试把「能力边界」与「字段不静默丢弃」两条设计约束固化下来。
// 它们是契约测试：如果哪天有人为了「让它编过」而放宽校验，测试会失败。
//
// 单内核（ADR-0005）之后，这里只测 sing-box。原先成对出现的
// 「同一节点在另一个内核里如何」的用例已经删除：另一个内核不存在了，
// 保留它们只会让读者以为还存在两条代码路径。

func trojanNode(name string) *domain.Node {
	n := &domain.Node{
		Name:     name,
		Protocol: domain.ProtocolTrojan,
		Server:   "t.example.com",
		Port:     443,
		Password: "secret",
		Security: domain.SecurityTLS,
		TLS:      domain.TLSOptions{ServerName: "t.example.com"},
	}
	n.ComputeID()
	return n
}

func vlessRealityNode() *domain.Node {
	n := &domain.Node{
		Name:     "reality",
		Protocol: domain.ProtocolVLESS,
		Server:   "r.example.com",
		Port:     443,
		UUID:     "2a4b7c1d-0000-1111-2222-333344445555",
		Flow:     "xtls-rprx-vision",
		Security: domain.SecurityReality,
		TLS: domain.TLSOptions{
			ServerName:       "www.microsoft.com",
			Fingerprint:      "chrome",
			RealityPublicKey: "PUBKEY",
			RealityShortID:   "ab12",
		},
	}
	n.ComputeID()
	return n
}

// baseRequest 构造一次合法的编译请求。
//
// 核心不再作为参数：单内核构建里只有 CoreSingBox 一个合法取值，
// 把它逐个调用点重复一遍只会掩盖「这里到底在测哪个内核」。
func baseRequest(nodes ...*domain.Node) *Request {
	return &Request{
		Core:  CoreSingBox,
		Nodes: nodes,
		Inbound: InboundOptions{
			Listen:    "127.0.0.1",
			HTTPPort:  7890,
			SocksPort: 7891,
		},
		Policy: PolicyGlobal,
	}
}

func TestCompileSingBoxTrojan(t *testing.T) {
	req := baseRequest(trojanNode("A"))
	out, report, err := CompileSingBox(req)
	if err != nil {
		t.Fatalf("CompileSingBox: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	proxy := cfg["outbounds"].([]any)[0].(map[string]any)
	// 已核实字段名：server / server_port / password（option/ 中的 json tag）。
	if proxy["server"] != "t.example.com" || proxy["server_port"] != float64(443) {
		t.Errorf("server block = %v", proxy)
	}
	if proxy["password"] != "secret" {
		t.Errorf("password = %v", proxy["password"])
	}
	tls := proxy["tls"].(map[string]any)
	if tls["enabled"] != true || tls["server_name"] != "t.example.com" {
		t.Errorf("tls block = %v", tls)
	}
	if report.CompiledNodes != 1 {
		t.Errorf("compiled nodes = %d, want 1", report.CompiledNodes)
	}
}

func TestCompileSingBoxRealityUsesUTLSAndRealityBlocks(t *testing.T) {
	req := baseRequest(vlessRealityNode())
	out, _, err := CompileSingBox(req)
	if err != nil {
		t.Fatalf("CompileSingBox: %v", err)
	}

	var cfg map[string]any
	_ = json.Unmarshal(out, &cfg)
	proxy := cfg["outbounds"].([]any)[0].(map[string]any)
	tls := proxy["tls"].(map[string]any)

	utls, ok := tls["utls"].(map[string]any)
	if !ok {
		t.Fatalf("utls block missing: %v", tls)
	}
	if utls["fingerprint"] != "chrome" {
		t.Errorf("fingerprint = %v", utls["fingerprint"])
	}
	reality, ok := tls["reality"].(map[string]any)
	if !ok {
		t.Fatalf("reality block missing: %v", tls)
	}
	if reality["public_key"] != "PUBKEY" || reality["short_id"] != "ab12" {
		t.Errorf("reality block = %v", reality)
	}
	if proxy["flow"] != "xtls-rprx-vision" {
		t.Errorf("flow = %v", proxy["flow"])
	}
}

// xhttp 是 sing-box 明确不具备的传输。
//
// 原先这个用例叫 TestXHTTPDifferenceBetweenCores，靠「Xray 能编、sing-box 不能」
// 来证明能力表在起作用。另一个内核删除后，这半边的对照消失了，但**断言本身
// 仍然成立且更重要**：面板如果只提供 xhttp，我们必须拒绝并说清原因，
// 而不是产出一份核心启动时才发现不认的配置。
func TestSingBoxRejectsXHTTP(t *testing.T) {
	n := trojanNode("xhttp")
	n.Transport = domain.TransportXHTTP
	n.TransportOptions = domain.TransportOptions{Path: "/xh", Host: "h.example.com"}
	n.ComputeID()

	_, report, err := CompileSingBox(baseRequest(n))
	if err == nil {
		t.Fatal("sing-box must reject xhttp")
	}
	errs := report.Errors()
	if len(errs) == 0 {
		t.Fatal("expected a field-level error for xhttp")
	}
	if errs[0].FieldPath != "transport.type" {
		t.Errorf("error field path = %q, want transport.type", errs[0].FieldPath)
	}
	if errs[0].MessageKey == "" {
		t.Error("error must carry a stable message key for i18n")
	}
}

// TCP 上的 HTTP 伪装头同样是 sing-box 不支持的字段。
// 理由与 xhttp 相同：拒绝并给出字段级错误，而不是静默丢掉。
func TestSingBoxRejectsTCPHeaderObfs(t *testing.T) {
	n := trojanNode("obfs")
	n.Transport = domain.TransportTCP
	n.TransportOptions = domain.TransportOptions{HeaderType: "http", Path: "/", Host: "h.example.com"}
	n.ComputeID()

	_, report, err := CompileSingBox(baseRequest(n))
	if err == nil {
		t.Fatal("sing-box must reject tcp header obfs")
	}
	if len(report.Errors()) == 0 {
		t.Fatal("expected a field-level error")
	}
}

// flow 必须配合 TLS 或 REALITY。
func TestFlowRequiresTLS(t *testing.T) {
	n := &domain.Node{
		Name:     "flow-no-tls",
		Protocol: domain.ProtocolVLESS,
		Server:   "v.example.com",
		Port:     443,
		UUID:     "uuid-1",
		Flow:     "xtls-rprx-vision",
		Security: domain.SecurityNone,
	}
	n.ComputeID()

	_, report, err := CompileSingBox(baseRequest(n))
	if err == nil {
		t.Fatal("expected failure: flow without TLS must be rejected")
	}
	found := false
	for _, e := range report.Errors() {
		if e.FieldPath == "flow" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a flow field error, got %v", report.Errors())
	}
}

// 隔离字段必须产生 warning，且不得进入产物。
func TestQuarantinedFieldsAreReportedNotSilentlyDropped(t *testing.T) {
	n := trojanNode("q")
	n.QuarantineField("some_future_option", "value")
	n.ComputeID()

	out, report, err := CompileSingBox(baseRequest(n))
	if err != nil {
		t.Fatalf("CompileSingBox: %v", err)
	}

	found := false
	for _, w := range report.Warnings() {
		if strings.HasPrefix(w.FieldPath, "quarantine.") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a quarantine warning, got %v", report.Warnings())
	}
	// 未知字段绝不能被写进配置。
	if strings.Contains(string(out), "some_future_option") {
		t.Error("quarantined field leaked into the emitted config")
	}
}

// 选定的节点不存在时必须回退并警告，不能静默换节点。
func TestSelectedNodeMissingFallsBackWithWarning(t *testing.T) {
	req := baseRequest(trojanNode("A"), trojanNode("B"))
	req.SelectedNodeID = "does-not-exist"

	_, report, err := CompileSingBox(req)
	if err != nil {
		t.Fatalf("CompileSingBox: %v", err)
	}
	found := false
	for _, w := range report.Warnings() {
		if w.MessageKey == "compiler.selected_node_missing" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a fallback warning, got %v", report.Warnings())
	}
}

// 全部节点不可编译时必须报错，不得产出空壳配置。
func TestNoCompilableNodeFails(t *testing.T) {
	bad := &domain.Node{
		Name:     "bad",
		Protocol: domain.ProtocolHysteria2, // 首轮不支持
		Server:   "h.example.com",
		Port:     443,
	}
	bad.ComputeID()

	_, report, err := CompileSingBox(baseRequest(bad))
	if err == nil {
		t.Fatal("expected failure when no node is compilable")
	}
	if len(report.Errors()) == 0 {
		t.Error("expected an error explaining why the node was rejected")
	}
}

func TestRequestValidation(t *testing.T) {
	good := baseRequest(trojanNode("A"))

	cases := []struct {
		name   string
		mutate func(*Request)
	}{
		{"unknown core", func(r *Request) { r.Core = "mihomo" }},
		{"same ports", func(r *Request) { r.Inbound.SocksPort = r.Inbound.HTTPPort }},
		{"zero http port", func(r *Request) { r.Inbound.HTTPPort = 0 }},
		{"empty listen", func(r *Request) { r.Inbound.Listen = "" }},
		{"unknown policy", func(r *Request) { r.Policy = "smart" }},
		{"empty policy", func(r *Request) { r.Policy = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 复制一份，避免用例之间互相污染。
			r := *good
			tc.mutate(&r)
			if _, _, err := Compile(&r); err == nil {
				t.Error("expected a validation error")
			}
		})
	}
}

// 策略 global / direct 必须改变默认出口。
func TestRoutingPolicyChangesDefaultOutbound(t *testing.T) {
	reqGlobal := baseRequest(trojanNode("A"))
	reqGlobal.Policy = PolicyGlobal
	outGlobal, _, err := CompileSingBox(reqGlobal)
	if err != nil {
		t.Fatalf("CompileSingBox global: %v", err)
	}

	reqDirect := baseRequest(trojanNode("A"))
	reqDirect.Policy = PolicyDirect
	outDirect, _, err := CompileSingBox(reqDirect)
	if err != nil {
		t.Fatalf("CompileSingBox direct: %v", err)
	}

	var cfgGlobal, cfgDirect map[string]any
	_ = json.Unmarshal(outGlobal, &cfgGlobal)
	_ = json.Unmarshal(outDirect, &cfgDirect)

	finalGlobal := cfgGlobal["route"].(map[string]any)["final"].(string)
	if !strings.HasPrefix(finalGlobal, "proxy-") {
		t.Errorf("global final = %q, want a proxy tag", finalGlobal)
	}
	finalDirect := cfgDirect["route"].(map[string]any)["final"].(string)
	if finalDirect != "direct" {
		t.Errorf("direct final = %q, want direct", finalDirect)
	}
}

// 入口必须按 allowLanAccess 决定监听地址。
func TestAllowLanAccessChangesListenAddress(t *testing.T) {
	req := baseRequest(trojanNode("A"))
	req.Inbound.AllowLanAccess = false
	out, _, err := CompileSingBox(req)
	if err != nil {
		t.Fatalf("CompileSingBox: %v", err)
	}
	if !strings.Contains(string(out), `"listen": "127.0.0.1"`) {
		t.Error("expected loopback listen by default")
	}

	req.Inbound.AllowLanAccess = true
	out, _, err = CompileSingBox(req)
	if err != nil {
		t.Fatalf("CompileSingBox: %v", err)
	}
	if !strings.Contains(string(out), `"listen": "0.0.0.0"`) {
		t.Error("expected all-interface listen when LAN access is enabled")
	}
}

func TestCapabilitySummaryIsAvailable(t *testing.T) {
	singbox, ok := Describe(CoreSingBox)
	if !ok {
		t.Fatal("sing-box capability missing")
	}
	// 能力表必须如实标注它被验证到了什么程度。
	//
	// 这条断言改过结论：原先要求它**永远**是 "unverified"，理由是
	// 「G4 用官方二进制逐项复核之后才允许改」。那次复核在 2026-10-16
	// 真的做了（见 capability.go 的注释），因此现在允许它带上版本号。
	//
	// 但不能因此放松：**空串不行**。空串既不是「已验证」也不是「未验证」，
	// 它只会让界面上的提示凭空消失，读者无从判断该不该信这张表。
	if singbox.VerifiedAgainst == "" {
		t.Error("能力表必须标注验证状态：要么 unverified，要么给出实测过的核心版本")
	}
	if !Supports(CoreSingBox, domain.ProtocolTrojan) {
		t.Error("sing-box should support trojan")
	}
	// 单内核构建里没有第二个内核的能力表可查；这里锁定「问别的核心一律回答没有」，
	// 避免有人日后为了让某个调用点通过而偷偷回退到 sing-box 的表。
	if _, ok := Describe(Core("mihomo")); ok {
		t.Error("only sing-box has a capability table")
	}
}

// 回归保护：出站 tag 不得重复。
//
// 曾经的缺陷：direct 策略下会同时追加两个 tag 为 "direct" 的出站，
// 而重复 tag 会让核心直接拒绝启动。此测试锁死该约束。
func TestNoDuplicateOutboundTags(t *testing.T) {
	for _, policy := range []OutboundPolicy{PolicyGlobal, PolicyDirect, PolicyRule} {
		t.Run(string(policy), func(t *testing.T) {
			req := baseRequest(trojanNode("A"), trojanNode("B"))
			req.Policy = policy

			out, _, err := Compile(req)
			if err != nil {
				t.Fatalf("%s: %v", policy, err)
			}

			var cfg struct {
				Outbounds []map[string]any `json:"outbounds"`
			}
			if err := json.Unmarshal(out, &cfg); err != nil {
				t.Fatalf("%s: invalid JSON: %v", policy, err)
			}

			seen := map[string]bool{}
			for _, ob := range cfg.Outbounds {
				tag, _ := ob["tag"].(string)
				if tag == "" {
					t.Errorf("%s: outbound without a tag: %v", policy, ob)
					continue
				}
				if seen[tag] {
					t.Errorf("%s: duplicate outbound tag %q", policy, tag)
				}
				seen[tag] = true
			}
			if !seen["direct"] {
				t.Errorf("%s: direct outbound missing", policy)
			}
		})
	}
}

// 选定的节点必须成为默认出口。
//
// 原先这条契约是用「第一个出站即默认出口」来断言的（那是另一个内核的规则）。
// sing-box 用 route.final 指定默认出口，出站顺序不影响行为，
// 所以断言直接落在 route.final 上——意图不变，而且不再依赖出站顺序。
func TestSelectedNodeBecomesRouteFinal(t *testing.T) {
	a := trojanNode("A")
	b := trojanNode("B")
	// 让两个节点确实不同（Port 不同即可产生不同 ID）。
	b.Port = 8443
	b.ComputeID()

	req := baseRequest(a, b)
	req.Policy = PolicyGlobal
	req.SelectedNodeID = b.ID

	out, _, err := CompileSingBox(req)
	if err != nil {
		t.Fatalf("CompileSingBox: %v", err)
	}

	var cfg struct {
		Route struct {
			Final string `json:"final"`
		} `json:"route"`
	}
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if cfg.Route.Final != outboundTag(b) {
		t.Errorf("route.final = %q, want the selected node %q", cfg.Route.Final, outboundTag(b))
	}
}
