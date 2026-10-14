// Package subscription 负责拉取并解析订阅内容，产出 domain.Node 列表。
//
// 支持范围（首轮，见设计基线 §6.3）：
//   - Base64 编码的节点 URI 列表（Xboard 的 general/v2rayn 等 flag 使用此格式）；
//   - Clash / ClashMeta YAML；
//   - sing-box JSON。
//
// 明确不支持：执行订阅内的脚本、加载任意本地文件、跟随无限跳转。
//
// 安全约束：订阅 URL 含 token，属秘密；本包不写日志，错误信息中不得出现完整 URL。
package subscription

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"proxyclient/shared/domain"
)

// maxNodes 限制单次订阅解析出的节点数，防止异常订阅耗尽内存。
const maxNodes = 20000

// ParseURI 把单条节点 URI 解析为 domain.Node。
//
// 支持的 scheme：ss / vmess / vless / trojan。
// 其他 scheme 返回错误，由调用方记入解析报告，而不是静默跳过。
func ParseURI(uri string) (*domain.Node, error) {
	trimmed := strings.TrimSpace(uri)
	if trimmed == "" {
		return nil, fmt.Errorf("empty node URI")
	}

	scheme, rest, ok := splitScheme(trimmed)
	if !ok {
		return nil, fmt.Errorf("node URI has no scheme")
	}

	var (
		node *domain.Node
		err  error
	)
	switch scheme {
	case "ss":
		node, err = parseShadowsocks(rest)
	case "vmess":
		node, err = parseVMess(rest)
	case "vless":
		node, err = parseVLESS(rest)
	case "trojan":
		node, err = parseTrojan(rest)
	default:
		return nil, fmt.Errorf("unsupported node URI scheme: %s", scheme)
	}
	if err != nil {
		return nil, err
	}

	node.Normalize()
	if err := node.Validate(); err != nil {
		return nil, err
	}
	node.ComputeID()
	return node, nil
}

// splitScheme 拆出 scheme 与剩余部分（不区分大小写）。
func splitScheme(uri string) (scheme, rest string, ok bool) {
	idx := strings.Index(uri, "://")
	if idx <= 0 {
		return "", "", false
	}
	return strings.ToLower(uri[:idx]), uri[idx+3:], true
}

// parseShadowsocks 解析 ss:// URI。
//
// 服务端实际格式（src/references/Xboard-master/app/Protocols/General.php:56-75）：
//
//	ss://<base64url(cipher:password)>@host:port#name
//	ss://<base64url(cipher:password)>@host:port/?plugin=...#name
//
// 同时兼容旧式整体 base64 形态 ss://<base64(cipher:password@host:port)>#name，
// 因为第三方订阅仍在广泛使用。
func parseShadowsocks(rest string) (*domain.Node, error) {
	// 情况一：整体 base64（不含 '@' 且不含 ':' 的明文分隔）
	if !strings.Contains(rest, "@") {
		body, frag := cutFragment(rest)
		decoded, err := decodeBase64Loose(body)
		if err != nil {
			return nil, fmt.Errorf("ss: cannot decode legacy payload: %w", err)
		}
		// 解码后形如 cipher:password@host:port
		at := strings.LastIndex(decoded, "@")
		if at < 0 {
			return nil, fmt.Errorf("ss: legacy payload missing '@'")
		}
		cred, hostPort := decoded[:at], decoded[at+1:]
		method, password, ok := strings.Cut(cred, ":")
		if !ok {
			return nil, fmt.Errorf("ss: legacy payload missing method separator")
		}
		host, port, err := splitHostPort(hostPort)
		if err != nil {
			return nil, err
		}
		return &domain.Node{
			Name:     frag,
			Protocol: domain.ProtocolShadowsocks,
			Server:   host,
			Port:     port,
			Method:   method,
			Password: password,
		}, nil
	}

	// 情况二：SIP002 风格
	u, err := url.Parse("ss://" + rest)
	if err != nil {
		return nil, fmt.Errorf("ss: invalid URI: %w", err)
	}
	if u.User == nil {
		return nil, fmt.Errorf("ss: missing credentials")
	}

	rawUser := u.User.Username()
	password, hasPassword := u.User.Password()

	var method string
	if hasPassword {
		// 形如 ss://method:password@host:port 的明文形态
		method = rawUser
	} else {
		decoded, derr := decodeBase64Loose(rawUser)
		if derr != nil {
			return nil, fmt.Errorf("ss: cannot decode credential: %w", derr)
		}
		var ok bool
		method, password, ok = strings.Cut(decoded, ":")
		if !ok {
			return nil, fmt.Errorf("ss: credential must be method:password")
		}
	}

	n := &domain.Node{
		Name:     fragmentName(u),
		Protocol: domain.ProtocolShadowsocks,
		Server:   u.Hostname(),
		Method:   method,
		Password: password,
	}
	n.Port, err = parsePort(u.Port())
	if err != nil {
		return nil, fmt.Errorf("ss: %w", err)
	}

	// plugin 参数本模型不直接承载，记入隔离区，避免静默丢弃。
	if plugin := u.Query().Get("plugin"); plugin != "" {
		n.QuarantineField("plugin", plugin)
	}
	return n, nil
}

// vmessPayload 对应服务端 buildVmess 输出的 JSON 结构。
//
// 事实来源：General.php:77-152。字段名沿用 v2rayN 约定。
type vmessPayload struct {
	Version string `json:"v"`
	PS      string `json:"ps"`
	Add     string `json:"add"`
	Port    any    `json:"port"`
	ID      string `json:"id"`
	Aid     any    `json:"aid"`
	Net     string `json:"net"`
	Type    string `json:"type"`
	Host    string `json:"host"`
	Path    string `json:"path"`
	TLS     string `json:"tls"`
	SNI     string `json:"sni"`
	FP      string `json:"fp"`
	Mode    string `json:"mode"`
}

// parseVMess 解析 vmess://base64(json)。
func parseVMess(rest string) (*domain.Node, error) {
	body, frag := cutFragment(rest)
	decoded, err := decodeBase64Loose(body)
	if err != nil {
		return nil, fmt.Errorf("vmess: cannot decode payload: %w", err)
	}

	var p vmessPayload
	if err := json.Unmarshal([]byte(decoded), &p); err != nil {
		return nil, fmt.Errorf("vmess: invalid payload JSON: %w", err)
	}

	port, err := coercePort(p.Port)
	if err != nil {
		return nil, fmt.Errorf("vmess: %w", err)
	}
	aid, err := coerceInt(p.Aid)
	if err != nil {
		return nil, fmt.Errorf("vmess: invalid aid: %w", err)
	}

	n := &domain.Node{
		Name:      firstNonEmpty(p.PS, frag),
		Protocol:  domain.ProtocolVMess,
		Server:    p.Add,
		Port:      port,
		UUID:      p.ID,
		AlterID:   aid,
		Transport: domain.Transport(p.Net),
	}
	n.TransportOptions = domain.TransportOptions{
		Path:        p.Path,
		Host:        p.Host,
		ServiceName: serviceNameFor(domain.Transport(p.Net), p.Path),
		HeaderType:  headerTypeFor(domain.Transport(p.Net), p.Type),
		Mode:        p.Mode,
	}
	if strings.EqualFold(p.TLS, "tls") {
		n.Security = domain.SecurityTLS
		n.TLS.ServerName = p.SNI
		n.TLS.Fingerprint = p.FP
	}
	return n, nil
}

// parseVLESS 解析 vless://uuid@host:port?params#name。
//
// 事实来源：General.php:243。
func parseVLESS(rest string) (*domain.Node, error) {
	u, err := url.Parse("vless://" + rest)
	if err != nil {
		return nil, fmt.Errorf("vless: invalid URI: %w", err)
	}
	if u.User == nil || u.User.Username() == "" {
		return nil, fmt.Errorf("vless: missing uuid")
	}

	port, err := parsePort(u.Port())
	if err != nil {
		return nil, fmt.Errorf("vless: %w", err)
	}

	q := u.Query()
	n := &domain.Node{
		Name:      fragmentName(u),
		Protocol:  domain.ProtocolVLESS,
		Server:    u.Hostname(),
		Port:      port,
		UUID:      u.User.Username(),
		Flow:      q.Get("flow"),
		Transport: domain.Transport(firstNonEmpty(q.Get("type"), q.Get("net"))),
	}
	n.TransportOptions = transportOptionsFromQuery(q)
	applySecurityFromQuery(n, q)
	return n, nil
}

// parseTrojan 解析 trojan://password@host:port?params#name。
//
// 事实来源：General.php:247-322。
func parseTrojan(rest string) (*domain.Node, error) {
	u, err := url.Parse("trojan://" + rest)
	if err != nil {
		return nil, fmt.Errorf("trojan: invalid URI: %w", err)
	}
	if u.User == nil || u.User.Username() == "" {
		return nil, fmt.Errorf("trojan: missing password")
	}

	port, err := parsePort(u.Port())
	if err != nil {
		return nil, fmt.Errorf("trojan: %w", err)
	}

	q := u.Query()
	n := &domain.Node{
		Name:      fragmentName(u),
		Protocol:  domain.ProtocolTrojan,
		Server:    u.Hostname(),
		Port:      port,
		Password:  u.User.Username(),
		Transport: domain.Transport(firstNonEmpty(q.Get("type"), q.Get("net"))),
	}
	n.TransportOptions = transportOptionsFromQuery(q)
	applySecurityFromQuery(n, q)
	return n, nil
}

// transportOptionsFromQuery 从查询参数提取传输选项。
//
// 服务端在 vless/trojan 的 query 中使用 type/path/host/serviceName/mode 等键。
func transportOptionsFromQuery(q url.Values) domain.TransportOptions {
	return domain.TransportOptions{
		Path:        q.Get("path"),
		Host:        q.Get("host"),
		ServiceName: firstNonEmpty(q.Get("serviceName"), q.Get("servicename")),
		HeaderType:  headerTypeFor(domain.Transport(q.Get("type")), q.Get("headerType")),
		Mode:        q.Get("mode"),
	}
}

// applySecurityFromQuery 依据 query 设置 TLS / Reality 参数。
func applySecurityFromQuery(n *domain.Node, q url.Values) {
	security := strings.ToLower(q.Get("security"))
	switch security {
	case "reality":
		n.Security = domain.SecurityReality
	case "tls":
		n.Security = domain.SecurityTLS
	case "", "none":
		// 保持零值，交由 Normalize 处理。
		return
	default:
		// 未知安全类型记入隔离区，不静默当作 tls。
		n.QuarantineField("security", security)
		return
	}

	n.TLS.ServerName = firstNonEmpty(q.Get("sni"), q.Get("peer"))
	n.TLS.Fingerprint = q.Get("fp")
	if v := q.Get("allowInsecure"); v == "1" || strings.EqualFold(v, "true") {
		n.TLS.AllowInsecure = true
	}
	if v := q.Get("insecure"); v == "1" || strings.EqualFold(v, "true") {
		n.TLS.AllowInsecure = true
	}
	if alpn := q.Get("alpn"); alpn != "" {
		n.TLS.ALPN = splitAndTrim(alpn, ",")
	}
	n.TLS.RealityPublicKey = q.Get("pbk")
	n.TLS.RealityShortID = q.Get("sid")
}

// serviceNameFor 在 grpc 传输下，服务端把 serviceName 放在 path 字段里。
func serviceNameFor(t domain.Transport, path string) string {
	if t == domain.TransportGRPC {
		return path
	}
	return ""
}

// headerTypeFor 处理 tcp 的 http 伪装：服务端用 type 字段承载 header type。
func headerTypeFor(t domain.Transport, typeField string) string {
	if t == domain.TransportTCP || t == "" {
		if typeField != "" && typeField != "none" {
			return typeField
		}
	}
	return ""
}

// cutFragment 拆出 fragment（不含 '#'）与主体。
func cutFragment(s string) (body, frag string) {
	if idx := strings.LastIndex(s, "#"); idx >= 0 {
		frag = s[idx+1:]
		if decoded, err := url.QueryUnescape(frag); err == nil {
			frag = decoded
		}
		return s[:idx], frag
	}
	return s, ""
}

// fragmentName 返回 URI fragment 作为节点名（已解码）。
func fragmentName(u *url.URL) string {
	if u.Fragment == "" {
		return ""
	}
	if decoded, err := url.QueryUnescape(u.Fragment); err == nil {
		return decoded
	}
	return u.Fragment
}

// splitHostPort 拆分 host:port，支持 IPv6 方括号形式。
func splitHostPort(s string) (host string, port int, err error) {
	idx := strings.LastIndex(s, ":")
	if idx < 0 {
		return "", 0, fmt.Errorf("missing port in %q", s)
	}
	host = strings.Trim(s[:idx], "[]")
	port, err = parsePort(s[idx+1:])
	if err != nil {
		return "", 0, err
	}
	return host, port, nil
}

// parsePort 解析端口并校验范围。
func parsePort(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("missing port")
	}
	p, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("invalid port %q", s)
	}
	if p <= 0 || p > 65535 {
		return 0, fmt.Errorf("port %d out of range", p)
	}
	return p, nil
}

// coercePort 兼容 JSON 中端口为字符串或数字的情况。
func coercePort(v any) (int, error) {
	switch t := v.(type) {
	case string:
		return parsePort(t)
	case float64:
		p := int(t)
		if p <= 0 || p > 65535 {
			return 0, fmt.Errorf("port %d out of range", p)
		}
		return p, nil
	case nil:
		return 0, fmt.Errorf("missing port")
	default:
		return 0, fmt.Errorf("unsupported port type %T", v)
	}
}

// coerceInt 兼容 JSON 中整数为字符串或数字的情况。
func coerceInt(v any) (int, error) {
	switch t := v.(type) {
	case nil:
		return 0, nil
	case string:
		if strings.TrimSpace(t) == "" {
			return 0, nil
		}
		return strconv.Atoi(strings.TrimSpace(t))
	case float64:
		return int(t), nil
	default:
		return 0, fmt.Errorf("unsupported integer type %T", v)
	}
}

// decodeBase64Loose 依次尝试多种 base64 变体。
//
// 现实中的订阅可能使用标准或 URL-safe 字母表，也可能省略或保留填充，
// 因此不能只试一种。
func decodeBase64Loose(s string) (string, error) {
	candidate := strings.TrimSpace(s)
	// URI 中 '#' 之后的内容已在上层剥离，这里只需处理可能的空白。
	candidate = strings.ReplaceAll(candidate, "\n", "")
	candidate = strings.ReplaceAll(candidate, "\r", "")

	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	var lastErr error
	for _, enc := range encodings {
		if decoded, err := enc.DecodeString(candidate); err == nil {
			return string(decoded), nil
		} else {
			lastErr = err
		}
	}
	return "", fmt.Errorf("unsupported base64 payload: %w", lastErr)
}

// firstNonEmpty 返回第一个非空字符串。
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// splitAndTrim 按分隔符拆分并去除空白与空项。
func splitAndTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
