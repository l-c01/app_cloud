#include "ui/simple_panel.hpp"

#include <wx/statline.h>
#include <functional>
#include <utility>

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

}  // namespace

SimplePanel::SimplePanel(wxWindow* parent, app::AppContext& context)
    : wxPanel(parent, wxID_ANY), context_(context) {
    BuildLayout();
    ApplyTexts();
    Update();
}

void SimplePanel::BuildLayout() {
    auto* root = new wxBoxSizer(wxVERTICAL);

    root->AddSpacer(24);

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

    root->AddSpacer(24);

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

    // 按钮文本随状态变化。
    connect_button_->SetLabel(wxString::FromUTF8(
        context_.T(connected ? "ui.action.disconnect" : "ui.action.connect")));

    // 没有配置面板地址时，打开网页按钮应当是禁用的，而不是点了没反应。
    open_panel_button_->Enable(!context_.config.web_base_url.empty());
    open_panel_button_->SetLabel(wxString::FromUTF8(context_.T("ui.action.open_panel")));

    Layout();
}

}  // namespace pcx::ui
