#include "ui/node_list_panel.hpp"

#include <wx/statline.h>

#include <cstddef>
#include <string>
#include <utility>

namespace pcx::ui {

NodeListPanel::NodeListPanel(wxWindow* parent, app::AppContext& context)
    : wxPanel(parent, wxID_ANY), context_(context) {
    BuildLayout();
    Update();
}

void NodeListPanel::BuildLayout() {
    auto* root = new wxBoxSizer(wxVERTICAL);

    auto* header = new wxBoxSizer(wxHORIZONTAL);
    auto* title_column = new wxBoxSizer(wxVERTICAL);
    title_text_ = new wxStaticText(this, wxID_ANY, wxEmptyString);
    {
        wxFont font = title_text_->GetFont();
        font.SetPointSize(font.GetPointSize() + 2);
        font.SetWeight(wxFONTWEIGHT_BOLD);
        title_text_->SetFont(font);
    }
    title_column->Add(title_text_, 0, wxBOTTOM, 2);
    subject_text_ = new wxStaticText(this, wxID_ANY, wxEmptyString);
    title_column->Add(subject_text_, 0);
    header->Add(title_column, 1, wxALIGN_CENTER_VERTICAL);
    root->Add(header, 0, wxEXPAND | wxLEFT | wxRIGHT | wxTOP, 8);

    root->AddSpacer(6);

    // wxLB_SINGLE 是默认值，这里显式写出：多选在这个界面上没有意义，
    // 而 SelectNode 只接受一个节点。
    node_list_ = new wxListBox(this, wxID_ANY, wxDefaultPosition, wxSize(320, 260),
                               wxArrayString(), wxLB_SINGLE);
    root->Add(node_list_, 1, wxEXPAND | wxLEFT | wxRIGHT, 8);

    root->AddSpacer(6);

    status_text_ = new wxStaticText(this, wxID_ANY, wxEmptyString);
    root->Add(status_text_, 0, wxEXPAND | wxLEFT | wxRIGHT, 8);

    root->AddSpacer(6);

    auto* buttons = new wxBoxSizer(wxHORIZONTAL);
    select_button_ = new wxButton(this, wxID_ANY, wxEmptyString);
    refresh_button_ = new wxButton(this, wxID_ANY, wxEmptyString);
    buttons->Add(select_button_, 0, wxRIGHT, 8);
    buttons->Add(refresh_button_, 0);
    root->Add(buttons, 0, wxLEFT | wxRIGHT | wxBOTTOM, 8);

    SetSizer(root);

    // 双击（以及键盘回车）即选中：这是「点击即切换」在 wxWidgets 里的
    // 对应动作。单击仍然会触发命令事件，但许多平台上单击只是移动高亮，
    // 绑定它会让用户在浏览列表时就把节点切来切去。
    node_list_->Bind(wxEVT_LISTBOX_DCLICK, [this](wxCommandEvent& event) {
        if (suppress_events_ || select_node_handler_ == nullptr) {
            return;
        }
        const int index = event.GetSelection();
        if (index < 0 || static_cast<std::size_t>(index) >= context_.nodes.nodes.size()) {
            return;
        }
        ipc::SelectNodeParams params;
        params.id = context_.nodes.nodes[static_cast<std::size_t>(index)].id;
        select_node_handler_(params);
    });

    select_button_->Bind(wxEVT_BUTTON, [this](wxCommandEvent&) {
        if (select_node_handler_ == nullptr) {
            return;
        }
        const int index = node_list_->GetSelection();
        if (index == wxNOT_FOUND) {
            // 没选中任何一项时不给服务端发一个空请求：
            // 那会得到一条 invalid_params，而用户看到的是「操作失败」，
            // 真正的原因（你没选节点）反而没人告诉他。
            SetStatus(context_.T("ui.nodes.select_first"));
            return;
        }
        if (static_cast<std::size_t>(index) >= context_.nodes.nodes.size()) {
            return;
        }
        ipc::SelectNodeParams params;
        params.id = context_.nodes.nodes[static_cast<std::size_t>(index)].id;
        select_node_handler_(params);
    });

    refresh_button_->Bind(wxEVT_BUTTON, [this](wxCommandEvent&) {
        if (refresh_handler_ != nullptr) {
            refresh_handler_();
        }
    });
}

void NodeListPanel::SetSelectNodeHandler(
    std::function<void(const ipc::SelectNodeParams&)> handler) {
    select_node_handler_ = std::move(handler);
}

void NodeListPanel::SetRefreshHandler(std::function<void()> handler) {
    refresh_handler_ = std::move(handler);
}

void NodeListPanel::SetStatus(const std::string& text) {
    status_text_->SetLabel(wxString::FromUTF8(text));
    Layout();
}

void NodeListPanel::RefreshLabels() {
    title_text_->SetLabel(wxString::FromUTF8(context_.T("ui.nodes.title")));
    refresh_button_->SetLabel(wxString::FromUTF8(context_.T("ui.nodes.refresh")));
    select_button_->SetLabel(wxString::FromUTF8(context_.T("ui.nodes.select")));

    // 标题下面一行说清「现在用的是哪个节点」——只靠列表高亮不够：
    // 高亮在列表滚动出视野后就看不见了。
    std::string subject;
    const auto& nodes = context_.nodes.nodes;
    const int selected = context_.nodes.SelectedIndex();
    if (selected >= 0 && static_cast<std::size_t>(selected) < nodes.size()) {
        subject = context_.T("ui.nodes.selected",
                             {nodes[static_cast<std::size_t>(selected)].Label()});
    } else {
        subject = context_.T("ui.label.no_node");
    }
    subject_text_->SetLabel(wxString::FromUTF8(subject));
}

void NodeListPanel::Update() {
    RefreshLabels();

    const auto& nodes = context_.nodes.nodes;

    // 重建列表：条目数变化时逐条比对反而更容易出错。
    // 期间抑制事件，避免把程序自己的改动当成用户操作。
    suppress_events_ = true;
    // 记住当前选择，重建后尽量恢复：否则用户选中一项、
    // 而一次后台刷新会让他的选择跳回原处。
    const int previous = node_list_->GetSelection();
    node_list_->Clear();
    for (const auto& node : nodes) {
        node_list_->Append(wxString::FromUTF8(node.Label()));
    }
    suppress_events_ = false;

    const int selected = context_.nodes.SelectedIndex();
    if (selected >= 0) {
        node_list_->SetSelection(selected);
    } else if (previous >= 0 && static_cast<std::size_t>(previous) < nodes.size()) {
        node_list_->SetSelection(previous);
    }

    const bool usable = context_.ServiceAvailable();
    refresh_button_->Enable(usable);
    // 没有节点时禁用「选择」而不是点了报错。
    select_button_->Enable(usable && !nodes.empty());

    if (!usable) {
        SetStatus(context_.T("ui.detail.service_unavailable"));
    } else if (nodes.empty()) {
        // 空列表必须给出下一步：告诉用户去刷新订阅，
        // 而不是让他对着一个空框猜。
        SetStatus(context_.T("ui.nodes.empty"));
    }

    Layout();
}

}  // namespace pcx::ui
