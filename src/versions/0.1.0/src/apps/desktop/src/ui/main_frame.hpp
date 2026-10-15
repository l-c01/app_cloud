// 主窗口 / main window.
//
// 职责：
//   - 承载登录页与三种界面模式并负责切换（同一份上下文，只换呈现）；
//   - 定时轮询服务状态与事件（见 ipc_client 的线程模型说明）；
//   - 把面板表达的**意图**转成对服务的调用，并把结果反馈回界面。
//
// 面板不直接调用服务：这样「按钮是否该禁用」「失败怎么提示」
// 这类决策只有一处实现。
//
// 页面切换的顺序是刻意的：未登录时**只能**看到登录页。
// 让用户面对一个全是禁用按钮的主界面，比直接让他登录更难理解——
// 界面上大多数操作（列节点、改选项）在没有会话时确实做不了。
#pragma once

#include <wx/wxprec.h>

#ifndef WX_PRECOMP
#include <wx/wx.h>
#endif

#include <chrono>
#include <string>

#include "app/app_context.hpp"
#include "ui/login_panel.hpp"
#include "ui/node_list_panel.hpp"
#include "ui/options_panel.hpp"
#include "ui/pro_panel.hpp"
#include "ui/simple_panel.hpp"

namespace pcx::ui {

// ProPage 是专业模式下的两个页面。
//
// 分成两页而不是把选项塞进现有面板：现有面板要在一屏里显示状态、
// 节点与日志，再叠上三十多个选项会让两边都看不清。
enum class ProPage {
    Overview,
    Options,
};

// 菜单与工具栏的自定义命令 ID。
//
// **必须自己定义**：wxID_LOGOUT 与 wxID_VIEW_OPTIONS 在 wxWidgets 里
// 并不存在（wxID_VIEW_* 只有 LIST / DETAILS 等少数几个）。
// 直接写这两个名字会在编译期报「was not declared in this scope」。
//
// 从 wxID_HIGHEST 起分配而不是从 1：1 以下已被 wxWidgets 的标准 ID 占用
// （wxID_OPEN、wxID_EXIT 等），撞上的表现是菜单项触发了别的命令——
// 那种问题比编译错误难查得多。
enum : int {
    kIdLogout = wxID_HIGHEST + 1,
    kIdViewOptions,
};

class MainFrame : public wxFrame {
public:
    MainFrame(app::AppContext& context);

private:
    void BuildMenuBar();
    void BuildLayout();

    // ApplyMode 切换界面模式并刷新呈现。
    void ApplyMode(app::UiMode mode, bool persist);
    // ApplyProPage 切换专业模式下的页面。
    void ApplyProPage(ProPage page);
    // SyncVisibility 按「是否已登录」决定显示登录页还是主界面。
    void SyncVisibility();
    // ApplyModeVisibility 按当前模式与专业模式页决定主界面的可见性。
    void ApplyModeVisibility();

    // PollTimer 定时刷新状态与事件。
    void OnPollTimer(wxTimerEvent& event);
    void OnClose(wxCloseEvent& event);

    // 面板意图的处理。
    void RequestConnect(bool connect);
    void RequestRefresh();
    void RequestSwitchNode(const std::string& node_id);
    void RequestNetworkMode(const std::string& mode);
    void RequestRoutingPolicy(const std::string& policy);
    void RequestSelectNode(const ipc::SelectNodeParams& params);
    void RequestLogin(const ipc::LoginParams& params);
    void RequestLogout();
    void RequestRefreshSubscription();
    void RequestReloadOptions();
    void RequestSaveOptions(const domain::Options& options);
    void OpenPanelWebsite();
    void ShowAbout();
    void ShowDiagnostics();

    // ShowError 用状态栏与日志展示错误，不弹模态框。
    //
    // 不弹模态框是刻意的：后台服务没起来是完全正常的情况，
    // 每次刷新都弹窗会让程序无法使用。
    void ShowError(const std::string& message);

    // DescribeError 把一次调用的失败整理成可显示的一行。
    //
    // 服务端的 messageKey 需要本地化，传输层的原因只能原样显示；
    // 区分逻辑放在这里，避免每个调用点各判断一次而出现不同结果。
    std::string DescribeError(const std::string& error) const;

    // ShowNotice 把一行结果写到状态栏与专业模式日志。
    void ShowNotice(const std::string& message);

    // RefreshMainView 刷新当前可见的主面板。
    void RefreshMainView();

    app::AppContext& context_;

    LoginPanel* login_panel_ = nullptr;
    SimplePanel* simple_panel_ = nullptr;
    ProPanel* pro_panel_ = nullptr;
    NodeListPanel* node_panel_ = nullptr;
    OptionsPanel* options_panel_ = nullptr;
    wxTimer poll_timer_;

    // last_message_ 用于避免状态栏每 500ms 重复写同样的文字。
    std::string last_message_;

    // busy_ 在一次连接/断开/登录操作进行中为 true。
    //
    // 连接可能耗时几十秒（编译配置、启动核心、改系统网络），
    // 登录也可能因面板不可达而等满超时，期间轮询会覆盖状态栏文字，
    // 让用户以为操作已经结束。置位时轮询不再改写状态栏。
    bool busy_ = false;

    // logging_in_ 在登录请求进行中为 true，用来忽略重复提交。
    bool logging_in_ = false;

    ProPage pro_page_ = ProPage::Overview;

    // next_session_poll_ 是下一次读取会话状态的时刻。
    //
    // 会话状态要读得有节奏：它连着服务端的登录态，而变化只可能来自
    // 用户自己的动作或订阅刷新。500ms 一次地读只会白白占用 IPC。
    std::chrono::steady_clock::time_point next_session_poll_;
};

}  // namespace pcx::ui
