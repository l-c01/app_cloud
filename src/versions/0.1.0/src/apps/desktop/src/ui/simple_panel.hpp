// 简单模式面板 / simple-mode panel.
//
// 设计目标（设计基线 §4.1）：让不懂代理概念的人也能完成「连上」这一件事。
//
// 因此这里**刻意不出现**：核心选择、路由策略、接入方式、日志。
// 这些不是被隐藏的次要功能，而是会让人做错决定的选项——
// 一个不懂分流规则的用户选了「全局直连」，表现就是「连上了但打不开网页」，
// 而他不会知道原因。
#pragma once

#include <wx/wxprec.h>

#ifndef WX_PRECOMP
#include <wx/wx.h>
#include <functional>
#endif

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

private:
    void BuildLayout();
    void ApplyTexts();

    app::AppContext& context_;

    wxStaticText* status_text_ = nullptr;
    wxStaticText* detail_text_ = nullptr;
    wxStaticText* node_text_ = nullptr;
    wxButton* connect_button_ = nullptr;
    wxButton* open_panel_button_ = nullptr;

    std::function<void(bool)> connect_handler_;
    std::function<void()> open_panel_handler_;

    // busy_ 表示已发起请求但结果未回；期间按钮禁用，避免重复点击。
    bool busy_ = false;
};

}  // namespace pcx::ui
