#include "ui/simple_panel.hpp"

#include <wx/statline.h>

#include <cstddef>
#include <functional>
#include <utility>
#include <vector>

#include "core/options.hpp"
#include "ui/ui_format.hpp"

namespace pcx::ui {

namespace {

// 状态码到文案键的映射。
//
// 取值来自 coordinator 的状态机；这里**只映射认识的**，
// 不认识的状态原样显示状态码——把未知状态强行显示成「未知」会丢掉排查线索。
std::string StateKey(const std::string& state) {
    if (state == "disconnected" || state.empty()) {
        return "ui.state.disconnected";
    }
    if (state == "connected") {
        return "ui.state.connected";
    }
    if (state == "connecting" || state == "preparing_network" ||
        state == "starting_core" || state == "validating") {
        return "ui.state.connecting";
    }
    if (state == "disconnecting") {
        return "ui.state.disconnecting";
    }
    if (state == "recovery_required") {
        return "ui.state.recovery_required";
    }
    if (state == "failed") {
        return "ui.state.failed";
    }
    return {};
}

// IndexOf 返回取值在列表中的下标；找不到返回 -1。
int IndexOf(const std::vector<std::string>& values, const std::string& wanted) {
    for (std::size_t i = 0; i < values.size(); ++i) {
        if (values[i] == wanted) {
            return static_cast<int>(i);
        }
    }
    // -1 表示未找到：调用方据此**保持原选择**，而不是跳到第 0 项。
    // 跳到「规则分流」会让服务端报了一个我不认识的取值变成静默改用别的模式。
    return -1;
}

}  // namespace

SimplePanel::SimplePanel(wxWindow* parent, app::AppContext& context)
    : wxPanel(parent, wxID_ANY), context_(context) {
    BuildLayout();
    ApplyTexts();
    Update();
}

void SimplePanel::BuildLayout() {
    auto* root = new wxBoxSizer(wxVERTICAL);

    root->AddSpacer(20);

    // 状态：这是用户唯一必须看懂的东西，因此字号明显更大。
    status_text_ = new wxStaticText(this, wxID_ANY, wxEmptyString);
    {
        wxFont font = status_text_->GetFont();
        font.SetPointSize(font.GetPointSize() + 6);
        font.SetWeight(wxFONTWEIGHT_BOLD);
        status_text_->SetFont(font);
    }
    root->Add(status_text_, 0, wxALIGN_CENTER_HORIZONTAL | wxLEFT | wxRIGHT, 24);

    root->AddSpacer(8);

    detail_text_ = new wxStaticText(this, wxID_ANY, wxEmptyString);
    {
        wxFont font = detail_text_->GetFont();
        font.SetPointSize(font.GetPointSize() + 1);
        detail_text_->SetFont(font);
    }
    root->Add(detail_text_, 0, wxALIGN_CENTER_HORIZONTAL | wxLEFT | wxRIGHT, 24);

    root->AddSpacer(12);

    node_text_ = new wxStaticText(this, wxID_ANY, wxEmptyString);
    root->Add(node_text_, 0, wxALIGN_CENTER_HORIZONTAL | wxLEFT | wxRIGHT, 24);

    // ---- 账户与用量 ----
    //
    // 放在状态下面而不是单独一页：简易模式的定位是「一屏」，
    // 用户需要知道「我是谁、还剩多少流量」，但不需要为此翻页。
    session_text_ = new wxStaticText(this, wxID_ANY, wxEmptyString);
    root->Add(session_text_, 0, wxALIGN_CENTER_HORIZONTAL | wxLEFT | wxRIGHT, 24);

    session_warning_text_ = new wxStaticText(this, wxID_ANY, wxEmptyString);
    session_warning_text_->SetForegroundColour(wxColour(160, 90, 0));
    root->Add(session_warning_text_, 0, wxALIGN_CENTER_HORIZONTAL | wxLEFT | wxRIGHT, 24);

    root->AddSpacer(16);

    // ---- 路由模式 ----
    //
    // 每平台都有的一项。默认「规则分流」由服务端与出厂配置决定，
    // 界面这里只负责显示与提交。
    //
    // 选项必须在**构造时**一次给全：wxRadioBox 没有 Append()
    // （只有 wxItemContainer 那一族才有）。先建空框再逐个 Append
    // 会直接编译失败，而错误落在 Append 上，看起来像用错了控件类型。
    //
    // 用 ui.policy.* 而不是新造一组文案：路由模式的取值与专业模式里的
    // 路由策略是同一组，两套翻译必然漂移成两句不同的话。
    routing_values_ = {domain::route_mode::kRule, domain::route_mode::kGlobal,
                       domain::route_mode::kDirect};
    wxArrayString routing_labels;
    for (const auto& value : routing_values_) {
        routing_labels.Add(wxString::FromUTF8(context_.T("ui.policy." + value)));
    }
    // 三个取值都必须能被一眼看见；下拉框会让「当前是直连」这种
    // 需要警惕的状态藏在一个合上的框里。
    routing_box_ = new wxRadioBox(this, wxID_ANY, wxEmptyString, wxDefaultPosition,
                                  wxDefaultSize, routing_labels, 1, wxRA_SPECIFY_COLS);
    root->Add(routing_box_, 0, wxEXPAND | wxLEFT | wxRIGHT, 24);

    routing_hint_text_ = new wxStaticText(this, wxID_ANY, wxEmptyString);
    routing_hint_text_->SetForegroundColour(wxColour(120, 80, 0));
    root->Add(routing_hint_text_, 0, wxEXPAND | wxLEFT | wxRIGHT, 24);

    root->AddSpacer(16);

    // 主操作按钮：整屏只有它是「有颜色」的，避免用户犹豫点哪个。
    connect_button_ = new wxButton(this, wxID_ANY, wxEmptyString);
    {
        wxFont font = connect_button_->GetFont();
        font.SetPointSize(font.GetPointSize() + 3);
        connect_button_->SetFont(font);
    }
    root->Add(connect_button_, 0, wxALIGN_CENTER_HORIZONTAL | wxLEFT | wxRIGHT, 24);

    root->AddSpacer(12);

    open_panel_button_ = new wxButton(this, wxID_ANY, wxEmptyString);
    root->Add(open_panel_button_, 0, wxALIGN_CENTER_HORIZONTAL | wxLEFT | wxRIGHT, 24);

    root->AddSpacer(12);

    // 操作结果（例如「已切换到节点 X」）。常驻控件、文本为空时不可见，
    // 避免每次提示都让整屏控件上下跳动。
    notice_text_ = new wxStaticText(this, wxID_ANY, wxEmptyString);
    root->Add(notice_text_, 0, wxALIGN_CENTER_HORIZONTAL | wxLEFT | wxRIGHT, 24);

    root->AddStretchSpacer(1);

    SetSizer(root);

    connect_button_->Bind(wxEVT_BUTTON, [this](wxCommandEvent&) {
        if (busy_ || connect_handler_ == nullptr) {
            return;
        }
        const bool should_connect = !context_.runtime_state.IsConnected();
        busy_ = true;
        connect_button_->Enable(false);
        connect_handler_(should_connect);
    });

    open_panel_button_->Bind(wxEVT_BUTTON, [this](wxCommandEvent&) {
        if (open_panel_handler_ != nullptr) {
            open_panel_handler_();
        }
    });

    routing_box_->Bind(wxEVT_RADIOBOX, [this](wxCommandEvent& event) {
        if (suppress_events_ || routing_mode_handler_ == nullptr) {
            return;
        }
        const int index = event.GetSelection();
        if (index < 0 || static_cast<std::size_t>(index) >= routing_values_.size()) {
            return;
        }
        routing_mode_handler_(routing_values_[static_cast<std::size_t>(index)]);
    });
}

void SimplePanel::ApplyTexts() {
    SetName(wxString::FromUTF8(context_.T("ui.mode.simple")));
    // 文案在 Update 里统一刷新，这里只处理创建期需要的固定文本。
}

void SimplePanel::SetConnectHandler(std::function<void(bool)> handler) {
    connect_handler_ = std::move(handler);
}

void SimplePanel::SetOpenPanelHandler(std::function<void()> handler) {
    open_panel_handler_ = std::move(handler);
}

void SimplePanel::SetRoutingModeHandler(
    std::function<void(const std::string&)> handler) {
    routing_mode_handler_ = std::move(handler);
}

void SimplePanel::SetStatus(const std::string& text) {
    notice_text_->SetLabel(wxString::FromUTF8(text));
    Layout();
}

void SimplePanel::RefreshSessionText() {
    // 未登录时不显示账户区块：显示「邮箱：」后面空着一格，
    // 看起来像界面坏了，而真实情况是还没登录（登录页会说明这一点）。
    if (!context_.has_session || !context_.session.logged_in) {
        session_text_->SetLabel(wxEmptyString);
        session_warning_text_->SetLabel(wxEmptyString);
        return;
    }

    std::string combined;
    for (const auto& line : SessionSummary(context_)) {
        if (!combined.empty()) {
            // 简易模式用换行而不是项目符号：这一屏已经有一个大状态文字，
            // 再加符号会让画面看起来比实际信息量更重。
            combined += "\n";
        }
        combined += line;
    }
    session_text_->SetLabel(wxString::FromUTF8(combined));

    const std::string warning = SessionWarning(context_);
    session_warning_text_->SetLabel(wxString::FromUTF8(warning));
}

void SimplePanel::Update() {
    const auto& state = context_.runtime_state;
    const bool connected = state.IsConnected();
    // 请求进行中时按钮保持禁用，直到状态从服务端回来。
    busy_ = false;
    connect_button_->Enable(true);

    // 状态文本
    std::string key = StateKey(state.state);
    if (key.empty()) {
        // 未知状态原样显示：把排查线索藏起来只会让问题更难定位。
        status_text_->SetLabel(wxString::FromUTF8(state.state.empty() ? "unknown" : state.state));
    } else {
        status_text_->SetLabel(wxString::FromUTF8(context_.T(key)));
    }

    // 详情：连接中显示节点，出错显示原因，正常时留空。
    std::string detail;
    if (state.NeedsAttention()) {
        if (!state.conflicts.empty()) {
            // 冲突必须说清「哪里不一致」，只说「有冲突」用户无法处理。
            detail = context_.T("ui.detail.network_conflict",
                                {state.conflicts.front().key, state.conflicts.front().actual});
        } else if (!state.last_error.empty()) {
            detail = state.last_error;
        } else {
            detail = context_.T("ui.detail.needs_attention");
        }
    } else if (!context_.ServiceAvailable()) {
        // 服务没起来时明确说出来，而不是显示「未连接」让人以为是代理没连。
        detail = context_.T("ui.detail.service_unavailable");
    } else if (context_.has_session && !context_.session.logged_in) {
        // 未登录时的「未连接」原因与代理无关，直说。
        detail = context_.T("ui.detail.not_signed_in");
    } else if (!state.last_error.empty() && !connected) {
        detail = state.last_error;
    }
    detail_text_->SetLabel(wxString::FromUTF8(detail));

    // 节点
    if (state.active_node.empty()) {
        node_text_->SetLabel(wxString::FromUTF8(context_.T("ui.label.no_node")));
    } else {
        node_text_->SetLabel(wxString::FromUTF8(
            context_.T("ui.label.current_node", {state.active_node})));
    }

    RefreshSessionText();

    // 按钮文本随状态变化。
    connect_button_->SetLabel(wxString::FromUTF8(
        context_.T(connected ? "ui.action.disconnect" : "ui.action.connect")));

    // 没有配置面板地址时，打开网页按钮应当是禁用的，而不是点了没反应。
    open_panel_button_->Enable(!context_.config.web_base_url.empty());
    open_panel_button_->SetLabel(wxString::FromUTF8(context_.T("ui.action.open_panel")));

    // ---- 路由模式 ----
    //
    // 取值来源按优先级：服务端选项模型 > 本地偏好。
    // 选项模型才是权威——用户可能在专业模式里刚改过它。
    {
        std::string current = context_.preferences.routing_policy;
        if (context_.has_options && !context_.options.route_mode.empty()) {
            current = context_.options.route_mode;
        }

        suppress_events_ = true;
        const int index = IndexOf(routing_values_, current);
        if (index >= 0) {
            routing_box_->SetSelection(index);
        }
        suppress_events_ = false;

        // 选中「直连」时必须明确提示（ui-options.md 1 节）：
        // 它的表现是「连上了但打不开墙外网站」，那不是故障，
        // 是这一项的字面含义。
        if (index >= 0 && routing_values_[static_cast<std::size_t>(index)] ==
                              domain::route_mode::kDirect) {
            routing_hint_text_->SetLabel(
                wxString::FromUTF8(context_.T("ui.hint.direct_mode")));
        } else {
            routing_hint_text_->SetLabel(wxEmptyString);
        }

        routing_box_->SetLabel(wxString::FromUTF8(context_.T("ui.label.route_mode")));
        // 服务不可用时不允许改动：改了什么都不会生效，
        // 而用户会以为已经改好了。
        routing_box_->Enable(context_.ServiceAvailable());
    }

    Layout();
}

}  // namespace pcx::ui
