#include "ui/main_frame.hpp"

#include <wx/aboutdlg.h>
#include <wx/utils.h>

#include <chrono>

#include "core/json_util.hpp"

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

}  // namespace

MainFrame::MainFrame(app::AppContext& context)
    : wxFrame(nullptr, wxID_ANY, wxString::FromUTF8(context.config.DisplayName(context.language)),
              wxDefaultPosition, wxSize(880, 620)),
      context_(context) {
    BuildMenuBar();
    BuildLayout();

    CreateStatusBar(1);

    // 面板只表达意图，实际调用由窗口统一发起。
    simple_panel_->SetConnectHandler([this](bool connect) { RequestConnect(connect); });
    simple_panel_->SetOpenPanelHandler([this] { OpenPanelWebsite(); });

    pro_panel_->SetConnectHandler([this](bool connect) { RequestConnect(connect); });
    pro_panel_->SetRefreshHandler([this] { RequestRefresh(); });
    pro_panel_->SetSwitchNodeHandler([this](const std::string& id) { RequestSwitchNode(id); });
    pro_panel_->SetNetworkModeHandler([this](const std::string& mode) { RequestNetworkMode(mode); });
    pro_panel_->SetCoreHandler([this](const std::string& core) { RequestCore(core); });
    pro_panel_->SetRoutingPolicyHandler([this](const std::string& policy) {
        RequestRoutingPolicy(policy);
    });

    // 首次尝试连接后台服务。失败不弹窗：服务没起来是正常状态，
    // 界面照常显示，并在状态里说明。
    std::string error;
    if (!context_.ReconnectService(&error)) {
        ShowError(error);
    }

    Bind(wxEVT_TIMER, &MainFrame::OnPollTimer, this, poll_timer_.GetId());
    Bind(wxEVT_CLOSE_WINDOW, &MainFrame::OnClose, this);

    poll_timer_.Start(kPollIntervalMs);
    ApplyMode(context_.preferences.ui_mode, /*persist=*/false);
}

void MainFrame::BuildMenuBar() {
    auto* file_menu = new wxMenu();
    file_menu->Append(wxID_EXIT, wxString::FromUTF8(context_.T("ui.menu.quit")));

    auto* view_menu = new wxMenu();
    view_menu->AppendRadioItem(wxID_VIEW_LIST, wxString::FromUTF8(context_.T("ui.mode.simple")));
    view_menu->AppendRadioItem(wxID_VIEW_DETAILS,
                               wxString::FromUTF8(context_.T("ui.mode.professional")));
    view_menu->AppendSeparator();
    // 「记住模式」用可勾选项而不是让用户去配置文件里改：
    // 这是一个纯粹的偏好，放在界面里最直接。
    auto* remember = view_menu->AppendCheckItem(
        wxID_ANY, wxString::FromUTF8(context_.T("ui.menu.remember_mode")));
    remember->Check(context_.preferences.remember_ui_mode);
    Bind(wxEVT_MENU, [this, remember](wxCommandEvent&) {
        context_.preferences.remember_ui_mode = remember->IsChecked();
        if (remember->IsChecked()) {
            context_.preferences.ui_mode = context_.preferences.ui_mode;
        }
    }, remember->GetId());

    Bind(wxEVT_MENU, [this](wxCommandEvent&) {
        ApplyMode(app::UiMode::Simple, /*persist=*/true);
    }, wxID_VIEW_LIST);
    Bind(wxEVT_MENU, [this](wxCommandEvent&) {
        ApplyMode(app::UiMode::Professional, /*persist=*/true);
    }, wxID_VIEW_DETAILS);
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
    simple_panel_ = new SimplePanel(this, context_);
    pro_panel_ = new ProPanel(this, context_);
    root->Add(simple_panel_, 1, wxEXPAND);
    root->Add(pro_panel_, 1, wxEXPAND);
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
    pro_panel_->Show(!simple);

    if (auto* bar = GetMenuBar(); bar != nullptr) {
        bar->Check(wxID_VIEW_LIST, simple);
        bar->Check(wxID_VIEW_DETAILS, !simple);
    }

    Layout();
    if (simple) {
        simple_panel_->Update();
    } else {
        pro_panel_->Update();
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
        ShowError(error);
    }

    if (simple_panel_->IsShown()) {
        simple_panel_->Update();
    } else {
        pro_panel_->Update();
    }

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
        ShowError(error);
        return;
    }
    RequestRefresh();
}

void MainFrame::RequestRefresh() {
    std::string error;
    if (!context_.RefreshFromService(&error)) {
        ShowError(error);
    }
    if (simple_panel_->IsShown()) {
        simple_panel_->Update();
    } else {
        pro_panel_->Update();
    }
}

void MainFrame::RequestSwitchNode(const std::string& node_id) {
    std::string error;
    if (!context_.ipc.SwitchNode(node_id, kConnectTimeout, &error)) {
        ShowError(error);
        return;
    }
    RequestRefresh();
}

void MainFrame::RequestNetworkMode(const std::string& mode) {
    std::string error;
    if (!context_.ipc.SetNetworkMode(mode, kConnectTimeout, &error)) {
        ShowError(error);
        return;
    }
    // 接入方式是用户偏好，立刻记住。
    context_.preferences.traffic_entry = mode;
    RequestRefresh();
}

void MainFrame::RequestCore(const std::string& core) {
    // 核心切换需要重建配置，因此走一次完整的重连（断开再用新核心连接）。
    std::string error;
    const bool was_connected = context_.runtime_state.IsConnected();
    if (was_connected && !context_.ipc.Stop(kConnectTimeout, &error)) {
        ShowError(error);
        return;
    }
    context_.preferences.core = core;
    if (was_connected && !context_.ipc.Start(kConnectTimeout, &error)) {
        ShowError(error);
        return;
    }
    RequestRefresh();
}

void MainFrame::RequestRoutingPolicy(const std::string& policy) {
    context_.preferences.routing_policy = policy;
    // 策略变化需要重新编译配置，因此与服务端的下一次 Start 一起生效。
    // 这里只记录选择，并如实提示需要重新连接。
    SetStatusText(wxString::FromUTF8(context_.T("ui.status.reconnect_for_policy")));
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
        // wx 3.2 的 API 是 SetDevelopers（复数）：它接受一组开发者，
        // 传入单个字符串会隐式构造一个元素的数组。
        info.SetDevelopers(wxString::FromUTF8(context_.config.publisher));
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
