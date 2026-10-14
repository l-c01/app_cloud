// 专业模式面板 / professional-mode panel.
//
// 相对简单模式，这里展开全部四个独立维度（设计基线 §4.1）：
//   界面模式 / 流量接入方式 / 路由策略 / Android 应用范围（桌面端不涉及后者）
//
// 但**不引入任何简单模式没有的状态**：两者读写同一份 AppContext。
#pragma once

#include <wx/wxprec.h>

#ifndef WX_PRECOMP
#include <wx/wx.h>
#endif

#include <functional>
#include <string>
#include <vector>

#include "app/app_context.hpp"

namespace pcx::ui {

class ProPanel : public wxPanel {
public:
    ProPanel(wxWindow* parent, app::AppContext& context);

    // Update 按最新状态刷新界面。
    void Update();

    // SetNodeList 更新可选节点。
    void SetNodeList(const std::vector<std::string>& node_ids,
                     const std::vector<std::string>& node_labels);

    // AppendLog 追加一行日志（应当已经是脱敏后的文本）。
    void AppendLog(const std::string& line);

    void SetConnectHandler(std::function<void(bool connect)> handler);
    void SetRefreshHandler(std::function<void()> handler);
    void SetSwitchNodeHandler(std::function<void(const std::string& node_id)> handler);
    void SetNetworkModeHandler(std::function<void(const std::string& mode)> handler);
    void SetRoutingPolicyHandler(std::function<void(const std::string& policy)> handler);

private:
    void BuildLayout();
    void PopulateStaticChoices();
    void ReportUnverifiedCapabilities();

    app::AppContext& context_;

    wxStaticText* status_text_ = nullptr;
    wxStaticText* detail_text_ = nullptr;
    wxStaticText* warning_text_ = nullptr;

    wxButton* connect_button_ = nullptr;
    wxButton* refresh_button_ = nullptr;

    wxListBox* node_list_ = nullptr;
    wxChoice* mode_choice_ = nullptr;
    wxChoice* policy_choice_ = nullptr;
    wxTextCtrl* log_view_ = nullptr;
    wxButton* copy_logs_button_ = nullptr;

    std::function<void(bool)> connect_handler_;
    std::function<void()> refresh_handler_;
    std::function<void(const std::string&)> switch_node_handler_;
    std::function<void(const std::string&)> network_mode_handler_;
    std::function<void(const std::string&)> policy_handler_;

    // 选择控件的取值与其显示名一一对应；按下标取回取值。
    std::vector<std::string> mode_values_;
    std::vector<std::string> policy_values_;

    // 节点列表的稳定 ID 与显示名。
    std::vector<std::string> node_ids_;
    std::vector<std::string> node_labels_;

    // suppress_events_ 在程序化设置选择项时置位，避免把程序自己的改动
    // 当成用户操作再发一次请求（那会导致「切换接入方式」被触发两次）。
    bool suppress_events_ = false;
};

}  // namespace pcx::ui
