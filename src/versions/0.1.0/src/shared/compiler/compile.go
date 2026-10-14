// Package compiler 把规范节点模型编译为 sing-box 配置。
//
// 核心原则（设计基线 §5.2、§7.1）：
//   - 只有一个生成器：本构建只发布 sing-box（ADR-0005），
//     因此不再需要在多种配置格式之间做选择或转换；
//   - 不支持的参数必须产生**字段级错误**，绝不静默丢弃；
//   - 编译产物附带报告：能力校验结果、被拒绝的字段、被隔离的字段。
//
// 本包不启动进程、不读文件、不做网络请求；它只产出配置字节与报告。
// 原生配置检查（sing-box check）由 runtime 层负责调用。
package compiler

import (
	"fmt"
	"sort"
	"strings"

	"proxyclient/shared/domain"
)

// Core 标识目标核心。
type Core string

const (
	// CoreSingBox 是本构建唯一发布的内核（ADR-0005）。
	CoreSingBox Core = "sing-box"
)

// Valid 判断核心标识是否受支持。
//
// 单内核下只有一个合法取值。保留 Core 类型与这道校验，是为了让
// 「调用方传了别的核心」在编译层就**显式失败**，而不是被静默当成
// sing-box 去生成一份与请求不符的配置。
func (c Core) Valid() bool {
	return c == CoreSingBox
}

// IssueLevel 是报告条目的严重程度。
type IssueLevel string

const (
	// LevelError 表示该节点无法编译，必须排除在产物之外。
	LevelError IssueLevel = "error"
	// LevelWarning 表示可以编译，但存在需要注意的取舍或限制。
	LevelWarning IssueLevel = "warning"
	// LevelInfo 表示信息性提示。
	LevelInfo IssueLevel = "info"
)

// Issue 是一条编译报告条目。
//
// 每一条都必须能定位到具体节点与具体字段，避免出现「有问题但不知道哪里」的报告。
type Issue struct {
	Level IssueLevel
	// NodeID 是 domain.Node.ID；为空表示与具体节点无关（例如全局设置问题）。
	NodeID string
	// NodeName 便于人阅读，不作为程序判断依据。
	NodeName string
	// FieldPath 例如 "tls.reality.publicKey"、"transport.type"。
	FieldPath string
	// MessageKey 是稳定的消息键，供 i18n 使用（R10）。
	MessageKey string
	// Message 是兜底的中文说明；正式界面文案由 i18n 资源提供。
	Message string
}

func (i Issue) String() string {
	loc := i.FieldPath
	if loc == "" {
		loc = "-"
	}
	name := i.NodeName
	if name == "" {
		name = i.NodeID
	}
	if name == "" {
		return fmt.Sprintf("[%s] %s: %s", i.Level, loc, i.Message)
	}
	return fmt.Sprintf("[%s] %s (%s): %s", i.Level, loc, name, i.Message)
}

// Compile 编译请求中的节点。
//
// 这是调用方通常使用的入口。单内核构建里不再按核心分派，
// 但 Request.Core 仍然保留（IPC 契约里有它）：传进来别的核心必须
// **明确报错**，而不是悄悄改用 sing-box 编出一份与请求不符的配置——
// 那会让「界面选的东西」和「实际生效的东西」不一致，而用户无从发现。
func Compile(req *Request) ([]byte, *Report, error) {
	if req == nil {
		return nil, nil, fmt.Errorf("nil request")
	}
	if req.Core != CoreSingBox {
		return nil, nil, fmt.Errorf("unsupported core %q; this build only ships sing-box (see ADR-0005)", req.Core)
	}
	return CompileSingBox(req)
}

// Report 是一次编译的完整报告。
type Report struct {
	Core Core
	// InboundPorts 记录实际使用的本地端口，便于 UI 展示与冲突排查。
	InboundPorts map[string]int
	// CompilerNodes 是成功进入产物的节点数。
	CompiledNodes int
	// Issues 按发现顺序记录；调用方应按 Level 分组展示。
	Issues []Issue
}

// HasErrors 表示产物是否不可用于连接。
func (r *Report) HasErrors() bool {
	for _, i := range r.Issues {
		if i.Level == LevelError {
			return true
		}
	}
	return false
}

// Errors 返回所有错误级条目。
func (r *Report) Errors() []Issue { return r.filter(LevelError) }

// Warnings 返回所有警告级条目。
func (r *Report) Warnings() []Issue { return r.filter(LevelWarning) }

func (r *Report) filter(level IssueLevel) []Issue {
	var out []Issue
	for _, i := range r.Issues {
		if i.Level == level {
			out = append(out, i)
		}
	}
	return out
}

func (r *Report) add(level IssueLevel, node *domain.Node, fieldPath, messageKey, message string) {
	issue := Issue{Level: level, FieldPath: fieldPath, MessageKey: messageKey, Message: message}
	if node != nil {
		issue.NodeID = node.ID
		issue.NodeName = node.Name
	}
	r.Issues = append(r.Issues, issue)
}

// Errorf 追加一条错误级条目。
func (r *Report) Errorf(node *domain.Node, fieldPath, messageKey, format string, args ...any) {
	r.add(LevelError, node, fieldPath, messageKey, fmt.Sprintf(format, args...))
}

// Warnf 追加一条警告级条目。
func (r *Report) Warnf(node *domain.Node, fieldPath, messageKey, format string, args ...any) {
	r.add(LevelWarning, node, fieldPath, messageKey, fmt.Sprintf(format, args...))
}

// Infof 追加一条信息级条目。
func (r *Report) Infof(node *domain.Node, fieldPath, messageKey, format string, args ...any) {
	r.add(LevelInfo, node, fieldPath, messageKey, fmt.Sprintf(format, args...))
}

// InboundOptions 描述本地入口。
//
// 首轮只做本地代理入口；TUN / 系统代理 / PAC 属于接入方式，
// 由 runtime 与 platform 层处理，不在这里生成。
type InboundOptions struct {
	// Listen 通常是 127.0.0.1；allowLanAccess 时才绑定 0.0.0.0。
	Listen string
	// HTTPPort 与 SocksPort 为本地代理端口，必须由调用方做冲突检测。
	HTTPPort  int
	SocksPort int
	// AllowLanAccess 为真时入口监听所有网卡。
	AllowLanAccess bool
}

// OutboundPolicy 是路由策略，对应设计基线里的三维度之一。
type OutboundPolicy string

const (
	// PolicyRule 表示按规则分流。
	PolicyRule OutboundPolicy = "rule"
	// PolicyGlobal 表示全部走代理。
	PolicyGlobal OutboundPolicy = "global"
	// PolicyDirect 表示全部直连。
	PolicyDirect OutboundPolicy = "direct"
)

// Request 是一次编译请求。
type Request struct {
	Core Core
	// Nodes 是候选节点；不在 Supported 协议集合内的节点会产生错误级条目。
	Nodes []*domain.Node
	// SelectedNodeID 是用户选定的节点，将成为默认出口。
	// 为空且策略为 rule/global 时，取第一个可用节点。
	SelectedNodeID string
	Inbound        InboundOptions
	Policy         OutboundPolicy
	// LogLevel 传给核心的日志级别（如 warning / info / debug）。
	LogLevel string
	// Options 是界面上的完整选项模型（路由模式、DNS、TUN、分应用等）。
	//
	// 用指针且**允许为 nil**是刻意的：nil 表示「调用方尚未接入选项」，
	// 此时生成的配置与接入之前逐字节一致。这让整套选项可以逐块接入，
	// 而不必一次性改完所有调用方与既有测试。
	//
	// 非 nil 时，其中某些值与上面的显式字段语义重叠
	// （例如 RouteMode 与 Policy）。重叠处以**显式字段优先**：
	// 它们来自更早的调用约定，而 Options 负责补上显式字段覆盖不到的部分。
	Options *domain.Options
}

// dnsOptions 返回要生成的 DNS 设置；未接入选项时为 nil，
// 此时不会生成 dns 段。
func (r *Request) dnsOptions() *domain.DNSSettings {
	if r.Options == nil {
		return nil
	}
	return &r.Options.DNS
}

// tunOptions 返回 TUN 设置；未接入选项时为 nil，此时不会生成 TUN 入站。
func (r *Request) tunOptions() *domain.TUNSettings {
	if r.Options == nil {
		return nil
	}
	return &r.Options.Inbound.TUN
}

// perAppOptions 返回分应用设置；未接入选项时为 nil。
//
// 它只在 TUN 模式下有意义——系统代理与本地端口都是整机级别的，
// 没有「按应用分流」这一说。因此调用方（TUN 入站的构造）为 nil 时
// 自然就不设包名过滤。
func (r *Request) perAppOptions() *domain.PerApp {
	if r.Options == nil {
		return nil
	}
	return &r.Options.Inbound.PerApp
}

// validate 做与核心无关的请求级校验。
func (r *Request) validate() error {
	if !r.Core.Valid() {
		return fmt.Errorf("unsupported core: %q", r.Core)
	}
	if r.Inbound.Listen == "" {
		return fmt.Errorf("inbound listen address is required")
	}
	if r.Inbound.HTTPPort <= 0 || r.Inbound.HTTPPort > 65535 {
		return fmt.Errorf("invalid http port: %d", r.Inbound.HTTPPort)
	}
	if r.Inbound.SocksPort <= 0 || r.Inbound.SocksPort > 65535 {
		return fmt.Errorf("invalid socks port: %d", r.Inbound.SocksPort)
	}
	if r.Inbound.HTTPPort == r.Inbound.SocksPort {
		return fmt.Errorf("http and socks ports must differ")
	}
	switch r.Policy {
	case PolicyRule, PolicyGlobal, PolicyDirect:
	case "":
		return fmt.Errorf("routing policy is required")
	default:
		return fmt.Errorf("unsupported routing policy: %q", r.Policy)
	}
	return nil
}

// listenAddress 返回入口应绑定的地址。
func (r *Request) listenAddress() string {
	if r.Inbound.AllowLanAccess {
		return "0.0.0.0"
	}
	return r.Inbound.Listen
}

// logLevel 返回规范化后的日志级别。
func (r *Request) logLevel() string {
	if strings.TrimSpace(r.LogLevel) == "" {
		return "warning"
	}
	return strings.TrimSpace(r.LogLevel)
}

// outboundTag 由节点 ID 派生，保证同一节点在多次编译中得到相同的 tag，
// 便于运行期热切换与日志对照。
//
// 它留在 compile.go 而不是具体生成器里：出站 tag 的约定与「用哪个内核
// 生成配置」无关，而 singbox.go 与去重逻辑都要用到它。
func outboundTag(n *domain.Node) string {
	if n == nil {
		return "proxy"
	}
	id := n.ID
	if len(id) > 8 {
		id = id[:8]
	}
	if id == "" {
		return "proxy"
	}
	return "proxy-" + id
}

// dedupeByOutboundTag 移除会产生重复出站 tag 的节点。
//
// 为什么必须做：outboundTag 由节点 ID 派生，而 ID 只覆盖**连接参数**——
// 订阅里「服务器/端口/密码完全相同、只有名字不同」的两条节点很常见
// （同一个入口的不同倍率、不同备注），它们会得到同一个 tag。
// 而 sing-box 遇到重复 tag 会直接拒绝启动，用户看到的错误与真正的原因
// （订阅里有两条重复节点）相距很远，几乎不可能自己定位。
//
// 保留第一个，其余记为**警告**而不是静默丢弃：
// 用户需要知道自己的订阅里有哪些条目没有被编译进去。
func dedupeByOutboundTag(usable []*domain.Node, report *Report) []*domain.Node {
	seen := make(map[string]bool, len(usable))
	out := make([]*domain.Node, 0, len(usable))
	for _, n := range usable {
		tag := outboundTag(n)
		if seen[tag] {
			report.Warnf(n, "node.id", "compiler.duplicate_node",
				"节点 %q 与前面的节点连接参数完全相同（出站 tag 均为 %q），本次编译已跳过它；同时保留两者会导致核心因重复 tag 拒绝启动",
				n.Name, tag)
			continue
		}
		seen[tag] = true
		out = append(out, n)
	}
	return out
}

// selectNode 选出默认出口节点。
func selectNode(req *Request, report *Report, usable []*domain.Node) *domain.Node {
	if req.SelectedNodeID != "" {
		for _, n := range usable {
			if n.ID == req.SelectedNodeID {
				return n
			}
		}
		report.Warnf(nil, "selectedNodeId", "compiler.selected_node_missing",
			"选定的节点 %s 不在可用列表中，已回退到第一个可用节点", req.SelectedNodeID)
	}
	if len(usable) == 0 {
		return nil
	}
	if req.SelectedNodeID == "" {
		report.Infof(usable[0], "selectedNodeId", "compiler.selected_node_defaulted",
			"未指定节点，使用列表中的第一个可用节点")
	}
	return usable[0]
}

// sortIssues 让报告顺序稳定，便于比对与测试。
func sortIssues(issues []Issue) {
	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].Level != issues[j].Level {
			return issues[i].Level < issues[j].Level
		}
		if issues[i].NodeID != issues[j].NodeID {
			return issues[i].NodeID < issues[j].NodeID
		}
		return issues[i].FieldPath < issues[j].FieldPath
	})
}
