// JSON 辅助工具 / JSON helpers.
//
// 本模块只做三件事，刻意不做更多：
//   1. 安全取值：类型不符时返回默认值，而不是抛异常或产生未定义行为；
//   2. 脱敏：把 JSON 里的凭据替换掉，供日志与诊断输出使用；
//   3. 信封判定：与服务端一致地判断一次响应是否成功。
//
// 为什么不自己写解析器：JSON 的转义、UTF-8、数字精度这些细节很容易写错，
// 而错误表现通常是「偶发解析失败」，极难排查。这里用 nlohmann/json。
#pragma once

#include <optional>
#include <string>
#include <string_view>
#include <vector>

#include <nlohmann/json.hpp>

namespace pcx::json {

using Json = nlohmann::json;

// ---------------------------------------------------------------------------
// 安全取值
// ---------------------------------------------------------------------------

// GetString 取字符串字段；缺失或类型不符时返回 fallback。
//
// 类型不符时**不抛异常**：服务端字段类型随版本变化是常态，
// 一个字段类型变了不应该让整个界面崩掉。
std::string GetString(const Json& obj, std::string_view key, const std::string& fallback = "");

// GetInt 取整数字段；缺失或类型不符时返回 fallback。
//
// 注意：JSON 数字可能是浮点。仅当小数部分为 0 时才接受，否则视为类型不符。
std::int64_t GetInt(const Json& obj, std::string_view key, std::int64_t fallback = 0);

// GetBool 取布尔字段；缺失或类型不符时返回 fallback。
bool GetBool(const Json& obj, std::string_view key, bool fallback = false);

// GetObject 取对象字段；缺失或不是对象时返回空对象。
Json GetObject(const Json& obj, std::string_view key);

// ---------------------------------------------------------------------------
// 脱敏
// ---------------------------------------------------------------------------

// IsSensitiveKey 判断字段名是否应当脱敏。
//
// 判定基于字段名（大小写不敏感的子串匹配），覆盖：
//   password / token / session / authorization / secret / uuid / subscribe
//
// 宁可多脱敏也不要漏：日志里多出现一个 <redacted> 没有代价，
// 漏掉一个订阅 token 就是凭据泄漏。
bool IsSensitiveKey(std::string_view key);

// Redact 返回脱敏后的副本，用于日志与诊断输出。
//
// 行为：
//   - 按键名脱敏（值整体替换为 "<redacted>"）；
//   - 递归处理嵌套对象与数组；
//   - 不修改输入。
//
// 深度上限用于防御恶意/异常的深层嵌套导致栈溢出。
Json Redact(const Json& value, int max_depth = 32);

// RedactUrl 脱敏 URL 里的查询参数与用户信息。
//
// 订阅链接形如 https://host/api/v1/client/subscribe?token=SECRET，
// 整条链接就是凭据，因此不能整条记录，也不能只依赖键名匹配。
std::string RedactUrl(std::string_view url);

// ---------------------------------------------------------------------------
// 信封
// ---------------------------------------------------------------------------

// Envelope 是服务端统一响应信封（与服务端 app/Helpers/ApiResponse.php 一致）。
struct Envelope {
    std::string status;   // "success" 或 "fail"
    std::string message;
    Json data = Json::object();
    Json error = nullptr;
};

// IsSuccess 报告信封是否为成功。
//
// 决策只看 status 字段：message 可能被面板插件改写或本地化，
// 拿它做判断会在换语言或改文案时静默出错。
bool IsSuccess(const Envelope& env);

// ParseEnvelope 解析信封。
//
// 返回 std::nullopt 表示响应不是 Xboard 信封（非对象、缺 status、
// 或 status 取值不认识）——这通常意味着地址不对或中间有拦截页。
std::optional<Envelope> ParseEnvelope(const Json& root);

// ---------------------------------------------------------------------------
// 其它
// ---------------------------------------------------------------------------

// Compact 生成紧凑 JSON 文本，供日志使用（已脱敏的单行输出）。
std::string Compact(const Json& value);

// TryParse 解析 JSON 文本；失败返回 std::nullopt。
std::optional<Json> TryParse(std::string_view text);

}  // namespace pcx::json
