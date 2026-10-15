#include "ui/login_panel.hpp"

#include <wx/statline.h>

#include <string>
#include <utility>

#include "ui/ui_format.hpp"

namespace pcx::ui {

namespace {

// IsKnownErrorKey 判断一个字符串是不是**本界面认识**的会话错误键。
//
// 为什么要判断：服务端返回的 messageKey 可能是本客户端还没有翻译的键。
// 直接把它交给 T() 会显示键名本身（例如 session.error.rate_limited），
// 用户看到的一行像变量名，那比一句英文原文更糟。
//
// 判据是「界面自己的文案表里有没有这个键」，而不是一张手写的键清单：
// 清单会在每次新增文案时被忘掉。文案仍然只在 locales/*.json 里有一份。
bool IsKnownErrorKey(const app::AppContext& context, const std::string& key) {
    if (!LooksLikeMessageKey(key)) {
        return false;
    }
    // T() 在缺键时返回键名本身，因此「翻译结果与键不同」就等于键存在。
    return context.T(key) != key;
}

}  // namespace

LoginPanel::LoginPanel(wxWindow* parent, app::AppContext& context)
    : wxPanel(parent, wxID_ANY), context_(context) {
    BuildLayout();
    ApplyTexts();
    Update();
}

void LoginPanel::BuildLayout() {
    auto* root = new wxBoxSizer(wxVERTICAL);

    root->AddSpacer(24);

    heading_text_ = new wxStaticText(this, wxID_ANY, wxEmptyString);
    {
        wxFont font = heading_text_->GetFont();
        font.SetPointSize(font.GetPointSize() + 5);
        font.SetWeight(wxFONTWEIGHT_BOLD);
        heading_text_->SetFont(font);
    }
    root->Add(heading_text_, 0, wxALIGN_CENTER_HORIZONTAL | wxLEFT | wxRIGHT, 24);

    root->AddSpacer(6);

    hint_text_ = new wxStaticText(this, wxID_ANY, wxEmptyString);
    root->Add(hint_text_, 0, wxALIGN_CENTER_HORIZONTAL | wxLEFT | wxRIGHT, 24);

    root->AddSpacer(16);

    // 表单用两列表格：标签右对齐在左列，输入框在右列拉伸。
    // 用 FlexGridSizer 而不是手算像素：语言切换后标签宽度会变，
    // 固定像素的布局在另一种语言下必然错位。
    auto* form = new wxFlexGridSizer(2, 8, 12);
    form->AddGrowableCol(1, 1);

    auto* url_label = new wxStaticText(this, wxID_ANY, wxEmptyString);
    url_label->SetName("ui.login.panel_url");
    panel_url_input_ = new wxTextCtrl(this, wxID_ANY, wxEmptyString);
    form->Add(url_label, 0, wxALIGN_CENTER_VERTICAL);
    form->Add(panel_url_input_, 1, wxEXPAND);

    auto* email_label = new wxStaticText(this, wxID_ANY, wxEmptyString);
    email_label->SetName("ui.login.email");
    email_input_ = new wxTextCtrl(this, wxID_ANY, wxEmptyString);
    form->Add(email_label, 0, wxALIGN_CENTER_VERTICAL);
    form->Add(email_input_, 1, wxEXPAND);

    auto* password_label = new wxStaticText(this, wxID_ANY, wxEmptyString);
    password_label->SetName("ui.login.password");
    // wxTE_PASSWORD 掩码显示密码，避免旁观者直接读到。
    // 不做「显示密码」按钮：那是一个方便的肩窥入口，
    // 而真正需要核对密码的场景（粘贴失败、输错字符）很少。
    password_input_ = new wxTextCtrl(this, wxID_ANY, wxEmptyString, wxDefaultPosition,
                                     wxDefaultSize, wxTE_PASSWORD);
    form->Add(password_label, 0, wxALIGN_CENTER_VERTICAL);
    form->Add(password_input_, 1, wxEXPAND);

    root->Add(form, 0, wxEXPAND | wxLEFT | wxRIGHT, 24);

    root->AddSpacer(12);

    // 记住密码：默认**不勾**。
    //
    // 默认勾上等于替用户决定「把密码存在这台机器上」，
    // 而共用电脑的场景下那是他自己不会预料到的后果。
    remember_checkbox_ = new wxCheckBox(this, wxID_ANY, wxEmptyString);
    remember_checkbox_->SetValue(false);
    root->Add(remember_checkbox_, 0, wxLEFT | wxRIGHT, 24);

    insecure_checkbox_ = new wxCheckBox(this, wxID_ANY, wxEmptyString);
    insecure_checkbox_->SetValue(false);
    root->Add(insecure_checkbox_, 0, wxLEFT | wxRIGHT | wxTOP, 24);

    root->AddSpacer(12);

    secure_text_ = new wxStaticText(this, wxID_ANY, wxEmptyString);
    secure_text_->SetName("ui.warn.no_secure_storage");
    root->Add(secure_text_, 0, wxEXPAND | wxLEFT | wxRIGHT, 24);

    root->AddSpacer(8);

    warning_text_ = new wxStaticText(this, wxID_ANY, wxEmptyString);
    root->Add(warning_text_, 0, wxEXPAND | wxLEFT | wxRIGHT, 24);

    root->AddSpacer(8);

    // 错误行常驻，只是文本为空。动态增删控件会让对话框在每次失败时
    // 改变尺寸与按钮位置——用户正要再点一次按钮，而它刚好移开了。
    error_text_ = new wxStaticText(this, wxID_ANY, wxEmptyString);
    error_text_->SetForegroundColour(wxColour(180, 0, 0));
    root->Add(error_text_, 0, wxEXPAND | wxLEFT | wxRIGHT, 24);

    root->AddSpacer(16);

    auto* buttons = new wxBoxSizer(wxHORIZONTAL);
    sign_in_button_ = new wxButton(this, wxID_ANY, wxEmptyString);
    {
        wxFont font = sign_in_button_->GetFont();
        font.SetPointSize(font.GetPointSize() + 2);
        sign_in_button_->SetFont(font);
    }
    open_panel_button_ = new wxButton(this, wxID_ANY, wxEmptyString);
    buttons->Add(sign_in_button_, 0, wxRIGHT, 12);
    buttons->Add(open_panel_button_, 0);
    root->Add(buttons, 0, wxALIGN_CENTER_HORIZONTAL | wxLEFT | wxRIGHT, 24);

    root->AddStretchSpacer(1);

    SetSizer(root);

    sign_in_button_->Bind(wxEVT_BUTTON, [this](wxCommandEvent&) {
        if (busy_ || sign_in_handler_ == nullptr) {
            return;
        }
        ClearError();
        SetBusy(true);
        sign_in_handler_(CollectParams());
    });

    // 回车即提交：登录页只有三个输入框，用户按回车是唯一自然的动作。
    password_input_->Bind(wxEVT_TEXT_ENTER, [this](wxCommandEvent&) {
        if (busy_ || sign_in_handler_ == nullptr) {
            return;
        }
        ClearError();
        SetBusy(true);
        sign_in_handler_(CollectParams());
    });

    open_panel_button_->Bind(wxEVT_BUTTON, [this](wxCommandEvent&) {
        if (open_panel_handler_ != nullptr) {
            open_panel_handler_();
        }
    });
}

void LoginPanel::ApplyTexts() {
    SetName(wxString::FromUTF8(context_.T("ui.login.title")));
}

void LoginPanel::RestorePanelUrl() {
    // 地址来源的优先级：已有会话 > 上次读到的面板地址 > 出厂配置。
    //
    // 让用户重新手打一遍面板地址是没必要的负担，而打错一个字符的
    // 后果是一句「不是 Xboard 面板」——他不会想到是地址里少了个斜杠。
    if (!panel_url_input_->GetValue().IsEmpty()) {
        return;
    }
    std::string url;
    if (context_.has_session && !context_.session.panel_url.empty()) {
        url = context_.session.panel_url;
    }
    if (url.empty()) {
        url = context_.config.web_base_url;
    }
    if (!url.empty()) {
        panel_url_input_->SetValue(wxString::FromUTF8(url));
    }
}

ipc::LoginParams LoginPanel::CollectParams() const {
    ipc::LoginParams params;
    params.panel_url = std::string(panel_url_input_->GetValue().utf8_str());
    params.email = std::string(email_input_->GetValue().utf8_str());
    params.password = std::string(password_input_->GetValue().utf8_str());
    params.allow_insecure_http = insecure_checkbox_->IsChecked();
    params.remember_password = remember_checkbox_->IsChecked();
    return params;
}

void LoginPanel::SetSignInHandler(
    std::function<void(const ipc::LoginParams&)> handler) {
    sign_in_handler_ = std::move(handler);
}

void LoginPanel::SetOpenPanelHandler(std::function<void()> handler) {
    open_panel_handler_ = std::move(handler);
}

void LoginPanel::ClearError() { error_text_->SetLabel(wxEmptyString); }

void LoginPanel::SetBusy(bool busy) {
    busy_ = busy;
    sign_in_button_->Enable(!busy_);
}

void LoginPanel::DisplayError(const std::string& message) {
    // 无论成功失败，到这里都表示这一次登录已经结束。
    SetBusy(false);
    if (message.empty()) {
        ClearError();
        return;
    }
    // 认识的键就翻译；不认识的（传输层的英文描述）原样显示。
    if (IsKnownErrorKey(context_, message)) {
        error_text_->SetLabel(wxString::FromUTF8(context_.T(message)));
    } else {
        error_text_->SetLabel(wxString::FromUTF8(message));
    }
    Layout();
}

void LoginPanel::Update() {
    // 这里**不**重置 busy_。
    //
    // Update 由主窗口的 500ms 轮询调用；若在这里恢复按钮，一次登录
    // 还没返回就会被重新启用，用户可以再点一次。重复提交不只是浪费请求：
    // 面板侧会把连续失败计入限流。
    // busy_ 因此只在两处改变：发起登录时置位，登录返回时由
    // DisplayError / SetBusy 清除。
    sign_in_button_->Enable(!busy_);

    RestorePanelUrl();

    // ---- 固定文案 ----
    heading_text_->SetLabel(wxString::FromUTF8(context_.T("ui.login.title")));
    hint_text_->SetLabel(wxString::FromUTF8(context_.T("ui.login.hint")));
    remember_checkbox_->SetLabel(wxString::FromUTF8(context_.T("ui.login.remember")));
    insecure_checkbox_->SetLabel(
        wxString::FromUTF8(context_.T("ui.login.allow_insecure")));
    sign_in_button_->SetLabel(wxString::FromUTF8(context_.T("ui.login.sign_in")));
    open_panel_button_->SetLabel(wxString::FromUTF8(context_.T("ui.action.open_panel")));

    // 表单标签按控件名刷新，与 pro_panel 的做法一致。
    for (wxWindow* child : GetChildren()) {
        const std::string name = child->GetName().ToStdString();
        if (name.rfind("ui.", 0) == 0) {
            child->SetLabel(wxString::FromUTF8(context_.T(name)));
        }
    }

    // ---- 安全存储提示 ----
    //
    // has_session 为 false 时说明还没读到会话状态，此时**不能**断言
    // 「不会记住登录」：那是把「不知道」说成了「确定不」。
    if (context_.has_session && context_.session.RememberWarningRequired()) {
        secure_text_->SetLabel(
            wxString::FromUTF8(context_.T("ui.warn.no_secure_storage")));
    } else {
        secure_text_->SetLabel(wxEmptyString);
    }

    // ---- 面板侧警告 ----
    if (context_.has_session && context_.session.subscription.has_value() &&
        !context_.session.subscription->warnings.empty()) {
        const auto& warnings = context_.session.subscription->warnings;
        warning_text_->SetLabel(wxString::FromUTF8(warnings.front()));
    } else {
        warning_text_->SetLabel(wxEmptyString);
    }

    // 没有面板地址时打开网页按钮应当是禁用的，而不是点了没反应。
    open_panel_button_->Enable(!context_.config.web_base_url.empty());

    Layout();
}

}  // namespace pcx::ui
