// 登录页 / login view.
//
// 它是**主界面的前置页**：未登录时只显示这一页。理由不是「好看」，
// 而是界面上的绝大多数操作在没有会话时无法工作（列不出节点、改不了选项），
// 让用户面对一个全是禁用按钮的主界面，比直接让他登录更难理解。
//
// 关于密码的三条硬约束（都由这里保证）：
//   1. 密码只在内存里存在到「发起登录」为止，随后立刻清空输入框的缓冲区；
//   2. 界面**不把密码写进 preferences**——是否持久化由服务端的
//      rememberPassword 决定，界面不持有第二份副本；
//   3. 密码绝不进日志或诊断输出（AppendLog 不接受任何来自本页的文本）。
#pragma once

#include <wx/wxprec.h>

#ifndef WX_PRECOMP
#include <wx/wx.h>
#include <functional>
#endif

#include <string>

#include "app/app_context.hpp"
#include "core/ipc_protocol.hpp"

namespace pcx::ui {

class LoginPanel : public wxPanel {
public:
    LoginPanel(wxWindow* parent, app::AppContext& context);

    // Update 按最新状态刷新界面。
    void Update();

    // DisplayError 显示一条错误。
    //
    // 传入的字符串有两种来源：服务端错误的 messageKey（可本地化），
    // 或传输层的英文描述。前者会被翻译，后者原样显示——
    // 假装能翻译一句任意英文只会让用户看到一条乱码。
    void DisplayError(const std::string& message);

    // ClearError 清掉错误行。发起登录前调用。
    void ClearError();

    // SetBusy 设置「登录进行中」并同步按钮可用性。
    //
    // 由主窗口调用：发布登录请求前置 true，结果回来置 false。
    // 面板自己不能在 Update 里复位——那会让 500ms 的轮询把按钮
    // 在请求返回之前重新启用。
    void SetBusy(bool busy);

    // SetSignInHandler 由主窗口注入：真正的登录调用是主窗口的职责。
    void SetSignInHandler(std::function<void(const ipc::LoginParams& params)> handler);

    // SetOpenPanelHandler 由主窗口注入：打开面板网页。
    void SetOpenPanelHandler(std::function<void()> handler);

private:
    void BuildLayout();
    void ApplyTexts();
    // RestorePanelUrl 用已有状态预填面板地址。
    void RestorePanelUrl();
    // CollectParams 读取表单，构造登录参数。
    ipc::LoginParams CollectParams() const;

    app::AppContext& context_;

    wxStaticText* heading_text_ = nullptr;
    wxStaticText* hint_text_ = nullptr;
    wxStaticText* warning_text_ = nullptr;
    wxStaticText* error_text_ = nullptr;
    wxStaticText* secure_text_ = nullptr;

    wxTextCtrl* panel_url_input_ = nullptr;
    wxTextCtrl* email_input_ = nullptr;
    wxTextCtrl* password_input_ = nullptr;
    wxCheckBox* remember_checkbox_ = nullptr;
    wxCheckBox* insecure_checkbox_ = nullptr;
    wxButton* sign_in_button_ = nullptr;
    wxButton* open_panel_button_ = nullptr;

    std::function<void(const ipc::LoginParams&)> sign_in_handler_;
    std::function<void()> open_panel_handler_;

    // busy_ 表示已发起登录但结果未回；期间按钮禁用，避免重复提交。
    // 重复提交的后果不只是浪费一次请求：面板侧可能计入失败次数而限流。
    bool busy_ = false;
};

}  // namespace pcx::ui
