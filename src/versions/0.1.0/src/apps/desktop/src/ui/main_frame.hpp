// 主窗口 / main window.
//
// 职责：
//   - 承载两种界面模式并负责切换（同一份上下文，只换呈现）；
//   - 定时轮询服务状态与事件（见 ipc_client 的线程模型说明）；
//   - 把面板表达的**意图**转成对服务的调用，并把结果反馈回界面。
//
// 面板不直接调用服务：这样「按钮是否该禁用」「失败怎么提示」
// 这类决策只有一处实现。
#pragma once

#include <wx/wxprec.h>

#ifndef WX_PRECOMP
#include <wx/wx.h>
#endif

#include <string>

#include "app/app_context.hpp"
#include "ui/pro_panel.hpp"
#include "ui/simple_panel.hpp"

namespace pcx::ui {

class MainFrame : public wxFrame {
public:
    MainFrame(app::AppContext& context);

private:
    void BuildMenuBar();
    void BuildLayout();

    // ApplyMode 切换界面模式并刷新呈现。
    void ApplyMode(app::UiMode mode, bool persist);

    // PollTimer 定时刷新状态与事件。
    void OnPollTimer(wxTimerEvent& event);
    void OnClose(wxCloseEvent& event);

    // 面板意图的处理。
    void RequestConnect(bool connect);
    void RequestRefresh();
    void RequestSwitchNode(const std::string& node_id);
    void RequestNetworkMode(const std::string& mode);
    void RequestRoutingPolicy(const std::string& policy);
    void OpenPanelWebsite();
    void ShowAbout();
    void ShowDiagnostics();

    // ShowError 用状态栏与日志展示错误，不弹模态框。
    //
    // 不弹模态框是刻意的：后台服务没起来是完全正常的情况，
    // 每次刷新都弹窗会让程序无法使用。
    void ShowError(const std::string& message);

    app::AppContext& context_;

    SimplePanel* simple_panel_ = nullptr;
    ProPanel* pro_panel_ = nullptr;
    wxTimer poll_timer_;

    // last_message_ 用于避免状态栏每 500ms 重复写同样的文字。
    std::string last_message_;

    // busy_ 在一次连接/断开操作进行中为 true。
    //
    // 连接可能耗时几十秒（编译配置、启动核心、改系统网络），
    // 期间轮询会覆盖状态栏文字，让用户以为操作已经结束。
    // 置位时轮询不再改写状态栏。
    bool busy_ = false;
};

}  // namespace pcx::ui
