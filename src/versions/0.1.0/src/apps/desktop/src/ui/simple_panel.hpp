// 简单模式面板 / simple-mode panel.
//
// 设计目标（设计基线 §4.1）：让不懂代理概念的人也能完成「连上」这一件事。
//
// 因此这里**刻意不出现**：核心选择、DNS、TUN 栈、嗅探、规则集、日志。
// 这些不是被隐藏的次要功能，而是会让人做错决定的选项——
// 一个不懂分流规则的用户选了「全局直连」，表现就是「连上了但打不开网页」，
// 而他不会知道原因。
//
// 但**路由模式**必须出现（ui-options.md 1 节）：规则模式决定「什么走代理」，
// 是普通用户真的会改的一项。反过来，分应用代理只在 Android 有意义
// （桌面端的系统代理与 TUN 都是全机的），因此这里不提供它——
// 放一个改了不生效的开关正是设计文档批评过的伪选项。
#pragma once

#include <wx/wxprec.h>

#ifndef WX_PRECOMP
#include <wx/wx.h>
#include <functional>
#endif

#include <string>
#include <vector>

#include "app/app_context.hpp"

namespace pcx::ui {

class SimplePanel : public wxPanel {
public:
    SimplePanel(wxWindow* parent, app::AppContext& context);

    // Update 按最新状态刷新界面。
    //
    // 由主窗口的定时器调用；面板自己不持有定时器，
    // 这样两种模式共用一份刷新节奏，不会出现两边状态不同步。
    void Update();

    // OnConnectRequested 由主窗口注入：真正发起连接是主窗口的职责，
    // 面板只负责表达意图。
    void SetConnectHandler(std::function<void(bool connect)> handler);

    // OnOpenPanelRequested 由主窗口注入：打开面板网页。
    void SetOpenPanelHandler(std::function<void()> handler);

    // SetRoutingModeHandler 由主窗口注入：路由模式改变。
    //
    // 取值是 rule / global / direct，与 domain.Options.RouteMode 一致。
    void SetRoutingModeHandler(std::function<void(const std::string& mode)> handler);

    // SetStatus 显示一行操作结果（成功或失败）。
    void SetStatus(const std::string& text);

private:
    void BuildLayout();
    void ApplyTexts();
    // RefreshSessionText 刷新账户与用量那几行。
    void RefreshSessionText();

    app::AppContext& context_;

    wxStaticText* status_text_ = nullptr;
    wxStaticText* detail_text_ = nullptr;
    wxStaticText* node_text_ = nullptr;
    wxStaticText* session_text_ = nullptr;
    wxStaticText* session_warning_text_ = nullptr;
    wxStaticText* routing_hint_text_ = nullptr;
    wxStaticText* notice_text_ = nullptr;
    wxButton* connect_button_ = nullptr;
    wxButton* open_panel_button_ = nullptr;

    // 路由模式三选一。用单选按钮而不是下拉框：
    // 三个取值都要能被一眼看见，而下拉框会让「当前是直连」这种
    // 需要警惕的状态藏在一个合上的框里。
    wxRadioBox* routing_box_ = nullptr;
    // routing_values_ 与单选项一一对应；按下标取回取值。
    std::vector<std::string> routing_values_;

    std::function<void(bool)> connect_handler_;
    std::function<void()> open_panel_handler_;
    std::function<void(const std::string&)> routing_mode_handler_;

    // busy_ 表示已发起请求但结果未回；期间按钮禁用，避免重复点击。
    bool busy_ = false;

    // suppress_events_ 在程序化设置单选项时置位，避免把程序自己的改动
    // 当成用户操作再发一次请求。
    bool suppress_events_ = false;
};

}  // namespace pcx::ui
