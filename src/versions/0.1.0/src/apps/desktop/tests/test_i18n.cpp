// i18n 的单元测试。
//
// 这些测试同时承担一个额外职责：验证 C++ 端能正确读取
// **Go 端使用的那份语言文件**。若两端的键或占位符约定漂移，
// 这里会直接失败。
#include "core/i18n.hpp"
#include "test_framework.hpp"

#include <cstdlib>
#include <filesystem>
#include <fstream>
#include <iostream>
#include <map>
#include <cstddef>
#include <utility>
#include <vector>

using pcx::i18n::Bundle;
using pcx::i18n::Catalog;

namespace {

Catalog MakeCatalog(std::string language,
                    std::map<std::string, std::string> messages) {
    Catalog c;
    c.language = std::move(language);
    c.messages = std::move(messages);
    return c;
}

// LocalesDir 返回仓库里的语言资源目录（由 CMake 通过环境变量传入）。
std::string LocalesDir() {
    const char* dir = std::getenv("PCX_LOCALES_DIR");
    return dir == nullptr ? std::string() : std::string(dir);
}

}  // namespace

// ---------------------------------------------------------------------------
// 基本行为
// ---------------------------------------------------------------------------

PCX_TEST(BundleRejectsEmptyCatalogs) {
    Bundle bundle;
    // 空语言码或空文案表是「加载出错」的信号，不能存进去，
    // 否则协商会选中一个什么都没有的语言。
    PCX_CHECK_FALSE(bundle.AddCatalog(MakeCatalog("", {{"a", "b"}})));
    PCX_CHECK_FALSE(bundle.AddCatalog(MakeCatalog("en", {})));
    PCX_CHECK(bundle.AddCatalog(MakeCatalog("en", {{"a", "b"}})));
}

PCX_TEST(TranslateUsesRequestedLanguage) {
    Bundle bundle;
    PCX_CHECK(bundle.AddCatalog(MakeCatalog("zh-CN", {{"greeting", "你好，{0}"}})));
    PCX_CHECK(bundle.AddCatalog(MakeCatalog("en", {{"greeting", "Hello, {0}"}})));

    PCX_CHECK_EQ(bundle.Translate("zh-CN", "greeting", {"世界"}),
                 std::string("你好，世界"));
    PCX_CHECK_EQ(bundle.Translate("en", "greeting", {"world"}),
                 std::string("Hello, world"));
}

PCX_TEST(TranslateFallsBackToTheFallbackLanguage) {
    Bundle bundle("en");
    PCX_CHECK(bundle.AddCatalog(MakeCatalog("zh-CN", {{"only_zh", "只有中文"}})));
    PCX_CHECK(bundle.AddCatalog(MakeCatalog("en", {{"only_en", "English only"}})));

    // 中文里没有这个键：应回落到英文，而不是显示键名。
    PCX_CHECK_EQ(bundle.Translate("zh-CN", "only_en"), std::string("English only"));
}

PCX_TEST(TranslateReturnsTheKeyWhenMissingEverywhere) {
    Bundle bundle;
    PCX_CHECK(bundle.AddCatalog(MakeCatalog("en", {{"a", "b"}})));
    // 返回键名而非空串：界面会显示 panel.unauthorized，问题立刻可见。
    PCX_CHECK_EQ(bundle.Translate("en", "panel.unauthorized"),
                 std::string("panel.unauthorized"));
}

// ---------------------------------------------------------------------------
// 语言协商
// ---------------------------------------------------------------------------

PCX_TEST(NegotiatePrefersExactMatch) {
    Bundle bundle;
    PCX_CHECK(bundle.AddCatalog(MakeCatalog("zh-CN", {{"a", "b"}})));
    PCX_CHECK(bundle.AddCatalog(MakeCatalog("en", {{"a", "c"}})));
    PCX_CHECK_EQ(bundle.Negotiate("zh-CN"), std::string("zh-cn"));
}

PCX_TEST(NegotiateMatchesByPrimarySubtag) {
    Bundle bundle;
    PCX_CHECK(bundle.AddCatalog(MakeCatalog("zh-CN", {{"a", "b"}})));
    PCX_CHECK(bundle.AddCatalog(MakeCatalog("en", {{"a", "c"}})));

    // zh-Hans / zh-TW 都应落到已加载的 zh-cn。
    PCX_CHECK_EQ(bundle.Negotiate("zh-Hans"), std::string("zh-cn"));
    PCX_CHECK_EQ(bundle.Negotiate("zh-TW"), std::string("zh-cn"));
    PCX_CHECK_EQ(bundle.Negotiate("zh"), std::string("zh-cn"));
    PCX_CHECK_EQ(bundle.Negotiate("en-GB"), std::string("en"));
}

PCX_TEST(NegotiateFallsBackForUnknownLanguage) {
    Bundle bundle("en");
    PCX_CHECK(bundle.AddCatalog(MakeCatalog("zh-CN", {{"a", "b"}})));
    PCX_CHECK(bundle.AddCatalog(MakeCatalog("en", {{"a", "c"}})));
    PCX_CHECK_EQ(bundle.Negotiate("fr"), std::string("en"));
}

PCX_TEST(NegotiateReturnsSomethingWhenOnlyOneLanguageExists) {
    Bundle bundle("en");
    PCX_CHECK(bundle.AddCatalog(MakeCatalog("de", {{"a", "b"}})));
    // 兜底语言不存在时也应返回一个已加载的语言，而不是空串。
    PCX_CHECK_EQ(bundle.Negotiate("fr"), std::string("de"));
}

PCX_TEST(NegotiateOnEmptyBundleIsEmpty) {
    Bundle bundle;
    PCX_CHECK_EQ(bundle.Negotiate("zh-CN"), std::string(""));
}

// ---------------------------------------------------------------------------
// 格式化
// ---------------------------------------------------------------------------

PCX_TEST(FormatReplacesPositionalPlaceholders) {
    PCX_CHECK_EQ(pcx::i18n::Format("Hello, {0}", {"world"}), std::string("Hello, world"));
    PCX_CHECK_EQ(pcx::i18n::Format("{0} and {1}", {"a", "b"}), std::string("a and b"));
    // 同一占位符可以重复出现。
    PCX_CHECK_EQ(pcx::i18n::Format("{0}-{0}", {"x"}), std::string("x-x"));
    // 没有参数时原样返回。
    PCX_CHECK_EQ(pcx::i18n::Format("plain", {}), std::string("plain"));
}

PCX_TEST(FormatLeavesBadPlaceholdersVisible) {
    // 越界占位符必须原样保留，让翻译缺陷立刻可见。
    PCX_CHECK_EQ(pcx::i18n::Format("a {0} b {2}", {"x"}), std::string("a x b {2}"));
    // 命名占位符不是本项目的语法。
    PCX_CHECK_EQ(pcx::i18n::Format("value {name}", {"x"}), std::string("value {name}"));
    // 未闭合的花括号。
    PCX_CHECK_EQ(pcx::i18n::Format("open {0", {"x"}), std::string("open {0"));
}

PCX_TEST(CountPlaceholdersMatchesTheGoImplementation) {
    PCX_CHECK_EQ(pcx::i18n::CountPlaceholders(""), std::size_t(0));
    PCX_CHECK_EQ(pcx::i18n::CountPlaceholders("plain"), std::size_t(0));
    PCX_CHECK_EQ(pcx::i18n::CountPlaceholders("{0}"), std::size_t(1));
    PCX_CHECK_EQ(pcx::i18n::CountPlaceholders("{0} {1}"), std::size_t(2));
    PCX_CHECK_EQ(pcx::i18n::CountPlaceholders("{0} {0}"), std::size_t(2));
    PCX_CHECK_EQ(pcx::i18n::CountPlaceholders("{name}"), std::size_t(0));
    PCX_CHECK_EQ(pcx::i18n::CountPlaceholders("{0"), std::size_t(0));
    PCX_CHECK_EQ(pcx::i18n::CountPlaceholders("{0} and {x}"), std::size_t(1));
}

PCX_TEST(NormalizeHandlesCommonForms) {
    PCX_CHECK_EQ(pcx::i18n::Normalize("zh_CN"), std::string("zh-cn"));
    PCX_CHECK_EQ(pcx::i18n::Normalize(" ZH-CN "), std::string("zh-cn"));
    PCX_CHECK_EQ(pcx::i18n::Normalize("EN_us"), std::string("en-us"));
    PCX_CHECK_EQ(pcx::i18n::Normalize("   "), std::string(""));
}

// ---------------------------------------------------------------------------
// 加载仓库里的真实语言文件
// ---------------------------------------------------------------------------

PCX_TEST(LoadsTheSharedLocaleFiles) {
    const std::string dir = LocalesDir();
    if (dir.empty()) {
        // 未设置环境变量时跳过，而不是伪装通过。
        std::cout << "      (skipped: PCX_LOCALES_DIR is not set)\n";
        return;
    }

    Bundle bundle("en");
    std::vector<std::string> warnings;
    const std::size_t loaded = bundle.LoadCatalogDirectory(dir, &warnings);
    for (const auto& warning : warnings) {
        std::cout << "      warning: " << warning << "\n";
    }

    PCX_CHECK_EQ(loaded, std::size_t(2));
    PCX_CHECK(bundle.Has("zh-CN"));
    PCX_CHECK(bundle.Has("en"));

    // 两个语言必须键集合一致：这是自动校验脚本在 CI 里也检查的事，
    // 这里再确认一次 C++ 端的加载没有丢键。
    const auto languages = bundle.Languages();
    PCX_CHECK_EQ(languages.size(), std::size_t(2));

    // 用真实键验证取值与格式化（面板错误是用户最常看到的一类）。
    PCX_CHECK_EQ(bundle.Translate("en", "panel.unauthorized"),
                 std::string("Your session has expired; please sign in again"));
    PCX_CHECK_EQ(bundle.Translate("zh-CN", "panel.unauthorized"),
                 std::string("登录状态已失效，请重新登录"));

    // 带参数的键：占位符必须被替换。
    const std::string rendered =
        bundle.Translate("en", "core.config_rejected", {"23"});
    PCX_CHECK_EQ(rendered.find("{0}"), std::string::npos);
    PCX_CHECK(rendered.find("23") != std::string::npos);
}

PCX_TEST(LoadCatalogFileRejectsMalformedInput) {
    namespace fs = std::filesystem;
    std::error_code ec;
    // 用带 error_code 的重载：测试不该因为临时目录不可用而抛异常崩掉。
    const fs::path dir = fs::temp_directory_path(ec) / "pcx_i18n_test";
    if (ec) {
        std::cout << "      (skipped: no temporary directory available)\n";
        return;
    }
    fs::create_directories(dir, ec);

    const fs::path broken = dir / "broken.json";
    {
        std::ofstream out(broken);
        out << "{not json";
    }
    Bundle bundle;
    std::string error;
    PCX_CHECK_FALSE(bundle.LoadCatalogFile(broken.string(), &error));
    PCX_CHECK_FALSE(error.empty());

    const fs::path no_messages = dir / "nomsg.json";
    {
        std::ofstream out(no_messages);
        out << R"({"language":"en"})";
    }
    error.clear();
    PCX_CHECK_FALSE(bundle.LoadCatalogFile(no_messages.string(), &error));

    const fs::path good = dir / "good.json";
    {
        std::ofstream out(good);
        out << R"({"language":"en","displayName":"English","messages":{"a":"b"}})";
    }
    PCX_CHECK(bundle.LoadCatalogFile(good.string(), &error));
    PCX_CHECK(bundle.Has("en"));
    PCX_CHECK_EQ(bundle.Translate("en", "a"), std::string("b"));

    fs::remove_all(dir, ec);
}
