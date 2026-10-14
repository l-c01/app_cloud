// 桌面端的多语言 / Desktop localisation.
//
// 关键约定：**与 Go 端读取同一份语言资源**（仓库根的 locales/*.json）。
//
// 为什么不各存一份：两套翻译文件必然漂移，最后表现为「同一个错误
// 在界面和日志里显示不同的话」。共用一份文件时，键集合与占位符
// 的一致性由 tools/scripts/check-i18n-keys.ps1 统一校验。
//
// 约定细节见 design/architecture/i18n-convention.md：
//   - 位置参数 {0}、{1}；
//   - 缺键返回键名本身（可见地暴露问题，而不是返回空串）；
//   - 语言协商顺序：请求 -> 精确 -> 主语言 -> 兜底 -> 任意可用。
#pragma once

#include <map>
#include <optional>
#include <string>
#include <string_view>
#include <vector>
#include <cstddef>

namespace pcx::i18n {

// Catalog 是单个语言的文案表。
struct Catalog {
    std::string language;
    std::string display_name;
    std::map<std::string, std::string> messages;
};

// Bundle 是已加载的语言集合。
class Bundle {
public:
    // Bundle 构造。fallback 为空时使用 "en"。
    explicit Bundle(std::string fallback = "en");

    // AddCatalog 加入一个语言。语言码为空或文案为空时返回 false。
    bool AddCatalog(Catalog catalog);

    // LoadCatalogFile 从文件加载语言资源。
    //
    // 失败时返回 false 并把原因写入 error（可为 nullptr）。
    // 语言码以文件中的 language 字段为准，取不到则用文件主名。
    bool LoadCatalogFile(const std::string& path, std::string* error = nullptr);

    // LoadCatalogDirectory 加载目录下全部 *.json。
    //
    // 返回成功加载的语言数。单个文件解析失败不会中断加载，
    // 因为「中文加载失败但英文可用」远好于整体不可用。
    std::size_t LoadCatalogDirectory(const std::string& dir,
                                     std::vector<std::string>* warnings = nullptr);

    // Negotiate 选择实际使用的语言。
    //
    // requested 为 "system" 或空时使用 DetectSystemLanguage()。
    // 集合为空时返回空串。
    std::string Negotiate(std::string_view requested = "") const;

    // Translate 取一条文案并按位置参数格式化。
    //
    // 找不到键时依次尝试兜底语言，最后返回键名本身。
    std::string Translate(std::string_view language, std::string_view key,
                          const std::vector<std::string>& args = {}) const;

    // Languages 返回已加载的语言码（已排序）。
    std::vector<std::string> Languages() const;

    // Has 报告某语言是否已加载。
    bool Has(std::string_view language) const;

    // Fallback 返回兜底语言码。
    const std::string& Fallback() const { return fallback_; }

private:
    const std::string* Lookup(std::string_view language, std::string_view key) const;

    std::map<std::string, Catalog> catalogs_;
    std::string fallback_;
};

// Format 按位置参数替换 {0}、{1} 等占位符。
//
// 越界或非数字占位符**原样保留**：界面会显示 {2}，
// 让翻译缺陷立刻可见，而不是静默变成空串。
std::string Format(std::string_view format, const std::vector<std::string>& args);

// CountPlaceholders 统计格式串里的占位符个数。
std::size_t CountPlaceholders(std::string_view format);

// Normalize 规范化语言码：小写、下划线转连字符、去空白。
std::string Normalize(std::string_view language);

// DetectSystemLanguage 尽力探测系统语言。
//
// 实现：读取 LC_ALL / LC_MESSAGES / LANG 环境变量（POSIX）。
// 已知限制：Windows 不通过环境变量暴露界面语言，那里会返回空串，
// 由协商流程回退到兜底语言。要正确支持 Windows 需调用
// GetUserDefaultUILanguage 并注入结果。
std::string DetectSystemLanguage();

// ResolveLocalesDirectory 按优先级找到语言资源目录。
//
// 顺序：
//   1. 环境变量 PCX_LOCALES_DIR（供测试与打包后自定义位置使用）；
//   2. 可执行文件同级的 share/proxyclient/locales；
//   3. 可执行文件向上若干级的 locales（开发期：从构建产物回到仓库根）。
//
// 返回空串表示都没找到。
std::string ResolveLocalesDirectory(const std::string& executable_path);

}  // namespace pcx::i18n
