package subscription

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"proxyclient/shared/domain"
)

// 本文件中的测试 URI 严格按 Xboard 服务端的生成逻辑构造，
// 事实来源：src/references/Xboard-master/app/Protocols/General.php
//   - shadowsocks: :56-75
//   - vmess:       :77-152
//   - vless:       :243
//   - trojan:      :247-322

func TestParseShadowsocksSIP002(t *testing.T) {
	// 服务端：base64url 无填充的 "cipher:password"，后接 @host:port#name
	cred := base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:secret"))
	uri := "ss://" + cred + "@1.2.3.4:8388#" + url.QueryEscape("香港 01")

	n, err := ParseURI(uri)
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}
	if n.Protocol != domain.ProtocolShadowsocks {
		t.Errorf("protocol = %q", n.Protocol)
	}
	if n.Method != "aes-256-gcm" {
		t.Errorf("method = %q, want aes-256-gcm", n.Method)
	}
	if n.Password != "secret" {
		t.Errorf("password = %q, want secret", n.Password)
	}
	if n.Server != "1.2.3.4" || n.Port != 8388 {
		t.Errorf("endpoint = %s:%d", n.Server, n.Port)
	}
	if n.Name != "香港 01" {
		t.Errorf("name = %q, want decoded name", n.Name)
	}
	if n.ID == "" {
		t.Error("ID should be computed")
	}
}

func TestParseShadowsocksLegacyWholeBase64(t *testing.T) {
	// 旧式形态：整段 base64("method:password@host:port")
	payload := base64.StdEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:pw@example.com:443"))
	n, err := ParseURI("ss://" + payload + "#legacy")
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}
	if n.Method != "chacha20-ietf-poly1305" || n.Server != "example.com" || n.Port != 443 {
		t.Errorf("got %s / %s:%d", n.Method, n.Server, n.Port)
	}
	if n.Name != "legacy" {
		t.Errorf("name = %q", n.Name)
	}
}

func TestParseShadowsocksPluginGoesToQuarantine(t *testing.T) {
	cred := base64.RawURLEncoding.EncodeToString([]byte("aes-128-gcm:pw"))
	uri := "ss://" + cred + "@h.example.com:8388/?plugin=" + url.QueryEscape("obfs-local;obfs=http") + "#n"

	n, err := ParseURI(uri)
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}
	// 不支持的字段必须保留在隔离区，不能静默丢弃。
	if _, ok := n.Quarantine["plugin"]; !ok {
		t.Error("plugin should be quarantined, not dropped")
	}
}

func TestParseVMess(t *testing.T) {
	// 服务端构造的字段集合（General.php:80-92）
	payload := map[string]any{
		"v":    "2",
		"ps":   "东京 02",
		"add":  "jp.example.com",
		"port": "443",
		"id":   "b831381d-6324-4d53-ad4f-8cda48b30811",
		"aid":  "0",
		"net":  "ws",
		"type": "none",
		"host": "cdn.example.com",
		"path": "/ws",
		"tls":  "tls",
		"sni":  "cdn.example.com",
		"fp":   "chrome",
	}
	raw, _ := json.Marshal(payload)
	uri := "vmess://" + base64.StdEncoding.EncodeToString(raw)

	n, err := ParseURI(uri)
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}
	if n.Protocol != domain.ProtocolVMess {
		t.Errorf("protocol = %q", n.Protocol)
	}
	if n.UUID != payload["id"] {
		t.Errorf("uuid = %q", n.UUID)
	}
	if n.Server != "jp.example.com" || n.Port != 443 {
		t.Errorf("endpoint = %s:%d", n.Server, n.Port)
	}
	if n.Transport != domain.TransportWS {
		t.Errorf("transport = %q, want ws", n.Transport)
	}
	if n.TransportOptions.Path != "/ws" || n.TransportOptions.Host != "cdn.example.com" {
		t.Errorf("transport opts = %+v", n.TransportOptions)
	}
	if n.Security != domain.SecurityTLS {
		t.Errorf("security = %q, want tls", n.Security)
	}
	if n.TLS.Fingerprint != "chrome" {
		t.Errorf("fingerprint = %q", n.TLS.Fingerprint)
	}
	if n.Name != "东京 02" {
		t.Errorf("name = %q", n.Name)
	}
}

func TestParseVMessGRPCUsesPathAsServiceName(t *testing.T) {
	payload := map[string]any{
		"v": "2", "ps": "grpc-node", "add": "g.example.com", "port": 8443,
		"id": "11111111-2222-3333-4444-555555555555", "aid": 0,
		"net": "grpc", "path": "my.Service", "tls": "tls",
	}
	raw, _ := json.Marshal(payload)
	n, err := ParseURI("vmess://" + base64.StdEncoding.EncodeToString(raw))
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}
	if n.TransportOptions.ServiceName != "my.Service" {
		t.Errorf("serviceName = %q, want my.Service", n.TransportOptions.ServiceName)
	}
}

func TestParseVLESSWithReality(t *testing.T) {
	uri := "vless://2a4b7c1d-0000-1111-2222-333344445555@r.example.com:443" +
		"?type=tcp&security=reality&pbk=PUBKEY123&sid=ab12&sni=www.microsoft.com&fp=chrome&flow=xtls-rprx-vision#Reality"

	n, err := ParseURI(uri)
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}
	if n.Protocol != domain.ProtocolVLESS {
		t.Errorf("protocol = %q", n.Protocol)
	}
	if n.Security != domain.SecurityReality {
		t.Errorf("security = %q, want reality", n.Security)
	}
	if n.TLS.RealityPublicKey != "PUBKEY123" || n.TLS.RealityShortID != "ab12" {
		t.Errorf("reality = %q / %q", n.TLS.RealityPublicKey, n.TLS.RealityShortID)
	}
	if n.Flow != "xtls-rprx-vision" {
		t.Errorf("flow = %q", n.Flow)
	}
	if n.Transport != domain.TransportTCP {
		t.Errorf("transport = %q, want tcp", n.Transport)
	}
}

func TestParseVLESSRealityWithoutPublicKeyRejected(t *testing.T) {
	// reality 缺少 pbk 属于数据不自洽，必须在模型层拒绝。
	uri := "vless://uuid-1@r.example.com:443?security=reality&sni=a.com#bad"
	if _, err := ParseURI(uri); err == nil {
		t.Fatal("expected validation error for reality without public key")
	}
}

func TestParseTrojanWithWebSocketTLS(t *testing.T) {
	uri := "trojan://p%40ssw0rd@t.example.com:443" +
		"?security=tls&type=ws&path=%2Fws&host=cdn.example.com&sni=t.example.com#Trojan-WS"

	n, err := ParseURI(uri)
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}
	if n.Protocol != domain.ProtocolTrojan {
		t.Errorf("protocol = %q", n.Protocol)
	}
	if n.Password != "p@ssw0rd" {
		t.Errorf("password = %q, want p@ssw0rd", n.Password)
	}
	if n.Transport != domain.TransportWS {
		t.Errorf("transport = %q", n.Transport)
	}
	if n.TransportOptions.Path != "/ws" {
		t.Errorf("path = %q", n.TransportOptions.Path)
	}
	if n.Security != domain.SecurityTLS || n.TLS.ServerName != "t.example.com" {
		t.Errorf("security = %q sni = %q", n.Security, n.TLS.ServerName)
	}
}

func TestParseURIRejectsUnsupportedScheme(t *testing.T) {
	// 注意：这里**不能**再用 hysteria2 当例子了。
	//
	// 这条断言原先写的就是 `hysteria2://x@h:443`——它把「我们还不支持
	// hysteria2」固化成了一个正确的期望。实现之后那条断言变成了
	// 「实现是错误的」，于是必须改。留下这段说明是因为：
	// **把已知缺口写成断言是合理的，但实现之后一定要回来改它**，
	// 否则测试会挡着正确的改动。
	if _, err := ParseURI("tuic://x@h:443"); err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
	if _, err := ParseURI("not-a-uri"); err == nil {
		t.Fatal("expected error for URI without scheme")
	}
}

// Hysteria2 此前**完全无法使用**：解析器不认这个 scheme，
// 于是订阅里这类节点被整条丢掉（真实订阅里就有 2 个）。
//
// 用例的字段构成照抄真实订阅里的链接，因此它验证的是真实形状，
// 不是我想象出来的形状。
func TestParseHysteria2(t *testing.T) {
	// 真实形状：hysteria2://<password>@host:port?sni=...&insecure=0#<name>
	uri := "hysteria2://secret-pw@hy.example.com:42555?sni=hy.example.com&insecure=0#" +
		url.QueryEscape("[Hy2]us-Hysteria")

	n, err := ParseURI(uri)
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}
	if n.Protocol != domain.ProtocolHysteria2 {
		t.Errorf("protocol = %q，期望 hysteria2", n.Protocol)
	}
	if n.Password != "secret-pw" {
		t.Errorf("password = %q，期望 secret-pw", n.Password)
	}
	if n.Server != "hy.example.com" || n.Port != 42555 {
		t.Errorf("endpoint = %s:%d，期望 hy.example.com:42555", n.Server, n.Port)
	}
	// hysteria2 自带 TLS：URI 里没有 security 参数也必须当成加密，
	// 不能因为缺参数就把节点当明文。
	if n.Security != domain.SecurityTLS {
		t.Errorf("security = %q，期望 tls（该协议永远走 TLS）", n.Security)
	}
	if n.Name != "[Hy2]us-Hysteria" {
		t.Errorf("name = %q", n.Name)
	}
	if n.TLS.AllowInsecure {
		t.Errorf("insecure=0 时不应打开证书校验豁免")
	}
}

func TestParseHysteria2DefaultsSNIToServer(t *testing.T) {
	n, err := ParseURI("hysteria2://pw@hy.example.com:443#n")
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}
	if n.TLS.ServerName != "hy.example.com" {
		t.Errorf("缺少 sni 时应当用服务器地址兜底，实际 %q", n.TLS.ServerName)
	}
}

func TestParseHysteria2RequiresPassword(t *testing.T) {
	// 空密码会让核心以「认证失败」告终，而那个错误看起来像服务端的问题。
	if _, err := ParseURI("hysteria2://@hy.example.com:443#n"); err == nil {
		t.Error("缺少密码时应当报错")
	}
}

func TestParseHysteria2Insecure(t *testing.T) {
	n, err := ParseURI("hysteria2://pw@hy.example.com:443?insecure=1#n")
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}
	if !n.TLS.AllowInsecure {
		t.Error("insecure=1 时应当打开证书校验豁免")
	}
}

// obfs 与 obfs-password 必须成对；只给一个时不能生成半个配置。
func TestParseHysteria2ObfsPairing(t *testing.T) {
	full, err := ParseURI("hysteria2://pw@h.example.com:443?obfs=salamander&obfs-password=s3cr3t#n")
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}
	if full.Quarantine["obfs"] != "salamander" || full.Quarantine["obfs-password"] != "s3cr3t" {
		t.Errorf("成对的 obfs 应当被保留，实际 %v", full.Quarantine)
	}

	// 只有 obfs，没有密码：仍然保留（可见），但不构成可用组合。
	half, err := ParseURI("hysteria2://pw@h.example.com:443?obfs=salamander#n")
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}
	if half.Quarantine["obfs"] != "salamander" {
		t.Errorf("半套 obfs 也应当被记录而不是丢掉，实际 %v", half.Quarantine)
	}
}

func TestParseURIRejectsBadPort(t *testing.T) {
	uri := "trojan://pw@h.example.com:99999?security=tls#x"
	if _, err := ParseURI(uri); err == nil {
		t.Fatal("expected error for out-of-range port")
	}
}

func TestParseBase64URIListSkipsBadEntries(t *testing.T) {
	good1 := "trojan://pw@a.example.com:443?security=tls#A"
	good2 := "trojan://pw@b.example.com:443?security=tls#B"
	bad := "this-is-not-a-uri"

	list := strings.Join([]string{good1, bad, good2}, "\r\n")
	encoded := base64.StdEncoding.EncodeToString([]byte(list))

	nodes, warnings, rejected, err := ParseBase64URIList([]byte(encoded))
	if err != nil {
		t.Fatalf("ParseBase64URIList: %v", err)
	}
	// 一个坏节点不应导致整个订阅失败。
	if len(nodes) != 2 {
		t.Errorf("nodes = %d, want 2", len(nodes))
	}
	if rejected != 1 {
		t.Errorf("rejected = %d, want 1", rejected)
	}
	if len(warnings) != 1 {
		t.Errorf("warnings = %d, want 1", len(warnings))
	}
}

func TestParseBase64URIListAcceptsPlainTextList(t *testing.T) {
	// 少数部署直接返回明文列表，也应支持。
	plain := "trojan://pw@a.example.com:443?security=tls#A"
	nodes, _, _, err := ParseBase64URIList([]byte(plain))
	if err != nil {
		t.Fatalf("ParseBase64URIList: %v", err)
	}
	if len(nodes) != 1 {
		t.Errorf("nodes = %d, want 1", len(nodes))
	}
}

func TestDetectFormat(t *testing.T) {
	uriList := base64.StdEncoding.EncodeToString([]byte("trojan://pw@a.com:443#A"))
	cases := []struct {
		name string
		body string
		want Format
	}{
		{"uri list base64", uriList, FormatURIList},
		{"uri list plain", "trojan://pw@a.com:443#A", FormatURIList},
		{"singbox json", `{"outbounds":[{"type":"direct"}]}`, FormatSingBoxJSON},
		{"clash yaml", "proxies:\n  - name: a\n", FormatClashYAML},
		{"clash meta yaml", "proxy-groups:\n  - name: g\n", FormatClashYAML},
		{"empty", "", FormatUnknown},
		{"garbage", "hello world", FormatUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DetectFormat([]byte(tc.body)); got != tc.want {
				t.Errorf("DetectFormat = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseUserInfoHeader(t *testing.T) {
	// 服务端生成格式见 General.php:53
	info := ParseUserInfoHeader("upload=1024; download=2048; total=10737418240; expire=1735689600")
	if !info.Present {
		t.Fatal("Present should be true")
	}
	if info.Upload != 1024 || info.Download != 2048 || info.Total != 10737418240 || info.Expire != 1735689600 {
		t.Errorf("parsed = %+v", info)
	}
	if info.Used() != 3072 {
		t.Errorf("Used = %d, want 3072", info.Used())
	}
}

func TestParseUserInfoHeaderAbsent(t *testing.T) {
	info := ParseUserInfoHeader("")
	if info.Present {
		t.Error("Present should be false when header is absent")
	}
	// 部分字段缺失时不应报错，缺失值按 0 处理。
	partial := ParseUserInfoHeader("upload=5")
	if !partial.Present || partial.Upload != 5 || partial.Total != 0 {
		t.Errorf("partial = %+v", partial)
	}
}

func TestNodeIDIsStableAcrossRename(t *testing.T) {
	// 契约：稳定 ID 不包含显示名，改名不得改变用户已选节点。
	a, err := ParseURI("trojan://pw@a.example.com:443?security=tls#Name-One")
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}
	b, err := ParseURI("trojan://pw@a.example.com:443?security=tls#Name-Two")
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}
	if a.ID != b.ID {
		t.Errorf("ID changed on rename: %s vs %s", a.ID, b.ID)
	}
	if a.Name == b.Name {
		t.Error("names should differ in this fixture")
	}
}

func TestNodeIDDiffersOnParameterChange(t *testing.T) {
	a, _ := ParseURI("trojan://pw@a.example.com:443?security=tls#N")
	b, _ := ParseURI("trojan://pw@a.example.com:8443?security=tls#N")
	if a.ID == b.ID {
		t.Error("ID must change when the port changes")
	}
}
