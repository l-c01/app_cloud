// 测试框架的实现 / test harness implementation.
#include "test_framework.hpp"

#include <algorithm>
#include <iostream>
#include <functional>
#include <utility>
#include <vector>

namespace pcx::test {

namespace {

// 当前用例收集到的失败数。用全局而不是成员，因为用例是普通函数。
int g_failures_in_case = 0;

}  // namespace

std::vector<Case>& registry() {
    // 函数内静态变量：避免静态初始化顺序问题。
    static std::vector<Case> cases;
    return cases;
}

Registrar::Registrar(const char* name, std::function<void()> fn) {
    registry().push_back(Case{name, std::move(fn)});
}

void Fail(const char* file, int line, const std::string& message) {
    ++g_failures_in_case;
    // 输出到 stdout 而不是 stderr：CI 日志里两者的顺序常被打乱，
    // 放在一起看更清楚。
    std::cout << "  FAIL " << file << ":" << line << "\n"
              << "      " << message << "\n";
}

int RunAll() {
    auto& cases = registry();
    // 按名字排序：注册顺序取决于静态初始化，跨平台可能不同，
    // 排序后输出稳定，diff 才有意义。
    std::sort(cases.begin(), cases.end(),
              [](const Case& a, const Case& b) { return a.name < b.name; });

    int failed_cases = 0;
    for (const auto& c : cases) {
        g_failures_in_case = 0;
        std::cout << "[ RUN  ] " << c.name << "\n";
        try {
            c.fn();
        } catch (const std::exception& e) {
            // 用例抛异常也要记录，并继续跑其余用例。
            Fail("<exception>", 0,
                 std::string("uncaught exception: ") + e.what());
        } catch (...) {
            Fail("<exception>", 0, "uncaught non-standard exception");
        }
        if (g_failures_in_case == 0) {
            std::cout << "[  OK  ] " << c.name << "\n";
        } else {
            std::cout << "[ FAIL ] " << c.name << " (" << g_failures_in_case
                      << " assertion(s))\n";
            ++failed_cases;
        }
    }

    std::cout << "\n"
              << cases.size() << " test(s) run, " << failed_cases
              << " failed.\n";
    return failed_cases;
}

}  // namespace pcx::test

// 进程入口。
//
// 必须在这里定义：test_main.cpp 是唯一始终参与链接的测试翻译单元。
// 少了它，pcx_tests 链接时会报 `undefined reference to main'——
// 链接错误不会指出「你忘了写入口」，只会列出这个符号，
// 很容易被误读成构件配置问题。
int main() {
    return pcx::test::RunAll();
}
