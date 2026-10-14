// 应用设置 / application settings.
//
// **两类设置刻意分开存放**，这不是过度设计：
//
//   Config       出厂配置，来自仓库里的 design/client-config.json（打包后随程序分发）。
//                它描述「这个构建是什么」：软件名、面板地址、默认端口……
//                普通用户不应修改它，应用也**绝不写它**。
//
//   Preferences  用户偏好：界面模式、语言、上次选中的节点……
//                它是用户的选择，应用可以写。
//
// 混在一起的后果很具体：应用升级会覆盖用户选择；或者用户的选择被写进
// 出厂配置，于是「重置为默认值」变成了不可能——因为默认值已经被改掉了。
#pragma once

#include <optional>
#include <string>
#include <vector>

#include <nlohmann/json.hpp>

#include "core/i18n.hpp"

namespace pcx::app {

using Json = nlohmann::json;

// UiMode 是界面模式。
//
// 两种模式共用同一套配置与同一个节点库（设计基线 §4.1），
// 切换模式**不改变**已经建立的行为，只改变呈现与可见选项。
enum class UiMode {
    Simple,
    Professional,
};

// UiModeToString / UiModeFromString 负责持久化表示。
std::string UiModeToString(UiMode mode);
std::optional<UiMode> UiModeFromString(const std::string& text);

// TrafficEntry 是接入方式。
//
// 取值与 client-config.json 的 trafficEntry 以及 netmgr 的常量一致。
namespace traffic_entry {
inline constexpr const char* kSystemProxy = "system_proxy";
inline constexpr const char* kTun = "tun";
inline constexpr const char* kPac = "pac";
inline constexpr const char* kLocalProxy = "local_proxy";
}  // namespace traffic_entry

// ---------------------------------------------------------------------------
// 出厂配置
// ---------------------------------------------------------------------------

// Config 是出厂配置的只读视图。
//
// 只保留界面真正需要的字段：解析整个文件再暴露成结构体，
// 会让「配置里多了个字段」变成必须改代码的事。
struct Config {
    // project
    std::string repository_url;
    std::string license;

    // branding
    std::string application_name = "default";
    std::string display_name_zh;
    std::string display_name_en;
    std::string publisher;
    std::string application_id;
    std::string android_package_id;
    std::string homepage_url;
    std::string support_url;

    // panel
    std::string api_base_url;
    std::string web_base_url;
    int timeout_seconds = 15;

    // panelLinks
    std::string registration_url;
    std::string password_reset_url;
    std::string purchase_url;
    std::string documentation_url;

    // clientDefaults
    UiMode default_ui_mode = UiMode::Simple;
    bool default_remember_ui_mode = false;
    std::string default_language = "system";
    std::string fallback_language = "en";
    std::string default_routing_policy = "rule";
    std::string local_proxy_host = "127.0.0.1";
    int http_proxy_port = 7890;
    int socks_proxy_port = 7891;
    bool connect_on_launch = false;
    bool allow_lan_access = false;
    // traffic_entry 按平台给出默认值；这里保存当前平台的那一个。
    std::string default_traffic_entry = traffic_entry::kLocalProxy;

    // testSettings
    bool show_connection_diagnostics = true;
    bool enable_api_settings_editor = true;
    std::string test_target_url;

    // 解析过程中的问题（缺字段、类型不符）。界面可据此提示配置有误，
    // 但不阻止启动——配置不完整时用默认值仍然能打开界面。
    std::vector<std::string> warnings;

    // 语言相关的显示名。
    std::string DisplayName(const std::string& language) const;
};

// LoadConfig 从文件加载出厂配置。
//
// 失败时返回带默认值的 Config 并填入 error；界面仍可启动，
// 因为「配置读不到」不应该让用户连界面都看不到。
Config LoadConfig(const std::string& path, std::string* error);

// ResolveConfigPath 按优先级找到出厂配置文件。
//
// 顺序：
//   1. 环境变量 PCX_CONFIG（供测试与自定义部署使用）
//   2. 可执行文件同级的 client-config.json
//   3. 可执行文件同级 share/proxyclient/client-config.json
//   4. 从可执行文件向上若干级的 design/client-config.json（开发期）
std::string ResolveConfigPath(const std::string& executable_path);

// ---------------------------------------------------------------------------
// 用户偏好
// ---------------------------------------------------------------------------

// Preferences 是用户偏好。
struct Preferences {
    UiMode ui_mode = UiMode::Simple;
    // remember_ui_mode 为 false 时，每次启动都用出厂默认模式。
    bool remember_ui_mode = false;
    // language 是用户选择的语言；"system" 表示跟随系统。
    std::string language = "system";

    // 上次使用的接入方式与路由策略，便于用户下次打开时保持一致性。
    //
    // 这里不再存「上次用的内核」：本构建只发布 sing-box（ADR-0005），
    // 存一个只有一个取值的偏好，只会让用户以为存在别的选择。
    std::string traffic_entry;
    std::string routing_policy;

    static Preferences Defaults(const Config& config);
};

// PreferencesPath 返回用户偏好的存放路径。
std::string PreferencesPath();

// LoadPreferences 读取用户偏好。
//
// 文件不存在时返回 config 给出的默认值，不算错误。
Preferences LoadPreferences(const std::string& path, const Config& config, std::string* error);

// SavePreferences 写入用户偏好。
//
// 采用「先写临时文件再改名」：中途失败时不会留下半截 JSON，
// 那会让下次启动丢掉全部偏好。
bool SavePreferences(const std::string& path, const Preferences& prefs, std::string* error);

// SerialisePreferences / ParsePreferences 供测试与诊断使用。
Json SerialisePreferences(const Preferences& prefs);
Preferences ParsePreferences(const Json& source, const Config& config);

}  // namespace pcx::app
