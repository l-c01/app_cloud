#include "ui/options_panel.hpp"

#include <wx/notebook.h>
#include <wx/statline.h>

#include <cstddef>
#include <cstdlib>
#include <string>
#include <utility>
#include <vector>

#include "core/options.hpp"
#include "ui/ui_format.hpp"

namespace pcx::ui {

namespace {

// kListPlaceholder 是只读列表为空时显示的一行说明。
//
// 显示空列表会让用户以为「这里坏了」或者「我的规则丢了」，
// 而真实情况是这个模型里本来就没有条目。
constexpr const char* kListPlaceholder = "-";

// IndexOf 返回取值在列表中的下标；找不到返回 -1。
int IndexOf(const std::vector<std::string>& values, const std::string& wanted) {
    for (std::size_t i = 0; i < values.size(); ++i) {
        if (values[i] == wanted) {
            return static_cast<int>(i);
        }
    }
    // -1 表示未找到：调用方据此**保持原选择**。
    // 回退到下标 0 会把「服务端给了一个我不认识的取值」变成
    // 「界面自作主张改了用户的配置」。
    return -1;
}

// JoinLines 用换行拼接字符串列表，供多行输入框使用。
std::string JoinLines(const std::vector<std::string>& values) {
    std::string out;
    for (const auto& value : values) {
        if (!out.empty()) {
            out += "\n";
        }
        out += value;
    }
    return out;
}

// SplitLines 按行拆分，去掉空行与首尾空白。
//
// 去空白是必要的：用户从网页复制列表时经常带上行首缩进，
// 而带空格的域名在规则里匹配不上任何东西。
std::vector<std::string> SplitLines(const std::string& text) {
    std::vector<std::string> out;
    std::string current;
    const auto flush = [&out, &current]() {
        std::size_t begin = 0;
        std::size_t end = current.size();
        while (begin < end && (current[begin] == ' ' || current[begin] == '\t' ||
                               current[begin] == '\r')) {
            ++begin;
        }
        while (end > begin && (current[end - 1] == ' ' || current[end - 1] == '\t' ||
                               current[end - 1] == '\r')) {
            --end;
        }
        if (end > begin) {
            out.push_back(current.substr(begin, end - begin));
        }
        current.clear();
    };
    for (const char ch : text) {
        if (ch == '\n') {
            flush();
            continue;
        }
        current.push_back(ch);
    }
    flush();
    return out;
}

// GetIntText 读取整数输入框；空或非数字时返回 fallback。
//
// 不报错是因为这些框在用户编辑过程中会短暂地是空的或只有负号，
// 每次都弹错会让人没法打字。真正的合法性检查在服务端。
int GetIntText(const wxTextCtrl* control, int fallback) {
    if (control == nullptr) {
        return fallback;
    }
    const std::string text = std::string(control->GetValue().utf8_str());
    if (text.empty()) {
        return fallback;
    }
    const long value = std::strtol(text.c_str(), nullptr, 10);
    return static_cast<int>(value);
}

std::string GetText(const wxTextCtrl* control) {
    if (control == nullptr) {
        return {};
    }
    return std::string(control->GetValue().utf8_str());
}

void SetText(wxTextCtrl* control, const std::string& value) {
    if (control != nullptr) {
        control->SetValue(wxString::FromUTF8(value));
    }
}

std::string GetChoice(const wxChoice* control,
                      const std::vector<std::string>& values, const std::string& fallback) {
    if (control == nullptr) {
        return fallback;
    }
    const int index = control->GetSelection();
    if (index < 0 || static_cast<std::size_t>(index) >= values.size()) {
        return fallback;
    }
    return values[static_cast<std::size_t>(index)];
}

// AppendTranslated 按「固定前缀 + 取值」的规则填充一个选择框。
//
// 取值来自线上契约，显示名来自文案；两者必须一一对应，
// 因此下标只能靠同时追加来保证。
void AppendTranslated(wxChoice* choice, const std::vector<std::string>& values,
                      const std::string& key_prefix, const app::AppContext& context) {
    for (const auto& value : values) {
        choice->Append(wxString::FromUTF8(context.T(key_prefix + value)));
    }
}

}  // namespace

OptionsPanel::OptionsPanel(wxWindow* parent, app::AppContext& context)
    : wxPanel(parent, wxID_ANY), context_(context) {
    BuildLayout();
    Update();
}

wxWindow* OptionsPanel::BuildProxiesPage(wxWindow* parent) {
    auto* page = new wxPanel(parent, wxID_ANY);
    auto* root = new wxBoxSizer(wxVERTICAL);

    auto* grid = new wxFlexGridSizer(2, 8, 12);
    grid->AddGrowableCol(1, 1);

    auto* url_label = new wxStaticText(page, wxID_ANY, wxEmptyString);
    url_label->SetName(UiKey("options.delay_test_url"));
    delay_test_url_ = new wxTextCtrl(page, wxID_ANY, wxEmptyString);
    grid->Add(url_label, 0, wxALIGN_CENTER_VERTICAL);
    grid->Add(delay_test_url_, 1, wxEXPAND);

    auto* timeout_label = new wxStaticText(page, wxID_ANY, wxEmptyString);
    timeout_label->SetName(UiKey("options.delay_test_timeout"));
    delay_test_timeout_ = new wxTextCtrl(page, wxID_ANY, wxEmptyString);
    grid->Add(timeout_label, 0, wxALIGN_CENTER_VERTICAL);
    grid->Add(delay_test_timeout_, 1, wxEXPAND);

    root->Add(grid, 0, wxEXPAND | wxALL, 8);

    // ---- 代理组（只读） ----
    auto* groups_label = new wxStaticText(page, wxID_ANY, wxEmptyString);
    groups_label->SetName(UiKey("options.groups"));
    root->Add(groups_label, 0, wxLEFT | wxRIGHT | wxTOP, 8);

    groups_list_ = new wxListBox(page, wxID_ANY, wxDefaultPosition, wxSize(420, 180),
                                 wxArrayString(), wxLB_SINGLE);
    root->Add(groups_list_, 1, wxEXPAND | wxLEFT | wxRIGHT, 8);

    // 只读的理由必须写在界面上，否则用户会以为是自己没找到编辑入口。
    auto* groups_note = new wxStaticText(page, wxID_ANY, wxEmptyString);
    groups_note->SetName(UiKey("options.groups_readonly"));
    root->Add(groups_note, 0, wxEXPAND | wxALL, 8);

    page->SetSizer(root);
    return page;
}

wxWindow* OptionsPanel::BuildRulesPage(wxWindow* parent) {
    auto* page = new wxPanel(parent, wxID_ANY);
    auto* root = new wxBoxSizer(wxVERTICAL);

    auto* grid = new wxFlexGridSizer(2, 8, 12);
    grid->AddGrowableCol(1, 1);

    auto* mode_label = new wxStaticText(page, wxID_ANY, wxEmptyString);
    mode_label->SetName("ui.label.route_mode");
    route_mode_choice_ = new wxChoice(page, wxID_ANY);
    route_mode_values_ = {domain::route_mode::kRule, domain::route_mode::kGlobal,
                          domain::route_mode::kDirect};
    AppendTranslated(route_mode_choice_, route_mode_values_, "ui.policy.", context_);
    grid->Add(mode_label, 0, wxALIGN_CENTER_VERTICAL);
    grid->Add(route_mode_choice_, 1, wxEXPAND);

    auto* action_label = new wxStaticText(page, wxID_ANY, wxEmptyString);
    action_label->SetName(UiKey("options.final_action"));
    final_action_choice_ = new wxChoice(page, wxID_ANY);
    // 兜底动作只有「走代理」与「直连」两种：拒绝会让全部流量断掉，
    // 那不是「兜底」该做的事。
    action_values_ = {domain::rule_action::kProxy, domain::rule_action::kDirect};
    AppendTranslated(final_action_choice_, action_values_, "ui.action_kind.", context_);
    grid->Add(action_label, 0, wxALIGN_CENTER_VERTICAL);
    grid->Add(final_action_choice_, 1, wxEXPAND);

    root->Add(grid, 0, wxEXPAND | wxALL, 8);

    auto* final_note = new wxStaticText(page, wxID_ANY, wxEmptyString);
    final_note->SetName(UiKey("options.final_action_note"));
    root->Add(final_note, 0, wxEXPAND | wxLEFT | wxRIGHT, 8);

    auto* rules_label = new wxStaticText(page, wxID_ANY, wxEmptyString);
    rules_label->SetName(UiKey("options.rules"));
    root->Add(rules_label, 0, wxLEFT | wxRIGHT | wxTOP, 8);
    rules_list_ = new wxListBox(page, wxID_ANY, wxDefaultPosition, wxSize(420, 160),
                                wxArrayString(), wxLB_SINGLE);
    root->Add(rules_list_, 1, wxEXPAND | wxLEFT | wxRIGHT, 8);

    auto* rules_note = new wxStaticText(page, wxID_ANY, wxEmptyString);
    rules_note->SetName(UiKey("options.rules_readonly"));
    root->Add(rules_note, 0, wxEXPAND | wxALL, 8);

    auto* sets_label = new wxStaticText(page, wxID_ANY, wxEmptyString);
    sets_label->SetName(UiKey("options.rule_sets"));
    root->Add(sets_label, 0, wxLEFT | wxRIGHT, 8);
    rule_sets_list_ = new wxListBox(page, wxID_ANY, wxDefaultPosition, wxSize(420, 120),
                                    wxArrayString(), wxLB_SINGLE);
    root->Add(rule_sets_list_, 1, wxEXPAND | wxLEFT | wxRIGHT | wxBOTTOM, 8);

    page->SetSizer(root);
    return page;
}

wxWindow* OptionsPanel::BuildDnsPage(wxWindow* parent) {
    auto* page = new wxPanel(parent, wxID_ANY);
    auto* root = new wxBoxSizer(wxVERTICAL);

    dns_enabled_ = new wxCheckBox(page, wxID_ANY, wxEmptyString);
    dns_enabled_->SetName(UiKey("options.dns_enabled"));
    root->Add(dns_enabled_, 0, wxALL, 8);

    auto* grid = new wxFlexGridSizer(2, 8, 12);
    grid->AddGrowableCol(1, 1);

    auto* fake_label = new wxStaticText(page, wxID_ANY, wxEmptyString);
    fake_label->SetName(UiKey("options.dns_fake_ip_range"));
    dns_fake_ip_range_ = new wxTextCtrl(page, wxID_ANY, wxEmptyString);
    grid->Add(fake_label, 0, wxALIGN_CENTER_VERTICAL);
    grid->Add(dns_fake_ip_range_, 1, wxEXPAND);

    auto* servers_label = new wxStaticText(page, wxID_ANY, wxEmptyString);
    servers_label->SetName(UiKey("options.dns_nameservers"));
    dns_nameservers_ = new wxTextCtrl(page, wxID_ANY, wxEmptyString, wxDefaultPosition,
                                      wxSize(360, 80), wxTE_MULTILINE);
    grid->Add(servers_label, 0, wxALIGN_TOP);
    grid->Add(dns_nameservers_, 1, wxEXPAND);

    auto* filter_label = new wxStaticText(page, wxID_ANY, wxEmptyString);
    filter_label->SetName(UiKey("options.dns_fake_ip_filter"));
    dns_fake_ip_filter_ = new wxTextCtrl(page, wxID_ANY, wxEmptyString, wxDefaultPosition,
                                         wxSize(360, 100), wxTE_MULTILINE);
    grid->Add(filter_label, 0, wxALIGN_TOP);
    grid->Add(dns_fake_ip_filter_, 1, wxEXPAND);

    root->Add(grid, 0, wxEXPAND | wxLEFT | wxRIGHT, 8);

    dns_fake_ip_ = new wxCheckBox(page, wxID_ANY, wxEmptyString);
    dns_fake_ip_->SetName(UiKey("options.dns_fake_ip"));
    root->Add(dns_fake_ip_, 0, wxALL, 8);

    dns_split_ = new wxCheckBox(page, wxID_ANY, wxEmptyString);
    dns_split_->SetName(UiKey("options.dns_split"));
    root->Add(dns_split_, 0, wxLEFT | wxRIGHT | wxBOTTOM, 8);

    dns_use_hosts_ = new wxCheckBox(page, wxID_ANY, wxEmptyString);
    dns_use_hosts_->SetName(UiKey("options.dns_use_hosts"));
    root->Add(dns_use_hosts_, 0, wxLEFT | wxRIGHT | wxBOTTOM, 8);

    dns_ipv6_ = new wxCheckBox(page, wxID_ANY, wxEmptyString);
    dns_ipv6_->SetName(UiKey("options.dns_ipv6"));
    root->Add(dns_ipv6_, 0, wxLEFT | wxRIGHT | wxBOTTOM, 8);

    // IPv6 默认关这一条要讲清后果，否则用户打开后遇到
    // 「某些网站慢或超时」不会联想到它。
    auto* ipv6_note = new wxStaticText(page, wxID_ANY, wxEmptyString);
    ipv6_note->SetName(UiKey("options.dns_ipv6_note"));
    root->Add(ipv6_note, 0, wxEXPAND | wxLEFT | wxRIGHT | wxBOTTOM, 8);

    auto* filter_note = new wxStaticText(page, wxID_ANY, wxEmptyString);
    filter_note->SetName(UiKey("options.dns_filter_note"));
    root->Add(filter_note, 0, wxEXPAND | wxLEFT | wxRIGHT | wxBOTTOM, 8);

    page->SetSizer(root);
    return page;
}

wxWindow* OptionsPanel::BuildInboundPage(wxWindow* parent) {
    auto* page = new wxPanel(parent, wxID_ANY);
    auto* root = new wxBoxSizer(wxVERTICAL);

    auto* grid = new wxFlexGridSizer(2, 8, 12);
    grid->AddGrowableCol(1, 1);

    auto* entry_label = new wxStaticText(page, wxID_ANY, wxEmptyString);
    entry_label->SetName("ui.label.network_mode");
    inbound_entry_choice_ = new wxChoice(page, wxID_ANY);
    entry_values_ = {app::traffic_entry::kSystemProxy, app::traffic_entry::kPac,
                     app::traffic_entry::kLocalProxy, app::traffic_entry::kTun};
    AppendTranslated(inbound_entry_choice_, entry_values_, "ui.entry.", context_);
    grid->Add(entry_label, 0, wxALIGN_CENTER_VERTICAL);
    grid->Add(inbound_entry_choice_, 1, wxEXPAND);

    auto* listen_label = new wxStaticText(page, wxID_ANY, wxEmptyString);
    listen_label->SetName(UiKey("options.inbound_listen"));
    inbound_listen_ = new wxTextCtrl(page, wxID_ANY, wxEmptyString);
    grid->Add(listen_label, 0, wxALIGN_CENTER_VERTICAL);
    grid->Add(inbound_listen_, 1, wxEXPAND);

    auto* port_label = new wxStaticText(page, wxID_ANY, wxEmptyString);
    port_label->SetName(UiKey("options.mixed_port"));
    inbound_port_ = new wxTextCtrl(page, wxID_ANY, wxEmptyString);
    grid->Add(port_label, 0, wxALIGN_CENTER_VERTICAL);
    grid->Add(inbound_port_, 1, wxEXPAND);

    root->Add(grid, 0, wxEXPAND | wxALL, 8);

    inbound_allow_lan_ = new wxCheckBox(page, wxID_ANY, wxEmptyString);
    inbound_allow_lan_->SetName(UiKey("options.allow_lan"));
    root->Add(inbound_allow_lan_, 0, wxLEFT | wxRIGHT, 8);

    // 允许局域网连接默认关，且开启时必须有明确警告：
    // 同一网络下任何人都能用这个出口，公共 Wi-Fi 下等于把网络身份分享出去。
    auto* lan_note = new wxStaticText(page, wxID_ANY, wxEmptyString);
    lan_note->SetName(UiKey("options.allow_lan_note"));
    lan_note->SetForegroundColour(wxColour(160, 90, 0));
    root->Add(lan_note, 0, wxEXPAND | wxLEFT | wxRIGHT | wxBOTTOM, 8);

    root->Add(new wxStaticLine(page), 0, wxEXPAND | wxALL, 8);

    tun_enabled_ = new wxCheckBox(page, wxID_ANY, wxEmptyString);
    tun_enabled_->SetName(UiKey("options.tun_enabled"));
    root->Add(tun_enabled_, 0, wxLEFT | wxRIGHT | wxBOTTOM, 8);

    auto* tun_grid = new wxFlexGridSizer(2, 8, 12);
    tun_grid->AddGrowableCol(1, 1);

    auto* stack_label = new wxStaticText(page, wxID_ANY, wxEmptyString);
    stack_label->SetName(UiKey("options.tun_stack"));
    tun_stack_choice_ = new wxChoice(page, wxID_ANY);
    stack_values_ = {domain::tunnel_stack::kSystem, domain::tunnel_stack::kGvisor,
                     domain::tunnel_stack::kMixed};
    AppendTranslated(tun_stack_choice_, stack_values_, "ui.stack.", context_);
    tun_grid->Add(stack_label, 0, wxALIGN_CENTER_VERTICAL);
    tun_grid->Add(tun_stack_choice_, 1, wxEXPAND);

    auto* device_label = new wxStaticText(page, wxID_ANY, wxEmptyString);
    device_label->SetName(UiKey("options.tun_device"));
    tun_device_ = new wxTextCtrl(page, wxID_ANY, wxEmptyString);
    tun_grid->Add(device_label, 0, wxALIGN_CENTER_VERTICAL);
    tun_grid->Add(tun_device_, 1, wxEXPAND);

    auto* mtu_label = new wxStaticText(page, wxID_ANY, wxEmptyString);
    mtu_label->SetName(UiKey("options.tun_mtu"));
    tun_mtu_ = new wxTextCtrl(page, wxID_ANY, wxEmptyString);
    tun_grid->Add(mtu_label, 0, wxALIGN_CENTER_VERTICAL);
    tun_grid->Add(tun_mtu_, 1, wxEXPAND);

    auto* dns_mode_label = new wxStaticText(page, wxID_ANY, wxEmptyString);
    dns_mode_label->SetName(UiKey("options.tun_dns_mode"));
    tun_dns_mode_choice_ = new wxChoice(page, wxID_ANY);
    dns_mode_values_ = {domain::tun_dns_mode::kDisabled, domain::tun_dns_mode::kNative,
                        domain::tun_dns_mode::kHijack};
    AppendTranslated(tun_dns_mode_choice_, dns_mode_values_, "ui.tun_dns.", context_);
    tun_grid->Add(dns_mode_label, 0, wxALIGN_CENTER_VERTICAL);
    tun_grid->Add(tun_dns_mode_choice_, 1, wxEXPAND);

    auto* dns_addr_label = new wxStaticText(page, wxID_ANY, wxEmptyString);
    dns_addr_label->SetName(UiKey("options.tun_dns_address"));
    tun_dns_address_ = new wxTextCtrl(page, wxID_ANY, wxEmptyString);
    tun_grid->Add(dns_addr_label, 0, wxALIGN_CENTER_VERTICAL);
    tun_grid->Add(tun_dns_address_, 1, wxEXPAND);

    root->Add(tun_grid, 0, wxEXPAND | wxLEFT | wxRIGHT, 8);

    // DNS 通告地址只接受 IP 不含端口，写错会被核心拒绝启动，
    // 而错误信息来自核心深处，与用户的操作相距很远。
    auto* dns_addr_note = new wxStaticText(page, wxID_ANY, wxEmptyString);
    dns_addr_note->SetName(UiKey("options.tun_dns_address_note"));
    root->Add(dns_addr_note, 0, wxEXPAND | wxLEFT | wxRIGHT | wxTOP, 8);

    tun_auto_route_ = new wxCheckBox(page, wxID_ANY, wxEmptyString);
    tun_auto_route_->SetName(UiKey("options.tun_auto_route"));
    root->Add(tun_auto_route_, 0, wxALL, 8);

    tun_strict_route_ = new wxCheckBox(page, wxID_ANY, wxEmptyString);
    tun_strict_route_->SetName(UiKey("options.tun_strict_route"));
    root->Add(tun_strict_route_, 0, wxLEFT | wxRIGHT | wxBOTTOM, 8);

    auto* strict_note = new wxStaticText(page, wxID_ANY, wxEmptyString);
    strict_note->SetName(UiKey("options.tun_strict_note"));
    root->Add(strict_note, 0, wxEXPAND | wxLEFT | wxRIGHT | wxBOTTOM, 8);

    // 分应用代理只在 Android 有意义。桌面端必须**说明它不存在**，
    // 而不是给一个改了不生效的开关：系统代理与 TUN 都是全机的。
    auto* per_app_note = new wxStaticText(page, wxID_ANY, wxEmptyString);
    per_app_note->SetName(UiKey("options.per_app_desktop_note"));
    root->Add(per_app_note, 0, wxEXPAND | wxLEFT | wxRIGHT | wxBOTTOM, 8);

    page->SetSizer(root);
    return page;
}

wxWindow* OptionsPanel::BuildSniffingPage(wxWindow* parent) {
    auto* page = new wxPanel(parent, wxID_ANY);
    auto* root = new wxBoxSizer(wxVERTICAL);

    sniff_enabled_ = new wxCheckBox(page, wxID_ANY, wxEmptyString);
    sniff_enabled_->SetName(UiKey("options.sniff_enabled"));
    root->Add(sniff_enabled_, 0, wxALL, 8);

    sniff_override_ = new wxCheckBox(page, wxID_ANY, wxEmptyString);
    sniff_override_->SetName(UiKey("options.sniff_override"));
    root->Add(sniff_override_, 0, wxLEFT | wxRIGHT | wxBOTTOM, 8);

    auto* override_note = new wxStaticText(page, wxID_ANY, wxEmptyString);
    override_note->SetName(UiKey("options.sniff_override_note"));
    root->Add(override_note, 0, wxEXPAND | wxLEFT | wxRIGHT | wxBOTTOM, 8);

    root->Add(new wxStaticLine(page), 0, wxEXPAND | wxALL, 8);

    sniff_tls_ = new wxCheckBox(page, wxID_ANY, wxEmptyString);
    sniff_tls_->SetName(UiKey("options.sniff_tls"));
    sniff_http_ = new wxCheckBox(page, wxID_ANY, wxEmptyString);
    sniff_http_->SetName(UiKey("options.sniff_http"));
    sniff_quic_ = new wxCheckBox(page, wxID_ANY, wxEmptyString);
    sniff_quic_->SetName(UiKey("options.sniff_quic"));
    root->Add(sniff_tls_, 0, wxLEFT | wxRIGHT | wxBOTTOM, 8);
    root->Add(sniff_http_, 0, wxLEFT | wxRIGHT | wxBOTTOM, 8);
    root->Add(sniff_quic_, 0, wxLEFT | wxRIGHT | wxBOTTOM, 8);

    // **必须说明协议列表目前不生效。**
    //
    // 依据：入站层只有 sniff（布尔）与 sniff_override_destination；
    // 协议列表住在路由动作里，需要单独一条 action: "sniff" 的规则。
    // 生成器目前不生成那条规则，因此这三项存得下去、但不会起作用。
    // 不说明的话，它正好是 ui-options.md 第 0 节批评的伪选项。
    sniff_note_ = new wxStaticText(page, wxID_ANY, wxEmptyString);
    sniff_note_->SetName(UiKey("options.sniff_protocols_note"));
    sniff_note_->SetForegroundColour(wxColour(160, 90, 0));
    root->Add(sniff_note_, 0, wxEXPAND | wxALL, 8);

    page->SetSizer(root);
    return page;
}

wxWindow* OptionsPanel::BuildSubscriptionPage(wxWindow* parent) {
    auto* page = new wxPanel(parent, wxID_ANY);
    auto* root = new wxBoxSizer(wxVERTICAL);

    auto* grid = new wxFlexGridSizer(2, 8, 12);
    grid->AddGrowableCol(1, 1);

    auto* url_label = new wxStaticText(page, wxID_ANY, wxEmptyString);
    url_label->SetName(UiKey("options.sub_url"));
    sub_url_ = new wxTextCtrl(page, wxID_ANY, wxEmptyString);
    grid->Add(url_label, 0, wxALIGN_CENTER_VERTICAL);
    grid->Add(sub_url_, 1, wxEXPAND);

    auto* interval_label = new wxStaticText(page, wxID_ANY, wxEmptyString);
    interval_label->SetName(UiKey("options.sub_interval"));
    sub_interval_ = new wxTextCtrl(page, wxID_ANY, wxEmptyString);
    grid->Add(interval_label, 0, wxALIGN_CENTER_VERTICAL);
    grid->Add(sub_interval_, 1, wxEXPAND);

    auto* prefix_label = new wxStaticText(page, wxID_ANY, wxEmptyString);
    prefix_label->SetName(UiKey("options.sub_name_prefix"));
    sub_name_prefix_ = new wxTextCtrl(page, wxID_ANY, wxEmptyString);
    grid->Add(prefix_label, 0, wxALIGN_CENTER_VERTICAL);
    grid->Add(sub_name_prefix_, 1, wxEXPAND);

    root->Add(grid, 0, wxEXPAND | wxALL, 8);

    // 订阅链接本身就是凭据（查询串里的 token 就是访问权限）。
    // 必须让用户知道它会离开本机、进入选项模型。
    auto* url_note = new wxStaticText(page, wxID_ANY, wxEmptyString);
    url_note->SetName(UiKey("options.sub_url_note"));
    root->Add(url_note, 0, wxEXPAND | wxLEFT | wxRIGHT | wxBOTTOM, 8);

    sub_auto_update_ = new wxCheckBox(page, wxID_ANY, wxEmptyString);
    sub_auto_update_->SetName(UiKey("options.sub_auto_update"));
    root->Add(sub_auto_update_, 0, wxLEFT | wxRIGHT | wxBOTTOM, 8);

    sub_via_proxy_ = new wxCheckBox(page, wxID_ANY, wxEmptyString);
    sub_via_proxy_->SetName(UiKey("options.sub_via_proxy"));
    root->Add(sub_via_proxy_, 0, wxLEFT | wxRIGHT | wxBOTTOM, 8);

    auto* via_note = new wxStaticText(page, wxID_ANY, wxEmptyString);
    via_note->SetName(UiKey("options.sub_via_proxy_note"));
    root->Add(via_note, 0, wxEXPAND | wxLEFT | wxRIGHT | wxBOTTOM, 8);

    page->SetSizer(root);
    return page;
}

wxWindow* OptionsPanel::BuildAppPage(wxWindow* parent) {
    auto* page = new wxPanel(parent, wxID_ANY);
    auto* root = new wxBoxSizer(wxVERTICAL);

    app_auto_start_ = new wxCheckBox(page, wxID_ANY, wxEmptyString);
    app_auto_start_->SetName(UiKey("options.app_auto_start"));
    root->Add(app_auto_start_, 0, wxALL, 8);

    // 「开机自启」不等于「开机接管网络」，用户往往以为是一回事，
    // 因此这里明确写出区别。
    auto* auto_start_note = new wxStaticText(page, wxID_ANY, wxEmptyString);
    auto_start_note->SetName(UiKey("options.app_auto_start_note"));
    root->Add(auto_start_note, 0, wxEXPAND | wxLEFT | wxRIGHT | wxBOTTOM, 8);

    app_start_minimized_ = new wxCheckBox(page, wxID_ANY, wxEmptyString);
    app_start_minimized_->SetName(UiKey("options.app_start_minimized"));
    root->Add(app_start_minimized_, 0, wxLEFT | wxRIGHT | wxBOTTOM, 8);

    app_auto_connect_ = new wxCheckBox(page, wxID_ANY, wxEmptyString);
    app_auto_connect_->SetName(UiKey("options.app_auto_connect"));
    root->Add(app_auto_connect_, 0, wxLEFT | wxRIGHT | wxBOTTOM, 8);

    auto* grid = new wxFlexGridSizer(2, 8, 12);
    grid->AddGrowableCol(1, 1);

    auto* language_label = new wxStaticText(page, wxID_ANY, wxEmptyString);
    language_label->SetName(UiKey("options.app_language"));
    app_language_ = new wxTextCtrl(page, wxID_ANY, wxEmptyString);
    grid->Add(language_label, 0, wxALIGN_CENTER_VERTICAL);
    grid->Add(app_language_, 1, wxEXPAND);

    auto* theme_label = new wxStaticText(page, wxID_ANY, wxEmptyString);
    theme_label->SetName(UiKey("options.app_theme"));
    app_theme_ = new wxTextCtrl(page, wxID_ANY, wxEmptyString);
    grid->Add(theme_label, 0, wxALIGN_CENTER_VERTICAL);
    grid->Add(app_theme_, 1, wxEXPAND);

    auto* log_label = new wxStaticText(page, wxID_ANY, wxEmptyString);
    log_label->SetName(UiKey("options.app_log_level"));
    app_log_level_choice_ = new wxChoice(page, wxID_ANY);
    // 取值与 sing-box 的 log.level 一致（trace/debug/info/warn/error）。
    // 注意核心认的是 warn 而不是 warning，写错核心会拒绝启动。
    log_level_values_ = {"trace", "debug", "info", "warn", "error"};
    AppendTranslated(app_log_level_choice_, log_level_values_, "ui.loglevel.", context_);
    grid->Add(log_label, 0, wxALIGN_CENTER_VERTICAL);
    grid->Add(app_log_level_choice_, 1, wxEXPAND);

    auto* lines_label = new wxStaticText(page, wxID_ANY, wxEmptyString);
    lines_label->SetName(UiKey("options.app_log_buffer"));
    app_log_lines_ = new wxTextCtrl(page, wxID_ANY, wxEmptyString);
    grid->Add(lines_label, 0, wxALIGN_CENTER_VERTICAL);
    grid->Add(app_log_lines_, 1, wxEXPAND);

    root->Add(grid, 0, wxEXPAND | wxLEFT | wxRIGHT, 8);

    // 日志行数只影响界面内保留多少行，不写盘——这一点要说清，
    // 否则用户会以为调大它能保住日志。
    auto* lines_note = new wxStaticText(page, wxID_ANY, wxEmptyString);
    lines_note->SetName(UiKey("options.app_log_buffer_note"));
    root->Add(lines_note, 0, wxEXPAND | wxALL, 8);

    page->SetSizer(root);
    return page;
}

void OptionsPanel::BuildLayout() {
    auto* root = new wxBoxSizer(wxVERTICAL);

    auto* header = new wxBoxSizer(wxHORIZONTAL);
    auto* header_column = new wxBoxSizer(wxVERTICAL);
    source_text_ = new wxStaticText(this, wxID_ANY, wxEmptyString);
    {
        wxFont font = source_text_->GetFont();
        font.SetPointSize(font.GetPointSize() + 2);
        font.SetWeight(wxFONTWEIGHT_BOLD);
        source_text_->SetFont(font);
    }
    header_column->Add(source_text_, 0, wxBOTTOM, 2);
    status_text_ = new wxStaticText(this, wxID_ANY, wxEmptyString);
    header_column->Add(status_text_, 0);
    header->Add(header_column, 1, wxALIGN_CENTER_VERTICAL | wxLEFT, 8);

    reload_button_ = new wxButton(this, wxID_ANY, wxEmptyString);
    save_button_ = new wxButton(this, wxID_ANY, wxEmptyString);
    header->Add(reload_button_, 0, wxALL | wxALIGN_CENTER_VERTICAL, 8);
    header->Add(save_button_, 0, wxALL | wxALIGN_CENTER_VERTICAL, 8);

    root->Add(header, 0, wxEXPAND);

    notebook_ = new wxNotebook(this, wxID_ANY);
    notebook_->AddPage(BuildProxiesPage(notebook_),
                       wxString::FromUTF8(context_.T(UiKey("options.page.proxies"))));
    notebook_->AddPage(BuildRulesPage(notebook_),
                       wxString::FromUTF8(context_.T(UiKey("options.page.rules"))));
    notebook_->AddPage(BuildDnsPage(notebook_),
                       wxString::FromUTF8(context_.T(UiKey("options.page.dns"))));
    notebook_->AddPage(BuildInboundPage(notebook_),
                       wxString::FromUTF8(context_.T(UiKey("options.page.inbound"))));
    notebook_->AddPage(BuildSniffingPage(notebook_),
                       wxString::FromUTF8(context_.T(UiKey("options.page.sniffing"))));
    notebook_->AddPage(BuildSubscriptionPage(notebook_),
                       wxString::FromUTF8(context_.T(UiKey("options.page.subscription"))));
    notebook_->AddPage(BuildAppPage(notebook_),
                       wxString::FromUTF8(context_.T(UiKey("options.page.app"))));

    root->Add(notebook_, 1, wxEXPAND | wxALL, 8);

    // 保存按钮在页签下方**再放一个**：选项分散在七个页签上，
    // 用户在最后一个页签改完后不该被要求滚回顶部。
    auto* footer = new wxBoxSizer(wxHORIZONTAL);
    footer->AddStretchSpacer(1);
    save_bottom_ = new wxButton(this, wxID_ANY, wxEmptyString);
    footer->Add(save_bottom_, 0, wxALL, 8);
    root->Add(footer, 0, wxEXPAND);

    SetSizer(root);

    save_button_->Bind(wxEVT_BUTTON, [this](wxCommandEvent&) {
        if (save_handler_ != nullptr) {
            save_handler_(Collect());
        }
    });
    save_bottom_->Bind(wxEVT_BUTTON, [this](wxCommandEvent&) {
        if (save_handler_ != nullptr) {
            save_handler_(Collect());
        }
    });
    reload_button_->Bind(wxEVT_BUTTON, [this](wxCommandEvent&) {
        if (reload_handler_ != nullptr) {
            reload_handler_();
        }
    });
}

void OptionsPanel::SetSaveHandler(std::function<void(const domain::Options&)> handler) {
    save_handler_ = std::move(handler);
}

void OptionsPanel::SetReloadHandler(std::function<void()> handler) {
    reload_handler_ = std::move(handler);
}

void OptionsPanel::SetStatus(const std::string& text) {
    status_text_->SetLabel(wxString::FromUTF8(text));
    Layout();
}

void OptionsPanel::Reload() {
    // 只有读到了服务端的取值才覆盖界面。
    // 用一个本地的默认值覆盖会把用户正在编辑的内容吃掉。
    if (context_.has_options) {
        Apply(context_.options);
        SetStatus(context_.T(UiKey("options.loaded")));
    }
}

domain::Options OptionsPanel::Collect() const {
    // 从上下文里的模型出发：只读列表（代理组 / 规则 / 规则集）
    // 与界面不暴露的字段因此被原样带走。
    //
    // 从零开始构造是本模块最容易犯的错：那样一次保存就会把
    // 用户手写的规则与规则集全部清空，而界面里没有任何地方显示它们消失了。
    domain::Options options = context_.has_options ? context_.options
                                                   : domain::DefaultOptions();

    options.delay_test_url = GetText(delay_test_url_);
    options.delay_test_timeout = GetIntText(delay_test_timeout_, options.delay_test_timeout);

    options.route_mode =
        GetChoice(route_mode_choice_, route_mode_values_, options.route_mode);
    options.final_action =
        GetChoice(final_action_choice_, action_values_, options.final_action);

    options.dns.enabled = dns_enabled_->GetValue();
    options.dns.fake_ip = dns_fake_ip_->GetValue();
    options.dns.fake_ip_range = GetText(dns_fake_ip_range_);
    options.dns.nameservers = SplitLines(GetText(dns_nameservers_));
    options.dns.split_resolution = dns_split_->GetValue();
    options.dns.fake_ip_filter = SplitLines(GetText(dns_fake_ip_filter_));
    options.dns.use_hosts = dns_use_hosts_->GetValue();
    options.dns.ipv6 = dns_ipv6_->GetValue();

    options.inbound.traffic_entry =
        GetChoice(inbound_entry_choice_, entry_values_, options.inbound.traffic_entry);
    options.inbound.listen = GetText(inbound_listen_);
    options.inbound.mixed_port = GetIntText(inbound_port_, options.inbound.mixed_port);
    options.inbound.allow_lan = inbound_allow_lan_->GetValue();

    options.inbound.tun.enabled = tun_enabled_->GetValue();
    options.inbound.tun.stack =
        GetChoice(tun_stack_choice_, stack_values_, options.inbound.tun.stack);
    options.inbound.tun.device = GetText(tun_device_);
    options.inbound.tun.mtu = GetIntText(tun_mtu_, options.inbound.tun.mtu);
    options.inbound.tun.dns_mode =
        GetChoice(tun_dns_mode_choice_, dns_mode_values_, options.inbound.tun.dns_mode);
    options.inbound.tun.dns_address = SplitLines(GetText(tun_dns_address_));
    options.inbound.tun.auto_route = tun_auto_route_->GetValue();
    options.inbound.tun.strict_route = tun_strict_route_->GetValue();

    // 分应用代理桌面端不适用：**原样保留**服务端给的取值，
    // 而不是重置成 all——那会在服务端为 Android 侧准备时把设置抹掉。
    options.inbound.sniffer.enabled = sniff_enabled_->GetValue();
    options.inbound.sniffer.override_destination = sniff_override_->GetValue();
    std::vector<std::string> protocols;
    if (sniff_tls_->GetValue()) {
        protocols.push_back(domain::sniff_protocol::kTLS);
    }
    if (sniff_http_->GetValue()) {
        protocols.push_back(domain::sniff_protocol::kHTTP);
    }
    if (sniff_quic_->GetValue()) {
        protocols.push_back(domain::sniff_protocol::kQUIC);
    }
    options.inbound.sniffer.protocols = protocols;

    options.subscription.url = GetText(sub_url_);
    options.subscription.auto_update = sub_auto_update_->GetValue();
    options.subscription.update_interval =
        GetIntText(sub_interval_, options.subscription.update_interval);
    options.subscription.update_via_proxy = sub_via_proxy_->GetValue();
    options.subscription.name_prefix = GetText(sub_name_prefix_);

    options.app.auto_start = app_auto_start_->GetValue();
    options.app.start_minimized = app_start_minimized_->GetValue();
    options.app.auto_connect_on_start = app_auto_connect_->GetValue();
    options.app.language = GetText(app_language_);
    options.app.theme = GetText(app_theme_);
    options.app.log_level =
        GetChoice(app_log_level_choice_, log_level_values_, options.app.log_level);
    options.app.log_buffer_lines =
        GetIntText(app_log_lines_, options.app.log_buffer_lines);

    return options;
}

void OptionsPanel::Apply(const domain::Options& options) {
    SetText(delay_test_url_, options.delay_test_url);
    SetText(delay_test_timeout_, std::to_string(options.delay_test_timeout));

    // ---- 代理组（只读） ----
    groups_list_->Clear();
    for (const auto& group : options.groups) {
        std::string label = group.name + "  [" + group.type + "]  x" +
                            std::to_string(group.members.size());
        if (group.include_all) {
            label += " +all";
        }
        // 近似实现在列表行上就标注出来：进去看详情才发现「不保证会话保持」
        // 的用户，已经按 Clash 的经验配完了一遍。
        if (group.Approximated()) {
            label += "  (!)";
        }
        groups_list_->Append(wxString::FromUTF8(label));
    }
    if (options.groups.empty()) {
        groups_list_->Append(wxString::FromUTF8(kListPlaceholder));
    }

    // ---- 规则 ----
    const int mode_index = IndexOf(route_mode_values_, options.route_mode);
    if (mode_index >= 0) {
        route_mode_choice_->SetSelection(mode_index);
    }
    const int action_index = IndexOf(action_values_, options.final_action);
    if (action_index >= 0) {
        final_action_choice_->SetSelection(action_index);
    }

    rules_list_->Clear();
    // 序号必须显示：列表顺序即优先级，而「哪条在前」是排查时最先要看的东西。
    int order = 0;
    for (const auto& rule : options.rules) {
        ++order;
        std::string label = std::to_string(order) + ". " + rule.type + " " + rule.value +
                            " -> " + rule.action;
        if (!rule.group_name.empty()) {
            label += "(" + rule.group_name + ")";
        }
        rules_list_->Append(wxString::FromUTF8(label));
    }
    if (options.rules.empty()) {
        rules_list_->Append(wxString::FromUTF8(kListPlaceholder));
    }

    rule_sets_list_->Clear();
    for (const auto& set : options.rule_sets) {
        std::string label = set.name;
        if (!set.url.empty()) {
            label += "  " + set.url;
        } else {
            label += "  (built-in)";
        }
        if (!set.format.empty()) {
            label += "  [" + set.format + "]";
        }
        rule_sets_list_->Append(wxString::FromUTF8(label));
    }
    if (options.rule_sets.empty()) {
        rule_sets_list_->Append(wxString::FromUTF8(kListPlaceholder));
    }

    // ---- DNS ----
    dns_enabled_->SetValue(options.dns.enabled);
    dns_fake_ip_->SetValue(options.dns.fake_ip);
    SetText(dns_fake_ip_range_, options.dns.fake_ip_range);
    SetText(dns_nameservers_, JoinLines(options.dns.nameservers));
    dns_split_->SetValue(options.dns.split_resolution);
    SetText(dns_fake_ip_filter_, JoinLines(options.dns.fake_ip_filter));
    dns_use_hosts_->SetValue(options.dns.use_hosts);
    dns_ipv6_->SetValue(options.dns.ipv6);

    // ---- 流量接入 ----
    const int entry_index = IndexOf(entry_values_, options.inbound.traffic_entry);
    if (entry_index >= 0) {
        inbound_entry_choice_->SetSelection(entry_index);
    }
    SetText(inbound_listen_, options.inbound.listen);
    SetText(inbound_port_, std::to_string(options.inbound.mixed_port));
    inbound_allow_lan_->SetValue(options.inbound.allow_lan);

    tun_enabled_->SetValue(options.inbound.tun.enabled);
    const int stack_index = IndexOf(stack_values_, options.inbound.tun.stack);
    if (stack_index >= 0) {
        tun_stack_choice_->SetSelection(stack_index);
    }
    SetText(tun_device_, options.inbound.tun.device);
    SetText(tun_mtu_, std::to_string(options.inbound.tun.mtu));
    const int dns_mode_index = IndexOf(dns_mode_values_, options.inbound.tun.dns_mode);
    if (dns_mode_index >= 0) {
        tun_dns_mode_choice_->SetSelection(dns_mode_index);
    }
    SetText(tun_dns_address_, JoinLines(options.inbound.tun.dns_address));
    tun_auto_route_->SetValue(options.inbound.tun.auto_route);
    tun_strict_route_->SetValue(options.inbound.tun.strict_route);

    // ---- 嗅探 ----
    sniff_enabled_->SetValue(options.inbound.sniffer.enabled);
    sniff_override_->SetValue(options.inbound.sniffer.override_destination);
    const auto& protocols = options.inbound.sniffer.protocols;
    const auto has = [&protocols](const char* wanted) {
        for (const auto& value : protocols) {
            if (value == wanted) {
                return true;
            }
        }
        return false;
    };
    sniff_tls_->SetValue(has(domain::sniff_protocol::kTLS));
    sniff_http_->SetValue(has(domain::sniff_protocol::kHTTP));
    sniff_quic_->SetValue(has(domain::sniff_protocol::kQUIC));

    // ---- 订阅 ----
    SetText(sub_url_, options.subscription.url);
    sub_auto_update_->SetValue(options.subscription.auto_update);
    SetText(sub_interval_, std::to_string(options.subscription.update_interval));
    sub_via_proxy_->SetValue(options.subscription.update_via_proxy);
    SetText(sub_name_prefix_, options.subscription.name_prefix);

    // ---- 应用 ----
    app_auto_start_->SetValue(options.app.auto_start);
    app_start_minimized_->SetValue(options.app.start_minimized);
    app_auto_connect_->SetValue(options.app.auto_connect_on_start);
    SetText(app_language_, options.app.language);
    SetText(app_theme_, options.app.theme);
    const int log_index = IndexOf(log_level_values_, options.app.log_level);
    if (log_index >= 0) {
        app_log_level_choice_->SetSelection(log_index);
    }
    SetText(app_log_lines_, std::to_string(options.app.log_buffer_lines));
}

void OptionsPanel::Update() {
    // ---- 固定文案 ----
    for (wxWindow* child : GetChildren()) {
        const std::string name = child->GetName().ToStdString();
        if (name.rfind("ui.", 0) == 0) {
            child->SetLabel(wxString::FromUTF8(context_.T(name)));
        }
    }
    // 页签标题与页内静态文本不在直接子控件里，需要各自遍历。
    if (notebook_ != nullptr) {
        // key 是 std::string 而不是 const char*：UiKey 返回 std::string，
        // 存成 const char* 会立刻悬垂，而那种错误只在运行期表现为乱码。
        const struct PageKey {
            std::string key;
            std::size_t page;
        } kPageKeys[] = {
            {UiKey("options.page.proxies"), 0},      {UiKey("options.page.rules"), 1},
            {UiKey("options.page.dns"), 2},          {UiKey("options.page.inbound"), 3},
            {UiKey("options.page.sniffing"), 4},     {UiKey("options.page.subscription"), 5},
            {UiKey("options.page.app"), 6},
        };
        for (const auto& entry : kPageKeys) {
            if (entry.page >= notebook_->GetPageCount()) {
                continue;
            }
            notebook_->SetPageText(entry.page,
                                   wxString::FromUTF8(context_.T(entry.key)));
            wxWindow* page = notebook_->GetPage(entry.page);
            for (wxWindow* child : page->GetChildren()) {
                const std::string name = child->GetName().ToStdString();
                if (name.rfind("ui.", 0) == 0) {
                    // 复选框/静态文本都有 SetLabel；wxTextCtrl 的 GetName
                    // 不会被设成文案键，因此这里不会把输入框的内容清掉。
                    if (wxDynamicCast(child, wxTextCtrl) == nullptr) {
                        child->SetLabel(wxString::FromUTF8(context_.T(name)));
                    }
                }
            }
        }
    }

    save_button_->SetLabel(wxString::FromUTF8(context_.T(UiKey("options.save"))));
    save_bottom_->SetLabel(wxString::FromUTF8(context_.T(UiKey("options.save"))));
    reload_button_->SetLabel(wxString::FromUTF8(context_.T(UiKey("options.reload"))));

    // ---- 数据来源 ----
    if (!context_.has_options) {
        // 显示「尚未读取」而不是展示一份本地默认值：
        // 那会让人以为看到的就是当前配置，然后照着一份幻想去改。
        source_text_->SetLabel(wxString::FromUTF8(context_.T(UiKey("options.not_loaded"))));
    } else {
        source_text_->SetLabel(wxString::FromUTF8(context_.T(UiKey("options.from_service"))));
    }

    const bool usable = context_.ServiceAvailable();
    save_button_->Enable(usable && context_.has_options);
    save_bottom_->Enable(usable && context_.has_options);
    reload_button_->Enable(usable);

    Layout();
}

}  // namespace pcx::ui
