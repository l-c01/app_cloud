// 客户端选项模型 / client option model (C++ mirror).
//
// **这是 Go 端 shared/domain/options.go 的镜像。**
// 与 ipc_protocol.hpp 一样，JSON 字段名必须与 Go 侧逐字一致；改动任一侧
// 都要同时改另一侧与 protocol.go 的 OptionsResult 说明。
//
// 为什么单独一个文件而不是塞进 ipc_protocol.hpp：
//   - 选项模型描述的是**配置本身**，不是 IPC 报文。它将来会被编译器与
//     本地缓存用到，而那些地方不该为了一个结构体去依赖整个 IPC 协议；
//   - protocol.go 用 json.RawMessage 承载它，特地在协议层回避了复制字段，
//     如果 C++ 侧又把字段塞回协议定义里，那条约定就白写了。
//
// 但**镜像三处**（Go domain / Go protocol / C++）本身是负担，
// 因此这里刻意保持 1:1 直译：不重命名、不合并、不做「界面专用」的包装字段。
// 重命名的代价是每次 Go 侧改字段名都得先翻译一遍，而漏翻译的表现是
// 「界面改了没反应」，正是设计文档批评过的伪选项。
//
// 已知的有意留白（不是遗漏）：
//   - Groups / Rules / RuleSets 是「读进来展示、原样写回去」的列表。
//     界面不提供图形化编辑器，但**必须原样保留**：丢掉它们等于把用户
//     手写的规则清空。为此只在 UI 层做只读展示，序列化时逐字段回填。
#pragma once

#include <cstdint>
#include <optional>
#include <string>
#include <vector>

#include <nlohmann/json.hpp>

namespace pcx::domain {

using Json = nlohmann::json;

// ---------------------------------------------------------------------------
// 取值常量（与 Go 侧一致）
// ---------------------------------------------------------------------------

// RouteMode 是路由模式，对应简易模式的三选一。
//
// 取值与 Clash 的 mode 一一对应，迁移过来的用户不需要重新理解一套概念。
namespace route_mode {
inline constexpr const char* kRule = "rule";
inline constexpr const char* kGlobal = "global";
inline constexpr const char* kDirect = "direct";
}  // namespace route_mode

// ProxyGroupType 是代理组类型。
//
// 注意 balance 在 sing-box 里**没有直接等价物**：界面选中它时必须注明
// 「不保证会话保持」，否则用户会以为和 Clash 的 load-balance 一样。
namespace group_type {
inline constexpr const char* kSelect = "select";
inline constexpr const char* kURLTest = "url_test";
inline constexpr const char* kFallback = "fallback";
inline constexpr const char* kBalance = "balance";
}  // namespace group_type

// RuleAction 是命中一条规则后做什么。
namespace rule_action {
inline constexpr const char* kProxy = "proxy";
inline constexpr const char* kDirect = "direct";
inline constexpr const char* kReject = "reject";
inline constexpr const char* kGroup = "group";
}  // namespace rule_action

// tunnel_stack 是 TUN 栈取值。
namespace tunnel_stack {
inline constexpr const char* kSystem = "system";
inline constexpr const char* kGvisor = "gvisor";
inline constexpr const char* kMixed = "mixed";
}  // namespace tunnel_stack

// tun_dns_mode 是 TUN 的 DNS 处理方式。
//
// 这三个取值来自 sing-box 的 option/tun.go（dns_mode 枚举），
// **不是**曾经写过的 dns_hijack——那个字段不存在，写进去核心会拒绝启动。
namespace tun_dns_mode {
inline constexpr const char* kDisabled = "disabled";
inline constexpr const char* kNative = "native";
inline constexpr const char* kHijack = "hijack";
}  // namespace tun_dns_mode

// sniff_protocol 是嗅探协议列表的取值。
namespace sniff_protocol {
inline constexpr const char* kTLS = "tls";
inline constexpr const char* kHTTP = "http";
inline constexpr const char* kQUIC = "quic";
}  // namespace sniff_protocol

// rule_set_format 是规则集格式，只认 sing-box 的两种。
//
// mihomo 的 .mrs/.yaml 与 sing-box 的 .srs 不通用，混用会加载失败，
// 因此这里不给「其它格式留空让用户填」的余地。
namespace rule_set_format {
inline constexpr const char* kSource = "source";
inline constexpr const char* kBinary = "binary";
}  // namespace rule_set_format

// ---------------------------------------------------------------------------
// 结构
// ---------------------------------------------------------------------------

// PerApp 是分应用代理设置（仅 Android 有意义）。
//
// Packages 的含义随 Mode 变化：排除模式下是排除名单，仅代理模式下是白名单。
// 用一个字段承载两种含义是有意的——拆成两个会允许
// 「排除了 3 个应用、同时只代理 2 个应用」这种自相矛盾的状态。
//
// 桌面端**不适用**：系统代理与 TUN 都是全机的。这里保留字段只是为了
// 能原样回写服务端返回的模型，界面不展示、不修改。
struct PerApp {
    std::string mode = "all";
    std::vector<std::string> packages;

    static PerApp Parse(const Json& source);
    Json ToJson() const;
};

// TUNSettings 是 TUN 入站设置。
struct TUNSettings {
    bool enabled = false;
    std::string stack;   // system / gvisor / mixed
    std::string device;  // 留空表示按平台默认
    int mtu = 0;
    bool auto_route = false;
    // strict_route 默认关：开着时部分应用会「能解析、连不上」，排查成本高。
    bool strict_route = false;
    // dns_mode 是 disabled / native / hijack；dns_address 只接受 IP，不含端口。
    std::string dns_mode;
    std::vector<std::string> dns_address;

    static TUNSettings Parse(const Json& source);
    Json ToJson() const;
};

// SniffSettings 是协议嗅探设置。
//
// **一句话：入站层只有一个开关，协议列表是路由层的东西。**
// override_destination 默认关：它会让连接的实际目的地与用户看到的域名不一致，
// 排查问题时容易误判。
struct SniffSettings {
    bool enabled = false;
    std::vector<std::string> protocols;  // tls / http / quic 的子集
    bool override_destination = false;

    static SniffSettings Parse(const Json& source);
    Json ToJson() const;
};

// InboundSettings 是流量接入设置。
struct InboundSettings {
    std::string listen;
    int mixed_port = 0;
    // allow_lan 默认关：开启后同一网络下任何人都能用这个代理出口，
    // 在公共 Wi-Fi 下等于把自己的网络身份分享出去。
    bool allow_lan = false;
    std::string traffic_entry;  // system_proxy / tun / local_port
    PerApp per_app;
    TUNSettings tun;
    SniffSettings sniffer;

    static InboundSettings Parse(const Json& source);
    Json ToJson() const;
};

// DNSSettings 是 DNS 覆写设置。
struct DNSSettings {
    // enabled 关闭时**不应生成 dns 段**，而不是生成一个空的。
    // 空 dns 段会让核心按自己的默认值解析，而用户以为自己关掉了覆写——
    // 「关了但行为变了」比「关不了」更难排查。
    bool enabled = false;
    bool fake_ip = false;
    std::string fake_ip_range;
    std::vector<std::string> nameservers;
    bool split_resolution = false;
    // fake_ip_filter 里必须有局域网、NTP、微信与 QQ 的回调域名，
    // 否则它们会拿到假地址而连不上，症状是「某些应用莫名其妙打不开」。
    std::vector<std::string> fake_ip_filter;
    bool use_hosts = false;
    // ipv6 默认关：多数代理链路对 IPv6 支持不完整，
    // 开着会表现为「某些网站慢或超时」，而用户很难联想到 IPv6。
    bool ipv6 = false;

    static DNSSettings Parse(const Json& source);
    Json ToJson() const;
};

// ProxyGroup 是一个代理组。
struct ProxyGroup {
    std::string name;
    std::string type;  // select / url_test / fallback / balance
    std::vector<std::string> members;
    bool include_all = false;
    std::string test_url;
    int test_interval = 0;
    int max_failures = 0;

    static ProxyGroup Parse(const Json& source);
    Json ToJson() const;

    // SupportsTestURL 报告 test_url / test_interval 对该类型是否有意义。
    bool SupportsTestURL() const;
    // Approximated 报告该类型是否是 sing-box 里没有直接等价物的近似实现。
    bool Approximated() const;
};

// Rule 是一条分流规则。
//
// **切片顺序即优先级**，因此界面必须保留顺序，不能按名字排序。
// 顺序错了的后果是「某条规则永远不生效」，而用户看不出是顺序问题。
struct Rule {
    std::string type;
    std::string value;
    std::string action;
    std::string group_name;

    static Rule Parse(const Json& source);
    Json ToJson() const;
};

// RuleSet 是一个规则集。
struct RuleSet {
    std::string name;
    std::string url;  // 为空表示内置（本地）规则集
    int update_interval = 0;
    // format 取 source / binary，留空时由核心按 URL 后缀推断。
    std::string format;

    static RuleSet Parse(const Json& source);
    Json ToJson() const;
};

// SubscriptionSettings 是订阅相关设置。
struct SubscriptionSettings {
    std::string url;
    bool auto_update = false;
    int update_interval = 0;  // 小时
    // update_via_proxy 默认开：面板本身可能需要代理才能访问。
    bool update_via_proxy = false;
    std::string name_prefix;

    static SubscriptionSettings Parse(const Json& source);
    Json ToJson() const;
};

// AppSettings 是应用行为设置。
//
// 这一组**不属于配置生成**：它们不产生任何 sing-box 字段，
// 是应用自身的行为。放在这里是因为服务端把它们和别的选项放在同一个模型里，
// 界面必须能原样回写，否则保存别的选项会把这几个值清掉。
struct AppSettings {
    bool auto_start = false;
    bool start_minimized = false;
    bool auto_connect_on_start = false;
    std::string language;
    std::string theme;
    std::string log_level;
    int log_buffer_lines = 0;

    static AppSettings Parse(const Json& source);
    Json ToJson() const;
};

// Options 是完整的客户端选项。
//
// 这是界面读写的**唯一**对象：简易模式改它、专业模式也改它，
// 只是各自暴露的字段不同。任何「简易模式专用字段」都会破坏这条性质。
struct Options {
    std::string route_mode = route_mode::kRule;

    // Groups / Rules / RuleSets 的顺序必须原样保留（见文件头说明）。
    std::vector<ProxyGroup> groups;
    std::vector<Rule> rules;
    std::vector<RuleSet> rule_sets;
    // final_action 默认「走代理」：回落直连会在规则没覆盖时**静默泄漏真实流量**。
    std::string final_action = rule_action::kProxy;

    DNSSettings dns;
    InboundSettings inbound;
    SubscriptionSettings subscription;
    AppSettings app;

    std::string delay_test_url;
    int delay_test_timeout = 0;
};

// ---------------------------------------------------------------------------
// 序列化
// ---------------------------------------------------------------------------

// ParseOptions 解析服务端返回的 options 对象。
//
// source 不是对象时返回 std::nullopt：**不做尽力解析**。
// 一个空的 Options 会让界面显示一整套错误的取值，比报错更难排查。
std::optional<Options> ParseOptions(const Json& source);

// SerializeOptions 生成 options 对象。
//
// 与 Go 的 omitempty 对齐：空字符串与空列表不写出去，避免用零值
// 覆盖服务端的默认值。布尔与数字**一律写出**——它们没有「未设置」的表示，
// 省略会被服务端解析成 false / 0。
Json SerializeOptions(const Options& options);

// DefaultOptions 返回可以直接连上的那套默认值。
//
// 只在「服务端还没给过 Options」时作为界面初值使用；真正的权威默认值
// 在 Go 侧（domain.DefaultOptions），这里不试图与它保持逐字节一致。
Options DefaultOptions();

}  // namespace pcx::domain
