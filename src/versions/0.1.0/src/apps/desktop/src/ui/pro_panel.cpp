#include "ui/pro_panel.hpp"

#include <wx/clipbrd.h>
#include <wx/statline.h>
#include <cstddef>
#include <functional>
#include <utility>
#include <vector>

namespace pcx::ui {

namespace {

// kMaxLogLines 是日志视图保留的行数上限。
//
// wxTextCtrl 在行数很多时会明显变慢；界面不需要无限历史，
// 完整日志在服务端。
constexpr int kMaxLogLines = 2000;

std::string StateKeyOf(const std::string& state) {
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

int IndexOf(const std::vector<std::string>& values, const std::string& wanted) {
    for (std::size_t i = 0; i < values.size(); ++i) {
        if (values[i] == wanted) {
            return static_cast<int>(i);
        }
    }
    // -1 表示未找到：调用方据此保持原选择，而不是跳到第 0 项。
    // 跳到第 0 项会让「服务端报了一个我不认识的核心」变成静默切换核心。
    return -1;
}

}  // namespace

ProPanel::ProPanel(wxWindow* parent, app::AppContext& context)
    : wxPanel(parent, wxID_ANY), context_(context) {
    BuildLayout();
    PopulateStaticChoices();
    Update();
}

void ProPanel::BuildLayout() {
    auto* root = new wxBoxSizer(wxVERTICAL);

    // ---- 顶部：状态与主操作 ----
    auto* top = new wxBoxSizer(wxHORIZONTAL);

    auto* status_column = new wxBoxSizer(wxVERTICAL);
    status_text_ = new wxStaticText(this, wxID_ANY, wxEmptyString);
    {
        wxFont font = status_text_->GetFont();
        font.SetPointSize(font.GetPointSize() + 3);
        font.SetWeight(wxFONTWEIGHT_BOLD);
        status_text_->SetFont(font);
    }
    status_column->Add(status_text_, 0, wxBOTTOM, 4);

    detail_text_ = new wxStaticText(this, wxID_ANY, wxEmptyString);
    status_column->Add(detail_text_, 0);

    top->Add(status_column, 1, wxALL | wxALIGN_CENTER_VERTICAL, 8);

    connect_button_ = new wxButton(this, wxID_ANY, wxEmptyString);
    top->Add(connect_button_, 0, wxALL | wxALIGN_CENTER_VERTICAL, 8);

    refresh_button_ = new wxButton(this, wxID_ANY, wxEmptyString);
    top->Add(refresh_button_, 0, wxALL | wxALIGN_CENTER_VERTICAL, 8);

    root->Add(top, 0, wxEXPAND);

    // 能力未实测的提示：单独一行，且默认隐藏。
    warning_text_ = new wxStaticText(this, wxID_ANY, wxEmptyString);
    root->Add(warning_text_, 0, wxEXPAND | wxLEFT | wxRIGHT, 8);

    root->Add(new wxStaticLine(this), 0, wxEXPAND | wxALL, 8);

    // ---- 中部：三个选择项 ----
    auto* options = new wxFlexGridSizer(2, 2, 8, 12);

    options->Add(new wxStaticText(this, wxID_ANY, wxEmptyString), 0,
                 wxALIGN_CENTER_VERTICAL);   // 占位，文本在 ApplyTexts 里统一设置

    // 为三组标签各建一个静态文本并保存，便于语言切换时更新。
    auto* core_label = new wxStaticText(this, wxID_ANY, wxEmptyString);
    core_choice_ = new wxChoice(this, wxID_ANY);
    auto* mode_label = new wxStaticText(this, wxID_ANY, wxEmptyString);
    mode_choice_ = new wxChoice(this, wxID_ANY);
    auto* policy_label = new wxStaticText(this, wxID_ANY, wxEmptyString);
    policy_choice_ = new wxChoice(this, wxID_ANY);

    options->Add(core_label, 0, wxALIGN_CENTER_VERTICAL);
    options->Add(core_choice_, 1, wxEXPAND);
    options->Add(mode_label, 0, wxALIGN_CENTER_VERTICAL);
    options->Add(mode_choice_, 1, wxEXPAND);
    options->Add(policy_label, 0, wxALIGN_CENTER_VERTICAL);
    options->Add(policy_choice_, 1, wxEXPAND);

    // 记录标签控件，供 Update 时按当前语言刷新。
    //
    // 这里存**完整的文案键**而不是 "label.core" 这样的片段：
    // 片段需要在别处拼上 "ui." 前缀，那样的拼接让自动校验无法确认
    // 这些键真的存在——任何 ui.* 键都会显得「被引用了」。
    core_label->SetName("ui.label.core");
    mode_label->SetName("ui.label.network_mode");
    policy_label->SetName("ui.label.routing_policy");

    root->Add(options, 0, wxEXPAND | wxLEFT | wxRIGHT, 8);

    root->AddSpacer(8);

    // ---- 节点列表与日志：左右分栏 ----
    auto* middle = new wxBoxSizer(wxHORIZONTAL);

    auto* node_column = new wxBoxSizer(wxVERTICAL);
    auto* node_title = new wxStaticText(this, wxID_ANY, wxEmptyString);
    node_title->SetName("ui.label.node_list");
    node_column->Add(node_title, 0, wxBOTTOM, 4);
    node_list_ = new wxListBox(this, wxID_ANY, wxDefaultPosition, wxSize(260, 220));
    node_column->Add(node_list_, 1, wxEXPAND);
    middle->Add(node_column, 0, wxEXPAND | wxALL, 8);

    auto* log_column = new wxBoxSizer(wxVERTICAL);
    auto* log_header = new wxBoxSizer(wxHORIZONTAL);
    auto* log_title = new wxStaticText(this, wxID_ANY, wxEmptyString);
    log_title->SetName("ui.label.logs");
    log_header->Add(log_title, 1, wxALIGN_CENTER_VERTICAL);
    copy_logs_button_ = new wxButton(this, wxID_ANY, wxEmptyString);
    log_header->Add(copy_logs_button_, 0);
    log_column->Add(log_header, 0, wxEXPAND | wxBOTTOM, 4);
    log_view_ = new wxTextCtrl(this, wxID_ANY, wxEmptyString, wxDefaultPosition,
                               wxSize(420, 220),
                               wxTE_MULTILINE | wxTE_READONLY | wxTE_DONTWRAP);
    log_column->Add(log_view_, 1, wxEXPAND);
    middle->Add(log_column, 1, wxEXPAND | wxALL, 8);

    root->Add(middle, 1, wxEXPAND);

    SetSizer(root);

    // ---- 事件绑定 ----
    connect_button_->Bind(wxEVT_BUTTON, [this](wxCommandEvent&) {
        if (connect_handler_ == nullptr) {
            return;
        }
        connect_handler_(!context_.runtime_state.IsConnected());
    });

    refresh_button_->Bind(wxEVT_BUTTON, [this](wxCommandEvent&) {
        if (refresh_handler_ != nullptr) {
            refresh_handler_();
        }
    });

    node_list_->Bind(wxEVT_LISTBOX, [this](wxCommandEvent& event) {
        if (suppress_events_ || switch_node_handler_ == nullptr) {
            return;
        }
        const int index = event.GetSelection();
        if (index < 0 || static_cast<std::size_t>(index) >= node_ids_.size()) {
            return;
        }
        switch_node_handler_(node_ids_[static_cast<std::size_t>(index)]);
    });

    core_choice_->Bind(wxEVT_CHOICE, [this](wxCommandEvent& event) {
        if (suppress_events_ || core_handler_ == nullptr) {
            return;
        }
        const int index = event.GetSelection();
        if (index < 0 || static_cast<std::size_t>(index) >= core_values_.size()) {
            return;
        }
        core_handler_(core_values_[static_cast<std::size_t>(index)]);
    });

    mode_choice_->Bind(wxEVT_CHOICE, [this](wxCommandEvent& event) {
        if (suppress_events_ || network_mode_handler_ == nullptr) {
            return;
        }
        const int index = event.GetSelection();
        if (index < 0 || static_cast<std::size_t>(index) >= mode_values_.size()) {
            return;
        }
        network_mode_handler_(mode_values_[static_cast<std::size_t>(index)]);
    });

    policy_choice_->Bind(wxEVT_CHOICE, [this](wxCommandEvent& event) {
        if (suppress_events_ || policy_handler_ == nullptr) {
            return;
        }
        const int index = event.GetSelection();
        if (index < 0 || static_cast<std::size_t>(index) >= policy_values_.size()) {
            return;
        }
        policy_handler_(policy_values_[static_cast<std::size_t>(index)]);
    });

    copy_logs_button_->Bind(wxEVT_BUTTON, [this](wxCommandEvent&) {
        // 复制到剪贴板。日志在进入视图前已经脱敏，
        // 这里不再做二次处理——但也不承诺它可以直接公开发布。
        if (wxTheClipboard->Open()) {
            wxTheClipboard->SetData(new wxTextDataObject(log_view_->GetValue()));
            wxTheClipboard->Close();
        }
    });
}

void ProPanel::PopulateStaticChoices() {
    // 接入方式：桌面端可选系统代理、PAC、仅本地端口、TUN。
    // 具体哪些可用由服务端能力决定，见 Update。
    mode_values_ = {app::traffic_entry::kSystemProxy, app::traffic_entry::kPac,
                    app::traffic_entry::kLocalProxy, app::traffic_entry::kTun};
    for (const auto& value : mode_values_) {
        // 用 ui.entry.* 而不是 ui.mode.*：后者已经用于界面模式
        // （简单/专业），两者是完全不同的概念，共用前缀必然混淆。
        mode_choice_->Append(wxString::FromUTF8(context_.T("ui.entry." + value)));
    }

    // 路由策略
    policy_values_ = {"rule", "global", "direct"};
    for (const auto& value : policy_values_) {
        policy_choice_->Append(wxString::FromUTF8(context_.T("ui.policy." + value)));
    }
}

void ProPanel::SetConnectHandler(std::function<void(bool)> handler) {
    connect_handler_ = std::move(handler);
}

void ProPanel::SetRefreshHandler(std::function<void()> handler) {
    refresh_handler_ = std::move(handler);
}

void ProPanel::SetSwitchNodeHandler(std::function<void(const std::string&)> handler) {
    switch_node_handler_ = std::move(handler);
}

void ProPanel::SetNetworkModeHandler(std::function<void(const std::string&)> handler) {
    network_mode_handler_ = std::move(handler);
}

void ProPanel::SetCoreHandler(std::function<void(const std::string&)> handler) {
    core_handler_ = std::move(handler);
}

void ProPanel::SetRoutingPolicyHandler(std::function<void(const std::string&)> handler) {
    policy_handler_ = std::move(handler);
}

void ProPanel::SetNodeList(const std::vector<std::string>& node_ids,
                           const std::vector<std::string>& node_labels) {
    node_ids_ = node_ids;
    node_labels_ = node_labels;

    // 重建列表时抑制事件：否则 Clear 与 Append 会触发选择变化，
    // 被当成用户操作而发一次切换节点的请求。
    suppress_events_ = true;
    node_list_->Clear();
    for (const auto& label : node_labels_) {
        node_list_->Append(wxString::FromUTF8(label));
    }
    suppress_events_ = false;
}

void ProPanel::AppendLog(const std::string& line) {
    log_view_->AppendText(wxString::FromUTF8(line) + "\n");
    // 超过上限时清掉最早的部分，避免界面越用越慢。
    if (log_view_->GetNumberOfLines() > kMaxLogLines) {
        const long excess = log_view_->GetNumberOfLines() - kMaxLogLines;
        log_view_->Remove(0, log_view_->XYToPosition(0, excess));
    }
}

void ProPanel::ReportUnverifiedCapabilities() {
    // 能力表未实测时必须如实说明：界面里某些选项可能并不真的可用，
    // 假装它们可用比不显示更糟。
    if (!context_.has_capabilities) {
        warning_text_->SetLabel(wxEmptyString);
        return;
    }
    for (const auto& core : context_.capabilities.cores) {
        if (core.verified_against == "unverified") {
            warning_text_->SetLabel(wxString::FromUTF8(
                context_.T("ui.warn.unverified_capability", {core.name})));
            return;
        }
    }
    warning_text_->SetLabel(wxEmptyString);
}

void ProPanel::Update() {
    const auto& state = context_.runtime_state;
    const bool connected = state.IsConnected();

    // ---- 状态文本 ----
    const std::string key = StateKeyOf(state.state);
    status_text_->SetLabel(wxString::FromUTF8(
        key.empty() ? (state.state.empty() ? std::string("unknown") : state.state)
                    : context_.T(key)));

    // ---- 详情 ----
    std::string detail;
    if (state.NeedsAttention()) {
        if (!state.conflicts.empty()) {
            detail = context_.T("ui.detail.network_conflict",
                                {state.conflicts.front().key, state.conflicts.front().actual});
        } else if (!state.last_error.empty()) {
            detail = state.last_error;
        } else {
            detail = context_.T("ui.detail.needs_attention");
        }
    } else if (!context_.ServiceAvailable()) {
        detail = context_.T("ui.detail.service_unavailable");
    } else if (node_ids_.empty()) {
        // 没有节点时直说，而不是让用户对着空列表点「连接」。
        detail = context_.T("ui.detail.no_nodes");
    } else if (!state.last_error.empty() && !connected) {
        detail = state.last_error;
    } else {
        detail = context_.T("ui.detail.service_ok");
    }
    detail_text_->SetLabel(wxString::FromUTF8(detail));

    // ---- 按钮 ----
    connect_button_->SetLabel(wxString::FromUTF8(
        context_.T(connected ? "ui.action.disconnect" : "ui.action.connect")));
    refresh_button_->SetLabel(wxString::FromUTF8(context_.T("ui.action.refresh")));
    copy_logs_button_->SetLabel(wxString::FromUTF8(context_.T("ui.action.copy_logs")));

    // ---- 固定标签 ----
    for (wxWindow* child : GetChildren()) {
        const std::string name = child->GetName().ToStdString();
        if (name.rfind("ui.label.", 0) == 0) {
            child->SetLabel(wxString::FromUTF8(context_.T(name)));
        }
    }

    // ---- 核心选择：来自服务端能力 ----
    suppress_events_ = true;
    if (context_.has_capabilities && !context_.capabilities.cores.empty()) {
        std::vector<std::string> values;
        wxArrayString labels;
        for (const auto& core : context_.capabilities.cores) {
            values.push_back(core.name);
            std::string label = core.name;
            if (!core.installed) {
                // 未安装的核心标明状态，而不是让它看起来可用。
                label += context_.T("ui.label.not_installed");
            }
            labels.Add(wxString::FromUTF8(label));
        }
        if (values != core_values_) {
            core_values_ = values;
            core_choice_->Clear();
            core_choice_->Append(labels);
        }
        const int index = IndexOf(core_values_, state.core.empty() ? core_values_.front()
                                                                  : state.core);
        if (index >= 0) {
            core_choice_->SetSelection(index);
        }
        core_choice_->Enable(true);
    } else {
        core_choice_->Enable(false);
    }

    // ---- 接入方式：只启用服务端声明支持的 ----
    if (context_.has_capabilities) {
        const auto& supported = context_.capabilities.traffic_entries;
        const int index = IndexOf(mode_values_, context_.preferences.traffic_entry);
        if (index >= 0) {
            // 当前取值不被支持时**不改动选择**，只是禁用控件；
            // 静默切换到别的接入方式会让用户以为自己的设置生效了。
            bool allowed = false;
            for (const auto& entry : supported) {
                if (entry == mode_values_[static_cast<std::size_t>(index)]) {
                    allowed = true;
                    break;
                }
            }
            mode_choice_->SetSelection(index);
            mode_choice_->Enable(allowed);
        } else {
            mode_choice_->Enable(false);
        }
    }

    // ---- 路由策略 ----
    {
        const int index = IndexOf(policy_values_, context_.preferences.routing_policy);
        if (index >= 0) {
            policy_choice_->SetSelection(index);
        }
    }

    // ---- 节点列表选中项 ----
    if (!node_ids_.empty() && !state.active_node.empty()) {
        const int index = IndexOf(node_ids_, state.active_node);
        if (index >= 0) {
            node_list_->SetSelection(index);
        }
    }
    suppress_events_ = false;

    ReportUnverifiedCapabilities();
    Layout();
}

}  // namespace pcx::ui
