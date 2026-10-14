#include "core/i18n.hpp"

#include <algorithm>
#include <cctype>
#include <cstdlib>
#include <filesystem>
#include <fstream>
#include <sstream>

#include <nlohmann/json.hpp>

namespace pcx::i18n {

namespace {

// kFallbackLanguage 与 Go 端 i18n.FallbackLanguage 保持一致。
constexpr std::string_view kFallbackLanguage = "en";

std::string ReadFile(const std::string& path, std::string* error) {
    std::ifstream in(path, std::ios::binary);
    if (!in) {
        if (error != nullptr) {
            *error = "cannot open " + path;
        }
        return {};
    }
    std::ostringstream buffer;
    buffer << in.rdbuf();
    return buffer.str();
}

// PrimarySubtag 返回主语言子标签（zh-CN -> zh）。
std::string PrimarySubtag(std::string_view language) {
    const auto pos = language.find('-');
    if (pos != std::string_view::npos && pos > 0) {
        return std::string(language.substr(0, pos));
    }
    return std::string(language);
}

}  // namespace

// ---------------------------------------------------------------------------
// Bundle
// ---------------------------------------------------------------------------

Bundle::Bundle(std::string fallback) : fallback_(Normalize(fallback)) {
    if (fallback_.empty()) {
        fallback_ = std::string(kFallbackLanguage);
    }
}

bool Bundle::AddCatalog(Catalog catalog) {
    const std::string language = Normalize(catalog.language);
    if (language.empty() || catalog.messages.empty()) {
        // 空语言码或空文案表一律拒绝：它是「加载出错」的信号，
        // 存进去只会让后续协商选中一个什么都没有的语言。
        return false;
    }
    catalog.language = language;
    catalogs_[language] = std::move(catalog);
    return true;
}

bool Bundle::LoadCatalogFile(const std::string& path, std::string* error) {
    const std::string text = ReadFile(path, error);
    if (text.empty()) {
        if (error != nullptr && error->empty()) {
            *error = "empty file: " + path;
        }
        return false;
    }

    // 解析失败不抛异常：语言文件写坏不该让程序起不来。
    const nlohmann::json parsed =
        nlohmann::json::parse(text.begin(), text.end(), nullptr, false);
    if (parsed.is_discarded() || !parsed.is_object()) {
        if (error != nullptr) {
            *error = "not a JSON object: " + path;
        }
        return false;
    }

    Catalog catalog;
    if (const auto it = parsed.find("language"); it != parsed.end() && it->is_string()) {
        catalog.language = it->get<std::string>();
    }
    if (const auto it = parsed.find("displayName"); it != parsed.end() && it->is_string()) {
        catalog.display_name = it->get<std::string>();
    }
    if (catalog.language.empty()) {
        // 兜底用文件主名，这样即使 language 字段缺失也能加载。
        catalog.language = std::filesystem::path(path).stem().string();
    }

    const auto messages = parsed.find("messages");
    if (messages == parsed.end() || !messages->is_object()) {
        if (error != nullptr) {
            *error = "no messages object: " + path;
        }
        return false;
    }
    for (auto it = messages->begin(); it != messages->end(); ++it) {
        if (it->is_string()) {
            catalog.messages[it.key()] = it->get<std::string>();
        }
    }

    if (!AddCatalog(std::move(catalog))) {
        if (error != nullptr) {
            *error = "catalog has no usable messages: " + path;
        }
        return false;
    }
    return true;
}

std::size_t Bundle::LoadCatalogDirectory(const std::string& dir,
                                         std::vector<std::string>* warnings) {
    std::error_code ec;
    if (!std::filesystem::is_directory(dir, ec)) {
        if (warnings != nullptr) {
            warnings->push_back("not a directory: " + dir);
        }
        return 0;
    }

    std::size_t loaded = 0;
    // 排序后遍历：加载顺序稳定，失败报告才有可比性。
    std::vector<std::filesystem::path> files;
    for (const auto& entry : std::filesystem::directory_iterator(dir, ec)) {
        if (entry.is_regular_file() && entry.path().extension() == ".json") {
            files.push_back(entry.path());
        }
    }
    std::sort(files.begin(), files.end());

    for (const auto& file : files) {
        std::string error;
        if (LoadCatalogFile(file.string(), &error)) {
            ++loaded;
        } else if (warnings != nullptr) {
            // 单个文件失败不中断：中文加载失败而英文可用，
            // 远好于整个界面没有文案。
            warnings->push_back(error);
        }
    }
    return loaded;
}

std::string Bundle::Negotiate(std::string_view requested) const {
    if (catalogs_.empty()) {
        return {};
    }

    std::string want = Normalize(requested);
    if (want.empty() || want == "system") {
        want = Normalize(DetectSystemLanguage());
    }

    // 1) 精确匹配。
    if (!want.empty() && catalogs_.count(want) != 0) {
        return want;
    }

    // 2) 主语言匹配（zh-Hans -> zh-CN；en-US -> en）。
    if (!want.empty()) {
        const std::string primary = PrimarySubtag(want);
        if (catalogs_.count(primary) != 0) {
            return primary;
        }
        for (const auto& language : Languages()) {
            if (PrimarySubtag(language) == primary) {
                return language;
            }
        }
    }

    // 3) 兜底语言。
    if (catalogs_.count(fallback_) != 0) {
        return fallback_;
    }
    const std::string fallback_primary = PrimarySubtag(fallback_);
    for (const auto& language : Languages()) {
        if (PrimarySubtag(language) == fallback_primary) {
            return language;
        }
    }

    // 4) 任意可用语言：保证总能返回一个能用的语言，而不是空串。
    return Languages().front();
}

std::string Bundle::Translate(std::string_view language, std::string_view key,
                              const std::vector<std::string>& args) const {
    const std::string* format = Lookup(language, key);
    if (format == nullptr) {
        format = Lookup(fallback_, key);
    }
    if (format == nullptr) {
        // 缺键返回键名本身：界面会显示 panel.unauthorized 这样的文本，
        // 问题立刻可见；返回空串则会变成难以察觉的空白。
        return std::string(key);
    }
    return Format(*format, args);
}

std::vector<std::string> Bundle::Languages() const {
    std::vector<std::string> out;
    out.reserve(catalogs_.size());
    for (const auto& [language, _] : catalogs_) {
        out.push_back(language);
    }
    std::sort(out.begin(), out.end());
    return out;
}

bool Bundle::Has(std::string_view language) const {
    return catalogs_.count(Normalize(language)) != 0;
}

const std::string* Bundle::Lookup(std::string_view language, std::string_view key) const {
    const auto catalog = catalogs_.find(Normalize(language));
    if (catalog == catalogs_.end()) {
        return nullptr;
    }
    const auto message = catalog->second.messages.find(std::string(key));
    if (message == catalog->second.messages.end() || message->second.empty()) {
        return nullptr;
    }
    return &message->second;
}

// ---------------------------------------------------------------------------
// 格式化
// ---------------------------------------------------------------------------

std::string Format(std::string_view format, const std::vector<std::string>& args) {
    if (args.empty() || format.find('{') == std::string_view::npos) {
        return std::string(format);
    }

    std::string out;
    out.reserve(format.size() + 16);

    for (std::size_t i = 0; i < format.size(); ++i) {
        const char ch = format[i];
        if (ch != '{') {
            out.push_back(ch);
            continue;
        }
        const auto close = format.find('}', i);
        if (close == std::string_view::npos) {
            out.push_back(ch);
            continue;
        }
        const std::string_view inner = format.substr(i + 1, close - i - 1);

        // 只接受纯数字；命名占位符不是本项目的语法。
        bool numeric = !inner.empty();
        std::size_t index = 0;
        for (const char digit : inner) {
            if (digit < '0' || digit > '9') {
                numeric = false;
                break;
            }
            index = index * 10 + static_cast<std::size_t>(digit - '0');
        }

        if (!numeric || index >= args.size()) {
            // 非法或越界：原样保留，让问题可见。
            out.append(format.substr(i, close - i + 1));
        } else {
            out.append(args[index]);
        }
        i = close;
    }
    return out;
}

std::size_t CountPlaceholders(std::string_view format) {
    std::size_t count = 0;
    for (std::size_t i = 0; i < format.size(); ++i) {
        if (format[i] != '{') {
            continue;
        }
        const auto close = format.find('}', i);
        if (close == std::string_view::npos) {
            break;
        }
        const std::string_view inner = format.substr(i + 1, close - i - 1);
        if (!inner.empty() &&
            std::all_of(inner.begin(), inner.end(),
                        [](char c) { return c >= '0' && c <= '9'; })) {
            ++count;
        }
        i = close;
    }
    return count;
}

// ---------------------------------------------------------------------------
// 语言探测
// ---------------------------------------------------------------------------

std::string Normalize(std::string_view language) {
    std::string out;
    out.reserve(language.size());
    for (const char ch : language) {
        if (ch == '_') {
            out.push_back('-');
        } else {
            out.push_back(static_cast<char>(std::tolower(static_cast<unsigned char>(ch))));
        }
    }
    // 去首尾空白。
    const auto first = out.find_first_not_of(" \t\r\n");
    if (first == std::string::npos) {
        return {};
    }
    const auto last = out.find_last_not_of(" \t\r\n");
    return out.substr(first, last - first + 1);
}

std::string DetectSystemLanguage() {
    for (const char* name : {"LC_ALL", "LC_MESSAGES", "LANG"}) {
        const char* raw = std::getenv(name);
        if (raw == nullptr) {
            continue;
        }
        std::string value(raw);
        // 形如 "zh_CN.UTF-8" 或 "en_US"：去掉编码与修饰后缀。
        for (const char marker : {'.', '@'}) {
            const auto pos = value.find(marker);
            if (pos != std::string::npos) {
                value = value.substr(0, pos);
            }
        }
        const std::string normalized = Normalize(value);
        // "C" 与 "POSIX" 表示未设置语言，不是可用取值。
        if (!normalized.empty() && normalized != "c" && normalized != "posix") {
            return normalized;
        }
    }
    return {};
}

std::string ResolveLocalesDirectory(const std::string& executable_path) {
    namespace fs = std::filesystem;

    // 1) 环境变量优先：测试与自定义安装位置都靠它。
    if (const char* override_dir = std::getenv("PCX_LOCALES_DIR");
        override_dir != nullptr && *override_dir != '\0') {
        std::error_code ec;
        if (fs::is_directory(override_dir, ec)) {
            return override_dir;
        }
    }

    std::error_code ec;
    fs::path dir = executable_path.empty() ? fs::current_path(ec)
                                           : fs::path(executable_path).parent_path();
    if (dir.empty()) {
        return {};
    }

    // 2) 安装布局：<prefix>/bin/<exe> 与 <prefix>/share/proxyclient/locales。
    {
        const fs::path candidate = dir.parent_path() / "share" / "proxyclient" / "locales";
        if (fs::is_directory(candidate, ec)) {
            return candidate.string();
        }
    }
    // 3) 开发布局：从构建产物目录向上找到仓库根的 locales。
    {
        fs::path cursor = dir;
        for (int depth = 0; depth < 8; ++depth) {
            const fs::path candidate = cursor / "locales";
            if (fs::is_directory(candidate, ec)) {
                return candidate.string();
            }
            if (!cursor.has_parent_path() || cursor.parent_path() == cursor) {
                break;
            }
            cursor = cursor.parent_path();
        }
    }
    return {};
}

}  // namespace pcx::i18n
