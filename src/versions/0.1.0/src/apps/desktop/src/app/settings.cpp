#include "app/settings.hpp"

#include <cstdlib>
#include <filesystem>
#include <fstream>
#include <sstream>
#include <optional>

#include "core/json_util.hpp"

namespace pcx::app {

namespace {

namespace fs = std::filesystem;

using json::GetBool;
using json::GetInt;
using json::GetObject;
using json::GetString;

// kPreferencesFileName 是用户偏好文件名。
//
// 刻意与出厂配置不同名：两者目的不同，同名会让用户以为它们是同一个东西，
// 也会让「重置偏好」变成可能误删出厂配置的危险操作。
constexpr const char* kPreferencesFileName = "preferences.json";

std::string ReadFile(const std::string& path) {
    std::ifstream in(path, std::ios::binary);
    if (!in) {
        return {};
    }
    std::ostringstream buffer;
    buffer << in.rdbuf();
    return buffer.str();
}

// CurrentPlatformEntry 返回当前平台对应的接入方式键。
const char* CurrentPlatformEntry() {
#if defined(_WIN32)
    return "windows";
#elif defined(__ANDROID__)
    return "android";
#else
    return "linux";
#endif
}

}  // namespace

// ---------------------------------------------------------------------------
// UiMode
// ---------------------------------------------------------------------------

std::string UiModeToString(UiMode mode) {
    // 持久化用 "simple" / "professional"，与 client-config.json 的取值一致。
    return mode == UiMode::Professional ? "professional" : "simple";
}

std::optional<UiMode> UiModeFromString(const std::string& text) {
    if (text == "simple") {
        return UiMode::Simple;
    }
    if (text == "professional") {
        return UiMode::Professional;
    }
    // 不认识的值返回 nullopt 而不是猜一个：静默回退会让「配置写错了」
    // 表现成「我的设置没生效」，很难查。
    return std::nullopt;
}

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

std::string Config::DisplayName(const std::string& language) const {
    const std::string normalized = i18n::Normalize(language);
    if (normalized.rfind("zh", 0) == 0 && !display_name_zh.empty()) {
        return display_name_zh;
    }
    if (normalized.rfind("en", 0) == 0 && !display_name_en.empty()) {
        return display_name_en;
    }
    // 两种语言都没填时回退出厂名，而不是空标题。
    return application_name.empty() ? std::string("default") : application_name;
}

Config LoadConfig(const std::string& path, std::string* error) {
    Config config;

    const std::string text = ReadFile(path);
    if (text.empty()) {
        if (error != nullptr) {
            *error = "cannot read the shipped config file: " + path;
        }
        // 用默认值继续：配置读不到不该让用户连界面都看不到。
        return config;
    }

    const auto parsed = json::TryParse(text);
    if (!parsed.has_value() || !parsed->is_object()) {
        if (error != nullptr) {
            *error = "the shipped config file is not a JSON object: " + path;
        }
        return config;
    }
    const Json& root = *parsed;

    const Json project = GetObject(root, "project");
    config.repository_url = GetString(project, "repositoryUrl");
    config.license = GetString(project, "license");

    const Json branding = GetObject(root, "branding");
    config.application_name = GetString(branding, "applicationName", "default");
    const Json names = GetObject(branding, "displayNames");
    config.display_name_zh = GetString(names, "zh-CN");
    config.display_name_en = GetString(names, "en");
    config.publisher = GetString(branding, "publisher");
    config.application_id = GetString(branding, "applicationId");
    config.android_package_id = GetString(branding, "androidPackageId");
    config.homepage_url = GetString(branding, "homepageUrl");
    config.support_url = GetString(branding, "supportUrl");

    const Json panel = GetObject(root, "panel");
    config.api_base_url = GetString(panel, "apiBaseUrl");
    config.web_base_url = GetString(panel, "webBaseUrl");
    config.timeout_seconds = static_cast<int>(GetInt(panel, "timeoutSeconds", 15));
    if (config.timeout_seconds <= 0) {
        config.warnings.push_back("panel.timeoutSeconds is not a positive number; using 15");
        config.timeout_seconds = 15;
    }

    const Json links = GetObject(root, "panelLinks");
    config.registration_url = GetString(links, "registrationUrl");
    config.password_reset_url = GetString(links, "passwordResetUrl");
    config.purchase_url = GetString(links, "purchaseUrl");
    config.documentation_url = GetString(links, "documentationUrl");

    const Json defaults = GetObject(root, "clientDefaults");
    if (const auto mode = UiModeFromString(GetString(defaults, "uiMode", "simple"));
        mode.has_value()) {
        config.default_ui_mode = *mode;
    } else {
        // 认不出的取值必须被报告：静默回退会让配置错误表现为「设置没生效」。
        config.warnings.push_back(
            "clientDefaults.uiMode is not 'simple' or 'professional'; using 'simple'");
        config.default_ui_mode = UiMode::Simple;
    }
    config.default_remember_ui_mode = GetBool(defaults, "rememberUiMode");
    config.default_language = GetString(defaults, "language", "system");
    config.fallback_language = GetString(defaults, "fallbackLanguage", "en");
    config.default_core = GetString(defaults, "core", "sing-box");
    config.default_routing_policy = GetString(defaults, "routingPolicy", "rule");
    config.local_proxy_host = GetString(defaults, "localProxyHost", "127.0.0.1");
    config.http_proxy_port = static_cast<int>(GetInt(defaults, "httpProxyPort", 7890));
    config.socks_proxy_port = static_cast<int>(GetInt(defaults, "socksProxyPort", 7891));
    config.connect_on_launch = GetBool(defaults, "connectOnLaunch");
    config.allow_lan_access = GetBool(defaults, "allowLanAccess");

    // trafficEntry 是按平台给值的对象；取当前平台那一个。
    const Json entries = GetObject(defaults, "trafficEntry");
    const std::string platform = CurrentPlatformEntry();
    const std::string platform_entry = GetString(entries, platform);
    if (platform_entry.empty()) {
        config.warnings.push_back(std::string("clientDefaults.trafficEntry has no value for '") +
                                  platform + "'; falling back to local_proxy");
        config.default_traffic_entry = traffic_entry::kLocalProxy;
    } else {
        config.default_traffic_entry = platform_entry;
    }

    const Json test_settings = GetObject(root, "testSettings");
    config.show_connection_diagnostics = GetBool(test_settings, "showConnectionDiagnostics", true);
    config.enable_api_settings_editor = GetBool(test_settings, "enableApiSettingsEditor", true);
    config.test_target_url = GetString(test_settings, "testTargetUrl");

    // 端口合法性：不检查的话，界面会拿一个不可能的端口去创建监听，
    // 而错误信息会来自核心深处，与真正的原因相距很远。
    const auto check_port = [&config](int port, const char* name) {
        if (port <= 0 || port > 65535) {
            config.warnings.push_back(std::string(name) + " is out of range; using the default");
            return false;
        }
        return true;
    };
    if (!check_port(config.http_proxy_port, "clientDefaults.httpProxyPort")) {
        config.http_proxy_port = 7890;
    }
    if (!check_port(config.socks_proxy_port, "clientDefaults.socksProxyPort")) {
        config.socks_proxy_port = 7891;
    }
    if (config.http_proxy_port == config.socks_proxy_port) {
        config.warnings.push_back("httpProxyPort and socksProxyPort are the same; the core will fail to bind");
    }

    if (error != nullptr) {
        error->clear();
    }
    return config;
}

std::string ResolveConfigPath(const std::string& executable_path) {
    // 1) 环境变量优先：测试与自定义部署都靠它。
    if (const char* override_path = std::getenv("PCX_CONFIG");
        override_path != nullptr && *override_path != '\0') {
        std::error_code ec;
        if (fs::is_regular_file(override_path, ec)) {
            return override_path;
        }
    }

    std::error_code ec;
    const fs::path dir = executable_path.empty() ? fs::current_path(ec)
                                                 : fs::path(executable_path).parent_path();
    if (dir.empty()) {
        return {};
    }

    // 2) 与可执行文件同级，以及安装布局下的 share 目录。
    for (const fs::path& candidate :
         {dir / "client-config.json", dir / "share" / "proxyclient" / "client-config.json"}) {
        if (fs::is_regular_file(candidate, ec)) {
            return candidate.string();
        }
    }

    // 3) 开发期：从构建产物目录向上找到仓库里的 design/client-config.json。
    fs::path cursor = dir;
    for (int depth = 0; depth < 8; ++depth) {
        const fs::path candidate = cursor / "design" / "client-config.json";
        if (fs::is_regular_file(candidate, ec)) {
            return candidate.string();
        }
        if (!cursor.has_parent_path() || cursor.parent_path() == cursor) {
            break;
        }
        cursor = cursor.parent_path();
    }
    return {};
}

// ---------------------------------------------------------------------------
// Preferences
// ---------------------------------------------------------------------------

Preferences Preferences::Defaults(const Config& config) {
    Preferences prefs;
    prefs.ui_mode = config.default_ui_mode;
    prefs.remember_ui_mode = config.default_remember_ui_mode;
    prefs.language = config.default_language;
    prefs.core = config.default_core;
    prefs.traffic_entry = config.default_traffic_entry;
    prefs.routing_policy = config.default_routing_policy;
    return prefs;
}

std::string PreferencesPath() {
    std::error_code ec;
#if defined(_WIN32)
    if (const char* appdata = std::getenv("APPDATA"); appdata != nullptr && *appdata != '\0') {
        return (fs::path(appdata) / "proxyclient" / kPreferencesFileName).string();
    }
#else
    if (const char* xdg = std::getenv("XDG_CONFIG_HOME"); xdg != nullptr && *xdg != '\0') {
        return (fs::path(xdg) / "proxyclient" / kPreferencesFileName).string();
    }
    if (const char* home = std::getenv("HOME"); home != nullptr && *home != '\0') {
        return (fs::path(home) / ".config" / "proxyclient" / kPreferencesFileName).string();
    }
#endif
    return {};
}

Preferences ParsePreferences(const Json& source, const Config& config) {
    // 从出厂默认值出发，用文件里存在的字段覆盖。
    // 这样配置文件缺字段时行为是可预期的，而不是变成零值。
    Preferences prefs = Preferences::Defaults(config);
    if (!source.is_object()) {
        return prefs;
    }

    if (const auto mode = UiModeFromString(GetString(source, "uiMode")); mode.has_value()) {
        prefs.ui_mode = *mode;
    }
    prefs.remember_ui_mode = GetBool(source, "rememberUiMode", prefs.remember_ui_mode);
    prefs.language = GetString(source, "language", prefs.language);

    // 空字符串表示「未设置」，不应覆盖默认值。
    const auto keep_if_set = [](const std::string& value, std::string& target) {
        if (!value.empty()) {
            target = value;
        }
    };
    keep_if_set(GetString(source, "core"), prefs.core);
    keep_if_set(GetString(source, "trafficEntry"), prefs.traffic_entry);
    keep_if_set(GetString(source, "routingPolicy"), prefs.routing_policy);

    return prefs;
}

Preferences LoadPreferences(const std::string& path, const Config& config, std::string* error) {
    const std::string text = ReadFile(path);
    if (text.empty()) {
        // 文件不存在是首次启动的正常情况，不是错误。
        if (error != nullptr) {
            error->clear();
        }
        return Preferences::Defaults(config);
    }

    const auto parsed = json::TryParse(text);
    if (!parsed.has_value()) {
        if (error != nullptr) {
            *error = "the preferences file is unreadable; using the shipped defaults";
        }
        return Preferences::Defaults(config);
    }

    if (error != nullptr) {
        error->clear();
    }
    return ParsePreferences(*parsed, config);
}

Json SerialisePreferences(const Preferences& prefs) {
    Json out = Json::object();
    out["schemaVersion"] = "1.0.0";
    out["uiMode"] = UiModeToString(prefs.ui_mode);
    out["rememberUiMode"] = prefs.remember_ui_mode;
    out["language"] = prefs.language;
    if (!prefs.core.empty()) {
        out["core"] = prefs.core;
    }
    if (!prefs.traffic_entry.empty()) {
        out["trafficEntry"] = prefs.traffic_entry;
    }
    if (!prefs.routing_policy.empty()) {
        out["routingPolicy"] = prefs.routing_policy;
    }
    return out;
}

bool SavePreferences(const std::string& path, const Preferences& prefs, std::string* error) {
    if (path.empty()) {
        if (error != nullptr) {
            *error = "cannot determine where to store preferences";
        }
        return false;
    }

    const fs::path target(path);
    std::error_code ec;
    fs::create_directories(target.parent_path(), ec);
    if (ec) {
        if (error != nullptr) {
            *error = "cannot create the preferences directory: " + ec.message();
        }
        return false;
    }

    // 先写临时文件再改名：中途失败不会留下半截 JSON，
    // 那会让下次启动丢掉全部偏好。
    const fs::path temporary = target.string() + ".tmp";
    {
        std::ofstream out(temporary, std::ios::binary | std::ios::trunc);
        if (!out) {
            if (error != nullptr) {
                *error = "cannot write the preferences file";
            }
            return false;
        }
        out << json::Compact(SerialisePreferences(prefs)) << "\n";
        out.flush();
        if (!out) {
            if (error != nullptr) {
                *error = "failed while writing the preferences file";
            }
            fs::remove(temporary, ec);
            return false;
        }
    }

    fs::rename(temporary, target, ec);
    if (ec) {
        if (error != nullptr) {
            *error = "cannot activate the preferences file: " + ec.message();
        }
        fs::remove(temporary, ec);
        return false;
    }

    if (error != nullptr) {
        error->clear();
    }
    return true;
}

}  // namespace pcx::app
