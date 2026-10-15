// 完整选项编辑器 / full option editor.
//
// 这是 ui-options.md 第 2 节在界面上的落地，分组与文档一致：
//   代理 Proxies / 规则 Rules / DNS / 流量接入 Inbound / 嗅探 Sniffing /
//   订阅 Subscription / 应用 App
//
// 三条设计约束（都由这个面板保证）：
//
//   1. **读改的是同一个对象**。每种模式各持一份配置是设计基线 §4.1 明确
//      禁止的：那会让「简单模式显示已连接、专业模式显示未连接」这类
//      互相矛盾的状态成为可能。因此这里读写的始终是 AppContext::options。
//
//   2. **改的是选项，不是 sing-box 配置**。每一项都对应一个真实字段
//      （见 ui-options.md 每张表的最后一列）。界面不提供「只是转成字符串」
//      的伪选项——改了没反应的开关比没有这个开关更糟。
//
//   3. **必须如实标注做不到的事**。已经有三处：
//      - 嗅探协议列表目前不生效（需要单独的路由动作，尚未生成）；
//      - 负载均衡是近似实现，不保证会话保持；
//      - 代理组 / 规则 / 规则集只读，界面不提供图形化编辑器。
//      假装它们能用，会让用户照着调半天而问题在别处。
//
// 为什么列表类（Groups / Rules / RuleSets）只读：它们要能增删排序才算
// 真正可用，而「排序即优先级」这个语义在只读列表里无法表达完整。
// 但**原样保留**是必须的：保存时逐字段回填，绝不能因为界面不认识
// 就把用户手写的规则清空。
#pragma once

#include <wx/wxprec.h>

#ifndef WX_PRECOMP
#include <wx/wx.h>
#include <functional>
#include <vector>
#endif

// wx/notebook.h **必须在 #ifndef WX_PRECOMP 之外**。
//
// 这条曾经是错的，而且只在 Windows 上暴露：定义 WX_PRECOMP 时
// （MSVC / vcpkg 那条构建路径会定义它）整个 #ifndef 块被跳过，
// 于是 wxNotebook 成了一个未知类型，报错是一串
// 「missing ';' before '*'」和「notebook_: undeclared identifier」——
// 指向成员声明处，而不是真正的原因（少了一个头文件）。
//
// Linux 上定义与不定义 WX_PRECOMP 都没事，因为那边的构建恰好不定义它，
// 于是这个缺陷在 Linux CI 上一直是绿的。
//
// 教训：**wx 的子模块头文件不要放进入这个条件块**。
// 它只该包住 <wx/wx.h> 那种「预编译头已经提供」的伞头文件。
#include <wx/notebook.h>

#include <string>

#include "app/app_context.hpp"
#include "core/options.hpp"

namespace pcx::ui {

class OptionsPanel : public wxPanel {
public:
    OptionsPanel(wxWindow* parent, app::AppContext& context);

    // Update 按最新状态刷新界面。
    void Update();

    // Reload 通知面板「上下文里的选项已经被重新读取」。
    //
    // 与 Update 分开：Update 由 500ms 轮询调用，而把服务端的取值
    // 覆盖到用户正在编辑的控件上会**吃掉他刚敲的内容**。
    void Reload();

    // SetSaveHandler 由主窗口注入：保存整套选项。
    void SetSaveHandler(std::function<void(const domain::Options& options)> handler);

    // SetReloadHandler 由主窗口注入：重新从服务端读取选项。
    void SetReloadHandler(std::function<void()> handler);

    // SetStatus 显示一行操作结果。
    void SetStatus(const std::string& text);

private:
    void BuildLayout();

    // 各分组各建一个页签，避免一屏塞几十个控件。
    wxWindow* BuildProxiesPage(wxWindow* parent);
    wxWindow* BuildRulesPage(wxWindow* parent);
    wxWindow* BuildDnsPage(wxWindow* parent);
    wxWindow* BuildInboundPage(wxWindow* parent);
    wxWindow* BuildSniffingPage(wxWindow* parent);
    wxWindow* BuildSubscriptionPage(wxWindow* parent);
    wxWindow* BuildAppPage(wxWindow* parent);

    // Collect 把控件里的取值收集成一份完整的选项模型。
    //
    // 从**当前上下文里的模型**出发再覆盖界面能改的字段：
    // 这样只读列表（代理组 / 规则 / 规则集）会被原样带走。
    domain::Options Collect() const;
    // Apply 把模型写入控件。
    void Apply(const domain::Options& options);

    app::AppContext& context_;

    wxNotebook* notebook_ = nullptr;
    wxStaticText* status_text_ = nullptr;
    wxStaticText* source_text_ = nullptr;
    wxButton* save_button_ = nullptr;
    wxButton* reload_button_ = nullptr;
    // save_bottom_ 与 save_button_ 是同一个动作的两个入口：
    // 选项分散在七个页签上，用户在最后一个页签改完后不该被要求滚回顶部。
    wxButton* save_bottom_ = nullptr;

    // ---- 代理 ----
    wxTextCtrl* delay_test_url_ = nullptr;
    wxTextCtrl* delay_test_timeout_ = nullptr;
    wxListBox* groups_list_ = nullptr;

    // ---- 规则 ----
    wxChoice* route_mode_choice_ = nullptr;
    wxChoice* final_action_choice_ = nullptr;
    wxListBox* rules_list_ = nullptr;
    wxListBox* rule_sets_list_ = nullptr;

    // ---- DNS ----
    wxCheckBox* dns_enabled_ = nullptr;
    wxCheckBox* dns_fake_ip_ = nullptr;
    wxTextCtrl* dns_fake_ip_range_ = nullptr;
    wxTextCtrl* dns_nameservers_ = nullptr;
    wxCheckBox* dns_split_ = nullptr;
    wxTextCtrl* dns_fake_ip_filter_ = nullptr;
    wxCheckBox* dns_use_hosts_ = nullptr;
    wxCheckBox* dns_ipv6_ = nullptr;

    // ---- 流量接入 ----
    wxChoice* inbound_entry_choice_ = nullptr;
    wxTextCtrl* inbound_listen_ = nullptr;
    wxTextCtrl* inbound_port_ = nullptr;
    wxCheckBox* inbound_allow_lan_ = nullptr;
    wxCheckBox* tun_enabled_ = nullptr;
    wxChoice* tun_stack_choice_ = nullptr;
    wxTextCtrl* tun_device_ = nullptr;
    wxTextCtrl* tun_mtu_ = nullptr;
    wxCheckBox* tun_auto_route_ = nullptr;
    wxCheckBox* tun_strict_route_ = nullptr;
    wxChoice* tun_dns_mode_choice_ = nullptr;
    wxTextCtrl* tun_dns_address_ = nullptr;

    // ---- 嗅探 ----
    wxCheckBox* sniff_enabled_ = nullptr;
    wxCheckBox* sniff_tls_ = nullptr;
    wxCheckBox* sniff_http_ = nullptr;
    wxCheckBox* sniff_quic_ = nullptr;
    wxCheckBox* sniff_override_ = nullptr;
    wxStaticText* sniff_note_ = nullptr;

    // ---- 订阅 ----
    wxTextCtrl* sub_url_ = nullptr;
    wxCheckBox* sub_auto_update_ = nullptr;
    wxTextCtrl* sub_interval_ = nullptr;
    wxCheckBox* sub_via_proxy_ = nullptr;
    wxTextCtrl* sub_name_prefix_ = nullptr;

    // ---- 应用 ----
    wxCheckBox* app_auto_start_ = nullptr;
    wxCheckBox* app_start_minimized_ = nullptr;
    wxCheckBox* app_auto_connect_ = nullptr;
    wxTextCtrl* app_language_ = nullptr;
    wxTextCtrl* app_theme_ = nullptr;
    wxChoice* app_log_level_choice_ = nullptr;
    wxTextCtrl* app_log_lines_ = nullptr;

    // ---- 取值表：控件下标 -> 线上取值 ----
    std::vector<std::string> route_mode_values_;
    std::vector<std::string> action_values_;
    std::vector<std::string> entry_values_;
    std::vector<std::string> stack_values_;
    std::vector<std::string> dns_mode_values_;
    std::vector<std::string> log_level_values_;

    std::function<void(const domain::Options&)> save_handler_;
    std::function<void()> reload_handler_;
};

}  // namespace pcx::ui
