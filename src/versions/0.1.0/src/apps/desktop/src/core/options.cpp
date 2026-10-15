#include "core/options.hpp"

#include <optional>
#include <string>
#include <utility>
#include <vector>

#include "core/json_util.hpp"

namespace pcx::domain {

using json::GetBool;
using json::GetInt;
using json::GetObject;
using json::GetString;

namespace {

// GetStringArray 取字符串数组；缺失或类型不符时返回空数组。
//
// 非字符串元素跳过而不是报错：服务端将来可能加入结构化元素，
// 那时旧客户端应当仍然能用。
std::vector<std::string> GetStringArray(const Json& obj, const char* key) {
    std::vector<std::string> out;
    if (!obj.is_object()) {
        return out;
    }
    const auto it = obj.find(key);
    if (it == obj.end() || !it->is_array()) {
        return out;
    }
    out.reserve(it->size());
    for (const auto& item : *it) {
        if (item.is_string()) {
            out.push_back(item.get<std::string>());
        }
    }
    return out;
}

// NeededString 对应 Go 的 omitempty 字符串字段：仅当非空时写出。
//
// 为什么不能一律写出：空串写回去会**覆盖**服务端的默认值。
// 例如 name_prefix 留空表示「不加前缀」，若写成 ""，
// 服务端可能把它当成显式的前缀值。
void NeededString(Json& out, const char* key, const std::string& value) {
    if (!value.empty()) {
        out[key] = value;
    }
}

// NeededArray 对应 Go 的 omitempty 切片字段。
void NeededArray(Json& out, const char* key,
                 const std::vector<std::string>& values) {
    if (!values.empty()) {
        out[key] = values;
    }
}

// NeededInt 对应 Go 的 omitempty 整数字段。
//
// 注意 0 在 Go 的 omitempty 里等同于未设置——DNS 的 mtu、更新间隔、
// 失败次数上限都是这个语义，因此这里照做而不是写成显式的 0。
void NeededInt(Json& out, const char* key, int value) {
    if (value != 0) {
        out[key] = value;
    }
}

}  // namespace

// ---------------------------------------------------------------------------
// PerApp
// ---------------------------------------------------------------------------

PerApp PerApp::Parse(const Json& source) {
    PerApp out;
    // 默认 all：取值缺失时按「不过滤任何应用」处理，而不是按空名单。
    // 空名单配合 exclude 模式的含义是「一个都不排除」，与 all 等价，
    // 但配合 include 模式就是「一个都不代理」——那是完全相反的行为。
    out.mode = GetString(source, "mode", "all");
    out.packages = GetStringArray(source, "packages");
    return out;
}

Json PerApp::ToJson() const {
    Json out = Json::object();
    out["mode"] = mode;
    NeededArray(out, "packages", packages);
    return out;
}

// ---------------------------------------------------------------------------
// TUNSettings
// ---------------------------------------------------------------------------

TUNSettings TUNSettings::Parse(const Json& source) {
    TUNSettings out;
    out.enabled = GetBool(source, "enabled");
    out.stack = GetString(source, "stack");
    out.device = GetString(source, "device");
    out.mtu = static_cast<int>(GetInt(source, "mtu"));
    out.auto_route = GetBool(source, "auto_route");
    out.strict_route = GetBool(source, "strict_route");
    out.dns_mode = GetString(source, "dns_mode");
    out.dns_address = GetStringArray(source, "dns_address");
    return out;
}

Json TUNSettings::ToJson() const {
    Json out = Json::object();
    out["enabled"] = enabled;
    NeededString(out, "stack", stack);
    NeededString(out, "device", device);
    NeededInt(out, "mtu", mtu);
    out["auto_route"] = auto_route;
    out["strict_route"] = strict_route;
    NeededString(out, "dns_mode", dns_mode);
    NeededArray(out, "dns_address", dns_address);
    return out;
}

// ---------------------------------------------------------------------------
// SniffSettings
// ---------------------------------------------------------------------------

SniffSettings SniffSettings::Parse(const Json& source) {
    SniffSettings out;
    out.enabled = GetBool(source, "enabled");
    out.protocols = GetStringArray(source, "protocols");
    out.override_destination = GetBool(source, "override_destination");
    return out;
}

Json SniffSettings::ToJson() const {
    Json out = Json::object();
    out["enabled"] = enabled;
    NeededArray(out, "protocols", protocols);
    out["override_destination"] = override_destination;
    return out;
}

// ---------------------------------------------------------------------------
// InboundSettings
// ---------------------------------------------------------------------------

InboundSettings InboundSettings::Parse(const Json& source) {
    InboundSettings out;
    out.listen = GetString(source, "listen");
    out.mixed_port = static_cast<int>(GetInt(source, "mixed_port"));
    out.allow_lan = GetBool(source, "allow_lan");
    out.traffic_entry = GetString(source, "traffic_entry");
    out.per_app = PerApp::Parse(GetObject(source, "per_app"));
    out.tun = TUNSettings::Parse(GetObject(source, "tun"));
    out.sniffer = SniffSettings::Parse(GetObject(source, "sniffer"));
    return out;
}

Json InboundSettings::ToJson() const {
    Json out = Json::object();
    out["listen"] = listen;
    out["mixed_port"] = mixed_port;
    out["allow_lan"] = allow_lan;
    out["traffic_entry"] = traffic_entry;
    out["per_app"] = per_app.ToJson();
    out["tun"] = tun.ToJson();
    out["sniffer"] = sniffer.ToJson();
    return out;
}

// ---------------------------------------------------------------------------
// DNSSettings
// ---------------------------------------------------------------------------

DNSSettings DNSSettings::Parse(const Json& source) {
    DNSSettings out;
    out.enabled = GetBool(source, "enabled");
    out.fake_ip = GetBool(source, "fake_ip");
    out.fake_ip_range = GetString(source, "fake_ip_range");
    out.nameservers = GetStringArray(source, "nameservers");
    out.split_resolution = GetBool(source, "split_resolution");
    out.fake_ip_filter = GetStringArray(source, "fake_ip_filter");
    out.use_hosts = GetBool(source, "use_hosts");
    out.ipv6 = GetBool(source, "ipv6");
    return out;
}

Json DNSSettings::ToJson() const {
    Json out = Json::object();
    out["enabled"] = enabled;
    out["fake_ip"] = fake_ip;
    NeededString(out, "fake_ip_range", fake_ip_range);
    NeededArray(out, "nameservers", nameservers);
    out["split_resolution"] = split_resolution;
    NeededArray(out, "fake_ip_filter", fake_ip_filter);
    out["use_hosts"] = use_hosts;
    out["ipv6"] = ipv6;
    return out;
}

// ---------------------------------------------------------------------------
// ProxyGroup
// ---------------------------------------------------------------------------

ProxyGroup ProxyGroup::Parse(const Json& source) {
    ProxyGroup out;
    out.name = GetString(source, "name");
    out.type = GetString(source, "type");
    out.members = GetStringArray(source, "members");
    out.include_all = GetBool(source, "include_all");
    out.test_url = GetString(source, "test_url");
    out.test_interval = static_cast<int>(GetInt(source, "test_interval"));
    out.max_failures = static_cast<int>(GetInt(source, "max_failures"));
    return out;
}

Json ProxyGroup::ToJson() const {
    Json out = Json::object();
    out["name"] = name;
    out["type"] = type;
    NeededArray(out, "members", members);
    out["include_all"] = include_all;
    NeededString(out, "test_url", test_url);
    NeededInt(out, "test_interval", test_interval);
    NeededInt(out, "max_failures", max_failures);
    return out;
}

bool ProxyGroup::SupportsTestURL() const {
    // 只有自动选择与故障转移会周期性地测速；手动选择与负载均衡
    // 拿到测试地址也不会用，展示它等于给用户一个改不动的开关。
    return type == group_type::kURLTest || type == group_type::kFallback;
}

bool ProxyGroup::Approximated() const {
    // 负载均衡在 sing-box 里没有直接等价物（见 ui-options.md 第 3 节）。
    // 界面必须据此注明「不保证会话保持」，而不是让用户以为和 Clash 一样。
    return type == group_type::kBalance;
}

// ---------------------------------------------------------------------------
// Rule
// ---------------------------------------------------------------------------

Rule Rule::Parse(const Json& source) {
    Rule out;
    out.type = GetString(source, "type");
    out.value = GetString(source, "value");
    out.action = GetString(source, "action");
    out.group_name = GetString(source, "group_name");
    return out;
}

Json Rule::ToJson() const {
    Json out = Json::object();
    out["type"] = type;
    out["value"] = value;
    out["action"] = action;
    NeededString(out, "group_name", group_name);
    return out;
}

// ---------------------------------------------------------------------------
// RuleSet
// ---------------------------------------------------------------------------

RuleSet RuleSet::Parse(const Json& source) {
    RuleSet out;
    out.name = GetString(source, "name");
    out.url = GetString(source, "url");
    out.update_interval = static_cast<int>(GetInt(source, "update_interval"));
    out.format = GetString(source, "format");
    return out;
}

Json RuleSet::ToJson() const {
    Json out = Json::object();
    out["name"] = name;
    NeededString(out, "url", url);
    NeededInt(out, "update_interval", update_interval);
    NeededString(out, "format", format);
    return out;
}

// ---------------------------------------------------------------------------
// SubscriptionSettings
// ---------------------------------------------------------------------------

SubscriptionSettings SubscriptionSettings::Parse(const Json& source) {
    SubscriptionSettings out;
    out.url = GetString(source, "url");
    out.auto_update = GetBool(source, "auto_update");
    out.update_interval = static_cast<int>(GetInt(source, "update_interval"));
    out.update_via_proxy = GetBool(source, "update_via_proxy");
    out.name_prefix = GetString(source, "name_prefix");
    return out;
}

Json SubscriptionSettings::ToJson() const {
    Json out = Json::object();
    NeededString(out, "url", url);
    out["auto_update"] = auto_update;
    NeededInt(out, "update_interval", update_interval);
    out["update_via_proxy"] = update_via_proxy;
    NeededString(out, "name_prefix", name_prefix);
    return out;
}

// ---------------------------------------------------------------------------
// AppSettings
// ---------------------------------------------------------------------------

AppSettings AppSettings::Parse(const Json& source) {
    AppSettings out;
    out.auto_start = GetBool(source, "auto_start");
    out.start_minimized = GetBool(source, "start_minimized");
    out.auto_connect_on_start = GetBool(source, "auto_connect_on_start");
    out.language = GetString(source, "language");
    out.theme = GetString(source, "theme");
    out.log_level = GetString(source, "log_level");
    out.log_buffer_lines = static_cast<int>(GetInt(source, "log_buffer_lines"));
    return out;
}

Json AppSettings::ToJson() const {
    Json out = Json::object();
    out["auto_start"] = auto_start;
    out["start_minimized"] = start_minimized;
    out["auto_connect_on_start"] = auto_connect_on_start;
    NeededString(out, "language", language);
    NeededString(out, "theme", theme);
    NeededString(out, "log_level", log_level);
    NeededInt(out, "log_buffer_lines", log_buffer_lines);
    return out;
}

// ---------------------------------------------------------------------------
// Options
// ---------------------------------------------------------------------------

std::optional<Options> ParseOptions(const Json& source) {
    if (!source.is_object()) {
        return std::nullopt;
    }

    Options out;
    out.route_mode = GetString(source, "route_mode", route_mode::kRule);
    out.final_action = GetString(source, "final_action", rule_action::kProxy);
    out.delay_test_url = GetString(source, "delay_test_url");
    out.delay_test_timeout = static_cast<int>(GetInt(source, "delay_test_timeout"));

    if (const auto it = source.find("groups");
        it != source.end() && it->is_array()) {
        out.groups.reserve(it->size());
        for (const auto& item : *it) {
            if (item.is_object()) {
                out.groups.push_back(ProxyGroup::Parse(item));
            }
        }
    }
    if (const auto it = source.find("rules"); it != source.end() && it->is_array()) {
        out.rules.reserve(it->size());
        for (const auto& item : *it) {
            if (item.is_object()) {
                out.rules.push_back(Rule::Parse(item));
            }
        }
    }
    if (const auto it = source.find("rule_sets");
        it != source.end() && it->is_array()) {
        out.rule_sets.reserve(it->size());
        for (const auto& item : *it) {
            if (item.is_object()) {
                out.rule_sets.push_back(RuleSet::Parse(item));
            }
        }
    }

    out.dns = DNSSettings::Parse(GetObject(source, "dns"));
    out.inbound = InboundSettings::Parse(GetObject(source, "inbound"));
    out.subscription = SubscriptionSettings::Parse(GetObject(source, "subscription"));
    out.app = AppSettings::Parse(GetObject(source, "app"));
    return out;
}

Json SerializeOptions(const Options& options) {
    Json out = Json::object();
    out["route_mode"] = options.route_mode;

    // 三个列表**始终写出**，即使是空的。
    //
    // 与别的字段相反：这里的空数组表达的是明确的意图——
    // 「用户把规则清空了」。省略它会让服务端保留旧规则，
    // 而用户看到的是自己刚删掉的规则又回来了。
    Json groups = Json::array();
    for (const auto& group : options.groups) {
        groups.push_back(group.ToJson());
    }
    out["groups"] = std::move(groups);

    Json rules = Json::array();
    for (const auto& rule : options.rules) {
        rules.push_back(rule.ToJson());
    }
    out["rules"] = std::move(rules);

    Json rule_sets = Json::array();
    for (const auto& set : options.rule_sets) {
        rule_sets.push_back(set.ToJson());
    }
    out["rule_sets"] = std::move(rule_sets);

    out["final_action"] = options.final_action;
    out["dns"] = options.dns.ToJson();
    out["inbound"] = options.inbound.ToJson();
    out["subscription"] = options.subscription.ToJson();
    out["app"] = options.app.ToJson();
    NeededString(out, "delay_test_url", options.delay_test_url);
    NeededInt(out, "delay_test_timeout", options.delay_test_timeout);
    return out;
}

Options DefaultOptions() {
    // 刻意只给「能直接连上」的那一档，理由与 Go 侧 domain.DefaultOptions
    // 的各项注释一致。这里不复制全部默认值（DNS 服务器列表、fake-ip 过滤
    // 列表等），因为真正的默认值由服务端给出；这份实现只保证界面在
    // 服务端尚未回应时不会显示一套自相矛盾的空值。
    Options out;
    out.route_mode = route_mode::kRule;
    out.final_action = rule_action::kProxy;
    out.dns.enabled = true;
    out.dns.fake_ip = true;
    out.inbound.listen = "127.0.0.1";
    out.inbound.mixed_port = 7890;
    out.inbound.traffic_entry = "system_proxy";
    out.inbound.tun.stack = tunnel_stack::kMixed;
    out.inbound.tun.mtu = 1500;
    out.inbound.tun.auto_route = true;
    out.inbound.tun.dns_mode = tun_dns_mode::kHijack;
    out.inbound.sniffer.enabled = true;
    out.inbound.sniffer.protocols = {sniff_protocol::kTLS, sniff_protocol::kHTTP,
                                     sniff_protocol::kQUIC};
    out.subscription.auto_update = true;
    out.subscription.update_interval = 6;
    out.subscription.update_via_proxy = true;
    out.app.log_level = "warning";
    out.app.log_buffer_lines = 1000;
    out.delay_test_url = "https://www.gstatic.com/generate_204";
    out.delay_test_timeout = 5000;
    return out;
}

}  // namespace pcx::domain
