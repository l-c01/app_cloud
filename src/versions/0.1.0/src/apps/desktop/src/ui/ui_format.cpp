#include "ui/ui_format.hpp"

#include <cstdint>
#include <cstdio>
#include <ctime>
#include <string>
#include <vector>

namespace pcx::ui {

namespace {

// kBytesPerUnit 与下面的单位表一一对应。
constexpr double kBytesPerUnit = 1000.0;

// FormatBytesWithUnits 走十进制单位。
std::string FormatBytesWithUnits(std::int64_t bytes) {
    // 负值只可能来自解析异常。显示 0 而不是负数：
    // 「已用 -1.2 GB」会让用户以为程序算错了，而真正的问题是服务端给了脏数据。
    if (bytes <= 0) {
        return "0 B";
    }
    constexpr const char* kUnits[] = {"B", "KB", "MB", "GB", "TB", "PB"};
    double value = static_cast<double>(bytes);
    std::size_t unit = 0;
    while (value >= kBytesPerUnit && unit + 1 < (sizeof(kUnits) / sizeof(kUnits[0]))) {
        value /= kBytesPerUnit;
        ++unit;
    }

    char buffer[64];
    if (unit == 0) {
        // 字节数不带小数：0.4 B 没有意义。
        std::snprintf(buffer, sizeof(buffer), "%lld %s",
                      static_cast<long long>(bytes), kUnits[unit]);
    } else {
        std::snprintf(buffer, sizeof(buffer), "%.2f %s", value, kUnits[unit]);
    }
    return std::string(buffer);
}

std::string FormatTime(std::int64_t unix_seconds, const char* pattern) {
    if (unix_seconds <= 0) {
        return {};
    }
    const std::time_t seconds = static_cast<std::time_t>(unix_seconds);
    // localtime 在 MSVC 下会触发 C4996（「不安全 CRT」），
    // 但那一族警告已在 CMakeLists 里按厂商偏好关闭；换成 localtime_s
    // 会让这份代码无法在 Linux 上编译，而这里刻意只用可移植的写法。
    const std::tm* converted = std::localtime(&seconds);
    if (converted == nullptr) {
        return {};
    }
    const std::tm local = *converted;

    char buffer[64];
    const std::size_t written = std::strftime(buffer, sizeof(buffer), pattern, &local);
    if (written == 0) {
        // 缓冲区太小或 pattern 非法：返回空串让调用方显示占位文本，
        // 而不是显示一段被截断的日期。
        return {};
    }
    return std::string(buffer);
}

}  // namespace

// ---------------------------------------------------------------------------
// 通用换算
// ---------------------------------------------------------------------------

std::string FormatBytes(std::int64_t bytes) { return FormatBytesWithUnits(bytes); }

std::string FormatUnixDate(std::int64_t unix_seconds) {
    return FormatTime(unix_seconds, "%Y-%m-%d");
}

// ---------------------------------------------------------------------------
// 会话摘要
// ---------------------------------------------------------------------------

std::vector<std::string> SessionSummary(const app::AppContext& context) {
    std::vector<std::string> lines;
    if (!context.has_session || !context.session.logged_in) {
        return lines;
    }

    const ipc::SessionState& session = context.session;
    std::string email;
    std::string plan;
    if (session.account.has_value()) {
        const ipc::AccountView& account = *session.account;
        email = account.email;
        plan = account.plan_name;
        // 封禁与过期必须显示：这两种状态下「连接」按钮还能点，
        // 而失败原因在服务端，用户看到的只是一个连不上的代理。
        if (account.banned) {
            lines.push_back(context.T("ui.session.banned"));
        }
        if (account.expired) {
            lines.push_back(context.T("ui.session.expired"));
        }
        // 账户摘要里的 warnings 是面板给出的提示，原样转达。
        for (const auto& warning : account.warnings) {
            lines.push_back(warning);
        }
    }

    const ipc::SubscriptionView* subscription =
        session.subscription.has_value() ? &*session.subscription : nullptr;

    // 套餐名优先取订阅里的：它来自最近一次拉取，比账户摘要更新。
    if (subscription != nullptr && !subscription->plan_name.empty()) {
        plan = subscription->plan_name;
    }
    if (!email.empty()) {
        lines.push_back(context.T("ui.session.email", {email}));
    }
    if (!plan.empty()) {
        lines.push_back(context.T("ui.session.plan", {plan}));
    }

    if (subscription != nullptr) {
        // 用量：present 为 false 时**不显示这一行**。
        // 显示「已用 0 B」会让用户以为流量没被统计，而实际是我们不知道。
        if (subscription->usage.present) {
            std::string usage = context.T(
                "ui.session.traffic",
                {FormatBytes(subscription->usage.UsedBytes()),
                 FormatBytes(subscription->usage.total)});
            if (subscription->usage.remaining > 0) {
                usage += "  " + context.T("ui.session.remaining",
                                          {FormatBytes(subscription->usage.remaining)});
            }
            lines.push_back(usage);
            const std::string expire = FormatUnixDate(subscription->usage.expire_unix);
            if (!expire.empty()) {
                lines.push_back(context.T("ui.session.expires", {expire}));
            }
        }

        lines.push_back(context.T("ui.session.nodes",
                                  {std::to_string(subscription->node_count)}));
        // 被拒绝的节点数必须显示。服务端会丢掉协议不支持的节点，
        // 静默丢弃会让用户以为「订阅里就是少了几个节点」。
        if (subscription->rejected_count > 0) {
            lines.push_back(context.T("ui.session.rejected_nodes",
                                      {std::to_string(subscription->rejected_count)}));
        }
    }

    if (subscription != nullptr) {
        for (const auto& warning : subscription->warnings) {
            lines.push_back(warning);
        }
    }
    return lines;
}

std::string SessionWarning(const app::AppContext& context) {
    // 安全存储不可用这一条**不依赖登录状态**：用户需要在填密码之前就知道
    // 「这次登录不会被记住」。等他登录完再提示，他已经产生了错误的预期。
    if (!context.has_session) {
        return {};
    }
    if (context.session.RememberWarningRequired()) {
        return context.T("ui.warn.no_secure_storage");
    }
    return {};
}

// ---------------------------------------------------------------------------
// 失败文本的形态判断
// ---------------------------------------------------------------------------

bool LooksLikeMessageKey(const std::string& text) {
    // 刻意保守：只在**明确**像键时才说是。
    //
    // 误判的代价不对称：把一句英文描述当成键，界面会把它整句显示出来
    // （只是没翻译，用户仍能读懂）；把键当成描述也一样是显示原文。
    // 真正的风险是**猜错方向后套用翻译**——所以这里只做形状判断，
    // 不做「猜它是什么意思」。
    if (text.empty() || text.find('.') == std::string::npos) {
        return false;
    }
    // 含空格或斜杠的一定不是键（多半是路径或句子）。
    if (text.find(' ') != std::string::npos || text.find('/') != std::string::npos) {
        return false;
    }
    bool previous_dot = true;  // 首段之前视作分隔符，禁止以点开头
    for (const char ch : text) {
        if (ch == '.') {
            if (previous_dot) {
                // 连续两个点或以点开头：不是键。
                return false;
            }
            previous_dot = true;
            continue;
        }
        const bool lower = ch >= 'a' && ch <= 'z';
        const bool digit = ch >= '0' && ch <= '9';
        if (!lower && !digit && ch != '_') {
            // 大写或其它字符说明这不是我们的键语法。
            return false;
        }
        previous_dot = false;
    }
    // 以点结尾说明它是运行时拼接的前缀，不是完整键。
    return !previous_dot;
}


}  // namespace pcx::ui