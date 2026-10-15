package domain

import "strings"

// 本文件定义客户端的**配置选项模型**——即界面上的每一个开关最终变成的东西。
//
// 设计依据：design/architecture/ui-options.md。
// 该文档逐项列出了要提供哪些选项、取值、默认值，以及每一项对应的
// sing-box 字段。本文件是那份文档在 Go 侧的落地。
//
// 三条原则（与设计文档一致）：
//
//   1. 两种模式共用同一份配置。简易模式是高级模式的**子集视图**，
//      不是另一套配置。各持一份的话很快就会出现「简单模式显示已连接、
//      专业模式显示未连接」——两个模式对同一件事给出不同答案。
//
//   2. 每个选项都对应一个真实的 sing-box 字段。这里不设「只是转成字符串」
//      的伪选项。伪选项的表现是：改了没反应，或者改了之后核心拒绝启动，
//      而错误信息来自核心深处，与用户的操作相距很远。
//
//   3. 默认值必须能直接连上。用户不打开高级模式、不改任何一项，也应当能用。
//      因此默认取「最安全且最可能可用」的那一档，而不是「最强大」的那一档。
//
// 本文件**只定义数据**，不做校验、不生成配置——那两件事分别在
// compiler 包里做。这样做的原因是：模型要能被界面直接序列化，
// 而校验与生成依赖核心的具体格式。混在一起会让界面为了取一个默认值
// 而依赖整个编译器。

// RouteMode 是路由模式，对应界面「简易模式」里的三选一。
//
// 与 Clash 的 mode 取值一一对应（rule / global / direct），
// 这样从 Clash 迁移过来的用户不需要重新理解一套概念。
type RouteMode string

const (
	// RouteRule 按规则分流：命中规则的走规则指定的出口，其余走兜底。
	RouteRule RouteMode = "rule"
	// RouteGlobal 全部走代理，忽略规则。
	RouteGlobal RouteMode = "global"
	// RouteDirect 全部直连，不经过任何代理。
	//
	// 界面选中这一项时必须给出明确提示：它的表现是「连上了但打不开墙外网站」。
	// 那不是故障，是这一项的字面含义。
	RouteDirect RouteMode = "direct"
)

// Valid 报告 m 是否是已知的路由模式。
func (m RouteMode) Valid() bool {
	return m == RouteRule || m == RouteGlobal || m == RouteDirect
}

// PerAppMode 是分应用代理的模式（仅 Android 有意义）。
type PerAppMode string

const (
	// PerAppAll 全部应用都走代理（不生成 include/exclude 列表）。
	PerAppAll PerAppMode = "all"
	// PerAppExclude 所选应用**不**走代理。
	PerAppExclude PerAppMode = "exclude"
	// PerAppInclude 只有所选应用走代理。
	PerAppInclude PerAppMode = "include"
)

// Valid 报告 m 是否是已知的分应用模式。
func (m PerAppMode) Valid() bool { return m == PerAppAll || m == PerAppExclude || m == PerAppInclude }

// PerApp 是分应用代理设置。
//
// Packages 的**含义随 Mode 变化**：排除模式下它是排除名单，
// 仅代理模式下它是白名单。用一个字段承载两种含义是有意的——
// 界面上的选择器与列表是一个整体，拆成两个字段会允许
// 「排除了 3 个应用、同时只代理 2 个应用」这种自相矛盾的状态。
//
// 代价是读代码的人必须同时看 Mode 才能理解 Packages。
// 因此取值时一律用 ExcludePackages / IncludePackages 两个方法，
// 不要直接读字段。
type PerApp struct {
	Mode     PerAppMode `json:"mode"`
	Packages []string   `json:"packages,omitempty"`
}

// ExcludePackages 返回应写入 sing-box 的 exclude_package 列表。
// 只有排除模式才有内容——include 与 exclude 在 sing-box 里是互斥的，
// 同时给出会被核心拒绝。
func (p PerApp) ExcludePackages() []string {
	if p.Mode != PerAppExclude {
		return nil
	}
	return p.Packages
}

// IncludePackages 返回应写入 sing-box 的 include_package 列表。
func (p PerApp) IncludePackages() []string {
	if p.Mode != PerAppInclude {
		return nil
	}
	return p.Packages
}

// DNSSettings 是 DNS 覆写设置，对应 ui-options.md 2.3 节。
type DNSSettings struct {
	// Enabled 关闭时**不应生成 dns 段**，而不是生成一个空的。
	// 空 dns 段会让核心按自己的默认值解析，而用户以为自己关掉了覆写——
	// 「关了但行为变了」比「关不了」更难排查。
	Enabled bool `json:"enabled"`

	// FakeIP 为真时使用 fake-ip 增强模式。
	FakeIP bool `json:"fake_ip"`
	// FakeIPRange 是 fake-ip 网段，仅在 FakeIP 为真时有意义。
	FakeIPRange string `json:"fake_ip_range,omitempty"`

	// Nameservers 是名称服务器列表，按顺序尝试。
	Nameservers []string `json:"nameservers,omitempty"`

	// SplitResolution 为真时按规则分流解析（国内域名用国内 DNS）。
	SplitResolution bool `json:"split_resolution"`

	// FakeIPFilter 是不使用 fake-ip 的域名列表。
	// 局域网、NTP、微信与 QQ 的回调域名必须在这里，
	// 否则它们会拿到假地址而连不上——症状是「某些应用莫名其妙打不开」。
	FakeIPFilter []string `json:"fake_ip_filter,omitempty"`

	// UseHosts 为真时使用系统 hosts。
	UseHosts bool `json:"use_hosts"`

	// IPv6 为真时解析 AAAA 记录。
	//
	// **默认关**。多数代理链路对 IPv6 支持不完整，开着会表现为
	// 「某些网站慢或超时」，而用户很难联想到 IPv6。
	IPv6 bool `json:"ipv6"`
}

// TUNSettings 是 TUN 入站设置，对应 ui-options.md 2.4 节。
type TUNSettings struct {
	Enabled bool `json:"enabled"`
	// Stack 取值 system / gvisor / mixed。
	Stack string `json:"stack,omitempty"`
	// Device 是设备名；留空表示按平台默认。
	Device string `json:"device,omitempty"`
	MTU    int    `json:"mtu,omitempty"`
	// AutoRoute 自动接管系统路由。
	AutoRoute bool `json:"auto_route"`
	// StrictRoute 严格路由。默认关：开着时部分应用会出现
	// 「能解析、连不上」，排查成本高。
	StrictRoute bool `json:"strict_route"`
	// DNSMode 是 TUN 的 DNS 处理方式，取 disabled / native / hijack。
	//
	// **这里曾经写作 `dns_hijack []string`（默认 `["any:53"]`）——那是个
	// 不存在的字段。** 核实依据：src/references/sing-box-testing/option/tun.go
	// 里 TunInboundOptions 只有
	//
	//	DNSMode    string `json:"dns_mode,omitempty" enum:"disabled,native,hijack"`
	//	DNSAddress badoption.Listable[netip.Addr] `json:"dns_address,omitempty"`
	//
	// 没有任何 dns_hijack。写进去会被核心按「未知字段」拒绝启动，
	// 而错误信息来自核心的解析器深处——这正是设计文档批评的伪选项。
	//
	// 原来那个列表想表达的语义（劫持 DNS 到核心自己的 DNS 模块）
	// 恰好就是 dns_mode 的 hijack 取值，因此这一项不需要丢失。
	DNSMode string `json:"dns_mode,omitempty"`
	// DNSAddress 是要通告给系统的 DNS 地址（仅 IP，不含端口）。
	// 留空表示由核心自行决定。
	DNSAddress []string `json:"dns_address,omitempty"`
}

// InboundSettings 是流量接入设置。
type InboundSettings struct {
	// Listen 是监听地址。允许局域网时是 0.0.0.0，否则是 127.0.0.1。
	Listen string `json:"listen"`
	// MixedPort 同时提供 HTTP 与 SOCKS。
	MixedPort int `json:"mixed_port"`
	// AllowLAN 允许局域网内其它设备使用本代理。
	//
	// **默认关**。开启后同一网络下的任何人都能用这个代理出口，
	// 在公共 Wi-Fi 下等于把自己的网络身份分享出去。
	AllowLAN bool `json:"allow_lan"`
	// TrafficEntry 取 system_proxy / tun / local_port。
	TrafficEntry string `json:"traffic_entry"`
	// PerApp 仅 Android 有意义。
	PerApp  PerApp        `json:"per_app"`
	TUN     TUNSettings   `json:"tun"`
	Sniffer SniffSettings `json:"sniffer"`
}

// SniffSettings 是协议嗅探设置。
type SniffSettings struct {
	Enabled bool `json:"enabled"`
	// Protocols 取 tls / http / quic 的子集。
	Protocols []string `json:"protocols,omitempty"`
	// OverrideDestination 用嗅探到的域名覆写连接目的地址。
	//
	// **默认关**。开着会让连接的实际目的地与用户看到的域名不一致，
	// 排查问题时容易误判。
	OverrideDestination bool `json:"override_destination"`
}

// ProxyGroupType 是代理组的类型。
type ProxyGroupType string

const (
	GroupSelect   ProxyGroupType = "select"
	GroupURLTest  ProxyGroupType = "url_test"
	GroupFallback ProxyGroupType = "fallback"
	GroupBalance  ProxyGroupType = "balance"
)

// ProxyGroup 是一个代理组。
//
// 注意「负载均衡」在 sing-box 里**没有直接等价物**（见 ui-options.md
// 第 3 节的已知差异）。选择近似实现时必须在界面上注明
// **不保证会话保持**，而不是让用户以为和 Clash 的 load-balance 一样。
type ProxyGroup struct {
	Name    string         `json:"name"`
	Type    ProxyGroupType `json:"type"`
	Members []string       `json:"members,omitempty"`
	// IncludeAll 为真时包含全部节点。
	IncludeAll bool `json:"include_all"`
	// TestURL 与 TestInterval 仅对 url_test / fallback 有意义。
	TestURL      string `json:"test_url,omitempty"`
	TestInterval int    `json:"test_interval,omitempty"`
	MaxFailures  int    `json:"max_failures,omitempty"`
}

// RuleType 是一条规则的匹配方式。
type RuleType string

const (
	RuleDomain        RuleType = "domain"
	RuleDomainSuffix  RuleType = "domain_suffix"
	RuleDomainKeyword RuleType = "domain_keyword"
	RuleIPCIDR        RuleType = "ip_cidr"
	RuleProcessName   RuleType = "process_name"
	RuleRuleSet       RuleType = "rule_set"
	RulePort          RuleType = "port"
)

// RuleAction 是命中一条规则后做什么。
type RuleAction string

const (
	ActionProxy  RuleAction = "proxy"
	ActionDirect RuleAction = "direct"
	ActionReject RuleAction = "reject"
	// ActionGroup 走指定的代理组；GroupName 必须同时给出。
	ActionGroup RuleAction = "group"
)

// Rule 是一条分流规则。
//
// **切片顺序即优先级**。因此 Rules 是切片而不是 map——
// 用 map 会丢掉顺序，而顺序错了的后果是「某条规则永远不生效」，
// 用户看不出是顺序问题。
type Rule struct {
	Type  RuleType `json:"type"`
	Value string   `json:"value"`
	// Action 为 ActionGroup 时，GroupName 必须非空。
	Action    RuleAction `json:"action"`
	GroupName string     `json:"group_name,omitempty"`
}

// RuleSet 是一个规则集。
type RuleSet struct {
	Name string `json:"name"`
	// URL 为空表示内置（本地）规则集。
	URL string `json:"url,omitempty"`
	// UpdateInterval 是更新间隔（小时）。
	UpdateInterval int `json:"update_interval,omitempty"`
	// Format 取 source / binary。**必须与核心期望的格式一致**：
	// mihomo 的 .mrs/.yaml 与 sing-box 的 .srs 不通用，
	// 混用会加载失败（见 ui-options.md 第 3 节）。
	//
	// 留空时按 URL 后缀推断（.srs → binary，.json → source），
	// 这与核心的行为一致（见 sing-box-testing 的
	// option/rule_set.go 里 ruleSetDefaultFormat）。推断不出来时
	// 编译器会明确报告，而不是猜一个——猜错的后果是加载失败，
	// 而错误信息来自核心的加载器，与「这个规则集填了什么」相距很远。
	//
	// 需要这个字段而不是一律留空，是因为**有一些下载链接没有后缀**
	// （面板或 CDN 直接把内容放在一个无后缀的路径上），
	// 此时只能由用户显式指定。
	Format string `json:"format,omitempty"`
}

// RuleSetFormatSource 与 RuleSetFormatBinary 是 sing-box 接受的两个
// rule_set.format 取值（源码依据：option/rule_set.go 的
// `Format string \`json:"format,omitempty" enum:"source,binary"\``）。
const (
	RuleSetFormatSource = "source"
	RuleSetFormatBinary = "binary"
)

// NormalizeRuleSetFormat 判断显式给出的格式是否受支持。
//
// 返回的第二个值为 false 表示**这个值不能原样写进配置**。
// 调用方应当据此报错，而不是把它照抄下去：核心读到未知的 format
// 会以 "unknown rule-set format" 拒绝启动，而那句话不会告诉用户
// 他填的是哪个值、应该填什么。
func NormalizeRuleSetFormat(f string) (string, bool) {
	switch f {
	case "":
		// 空是合法的：交给核心按后缀推断。
		return "", true
	case RuleSetFormatSource, RuleSetFormatBinary:
		return f, true
	default:
		return "", false
	}
}

// InferRuleSetFormat 按 URL 后缀推断格式，与 sing-box 的
// ruleSetDefaultFormat 保持一致（源码依据同上）。
//
// 只认 .srs 与 .json 两种后缀。其余后缀返回空串——**不是**猜一个，
// 而是让调用方报一条「需要显式指定格式」的警告。
// 猜错的表现是核心拒绝加载该规则集，而用户改的却是 URL。
func InferRuleSetFormat(rawURL string) string {
	// 去掉查询串与片段：CDN 常把签名挂在 ?token=... 上，
	// 直接取后缀会拿到 "srs?token=..."，什么也匹配不上。
	path := rawURL
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	switch {
	case strings.HasSuffix(strings.ToLower(path), ".srs"):
		return RuleSetFormatBinary
	case strings.HasSuffix(strings.ToLower(path), ".json"):
		return RuleSetFormatSource
	default:
		return ""
	}
}

// SubscriptionSettings 是订阅相关设置。
type SubscriptionSettings struct {
	URL string `json:"url,omitempty"`
	// AutoUpdate 自动更新订阅。
	AutoUpdate bool `json:"auto_update"`
	// UpdateInterval 更新间隔（小时）。
	UpdateInterval int `json:"update_interval,omitempty"`
	// UpdateViaProxy 更新订阅时走代理。
	// 面板本身可能需要代理才能访问，因此默认开。
	UpdateViaProxy bool `json:"update_via_proxy"`
	// NamePrefix 多订阅合并时的节点名前缀。
	NamePrefix string `json:"name_prefix,omitempty"`
}

// AppSettings 是应用行为设置。
type AppSettings struct {
	AutoStart          bool   `json:"auto_start"`
	StartMinimized     bool   `json:"start_minimized"`
	AutoConnectOnStart bool   `json:"auto_connect_on_start"`
	Language           string `json:"language,omitempty"`
	Theme              string `json:"theme,omitempty"`
	LogLevel           string `json:"log_level,omitempty"`
	LogBufferLines     int    `json:"log_buffer_lines,omitempty"`
}

// Options 是完整的客户端选项。
//
// 这是界面读写的**唯一**对象：简易模式改它、高级模式也改它，
// 只是各自暴露的字段不同。任何「简易模式专用字段」都会破坏这条性质。
type Options struct {
	RouteMode RouteMode `json:"route_mode"`

	Groups      []ProxyGroup `json:"groups,omitempty"`
	Rules       []Rule       `json:"rules,omitempty"`
	RuleSets    []RuleSet    `json:"rule_sets,omitempty"`
	FinalAction RuleAction   `json:"final_action"`

	DNS     DNSSettings          `json:"dns"`
	Inbound InboundSettings      `json:"inbound"`
	Sub     SubscriptionSettings `json:"subscription"`
	App     AppSettings          `json:"app"`

	// DelayTestURL 与 DelayTestTimeout 是节点延迟测试的参数。
	DelayTestURL     string `json:"delay_test_url,omitempty"`
	DelayTestTimeout int    `json:"delay_test_timeout,omitempty"`
}

// DefaultOptions 返回可以直接连上的那套默认值。
//
// 每个默认值都对应设计文档里的一条判断，不是随手取的：
//
//   - **FinalAction 是「走代理」而不是「直连」**：回落直连会在规则没覆盖时
//     **静默泄漏真实流量**——用户以为在走代理，实际上没有。
//     宁可多走一点代理，也不要悄悄漏流量。
//   - **IPv6 关**：多数代理链路支持不完整，症状是「某些网站慢」，难以联想。
//   - **AllowLAN 关**：公共 Wi-Fi 下等于把出口分享出去。
//   - **StrictRoute 关**、**OverrideDestination 关**：两者都会让实际行为
//     与用户看到的配置不一致，排查成本高。
//   - **DNS 的 FakeIP 开**：规则分流的常见配置依赖它；
//     但它同时带来 fake-ip 过滤必须配全的要求，因此过滤列表给了非空默认值。
func DefaultOptions() Options {
	return Options{
		RouteMode:   RouteRule,
		FinalAction: ActionProxy,
		DNS: DNSSettings{
			Enabled:     true,
			FakeIP:      true,
			FakeIPRange: "198.18.0.1/16",
			Nameservers: []string{
				"https://doh.pub/dns-query",
				"https://dns.alidns.com/dns-query",
				"114.114.114.114",
			},
			SplitResolution: true,
			// 这些域名不能走 fake-ip：局域网设备发现、时间同步、
			// 以及微信与 QQ 的回调。它们拿到假地址就再也连不上，
			// 而症状是「某些应用莫名其妙打不开」。
			FakeIPFilter: []string{
				"localhost.ptlogin2.qq.com",
				"localhost.sec.qq.com",
				"localhost.work.weixin.qq.com",
				"+.weixin.qq.com",
				"+.wechat.com",
				"time.windows.com",
				"+.msftconnecttest.com",
				"+.msftncsi.com",
				"+.lan",
				"+.ntp.org",
			},
			UseHosts: true,
			IPv6:     false,
		},
		Inbound: InboundSettings{
			Listen:       "127.0.0.1",
			MixedPort:    7890,
			AllowLAN:     false,
			TrafficEntry: "system_proxy",
			PerApp:       PerApp{Mode: PerAppAll},
			TUN: TUNSettings{
				Enabled:     false,
				Stack:       "mixed",
				MTU:         1500,
				AutoRoute:   true,
				StrictRoute: false,
				// 劫持 DNS 到核心自己的 DNS 模块。
				DNSMode: "hijack",
			},
			Sniffer: SniffSettings{
				Enabled:             true,
				Protocols:           []string{"tls", "http", "quic"},
				OverrideDestination: false,
			},
		},
		Sub: SubscriptionSettings{
			AutoUpdate:     true,
			UpdateInterval: 6,
			UpdateViaProxy: true,
		},
		App: AppSettings{
			AutoStart:          false,
			StartMinimized:     false,
			AutoConnectOnStart: false,
			LogLevel:           "warning",
			LogBufferLines:     1000,
		},
		DelayTestURL:     "https://www.gstatic.com/generate_204",
		DelayTestTimeout: 5000,
	}
}
