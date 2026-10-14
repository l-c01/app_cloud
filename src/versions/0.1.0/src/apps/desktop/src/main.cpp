// 程序入口 / application entry point.
//
// 启动顺序刻意如此：
//   1. 语言资源（之后所有提示才能本地化）
//   2. 出厂配置（决定软件名、默认值）
//   3. 用户偏好（覆盖默认值）
//   4. 主窗口（此时才可能报错，而报错文案已经能本地化）
//
// 任何一步失败都**不阻止启动**：配置读不到、语言资源缺失、后台服务没起来，
// 用户都应该能看到界面并知道发生了什么。一个连窗口都打不开的代理客户端
// 让人无从下手。
#include <wx/wxprec.h>

#ifndef WX_PRECOMP
#include <wx/wx.h>
#endif

#include <wx/stdpaths.h>
#include <wx/snglinst.h>

#include <memory>
#include <string>
#include <vector>
#include <cstddef>
#include <utility>

#include "app/app_context.hpp"
#include "core/i18n.hpp"
#include "ui/main_frame.hpp"

namespace {

// StartupNotice 收集启动期间的问题，稍后一次性展示。
//
// 逐个弹窗会让用户连点几次「确定」才能用，且最后一个窗口之前的
// 内容已经被忘掉了。
struct StartupNotice {
    std::vector<std::string> messages;
    void Add(std::string message) {
        if (!message.empty()) {
            messages.push_back(std::move(message));
        }
    }
};

}  // namespace

class ProxyClientApp : public wxApp {
public:
    bool OnInit() override;

private:
    void LoadLocalisation(StartupNotice& notice);
    void LoadSettings(StartupNotice& notice);
    void ReportNotices(const StartupNotice& notice);

    std::unique_ptr<pcx::app::AppContext> context_;
    // 单实例锁必须在整个进程生命周期内存活，因此作为成员持有，
    // 不能是局部变量——局部变量析构即释放锁。
    std::unique_ptr<wxSingleInstanceChecker> instance_checker_;
};

wxIMPLEMENT_APP(ProxyClientApp);

bool ProxyClientApp::OnInit() {
    if (!wxApp::OnInit()) {
        return false;
    }

    context_ = std::make_unique<pcx::app::AppContext>();

    const wxString executable = wxStandardPaths::Get().GetExecutablePath();
    context_->app_dir = std::string(executable.utf8_str());

    // 先加载语言资源，之后的提示才能本地化。
    StartupNotice notice;
    LoadLocalisation(notice);
    LoadSettings(notice);

    // 单实例检查放在语言加载之后，是为了让这条提示能被本地化。
    //
    // 为什么必须阻止第二个实例：两个界面同时控制同一份系统网络必然互相打架
    // （一个写入代理设置、另一个恢复原始值），而用户很难意识到自己开了两个。
    instance_checker_ = std::make_unique<wxSingleInstanceChecker>("proxyclient-single-instance");
    if (instance_checker_->IsAnotherRunning()) {
        wxMessageBox(wxString::FromUTF8(context_->T("ui.error.already_running")),
                     wxString::FromUTF8(context_->config.DisplayName(context_->language)),
                     wxOK | wxICON_INFORMATION);
        return false;
    }

    // 勾选了「记住模式」时用持久化的模式，否则用出厂默认。
    if (!context_->preferences.remember_ui_mode) {
        context_->preferences.ui_mode = context_->config.default_ui_mode;
    }

    auto* frame = new pcx::ui::MainFrame(*context_);
    frame->Show(true);

    ReportNotices(notice);
    return true;
}

void ProxyClientApp::LoadLocalisation(StartupNotice& notice) {
    std::vector<std::string> warnings;
    const std::string locales_dir = pcx::i18n::ResolveLocalesDirectory(context_->app_dir);
    if (locales_dir.empty()) {
        // 找不到语言资源不阻止启动：界面会显示键名，问题可见但不致命。
        notice.Add("Localisation files were not found; the interface will show message keys.");
    } else {
        const std::size_t loaded =
            context_->bundle.LoadCatalogDirectory(locales_dir, &warnings);
        if (loaded == 0) {
            notice.Add("No usable localisation files were found in " + locales_dir + ".");
        }
        for (const auto& warning : warnings) {
            notice.Add(warning);
        }
    }
    if (context_->bundle.Fallback().empty()) {
        // 兜底语言始终存在，这里只是防御。
        notice.Add("The localisation bundle has no fallback language.");
    }
}

void ProxyClientApp::LoadSettings(StartupNotice& notice) {
    // ---- 出厂配置 ----
    std::string error;
    const std::string config_path = pcx::app::ResolveConfigPath(context_->app_dir);
    if (config_path.empty()) {
        notice.Add("No client-config.json was found; using built-in defaults.");
    } else {
        context_->config = pcx::app::LoadConfig(config_path, &error);
        if (!error.empty()) {
            notice.Add(error);
        }
    }
    for (const auto& warning : context_->config.warnings) {
        notice.Add(warning);
    }

    // ---- 用户偏好（覆盖出厂默认） ----
    context_->preferences =
        pcx::app::LoadPreferences(pcx::app::PreferencesPath(), context_->config, &error);
    if (!error.empty()) {
        notice.Add(error);
    }

    // ---- 语言协商 ----
    context_->language = context_->bundle.Negotiate(context_->preferences.language);
    if (context_->language.empty()) {
        // 一个语言都没加载成功时，用兜底码继续，
        // 这样 T() 至少返回键名而不是空串。
        context_->language = pcx::i18n::Normalize(context_->config.fallback_language);
    }
}

void ProxyClientApp::ReportNotices(const StartupNotice& notice) {
    if (notice.messages.empty()) {
        return;
    }
    // 汇总成一条消息：逐个弹窗会让用户连点几次「确定」，
    // 而且前一条的内容在那个过程中已经被忘掉。
    std::string combined;
    for (const auto& message : notice.messages) {
        if (!combined.empty()) {
            combined += "\n";
        }
        combined += "- " + message;
    }
    wxLogWarning("%s", wxString::FromUTF8(combined));
}
