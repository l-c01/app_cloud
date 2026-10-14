// Package domain 定义客户端的规范领域模型。
//
// 设计目标（见 design/architecture 与 design/DESIGN.zh-CN.md §6.3）：
//   - 所有导入来源（面板订阅、第三方订阅、手工节点、原生配置）都先归一为本模型；
//   - 两个核心编译器只依赖本模型，不依赖任何具体订阅格式；
//   - 未知字段不得静默丢弃，一律进入 Quarantine 供用户查看。
//
// 本包只做数据建模，不做网络请求、不读文件、不调用平台接口。
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
)

// Protocol 是节点协议类型。
type Protocol string

const (
	ProtocolShadowsocks Protocol = "shadowsocks"
	ProtocolVMess       Protocol = "vmess"
	ProtocolVLESS       Protocol = "vless"
	ProtocolTrojan      Protocol = "trojan"
	// 以下协议在服务端存在，但需要扩展矩阵或原生配置支持，
	// 首轮标准协议集合只有上面四个（见设计基线 §6.3）。
	ProtocolHysteria  Protocol = "hysteria"
	ProtocolHysteria2 Protocol = "hysteria2"
	ProtocolTUIC      Protocol = "tuic"
	ProtocolAnyTLS    Protocol = "anytls"
	ProtocolSocks     Protocol = "socks"
	ProtocolHTTP      Protocol = "http"
)

// Transport 是传输层类型。
type Transport string

const (
	TransportTCP         Transport = "tcp"
	TransportWS          Transport = "ws"
	TransportGRPC        Transport = "grpc"
	TransportHTTP        Transport = "http"
	TransportH2          Transport = "h2"
	TransportHTTPUpgrade Transport = "httpupgrade"
	TransportXHTTP       Transport = "xhttp"
)

// Security 是传输安全类型。
type Security string

const (
	SecurityNone    Security = "none"
	SecurityTLS     Security = "tls"
	SecurityReality Security = "reality"
)

// TLSOptions 描述 TLS / Reality 参数。
type TLSOptions struct {
	// ServerName 即 SNI。
	ServerName string
	// AllowInsecure 对应服务端的 allow_insecure / insecure 语义。
	// 默认必须为 false；只有用户在专业模式里显式开启才允许为 true。
	AllowInsecure bool
	// Fingerprint 是 uTLS 指纹（如 chrome、firefox）。
	Fingerprint string
	// ALPN 列表。
	ALPN []string
	// Reality 参数，仅 Security == SecurityReality 时有效。
	RealityPublicKey string
	RealityShortID   string
}

// TransportOptions 描述传输层参数。不同传输只使用其中一部分字段。
type TransportOptions struct {
	// Path 用于 ws / h2 / httpupgrade / xhttp。
	Path string
	// Host 用于 ws / h2 / httpupgrade / xhttp 的 Host 头。
	Host string
	// ServiceName 用于 grpc。
	ServiceName string
	// HeaderType 用于 tcp 的 http 伪装（none 表示不启用）。
	HeaderType string
	// Mode 用于 xhttp。
	Mode string
}

// Multiplex 描述多路复用参数。
type Multiplex struct {
	Enabled    bool
	Protocol   string
	MaxStreams int
}

// Node 是规范化后的单个节点。
//
// 敏感字段：UUID、Password、RealityPublicKey 等属于连接凭据，
// 调用方必须按秘密处理，不得写入日志、诊断包或普通导出。
type Node struct {
	// ID 是由规范化参数派生的稳定标识。选择与回退都必须用它，不能用显示名匹配。
	ID string
	// Name 是展示名，可能重复，不可作为唯一标识。
	Name string
	// SourceID 标识节点来自哪个导入来源（订阅或手工集合）。
	SourceID string

	Protocol Protocol
	// Server 是主机名或 IP。IPv6 已去掉方括号。
	Server string
	Port   int

	// --- 凭据（按协议取用）---
	// UUID 用于 vmess / vless。
	UUID string
	// Password 用于 shadowsocks / trojan / socks / http。
	Password string
	// Method 是 shadowsocks 加密方式（如 aes-256-gcm、2022-blake3-aes-256-gcm）。
	Method string
	// Flow 用于 vless（如 xtls-rprx-vision）。
	Flow string
	// AlterID 用于 vmess。
	AlterID int

	Transport        Transport
	TransportOptions TransportOptions
	Security         Security
	TLS              TLSOptions
	Mux              *Multiplex

	// Quarantine 保存本模型不认识但来源提供的字段，避免静默丢弃。
	// 键为原始字段名，值为原始值。
	Quarantine map[string]any
}

// History 表示一次记录的历史，用于 ID 稳定性的说明性字段（保留接口位，未使用）。
type History struct{}

// Validate 做当前模型层能确定的基础校验。
//
// 它**不**判断某个核心是否支持该节点——那是编译器的能力校验职责。
// 这里只检查数据本身是否自洽，避免把明显残缺的节点送进编译器。
func (n *Node) Validate() error {
	if n == nil {
		return fmt.Errorf("node is nil")
	}
	if strings.TrimSpace(n.Server) == "" {
		return fmt.Errorf("node %q: server is empty", n.Name)
	}
	if n.Port <= 0 || n.Port > 65535 {
		return fmt.Errorf("node %q: port %d out of range", n.Name, n.Port)
	}
	switch n.Protocol {
	case ProtocolShadowsocks:
		if n.Method == "" {
			return fmt.Errorf("node %q: shadowsocks requires a method", n.Name)
		}
		if n.Password == "" {
			return fmt.Errorf("node %q: shadowsocks requires a password", n.Name)
		}
	case ProtocolVMess:
		if n.UUID == "" {
			return fmt.Errorf("node %q: vmess requires a uuid", n.Name)
		}
	case ProtocolVLESS:
		if n.UUID == "" {
			return fmt.Errorf("node %q: vless requires a uuid", n.Name)
		}
	case ProtocolTrojan:
		if n.Password == "" {
			return fmt.Errorf("node %q: trojan requires a password", n.Name)
		}
	case "":
		return fmt.Errorf("node %q: protocol is empty", n.Name)
	default:
		// 其它协议首轮由原生配置或扩展矩阵承载，模型层不额外约束。
	}
	if n.Security == SecurityReality && n.TLS.RealityPublicKey == "" {
		return fmt.Errorf("node %q: reality requires a public key", n.Name)
	}
	return nil
}

// Normalize 清理并规范化字段，使等价节点得到一致表示。
//
// 需要归一化的点：
//   - IPv6 去掉方括号；
//   - 主机名小写、去空白；
//   - 空 Transport 视为 tcp；
//   - TLS 关闭时清空 TLS 细节，避免残留参数造成编译器歧义。
func (n *Node) Normalize() {
	n.Server = normalizeHost(n.Server)
	n.Name = strings.TrimSpace(n.Name)
	n.Transport = normalizeTransport(n.Transport)
	if n.Security == "" {
		n.Security = SecurityNone
	}
	if n.Security == SecurityNone {
		n.TLS = TLSOptions{}
	}
	if n.Security != SecurityReality {
		n.TLS.RealityPublicKey = ""
		n.TLS.RealityShortID = ""
	}
	if n.Mux != nil && !n.Mux.Enabled {
		n.Mux = nil
	}
}

// ComputeID 依据规范化参数计算稳定 ID 并写入 n.ID。
//
// 稳定性要求（设计基线 §6.3）：以明确的 ID 或规范化参数摘要保持用户选择，
// 不能只用显示名称匹配。因此这里刻意**不包含** Name，
// 使改名不会改变用户已选节点。
func (n *Node) ComputeID() string {
	n.Normalize()
	h := sha256.New()
	// 字段顺序固定，确保同一节点每次得到相同摘要。
	parts := []string{
		string(n.Protocol),
		strings.ToLower(n.Server),
		strconv.Itoa(n.Port),
		n.UUID,
		n.Password,
		n.Method,
		n.Flow,
		strconv.Itoa(n.AlterID),
		string(n.Transport),
		n.TransportOptions.Path,
		n.TransportOptions.Host,
		n.TransportOptions.ServiceName,
		n.TransportOptions.HeaderType,
		n.TransportOptions.Mode,
		string(n.Security),
		n.TLS.ServerName,
		n.TLS.Fingerprint,
		n.TLS.RealityPublicKey,
		n.TLS.RealityShortID,
		strings.Join(n.TLS.ALPN, ","),
	}
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0}) // 分隔符，避免相邻字段拼接歧义
	}
	n.ID = hex.EncodeToString(h.Sum(nil))[:32]
	return n.ID
}

// Address 返回 host:port 形式，IPv6 自动加方括号。
func (n Node) Address() string {
	return net.JoinHostPort(n.Server, strconv.Itoa(n.Port))
}

// QuarantineField 记录一个未知字段。重复记录同一字段时保留首个值并追加提示。
func (n *Node) QuarantineField(key string, value any) {
	if n.Quarantine == nil {
		n.Quarantine = make(map[string]any)
	}
	if _, exists := n.Quarantine[key]; exists {
		return
	}
	n.Quarantine[key] = value
}

// QuarantinedKeys 返回排序后的未知字段名，便于生成稳定的报告输出。
func (n Node) QuarantinedKeys() []string {
	if len(n.Quarantine) == 0 {
		return nil
	}
	keys := make([]string, 0, len(n.Quarantine))
	for k := range n.Quarantine {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// normalizeHost 去掉 IPv6 方括号与首尾空白，并把主机名转为小写。
func normalizeHost(host string) string {
	h := strings.TrimSpace(host)
	h = strings.TrimPrefix(h, "[")
	h = strings.TrimSuffix(h, "]")
	return strings.ToLower(h)
}

// normalizeTransport 把来源中的传输别名归一到标准值。
func normalizeTransport(t Transport) Transport {
	switch strings.ToLower(strings.TrimSpace(string(t))) {
	case "", "tcp", "raw":
		return TransportTCP
	case "ws", "websocket":
		return TransportWS
	case "grpc", "gun":
		return TransportGRPC
	case "http":
		return TransportHTTP
	case "h2", "http/2":
		return TransportH2
	case "httpupgrade":
		return TransportHTTPUpgrade
	case "xhttp", "splithttp":
		return TransportXHTTP
	default:
		return t
	}
}
