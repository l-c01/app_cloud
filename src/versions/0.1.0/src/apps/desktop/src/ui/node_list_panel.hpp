// 节点列表视图 / subscription node list view.
//
// 职责很小，但每一条都有原因：
//   - 列出订阅里的节点（ListNodes），显示协议与地址；
//   - 让用户选一个（SelectNode），并显示当前选中的是哪个；
//   - 「刷新订阅」按钮（RefreshSubscription）。
//
// 为什么节点列表要单独一个面板而不是塞进专业模式：
// **这是普通用户唯一必须用的功能**（设计基线 §4.1）。把它藏在专业模式里，
// 简易模式的用户就只能连服务端给的默认节点，而换节点是遇到
// 「某个节点不通」时唯一能自救的动作。
//
// 本面板不含任何连接凭据：NodeView 本身就不带 password / uuid，
// 服务端保证过这一点（见 Go 端 NodeView 的注释）。
#pragma once

#include <wx/wxprec.h>

#ifndef WX_PRECOMP
#include <wx/wx.h>
#include <functional>
#endif

#include <cstddef>
#include <string>
#include <vector>

#include "app/app_context.hpp"
#include "core/ipc_protocol.hpp"

namespace pcx::ui {

class NodeListPanel : public wxPanel {
public:
    NodeListPanel(wxWindow* parent, app::AppContext& context);

    // Update 按最新状态刷新界面。
    void Update();

    // SetSelectNodeHandler 由主窗口注入：真正的 SelectNode 调用是主窗口的职责。
    void SetSelectNodeHandler(std::function<void(const ipc::SelectNodeParams&)> handler);

    // SetRefreshHandler 由主窗口注入：刷新订阅。
    void SetRefreshHandler(std::function<void()> handler);

    // SetStatus 显示一行操作结果（成功或失败）。
    //
    // 文本由调用方给出：面板不知道错误来自哪一层，
    // 也不该在这里猜测该怎么翻译。
    void SetStatus(const std::string& text);

private:
    void BuildLayout();
    void RefreshLabels();

    app::AppContext& context_;

    wxStaticText* title_text_ = nullptr;
    wxStaticText* subject_text_ = nullptr;
    wxStaticText* status_text_ = nullptr;
    wxListBox* node_list_ = nullptr;
    wxButton* refresh_button_ = nullptr;
    wxButton* select_button_ = nullptr;

    std::function<void(const ipc::SelectNodeParams&)> select_node_handler_;
    std::function<void()> refresh_handler_;

    // suppress_events_ 在程序化重建列表时置位。
    //
    // 否则 Clear() 与 Append() 触发的选择变化会被当成用户操作，
    // 直接发一次 SelectNode——用户在列表里点一下，却发了两次请求。
    bool suppress_events_ = false;
};

}  // namespace pcx::ui
