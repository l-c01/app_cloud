package compiler

import (
	"encoding/json"
	"strings"
	"testing"

	"proxyclient/shared/domain"
)

// 这些测试把「能力差异」与「字段不静默丢弃」两条设计约束固化下来。
// 它们是契约测试：如果哪天有人为了「让它编过」而放宽校验，测试会失败。

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

func baseRequest(core Core, nodes ...*domain.Node) *Request {
	return &Request{
		Core:  core,
		Nodes: nodes,
		Inbound: InboundOptions{
			Listen:    "127.0.0.1",
			HTTPPort:  7890,
			SocksPort: 7891,
		},
		Policy: PolicyGlobal,
	}
}

func TestCompileXrayTrojan(t *testing.T) {
	req := baseRequest(CoreXray, trojanNode("A"))
	out, report, err := CompileXray(req)
	if err != nil {
		t.Fatalf("CompileXray: %v (report: %v)", err, report.Issues)
	}

	var cfg map[string]any
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	outbounds := cfg["outbounds"].([]any)
	// 期望：proxy + direct + block
	if len(outbounds) != 3 {
		t.Errorf("outbound count = %d, want 3", len(outbounds))
	}

	proxy := outbounds[0].(map[string]any)
	if proxy["protocol"] != "trojan" {
		t.Errorf("protocol = %v", proxy["protocol"])
	}
	settings := proxy["settings"].(map[string]any)
	servers := settings["servers"].([]any)
	first := servers[0].(map[string]any)
	if first["address"] != "t.example.com" || first["password"] != "secret" {
		t.Errorf("trojan server block = %v", first)
	}

	// streamSettings 必须带上已核实的安全类型。
	ss := proxy["streamSettings"].(map[string]any)
	if ss["security"] != "tls" {
		t.Errorf("security = %v, want tls", ss["security"])
	}
	if report.CompiledNodes != 1 {
		t.Errorf("compiled nodes = %d, want 1", report.CompiledNodes)
	}
}

func TestCompileXrayVLESSReality(t *testing.T) {
	req := baseRequest(CoreXray, vlessRealityNode())
	out, _, err := CompileXray(req)
	if err != nil {
		t.Fatalf("CompileXray: %v", err)
	}

	var cfg map[string]any
	_ = json.Unmarshal(out, &cfg)
	proxy := cfg["outbounds"].([]any)[0].(map[string]any)
	ss := proxy["streamSettings"].(map[string]any)

	if ss["security"] != "reality" {
		t.Errorf("security = %v, want reality", ss["security"])
	}
	reality, ok := ss["realitySettings"].(map[string]any)
	if !ok {
		t.Fatalf("realitySettings missing: %v", ss)
	}
	if reality["publicKey"] != "PUBKEY" || reality["shortId"] != "ab12" {
		t.Errorf("reality block = %v", reality)
	}
	// REALITY 下 serverName 应在 realitySettings 内，而不是顶层 tlsSettings。
	if reality["serverName"] != "www.microsoft.com" {
		t.Errorf("realitySettings.serverName = %v", reality["serverName"])
	}
	if _, dup := ss["tlsSettings"]; dup {
		t.Errorf("tlsSettings should not be emitted for reality-only node: %v", ss["tlsSettings"])
	}

	// flow 必须在 users 中体现。
	users := proxy["settings"].(map[string]any)["vnext"].([]any)[0].(map[string]any)["users"].([]any)
	if users[0].(map[string]any)["flow"] != "xtls-rprx-vision" {
		t.Errorf("flow missing: %v", users[0])
	}
}

func TestCompileSingBoxTrojan(t *testing.T) {
	req := baseRequest(CoreSingBox, trojanNode("A"))
	out, _, err := CompileSingBox(req)
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
}

func TestCompileSingBoxRealityUsesUTLSAndRealityBlocks(t *testing.T) {
	req := baseRequest(CoreSingBox, vlessRealityNode())
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

// xhttp 是两核心的关键能力差异：Xray 支持，sing-box 不支持。
func TestXHTTPDifferenceBetweenCores(t *testing.T) {
	n := trojanNode("xhttp")
	n.Transport = domain.TransportXHTTP
	n.TransportOptions = domain.TransportOptions{Path: "/xh", Host: "h.example.com"}
	n.ComputeID()

	// Xray：应可编译。
	if _, report, err := CompileXray(baseRequest(CoreXray, n)); err != nil {
		t.Fatalf("xray should support xhttp: %v", err)
	} else if report.HasErrors() {
		t.Errorf("xray reported errors for xhttp: %v", report.Errors())
	}

	// sing-box：必须产生字段级错误，并被排除出产物。
	_, report, err := CompileSingBox(baseRequest(CoreSingBox, n))
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

// TCP 上的 HTTP 伪装头同样是能力差异。
func TestTCPHeaderObfsDifferenceBetweenCores(t *testing.T) {
	n := trojanNode("obfs")
	n.Transport = domain.TransportTCP
	n.TransportOptions = domain.TransportOptions{HeaderType: "http", Path: "/", Host: "h.example.com"}
	n.ComputeID()

	if _, report, err := CompileXray(baseRequest(CoreXray, n)); err != nil {
		t.Fatalf("xray should support tcp header obfs: %v", err)
	} else if report.HasErrors() {
		t.Errorf("xray reported errors: %v", report.Errors())
	}

	_, report, err := CompileSingBox(baseRequest(CoreSingBox, n))
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

	_, report, err := CompileXray(baseRequest(CoreXray, n))
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

	out, report, err := CompileXray(baseRequest(CoreXray, n))
	if err != nil {
		t.Fatalf("CompileXray: %v", err)
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
	req := baseRequest(CoreXray, trojanNode("A"), trojanNode("B"))
	req.SelectedNodeID = "does-not-exist"

	_, report, err := CompileXray(req)
	if err != nil {
		t.Fatalf("CompileXray: %v", err)
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

	_, report, err := CompileSingBox(baseRequest(CoreSingBox, bad))
	if err == nil {
		t.Fatal("expected failure when no node is compilable")
	}
	if len(report.Errors()) == 0 {
		t.Error("expected an error explaining why the node was rejected")
	}
}

func TestRequestValidation(t *testing.T) {
	good := baseRequest(CoreXray, trojanNode("A"))

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
	reqGlobal := baseRequest(CoreSingBox, trojanNode("A"))
	reqGlobal.Policy = PolicyGlobal
	outGlobal, _, err := CompileSingBox(reqGlobal)
	if err != nil {
		t.Fatalf("CompileSingBox global: %v", err)
	}

	reqDirect := baseRequest(CoreSingBox, trojanNode("A"))
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
	req := baseRequest(CoreXray, trojanNode("A"))
	req.Inbound.AllowLanAccess = false
	out, _, err := CompileXray(req)
	if err != nil {
		t.Fatalf("CompileXray: %v", err)
	}
	if !strings.Contains(string(out), `"listen": "127.0.0.1"`) {
		t.Error("expected loopback listen by default")
	}

	req.Inbound.AllowLanAccess = true
	out, _, err = CompileXray(req)
	if err != nil {
		t.Fatalf("CompileXray: %v", err)
	}
	if !strings.Contains(string(out), `"listen": "0.0.0.0"`) {
		t.Error("expected all-interface listen when LAN access is enabled")
	}
}

func TestCapabilitySummaryIsAvailable(t *testing.T) {
	xray, ok := Describe(CoreXray)
	if !ok {
		t.Fatal("xray capability missing")
	}
	singbox, ok := Describe(CoreSingBox)
	if !ok {
		t.Fatal("sing-box capability missing")
	}
	// 能力表必须显式标注「尚未实测」，避免被误当作已验证结论。
	if xray.VerifiedAgainst != "unverified" || singbox.VerifiedAgainst != "unverified" {
		t.Errorf("capability tables must be marked unverified until G4: %q / %q",
			xray.VerifiedAgainst, singbox.VerifiedAgainst)
	}
	if !Supports(CoreXray, domain.ProtocolTrojan) {
		t.Error("xray should support trojan")
	}
}

// 回归保护：出站 tag 不得重复。
//
// 曾经的缺陷：direct 策略下会同时追加两个 tag 为 "direct" 的出站，
// 而 Xray 遇到重复 tag 会直接拒绝启动。此测试锁死该约束。
func TestNoDuplicateOutboundTags(t *testing.T) {
	for _, policy := range []OutboundPolicy{PolicyGlobal, PolicyDirect, PolicyRule} {
		t.Run(string(policy), func(t *testing.T) {
			for _, core := range []Core{CoreXray, CoreSingBox} {
				req := baseRequest(core, trojanNode("A"), trojanNode("B"))
				req.Policy = policy

				out, _, err := Compile(req)
				if err != nil {
					t.Fatalf("%s/%s: %v", core, policy, err)
				}

				var cfg struct {
					Outbounds []map[string]any `json:"outbounds"`
				}
				if err := json.Unmarshal(out, &cfg); err != nil {
					t.Fatalf("%s/%s: invalid JSON: %v", core, policy, err)
				}

				seen := map[string]bool{}
				for _, ob := range cfg.Outbounds {
					tag, _ := ob["tag"].(string)
					if tag == "" {
						t.Errorf("%s/%s: outbound without a tag: %v", core, policy, ob)
						continue
					}
					if seen[tag] {
						t.Errorf("%s/%s: duplicate outbound tag %q", core, policy, tag)
					}
					seen[tag] = true
				}
				if !seen["direct"] {
					t.Errorf("%s/%s: direct outbound missing", core, policy)
				}
			}
		})
	}
}

// Xray 没有 route.final，第一个出站即默认出口；必须与选定节点一致。
func TestXrayFirstOutboundIsTheSelectedNode(t *testing.T) {
	a := trojanNode("A")
	b := trojanNode("B")
	// 让两个节点确实不同（Port 不同即可产生不同 ID）。
	b.Port = 8443
	b.ComputeID()

	req := baseRequest(CoreXray, a, b)
	req.Policy = PolicyGlobal
	req.SelectedNodeID = b.ID

	out, _, err := CompileXray(req)
	if err != nil {
		t.Fatalf("CompileXray: %v", err)
	}

	var cfg struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	firstTag, _ := cfg.Outbounds[0]["tag"].(string)
	if firstTag != outboundTag(b) {
		t.Errorf("first outbound = %q, want the selected node %q", firstTag, outboundTag(b))
	}
}

// direct 策略下，默认出口必须是 direct 而不是代理节点。
func TestXrayDirectPolicyPutsDirectFirst(t *testing.T) {
	req := baseRequest(CoreXray, trojanNode("A"))
	req.Policy = PolicyDirect

	out, _, err := CompileXray(req)
	if err != nil {
		t.Fatalf("CompileXray: %v", err)
	}

	var cfg struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	_ = json.Unmarshal(out, &cfg)

	if tag, _ := cfg.Outbounds[0]["tag"].(string); tag != "direct" {
		t.Errorf("first outbound = %q, want direct", tag)
	}
}
