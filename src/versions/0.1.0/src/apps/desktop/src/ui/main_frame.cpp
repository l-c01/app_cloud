#include "ui/main_frame.hpp"

#include <wx/aboutdlg.h>
#include <wx/utils.h>

#include <chrono>
#include <string>
#include <vector>

#include "core/json_util.hpp"
#include "core/options.hpp"
#include "ui/ui_format.hpp"

namespace pcx::ui {

namespace {

// kPollIntervalMs 是刷新间隔。
//
// 500ms 是权衡：更短会让界面在没有变化时也在做无用功（并且每次
// 都要走一次 IPC 往返），更长则用户点「连接」后会觉得反应迟钝。
constexpr int kPollIntervalMs = 500;

// kConnectTimeout 是连接/断开操作的等待上限。
//
// 比普通调用长得多：连接要编译配置、启动核心、改系统网络，
// 在慢机器上几秒钟是正常的。超时太短会让用户看到「失败」而实际上正在成功。
constexpr std::chrono::milliseconds kConnectTimeout{60000};

// kSessionPollInterval 是会话状态的读取间隔。
//
// 比运行状态慢得多是有意的：运行状态每 500ms 变化都很正常（连接过程中），
// 而会话状态只在用户登录/退出或订阅刷新后才会变。每次都读只是白占 IPC，
// 而 IPC 是单连接串行的——刷得太勤会让真正要紧的「连接」请求排在后面。
constexpr std::chrono::milliseconds kSessionPollInterval{3000};

}  // namespace

MainFrame::MainFrame(app::AppContext& context)
    : wxFrame(nullptr, wxID_ANY, wxString::FromUTF8(context.config.DisplayName(context.language)),
              wxDefaultPosition, wxSize(980, 680)),
      context_(context) {
    BuildMenuBar();
    BuildLayout();

    CreateStatusBar(1);

    // 面板只表达意图，实际调用由窗口统一发起。
    simple_panel_->SetConnectHandler([this](bool connect) { RequestConnect(connect); });
    simple_panel_->SetOpenPanelHandler([this] { OpenPanelWebsite(); });
    simple_panel_->SetRoutingModeHandler([this](const std::string& mode) {
        RequestRoutingPolicy(mode);
    });

    pro_panel_->SetConnectHandler([this](bool connect) { RequestConnect(connect); });
    pro_panel_->SetRefreshHandler([this] { RequestRefresh(); });
    pro_panel_->SetSwitchNodeHandler([this](const std::string& id) { RequestSwitchNode(id); });
    pro_panel_->SetNetworkModeHandler([this](const std::string& mode) { RequestNetworkMode(mode); });
    pro_panel_->SetRoutingPolicyHandler([this](const std::string& policy) {
        RequestRoutingPolicy(policy);
    });

    node_panel_->SetSelectNodeHandler([this](const ipc::SelectNodeParams& params) {
        RequestSelectNode(params);
    });
    node_panel_->SetRefreshHandler([this] { RequestRefreshSubscription(); });

    options_panel_->SetSaveHandler([this](const domain::Options& options) {
        RequestSaveOptions(options);
    });
    options_panel_->SetReloadHandler([this] { RequestReloadOptions(); });

    login_panel_->SetSignInHandler([this](const ipc::LoginParams& params) {
        RequestLogin(params);
    });
    login_panel_->SetOpenPanelHandler([this] { OpenPanelWebsite(); });

    // 首次尝试连接后台服务。失败不弹窗：服务没起来是正常状态，
    // 界面照常显示，并在状态里说明。
    std::string error;
    if (!context_.ReconnectService(&error)) {
        ShowError(error);
    }

    Bind(wxEVT_TIMER, &MainFrame::OnPollTimer, this, poll_timer_.GetId());
    Bind(wxEVT_CLOSE_WINDOW, &MainFrame::OnClose, this);

    // 首次读取会话与节点：不等定时器，否则界面会先显示登录页，
    // 几百毫秒后又跳走——用户会以为程序闪了一下。
    if (context_.ServiceAvailable()) {
        std::string inner;
        if (!context_.RefreshSession(&inner) && !inner.empty()) {
            ShowError(DescribeError(inner));
        }
        if (!context_.RefreshNodes(&inner) && !inner.empty()) {
            ShowError(DescribeError(inner));
        }
    }

    poll_timer_.Start(kPollIntervalMs);
    next_session_poll_ = std::chrono::steady_clock::now() + kSessionPollInterval;
    // 先确定专业模式的页面，再应用模式：反过来会让 ApplyMode 按
    // 上一次的页面状态去设置可见性，然后立刻被 ApplyProPage 改掉。
    ApplyProPage(ProPage::Overview);
    ApplyMode(context_.preferences.ui_mode, /*persist=*/false);
}

void MainFrame::BuildMenuBar() {
    auto* file_menu = new wxMenu();
    file_menu->Append(wxID_LOGOUT, wxString::FromUTF8(context_.T(UiKey("menu.logout"))));
    file_menu->Append(wxID_REFRESH,
                      wxString::FromUTF8(context_.T(UiKey("menu.refresh_subscription"))));
    file_menu->AppendSeparator();
    file_menu->Append(wxID_EXIT, wxString::FromUTF8(context_.T("ui.menu.quit")));

    auto* view_menu = new wxMenu();
    view_menu->AppendRadioItem(wxID_VIEW_LIST, wxString::FromUTF8(context_.T("ui.mode.simple")));
    view_menu->AppendRadioItem(wxID_VIEW_DETAILS,
                               wxString::FromUTF8(context_.T("ui.mode.professional")));
    view_menu->AppendRadioItem(wxID_VIEW_OPTIONS,
                               wxString::FromUTF8(context_.T(UiKey("mode.options"))));
    view_menu->AppendSeparator();
    // 「记住模式」用可勾选项而不是让用户去配置文件里改：
    // 这是一个纯粹的偏好，放在界面里最直接。
    auto* remember = view_menu->AppendCheckItem(
        wxID_ANY, wxString::FromUTF8(context_.T("ui.menu.remember_mode")));
    remember->Check(context_.preferences.remember_ui_mode);
    Bind(wxEVT_MENU, [this, remember](wxCommandEvent&) {
        context_.preferences.remember_ui_mode = remember->IsChecked();
    }, remember->GetId());

    Bind(wxEVT_MENU, [this](wxCommandEvent&) {
        ApplyMode(app::UiMode::Simple, /*persist=*/true);
    }, wxID_VIEW_LIST);
    Bind(wxEVT_MENU, [this](wxCommandEvent&) {
        ApplyMode(app::UiMode::Professional, /*persist=*/true);
        ApplyProPage(ProPage::Overview);
    }, wxID_VIEW_DETAILS);
    Bind(wxEVT_MENU, [this](wxCommandEvent&) {
        ApplyMode(app::UiMode::Professional, /*persist=*/true);
        ApplyProPage(ProPage::Options);
    }, wxID_VIEW_OPTIONS);

    Bind(wxEVT_MENU, [this](wxCommandEvent&) { RequestLogout(); }, wxID_LOGOUT);
    Bind(wxEVT_MENU, [this](wxCommandEvent&) { RequestRefreshSubscription(); }, wxID_REFRESH);

    // 「退出」菜单走 Close()，而不是直接把 OnClose 绑到菜单事件上。
    //
    // OnClose 的参数是 wxCloseEvent&，而 wxEVT_MENU 派发的是 wxCommandEvent&，
    // 类型不匹配无法绑定（wx 的事件表是编译期检查的）。
    // 更重要的是语义：退出应当走正常的关闭流程——Close() 会发出
    // wxEVT_CLOSE_WINDOW，从而经过 OnClose 里的保存与断开逻辑；
    // 直接调用 OnClose 或强行绑定反而会跳过这些步骤。
    Bind(wxEVT_MENU, [this](wxCommandEvent&) { Close(); }, wxID_EXIT);

    auto* help_menu = new wxMenu();
    help_menu->Append(wxID_ANY, wxString::FromUTF8(context_.T("ui.menu.diagnostics")));
    help_menu->Append(wxID_ABOUT, wxString::FromUTF8(context_.T("ui.menu.about")));

    // 诊断项的 id 需要单独取出才能绑定。
    const auto items = help_menu->GetMenuItems();
    if (!items.IsEmpty()) {
        Bind(wxEVT_MENU, [this](wxCommandEvent&) { ShowDiagnostics(); }, items[0]->GetId());
    }
    Bind(wxEVT_MENU, [this](wxCommandEvent&) { ShowAbout(); }, wxID_ABOUT);

    auto* bar = new wxMenuBar();
    bar->Append(file_menu, wxString::FromUTF8(context_.T("ui.menu.file")));
    bar->Append(view_menu, wxString::FromUTF8(context_.T("ui.menu.view")));
    bar->Append(help_menu, wxString::FromUTF8(context_.T("ui.menu.help")));
    SetMenuBar(bar);
}

void MainFrame::BuildLayout() {
    auto* root = new wxBoxSizer(wxVERTICAL);

    login_panel_ = new LoginPanel(this, context_);

    simple_panel_ = new SimplePanel(this, context_);

    // 专业模式：状态区与选项区分两页。
    //
    // 分页而不是并排：现有面板已经要在一屏里放状态、节点列表与日志，
    // 再叠上三十多个选项会让两边都看不清。节点列表因此独立成一栏
    // （NodeListPanel），两种模式共用——它是普通用户唯一必须用的功能，
    // 藏在专业模式里会让简易模式的用户没法学着换节点。
    pro_panel_ = new ProPanel(this, context_);
    options_panel_ = new OptionsPanel(this, context_);
    node_panel_ = new NodeListPanel(this, context_);

    auto* pro_row = new wxBoxSizer(wxHORIZONTAL);
    pro_row->Add(pro_panel_, 1, wxEXPAND);
    pro_row->Add(node_panel_, 0, wxEXPAND);

    auto* pro_column = new wxBoxSizer(wxVERTICAL);
    pro_column->Add(pro_row, 1, wxEXPAND);
    pro_column->Add(options_panel_, 1, wxEXPAND);

    root->Add(login_panel_, 1, wxEXPAND);
    root->Add(simple_panel_, 1, wxEXPAND);
    root->Add(pro_column, 1, wxEXPAND);
    SetSizer(root);
}

void MainFrame::ApplyMode(app::UiMode mode, bool persist) {
    context_.preferences.ui_mode = mode;
    if (persist && context_.preferences.remember_ui_mode) {
        // 只有勾选了「记住模式」才写盘。
        // 否则用户勾选前的临时切换会被持久化，下次启动看到的模式
        // 和他以为的不一样。
        std::string error;
        if (!app::SavePreferences(app::PreferencesPath(), context_.preferences, &error)) {
            ShowError(error);
        }
    }

    const bool simple = mode == app::UiMode::Simple;
    simple_panel_->Show(simple);
    // 专业模式下的具体页面由 pro_page_ 决定；ApplyProPage 也做同一件事，
    // 两处都写是为了让「只调用 ApplyMode」的路径（例如启动时）也正确。
    pro_panel_->Show(!simple && pro_page_ == ProPage::Overview);
    node_panel_->Show(!simple);
    options_panel_->Show(!simple && pro_page_ == ProPage::Options);

    if (auto* bar = GetMenuBar(); bar != nullptr) {
        bar->Check(wxID_VIEW_LIST, simple);
        bar->Check(wxID_VIEW_DETAILS, !simple && pro_page_ == ProPage::Overview);
        bar->Check(wxID_VIEW_OPTIONS, !simple && pro_page_ == ProPage::Options);
    }

    SyncVisibility();
    RefreshMainView();
}

void MainFrame::ApplyProPage(ProPage page) {
    pro_page_ = page;
    const bool options = page == ProPage::Options;
    const bool professional = context_.preferences.ui_mode == app::UiMode::Professional;

    pro_panel_->Show(professional && !options);
    // 节点列表两页都显示：改选项时也想看一眼当前有哪些节点，
    // 而为了看一眼就切回去会让选项页的滚动位置丢掉。
    node_panel_->Show(professional);
    options_panel_->Show(professional && options);

    if (auto* bar = GetMenuBar(); bar != nullptr) {
        bar->Check(wxID_VIEW_DETAILS, professional && !options);
        bar->Check(wxID_VIEW_OPTIONS, professional && options);
    }

    if (options) {
        // 每次进入选项页都重新读取：用户可能在别处（Android 端、
        // 另一个客户端）改过配置，而显示一份过期取值会让他改错东西。
        RequestReloadOptions();
    }

    SyncVisibility();
    RefreshMainView();
}

void MainFrame::SyncVisibility() {
    // 未登录（或还不知道会话状态）时只显示登录页。
    //
    // has_session 为 false 时也走登录页：那是「还没读到」，
    // 而显示主界面会让用户以为已经登录，点到什么都失败。
    const bool signed_in = context_.has_session && context_.session.logged_in;

    login_panel_->Show(!signed_in);
    if (signed_in) {
        ApplyModeVisibility();
    } else {
        // 主界面的各个面板都要一起藏起来，否则它们会和登录页叠在一起。
        simple_panel_->Show(false);
        pro_panel_->Show(false);
        node_panel_->Show(false);
        options_panel_->Show(false);
    }

    if (auto* bar = GetMenuBar(); bar != nullptr) {
        // 未登录时把与会话相关的菜单项禁用，而不是让它们点了报错。
        bar->Enable(wxID_LOGOUT, signed_in);
        bar->Enable(wxID_REFRESH, signed_in);
    }

    Layout();
}

void MainFrame::ApplyModeVisibility() {
    // 只负责「已登录」情况下的可见性，供 SyncVisibility 调用。
    const bool simple = context_.preferences.ui_mode == app::UiMode::Simple;
    simple_panel_->Show(simple);
    pro_panel_->Show(!simple && pro_page_ == ProPage::Overview);
    node_panel_->Show(!simple);
    options_panel_->Show(!simple && pro_page_ == ProPage::Options);
}

void MainFrame::RefreshMainView() {
    if (login_panel_->IsShown()) {
        login_panel_->Update();
        return;
    }
    if (simple_panel_->IsShown()) {
        simple_panel_->Update();
    }
    if (pro_panel_->IsShown()) {
        pro_panel_->Update();
    }
    if (options_panel_->IsShown()) {
        options_panel_->Update();
    }
    if (node_panel_->IsShown()) {
        node_panel_->Update();
    }
}

void MainFrame::OnPollTimer(wxTimerEvent&) {
    // 事件由界面线程取走，这正是在 ipc_client 里把事件排队而不是
    // 回调界面代码的原因。
    ipc::Event event;
    int processed = 0;
    while (context_.ipc.PollEvent(&event) && processed < 64) {
        ++processed;
        if (pro_panel_ != nullptr) {
            // 事件内容按脱敏后的紧凑 JSON 显示；
            // 服务端已经保证 payload 里没有凭据。
            pro_panel_->AppendLog("[" + event.kind + "] " + json::Compact(event.payload));
        }
    }

    std::string error;
    if (!context_.RefreshFromService(&error)) {
        ShowError(DescribeError(error));
    }

    // 会话状态按自己的节奏读：见 kSessionPollInterval 的说明。
    const auto now = std::chrono::steady_clock::now();
    if (now >= next_session_poll_) {
        next_session_poll_ = now + kSessionPollInterval;
        std::string session_error;
        // 读失败不改写状态栏：服务端不支持该操作时它是常态，
        // 而每 3 秒刷一次同样的错误会把状态栏占死。
        if (context_.RefreshSession(&session_error) && context_.ServiceAvailable()) {
            std::string nodes_error;
            (void)context_.RefreshNodes(&nodes_error);
        }
    }

    // 登录状态可能刚刚改变（例如服务端会话过期），可见性要跟着变。
    SyncVisibility();
    RefreshMainView();

    // 丢弃计数必须让用户看得见：静默少日志比多几行提示糟糕得多。
    if (busy_) {
        // 一次长操作正在进行，不要用轮询结果覆盖「正在进行」的提示。
        return;
    }
    const auto dropped_local = context_.ipc.DroppedLocally();
    const auto dropped_server = context_.ipc.DroppedByServer();
    if (dropped_local > 0 || dropped_server > 0) {
        SetStatusText(wxString::FromUTF8(
            context_.T("ui.status.events_dropped",
                       {std::to_string(dropped_server), std::to_string(dropped_local)})));
    } else if (context_.ServiceAvailable()) {
        SetStatusText(wxString::FromUTF8(context_.T("ui.detail.service_ok")));
    }
}

void MainFrame::OnClose(wxCloseEvent& event) {
    // 退出前保存偏好（只要用户选择了记住模式）。
    if (context_.preferences.remember_ui_mode) {
        std::string error;
        if (!app::SavePreferences(app::PreferencesPath(), context_.preferences, &error)) {
            ShowError(error);
        }
    }
    poll_timer_.Stop();
    context_.ipc.Disconnect();
    event.Skip();
}

// ---------------------------------------------------------------------------
// 面板意图
// ---------------------------------------------------------------------------

std::string MainFrame::DescribeError(const std::string& error) const {
    if (error.empty()) {
        return error;
    }
    // messageKey 需要本地化；传输层的英文描述只能原样显示。
    if (LooksLikeMessageKey(error)) {
        const std::string translated = context_.T(error);
        if (translated != error) {
            return translated;
        }
    }
    return error;
}

void MainFrame::ShowNotice(const std::string& message) {
    if (message.empty()) {
        return;
    }
    SetStatusText(wxString::FromUTF8(message));
    if (pro_panel_ != nullptr && pro_panel_->IsShown()) {
        pro_panel_->AppendLog(message);
    }
}

void MainFrame::RequestConnect(bool connect) {
    std::string error;
    if (!context_.ServiceAvailable()) {
        // 服务不可用时先尝试重连，而不是直接报「失败」。
        if (!context_.ReconnectService(&error)) {
            ShowError(error);
            return;
        }
    }

    // 连接可能耗时几十秒，期间必须让用户看到「正在进行」，
    // 否则他会以为点击没生效而反复点。
    busy_ = true;
    SetStatusText(wxString::FromUTF8(context_.T("ui.detail.busy")));

    const bool ok = connect ? context_.ipc.Start(kConnectTimeout, &error)
                            : context_.ipc.Stop(kConnectTimeout, &error);
    busy_ = false;

    if (!ok) {
        ShowError(DescribeError(error));
        return;
    }
    RequestRefresh();
}

void MainFrame::RequestRefresh() {
    std::string error;
    if (!context_.RefreshFromService(&error)) {
        ShowError(DescribeError(error));
    }
    std::string ignored;
    (void)context_.RefreshSession(&ignored);
    (void)context_.RefreshNodes(&ignored);
    RefreshMainView();
}

void MainFrame::RequestSwitchNode(const std::string& node_id) {
    ipc::SelectNodeParams params;
    params.id = node_id;
    RequestSelectNode(params);
}

void MainFrame::RequestSelectNode(const ipc::SelectNodeParams& params) {
    std::string error;
    const auto selected = context_.ipc.SelectNode(params, kConnectTimeout, &error);
    if (!selected.has_value()) {
        ShowError(DescribeError(error));
        node_panel_->SetStatus(DescribeError(error));
        return;
    }
    // 服务端回传的是**实际生效的 ID**：按名字请求时用户给的可能是重名中的
    // 一个，界面必须显示真正生效的那个。因此按它重新读取会话与节点列表，
    // 而不是沿用本地那份可能已经过期的列表。
    std::string ignored;
    (void)context_.RefreshSession(&ignored);
    (void)context_.RefreshNodes(&ignored);
    if (node_panel_ != nullptr) {
        node_panel_->SetStatus(context_.T(UiKey("nodes.selected_ok"), {*selected}));
    }
    RequestRefresh();
}

void MainFrame::RequestNetworkMode(const std::string& mode) {
    std::string error;
    if (!context_.ipc.SetNetworkMode(mode, kConnectTimeout, &error)) {
        ShowError(DescribeError(error));
        return;
    }
    // 接入方式是用户偏好，立刻记住。
    context_.preferences.traffic_entry = mode;
    RequestRefresh();
}

void MainFrame::RequestRoutingPolicy(const std::string& policy) {
    // 路由模式是选项模型的一部分，因此**必须**通过 SetOptions 落下去。
    //
    // 只改本地偏好是不够的：那会让「简易模式显示全局、服务端仍是规则」
    // 这种两边不一致的状态一直存在——正是设计基线 §4.1 要避免的情况。
    context_.preferences.routing_policy = policy;

    if (!context_.has_options) {
        // 选项还没读上来。先把它读出来，再带着这次改动一起提交；
        // 读不到就只记偏好，并如实说明「需要重新连接才生效」。
        std::string ignored;
        if (!context_.RefreshOptions(&ignored) || !context_.has_options) {
            ShowNotice(context_.T("ui.status.reconnect_for_policy"));
            return;
        }
    }

    domain::Options options = context_.options;
    options.route_mode = policy;
    std::string error;
    if (!context_.ipc.SetOptions(options, kConnectTimeout, &error)) {
        ShowError(DescribeError(error));
        return;
    }
    context_.options = options;
    ShowNotice(context_.T(UiKey("status.routing_policy_saved")));
    RequestRefresh();
}

void MainFrame::RequestLogin(const ipc::LoginParams& params) {
    if (logging_in_) {
        // 重复提交不只是浪费请求：面板侧会把连续失败计入限流。
        return;
    }
    std::string error;
    if (!context_.ServiceAvailable()) {
        if (!context_.ReconnectService(&error)) {
            login_panel_->SetBusy(false);
            login_panel_->DisplayError(DescribeError(error));
            return;
        }
    }

    logging_in_ = true;
    login_panel_->SetBusy(true);
    busy_ = true;
    SetStatusText(wxString::FromUTF8(context_.T(UiKey("login.in_progress"))));

    // 登录一定会走出本机（访问面板），因此用远程操作的超时。
    const bool ok = context_.ipc.Login(params, app::AppContext::RemoteCallTimeout(), &error);
    login_panel_->SetBusy(false);
    logging_in_ = false;
    busy_ = false;

    if (!ok) {
        // 密码错误、限流、面板不是 Xboard……这些都以 messageKey 返回，
        // 由登录页按本地化文案显示。
        //
        // 优先取 RpcError 的 message_key：那是**唯一**保证是 i18n 键的字段；
        // 调用方拿到的 error 可能已经是描述文本。
        std::string message = error;
        if (context_.ipc.HasRpcError() && !context_.ipc.LastRpcError().message_key.empty()) {
            message = context_.ipc.LastRpcError().message_key;
        }
        login_panel_->DisplayError(message);
        return;
    }

    login_panel_->ClearError();
    ShowNotice(context_.T(UiKey("login.succeeded")));

    // 登录成功后立刻读一次会话与节点，让主界面马上有内容。
    std::string ignored;
    (void)context_.RefreshSession(&ignored);
    (void)context_.RefreshNodes(&ignored);
    (void)context_.RefreshOptions(&ignored);

    SyncVisibility();
    RefreshMainView();
}

void MainFrame::RequestLogout() {
    if (!context_.ServiceAvailable()) {
        ShowError(context_.T("ui.detail.service_unavailable"));
        return;
    }
    std::string error;
    // 退出登录不删除已保存的密码：删除是用户的显式动作，
    // 不该被「退出登录」顺带完成。
    if (!context_.ipc.Logout(app::AppContext::RemoteCallTimeout(), &error)) {
        ShowError(DescribeError(error));
        return;
    }
    // 本地也把会话标记成未登录：不能等服务端下一次回报，
    // 否则界面会在退出后仍然显示账户信息一瞬间。
    context_.session.logged_in = false;
    context_.nodes = ipc::NodeList{};
    context_.has_options = false;

    ShowNotice(context_.T(UiKey("session.logged_out")));
    SyncVisibility();
    RefreshMainView();
}

void MainFrame::RequestRefreshSubscription() {
    if (!context_.ServiceAvailable()) {
        ShowError(context_.T("ui.detail.service_unavailable"));
        return;
    }
    if (!context_.has_session || !context_.session.logged_in) {
        // 未登录时刷新订阅没有意义，而且用户看到的会是一条
        // 「未认证」的错误——那不如直接说清楚。
        ShowError(context_.T(UiKey("nodes.need_login")));
        return;
    }

    busy_ = true;
    SetStatusText(wxString::FromUTF8(context_.T(UiKey("nodes.refreshing"))));

    std::string error;
    const auto subscription =
        context_.ipc.RefreshSubscription(app::AppContext::RemoteCallTimeout(), &error);
    busy_ = false;

    if (!subscription.has_value()) {
        ShowError(DescribeError(error));
        node_panel_->SetStatus(DescribeError(error));
        return;
    }

    // 服务端的返回值不替换会话里的订阅：真正的权威是 GetSessionState。
    // 这里只用它来判断成功，并给出「拉到了几个节点」的即时反馈。
    if (node_panel_ != nullptr) {
        node_panel_->SetStatus(
            context_.T(UiKey("nodes.refreshed"), {std::to_string(subscription->node_count)}));
    }

    std::string ignored;
    (void)context_.RefreshSession(&ignored);
    (void)context_.RefreshNodes(&ignored);
    RefreshMainView();
}

void MainFrame::RequestReloadOptions() {
    std::string error;
    if (!context_.RefreshOptions(&error)) {
        if (!error.empty()) {
            options_panel_->SetStatus(DescribeError(error));
        } else {
            // 服务端不支持该操作。显示「不可用」而不是一条错误：
            // 这不是失败，是这个构建里没有这项能力。
            options_panel_->SetStatus(context_.T(UiKey("options.unavailable")));
        }
        return;
    }
    options_panel_->Reload();
    options_panel_->Update();
}

void MainFrame::RequestSaveOptions(const domain::Options& options) {
    if (!context_.ServiceAvailable()) {
        options_panel_->SetStatus(context_.T("ui.detail.service_unavailable"));
        return;
    }
    busy_ = true;
    SetStatusText(wxString::FromUTF8(context_.T(UiKey("options.saving"))));

    std::string error;
    const bool ok = context_.ipc.SetOptions(options, kConnectTimeout, &error);
    busy_ = false;

    if (!ok) {
        options_panel_->SetStatus(DescribeError(error));
        ShowError(DescribeError(error));
        return;
    }

    // 保存成功后才更新本地模型：先更新再发请求的话，失败时界面
    // 会显示一份**没有生效**的配置，而用户以为已经改好了。
    context_.options = options;
    context_.has_options = true;
    options_panel_->SetStatus(context_.T(UiKey("options.saved")));
    ShowNotice(context_.T(UiKey("options.saved")));

    // 选项改了，运行状态可能随后变化（例如接了 TUN），刷新一次。
    RequestRefresh();
}

void MainFrame::OpenPanelWebsite() {
    if (context_.config.web_base_url.empty()) {
        // 按钮本应被禁用；这里再挡一次，避免通过快捷键触达。
        ShowError(context_.T("ui.error.no_panel_url"));
        return;
    }
    if (!wxLaunchDefaultBrowser(wxString::FromUTF8(context_.config.web_base_url))) {
        ShowError(context_.T("ui.error.cannot_open_browser"));
    }
}

void MainFrame::ShowAbout() {
    wxAboutDialogInfo info;
    info.SetName(wxString::FromUTF8(context_.config.application_name));
    info.SetVersion(wxString::FromUTF8(PCX_VERSION));
    if (!context_.config.publisher.empty()) {
        // SetDevelopers 收的是 wxArrayString（一组开发者），不是单个 wxString，
        // 而且 wxString -> wxArrayString 没有隐式转换。
        wxArrayString developers;
        developers.Add(wxString::FromUTF8(context_.config.publisher));
        info.SetDevelopers(developers);
    }
    if (!context_.config.homepage_url.empty()) {
        info.SetWebSite(wxString::FromUTF8(context_.config.homepage_url));
    }
    // 许可证留空时不显示许可证字段，而不是显示「未指定」。
    if (!context_.config.license.empty()) {
        info.SetLicence(wxString::FromUTF8(context_.config.license));
    }

    // 如实说明构建状态：这个版本仍处于未验证阶段。
    wxString description = wxString::FromUTF8(context_.T("ui.about.description"));
    description += "\n\n" + wxString::FromUTF8(context_.T("ui.about.unverified_build"));
    if (!context_.has_capabilities) {
        description += "\n\n" + wxString::FromUTF8(context_.T("ui.about.service_unknown"));
    } else {
        for (const auto& core : context_.capabilities.cores) {
            if (core.verified_against == "unverified") {
                description += "\n\n" +
                               wxString::FromUTF8(context_.T("ui.warn.unverified_capability",
                                                             {core.name}));
                break;
            }
        }
    }
    info.SetDescription(description);

    wxAboutBox(info, this);
}

void MainFrame::ShowDiagnostics() {
    // 诊断信息给的是**可复制的文本**，而不是一个只读框里的长句子。
    std::string report;
    report += "client version : " + std::string(PCX_VERSION) + "\n";
    report += "protocol       : " + std::to_string(ipc::kProtocolVersion) + "\n";
    report += "schema         : " + std::string(ipc::kSchemaVersion) + "\n";
    report += "language       : " + context_.language + "\n";
    report += "ui mode        : " + app::UiModeToString(context_.preferences.ui_mode) + "\n";
    report += "service        : " +
              std::string(context_.ServiceAvailable() ? "connected" : "unavailable") + "\n";
    if (!context_.has_capabilities) {
        report += "capabilities   : not read\n";
    } else {
        report += "platform       : " + context_.capabilities.platform + "\n";
        report += "network takeover: " +
                  std::string(context_.capabilities.network_takeover ? "yes" : "no") + "\n";
        for (const auto& core : context_.capabilities.cores) {
            report += "core           : " + core.name + " " + core.version +
                      " (verified against: " + core.verified_against + ")\n";
        }
    }
    // 会话状态只报告**非凭据**的事实。这里刻意不打印邮箱：
    // 诊断输出经常被粘贴到公开的地方，而邮箱属于个人信息。
    report += "signed in      : " +
              std::string(context_.has_session && context_.session.logged_in ? "yes" : "no") +
              "\n";
    report += "secure storage : " +
              std::string(context_.session.secure_storage ? "available" : "unavailable") + "\n";
    report += "nodes          : " + std::to_string(context_.nodes.nodes.size()) + "\n";
    report += "options read   : " + std::string(context_.has_options ? "yes" : "no") + "\n";
    report += "dropped events : local=" + std::to_string(context_.ipc.DroppedLocally()) +
              " server=" + std::to_string(context_.ipc.DroppedByServer()) + "\n";
    if (!context_.config.warnings.empty()) {
        report += "\nconfig warnings:\n";
        for (const auto& warning : context_.config.warnings) {
            report += "  - " + warning + "\n";
        }
    }
    report += "\n" + context_.T("ui.detail.logs_omitted") + "\n";

    wxMessageBox(wxString::FromUTF8(report),
                 wxString::FromUTF8(context_.T("ui.menu.diagnostics")), wxOK | wxICON_INFORMATION,
                 this);
}

void MainFrame::ShowError(const std::string& message) {
    if (message.empty()) {
        return;
    }
    // 状态栏 + 专业模式日志，不弹模态框。
    SetStatusText(wxString::FromUTF8(message));
    if (pro_panel_ != nullptr && pro_panel_->IsShown()) {
        pro_panel_->AppendLog("error: " + message);
    }
}

}  // namespace pcx::ui
