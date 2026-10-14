#include "core/json_util.hpp"

#include <algorithm>
#include <array>
#include <cctype>
#include <cmath>
#include <cstdint>

namespace pcx::json {

namespace {

std::string ToLower(std::string_view s) {
    std::string out;
    out.reserve(s.size());
    for (const char ch : s) {
        out.push_back(static_cast<char>(
            std::tolower(static_cast<unsigned char>(ch))));
    }
    return out;
}

// 敏感键名片段。命中任意一个即脱敏。
//
// 列表刻意偏保守（容易误伤）：
// 多脱敏一个无害，漏脱敏一个就是凭据泄漏。
constexpr std::array<std::string_view, 12> kSensitiveFragments = {
    "password", "passwd", "secret",     "token",  "session", "authorization",
    "auth",     "cookie",   "credential", "uuid",  "subscrib", "apikey"};

}  // namespace

// ---------------------------------------------------------------------------
// 安全取值
// ---------------------------------------------------------------------------

std::string GetString(const Json& obj, std::string_view key, const std::string& fallback) {
    if (!obj.is_object()) {
        return fallback;
    }
    const auto it = obj.find(std::string(key));
    if (it == obj.end() || !it->is_string()) {
        return fallback;
    }
    return it->get<std::string>();
}

std::int64_t GetInt(const Json& obj, std::string_view key, std::int64_t fallback) {
    if (!obj.is_object()) {
        return fallback;
    }
    const auto it = obj.find(std::string(key));
    if (it == obj.end()) {
        return fallback;
    }
    if (it->is_number_integer()) {
        return it->get<std::int64_t>();
    }
    if (it->is_number_unsigned()) {
        const auto value = it->get<std::uint64_t>();
        // 超出 int64 范围时返回兜底值，而不是回绕成负数。
        if (value > static_cast<std::uint64_t>(INT64_MAX)) {
            return fallback;
        }
        return static_cast<std::int64_t>(value);
    }
    if (it->is_number_float()) {
        const double value = it->get<double>();
        // 只有整数部分才算整数：2.7 显然不是端口号，应当视为类型不符。
        if (std::isfinite(value) && std::floor(value) == value &&
            value >= static_cast<double>(INT64_MIN) &&
            value <= static_cast<double>(INT64_MAX)) {
            return static_cast<std::int64_t>(value);
        }
        return fallback;
    }
    // 字符串里的数字不自动转换：那会让 "12abc" 之类的脏数据悄悄通过。
    return fallback;
}

bool GetBool(const Json& obj, std::string_view key, bool fallback) {
    if (!obj.is_object()) {
        return fallback;
    }
    const auto it = obj.find(std::string(key));
    if (it == obj.end() || !it->is_boolean()) {
        return fallback;
    }
    return it->get<bool>();
}

Json GetObject(const Json& obj, std::string_view key) {
    if (!obj.is_object()) {
        return Json::object();
    }
    const auto it = obj.find(std::string(key));
    if (it == obj.end() || !it->is_object()) {
        return Json::object();
    }
    return *it;
}

// ---------------------------------------------------------------------------
// 脱敏
// ---------------------------------------------------------------------------

bool IsSensitiveKey(std::string_view key) {
    const std::string lower = ToLower(key);
    for (const auto fragment : kSensitiveFragments) {
        if (lower.find(fragment) != std::string::npos) {
            return true;
        }
    }
    return false;
}

Json Redact(const Json& value, int max_depth) {
    if (max_depth <= 0) {
        // 超深嵌套：整体替换，避免栈溢出，也避免把未处理的内容漏出去。
        return "<redacted: nesting too deep>";
    }

    if (value.is_object()) {
        Json out = Json::object();
        for (auto it = value.begin(); it != value.end(); ++it) {
            if (IsSensitiveKey(it.key())) {
                out[it.key()] = "<redacted>";
            } else {
                out[it.key()] = Redact(it.value(), max_depth - 1);
            }
        }
        return out;
    }

    if (value.is_array()) {
        Json out = Json::array();
        for (const auto& item : value) {
            out.push_back(Redact(item, max_depth - 1));
        }
        return out;
    }

    return value;
}

std::string RedactUrl(std::string_view url) {
    const std::string text(url);

    // 先去掉 userinfo（user:pass@host）。
    std::string result = text;
    const auto scheme_end = result.find("://");
    if (scheme_end != std::string::npos) {
        const auto authority_start = scheme_end + 3;
        const auto authority_end = result.find('/', authority_start);
        const auto at = result.find('@', authority_start);
        if (at != std::string::npos &&
            (authority_end == std::string::npos || at < authority_end)) {
            result.replace(authority_start, at - authority_start, "<redacted>");
        }
    }

    // 再处理查询串：只保留参数名，值一律脱敏。
    //
    // 订阅链接的 token 就在查询串里，因此不能只对已知参数名脱敏——
    // 那样新增一个参数名就漏了。
    const auto query_start = result.find('?');
    if (query_start != std::string::npos) {
        std::string rebuilt = result.substr(0, query_start) + "?";
        const std::string query = result.substr(query_start + 1);
        bool first = true;
        std::size_t pos = 0;
        while (pos <= query.size()) {
            const auto amp = query.find('&', pos);
            const std::string pair =
                query.substr(pos, amp == std::string::npos ? std::string::npos
                                                           : amp - pos);
            if (!pair.empty()) {
                if (!first) {
                    rebuilt += "&";
                }
                const auto eq = pair.find('=');
                if (eq == std::string::npos) {
                    rebuilt += pair;
                } else {
                    rebuilt += pair.substr(0, eq) + "=<redacted>";
                }
                first = false;
            }
            if (amp == std::string::npos) {
                break;
            }
            pos = amp + 1;
        }
        // 保留片段标识。
        const auto fragment = result.find('#', query_start);
        if (fragment != std::string::npos) {
            rebuilt += result.substr(fragment);
        }
        result = rebuilt;
    }

    return result;
}

// ---------------------------------------------------------------------------
// 信封
// ---------------------------------------------------------------------------

bool IsSuccess(const Envelope& env) { return env.status == "success"; }

std::optional<Envelope> ParseEnvelope(const Json& root) {
    if (!root.is_object()) {
        return std::nullopt;
    }
    const auto it = root.find("status");
    if (it == root.end() || !it->is_string()) {
        return std::nullopt;
    }

    Envelope env;
    env.status = it->get<std::string>();
    // status 只认这两个值。别的值说明这不是 Xboard 信封，
    // 用「不是 success 就算失败」会把它误当成业务失败。
    if (env.status != "success" && env.status != "fail") {
        return std::nullopt;
    }
    env.message = GetString(root, "message");
    if (const auto data = root.find("data"); data != root.end()) {
        env.data = *data;
    }
    if (const auto err = root.find("error"); err != root.end()) {
        env.error = *err;
    }
    return env;
}

// ---------------------------------------------------------------------------
// 其它
// ---------------------------------------------------------------------------

std::string Compact(const Json& value) {
    // dump 的第二个参数 indent = -1 表示紧凑输出。
    return value.dump(-1, ' ', false, Json::error_handler_t::replace);
}

std::optional<Json> TryParse(std::string_view text) {
    if (text.empty()) {
        return std::nullopt;
    }
    // 不开异常：解析失败是常态（用户填错地址、中间有拦截页），
    // 不该把异常当控制流。
    Json parsed = Json::parse(text.begin(), text.end(), nullptr, false);
    if (parsed.is_discarded()) {
        return std::nullopt;
    }
    return parsed;
}

}  // namespace pcx::json
