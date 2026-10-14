// settings 的单元测试。
//
// 其中一个用例直接加载**仓库里真实的** design/client-config.json：
// 出厂配置是打包时分发的文件，它解析不了的话，用户看到的是
// 「软件名变成了 default、端口全错」，而且不会有任何报错。
#include "app/settings.hpp"
#include "test_framework.hpp"

#include <cstdlib>
#include <filesystem>
#include <fstream>
#include <iostream>
#include <cstddef>

using pcx::app::Config;
using pcx::app::LoadConfig;
using pcx::app::LoadPreferences;
using pcx::app::ParsePreferences;
using pcx::app::Preferences;
using pcx::app::SavePreferences;
using pcx::app::SerialisePreferences;
using pcx::app::UiMode;
using pcx::app::UiModeFromString;
using pcx::app::UiModeToString;

namespace {

// WriteTemp 写一个临时文件，返回路径。
std::string WriteTemp(const std::string& name, const std::string& content) {
    std::error_code ec;
    const std::filesystem::path dir = std::filesystem::temp_directory_path(ec) / "pcx_settings_test";
    if (ec) {
        return {};
    }
    std::filesystem::create_directories(dir, ec);
    const std::filesystem::path file = dir / name;
    std::ofstream out(file, std::ios::binary | std::ios::trunc);
    out << content;
    return file.string();
}

}  // namespace

// ---------------------------------------------------------------------------
// UiMode
// ---------------------------------------------------------------------------

PCX_TEST(UiModeRoundTripsThroughItsPersistedForm) {
    // 持久化取值必须与 client-config.json 里的一致：不一致会让
    // 「记住界面模式」在重启后失效。
    PCX_CHECK_EQ(UiModeToString(UiMode::Simple), std::string("simple"));
    PCX_CHECK_EQ(UiModeToString(UiMode::Professional), std::string("professional"));

    const auto simple = UiModeFromString("simple");
    PCX_CHECK(simple.has_value());
    PCX_CHECK(*simple == UiMode::Simple);

    const auto pro = UiModeFromString("professional");
    PCX_CHECK(pro.has_value());
    PCX_CHECK(*pro == UiMode::Professional);
}

PCX_TEST(UiModeRejectsUnknownValues) {
    // 返回 nullopt 而不是猜一个：静默回退会让「配置写错了」
    // 表现成「我的设置没生效」，非常难查。
    PCX_CHECK_FALSE(UiModeFromString("").has_value());
    PCX_CHECK_FALSE(UiModeFromString("Simple").has_value());   // 大小写敏感
    PCX_CHECK_FALSE(UiModeFromString("expert").has_value());
}

// ---------------------------------------------------------------------------
// 出厂配置
// ---------------------------------------------------------------------------

PCX_TEST(LoadConfigParsesTheShippedFile) {
    const char* dir = std::getenv("PCX_CONFIG_DIR");
    if (dir == nullptr || *dir == '\0') {
        std::cout << "      (skipped: PCX_CONFIG_DIR is not set)\n";
        return;
    }
    const std::string path = std::string(dir) + "/client-config.json";

    std::string error;
    const Config config = LoadConfig(path, &error);
    PCX_CHECK(error.empty());

    // 用户明确要求出厂名为 "default"，且仓库地址与许可证留空待填。
    PCX_CHECK_EQ(config.application_name, std::string("default"));
    PCX_CHECK_EQ(config.repository_url, std::string(""));
    PCX_CHECK_EQ(config.license, std::string(""));

    // 默认值必须来自文件而不是硬编码兜底。
    PCX_CHECK_EQ(config.http_proxy_port, 7890);
    PCX_CHECK_EQ(config.socks_proxy_port, 7891);
    PCX_CHECK_EQ(config.local_proxy_host, std::string("127.0.0.1"));
    PCX_CHECK_EQ(config.timeout_seconds, 15);
    PCX_CHECK(config.default_ui_mode == UiMode::Simple);
    PCX_CHECK_EQ(config.fallback_language, std::string("en"));

    // 本平台必须拿到一个接入方式默认值，而不是空串。
    PCX_CHECK_FALSE(config.default_traffic_entry.empty());
    // 面板地址默认留空是设计基线的要求（源码里不带任何生产地址）。
    PCX_CHECK_EQ(config.api_base_url, std::string(""));
    // 配置本身合法，不应产生警告。
    if (!config.warnings.empty()) {
        for (const auto& warning : config.warnings) {
            std::cout << "      unexpected warning: " << warning << "\n";
        }
    }
    PCX_CHECK_EQ(config.warnings.size(), std::size_t(0));
}

PCX_TEST(LoadConfigWithoutAFileStillReturnsUsableDefaults) {
    // 配置读不到不该让用户连界面都看不到。
    std::string error;
    const Config config = LoadConfig("/nonexistent/client-config.json", &error);
    PCX_CHECK_FALSE(error.empty());
    PCX_CHECK_EQ(config.application_name, std::string("default"));
    PCX_CHECK_EQ(config.http_proxy_port, 7890);
}

PCX_TEST(LoadConfigRejectsMalformedJson) {
    const std::string path = WriteTemp("broken.json", "{not json");
    if (path.empty()) {
        std::cout << "      (skipped: no temporary directory)\n";
        return;
    }
    std::string error;
    const Config config = LoadConfig(path, &error);
    PCX_CHECK_FALSE(error.empty());
    // 仍然给出可用的默认值。
    PCX_CHECK_EQ(config.http_proxy_port, 7890);
}

PCX_TEST(LoadConfigReportsUnknownUiModeInsteadOfSilentlyDefaulting) {
    const std::string path = WriteTemp("badmode.json", R"({
        "clientDefaults": { "uiMode": "expert" }
    })");
    if (path.empty()) {
        return;
    }
    std::string error;
    const Config config = LoadConfig(path, &error);
    // 必须产生一条警告：否则「配置写错了」会表现成「设置没生效」。
    bool reported = false;
    for (const auto& warning : config.warnings) {
        if (warning.find("uiMode") != std::string::npos) {
            reported = true;
        }
    }
    PCX_CHECK(reported);
    PCX_CHECK(config.default_ui_mode == UiMode::Simple);
}

PCX_TEST(LoadConfigRejectsOutOfRangePorts) {
    const std::string path = WriteTemp("badports.json", R"({
        "clientDefaults": { "httpProxyPort": 99999, "socksProxyPort": -1 }
    })");
    if (path.empty()) {
        return;
    }
    std::string error;
    const Config config = LoadConfig(path, &error);
    // 不检查的话，界面会拿一个不可能的端口去创建监听，
    // 而错误信息会来自核心深处，与真正的原因相距很远。
    PCX_CHECK_EQ(config.http_proxy_port, 7890);
    PCX_CHECK_EQ(config.socks_proxy_port, 7891);
    PCX_CHECK(config.warnings.size() >= 2);
}

PCX_TEST(LoadConfigWarnsWhenBothPortsAreIdentical) {
    const std::string path = WriteTemp("sameports.json", R"({
        "clientDefaults": { "httpProxyPort": 7890, "socksProxyPort": 7890 }
    })");
    if (path.empty()) {
        return;
    }
    std::string error;
    const Config config = LoadConfig(path, &error);
    bool reported = false;
    for (const auto& warning : config.warnings) {
        if (warning.find("same") != std::string::npos) {
            reported = true;
        }
    }
    PCX_CHECK(reported);
}

PCX_TEST(DisplayNameFallsBackToTheApplicationName) {
    Config config;
    config.application_name = "default";
    // 语言显示名留空时回退出厂名，而不是空标题。
    PCX_CHECK_EQ(config.DisplayName("zh-CN"), std::string("default"));
    PCX_CHECK_EQ(config.DisplayName("en"), std::string("default"));

    config.display_name_zh = "示例客户端";
    PCX_CHECK_EQ(config.DisplayName("zh-CN"), std::string("示例客户端"));
    PCX_CHECK_EQ(config.DisplayName("zh-Hans"), std::string("示例客户端"));
    // 没填英文名时仍回退出厂名，而不是拿中文名去凑。
    PCX_CHECK_EQ(config.DisplayName("en"), std::string("default"));
}

// ---------------------------------------------------------------------------
// 用户偏好
// ---------------------------------------------------------------------------

PCX_TEST(PreferencesDefaultFromConfig) {
    Config config;
    config.default_ui_mode = UiMode::Professional;
    config.default_remember_ui_mode = true;
    config.default_language = "zh-CN";
    config.default_core = "xray";

    const Preferences prefs = Preferences::Defaults(config);
    PCX_CHECK(prefs.ui_mode == UiMode::Professional);
    PCX_CHECK(prefs.remember_ui_mode);
    PCX_CHECK_EQ(prefs.language, std::string("zh-CN"));
    PCX_CHECK_EQ(prefs.core, std::string("xray"));
}

PCX_TEST(PreferencesRoundTripThroughJson) {
    Preferences original;
    original.ui_mode = UiMode::Professional;
    original.remember_ui_mode = true;
    original.language = "zh-CN";
    original.core = "xray";
    original.traffic_entry = "tun";
    original.routing_policy = "global";

    Config config;
    const Preferences restored = ParsePreferences(SerialisePreferences(original), config);

    PCX_CHECK(restored.ui_mode == UiMode::Professional);
    PCX_CHECK(restored.remember_ui_mode);
    PCX_CHECK_EQ(restored.language, std::string("zh-CN"));
    PCX_CHECK_EQ(restored.core, std::string("xray"));
    PCX_CHECK_EQ(restored.traffic_entry, std::string("tun"));
    PCX_CHECK_EQ(restored.routing_policy, std::string("global"));
}

PCX_TEST(ParsePreferencesKeepsDefaultsForMissingFields) {
    // 从出厂默认值出发再用文件覆盖：缺字段时行为可预期，而不是变成零值。
    Config config;
    config.default_core = "sing-box";
    config.default_routing_policy = "rule";

    const Preferences prefs = ParsePreferences(nlohmann::json::object(), config);
    PCX_CHECK_EQ(prefs.core, std::string("sing-box"));
    PCX_CHECK_EQ(prefs.routing_policy, std::string("rule"));
    PCX_CHECK(prefs.ui_mode == config.default_ui_mode);
}

PCX_TEST(ParsePreferencesIgnoresEmptyStrings) {
    Config config;
    config.default_core = "sing-box";

    nlohmann::json source = nlohmann::json::object();
    source["core"] = "";
    // 空串表示「未设置」，不应把默认值覆盖成空。
    const Preferences prefs = ParsePreferences(source, config);
    PCX_CHECK_EQ(prefs.core, std::string("sing-box"));
}

PCX_TEST(LoadPreferencesWithoutAFileIsNotAnError) {
    Config config;
    std::string error = "sentinel";
    const Preferences prefs = LoadPreferences("/nonexistent/preferences.json", config, &error);
    // 首次启动就是这个情况，不能报错。
    PCX_CHECK(error.empty());
    PCX_CHECK(prefs.ui_mode == config.default_ui_mode);
}

PCX_TEST(SavePreferencesWritesAtomicallyAndLeavesNoTempFile) {
    std::error_code ec;
    const std::filesystem::path dir = std::filesystem::temp_directory_path(ec) / "pcx_settings_test";
    if (ec) {
        return;
    }
    std::filesystem::create_directories(dir, ec);
    const std::string path = (dir / "prefs.json").string();

    Preferences prefs;
    prefs.ui_mode = UiMode::Professional;
    prefs.core = "xray";

    std::string error;
    PCX_CHECK(SavePreferences(path, prefs, &error));
    PCX_CHECK(error.empty());

    // 不应残留临时文件：那意味着下次启动可能读到半截内容。
    PCX_CHECK_FALSE(std::filesystem::exists(path + ".tmp", ec));

    Config config;
    const Preferences loaded = LoadPreferences(path, config, &error);
    PCX_CHECK(loaded.ui_mode == UiMode::Professional);
    PCX_CHECK_EQ(loaded.core, std::string("xray"));

    std::filesystem::remove_all(dir, ec);
}

PCX_TEST(SavePreferencesRejectsAnEmptyPath) {
    std::string error;
    PCX_CHECK_FALSE(SavePreferences("", Preferences{}, &error));
    PCX_CHECK_FALSE(error.empty());
}
