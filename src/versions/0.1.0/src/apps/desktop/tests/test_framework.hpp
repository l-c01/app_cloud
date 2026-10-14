// 极简测试框架 / Minimal test harness.
//
// 为什么不用现成的框架：桌面端当前只依赖 nlohmann/json 与 wxWidgets，
// 为了几个单元测试再引入 GoogleTest/Catch2 属于增加构建面。
// 这个头文件 + 一个实现文件（test_main.cpp）足够覆盖需求。
//
// 能力刻意有限：
//   - 注册测试用例并顺序执行；
//   - 断言失败时记录文件与行号，继续跑完其余用例（一次看到全部失败）；
//   - 结束打印摘要并返回非零退出码。
//
// 它**不**提供 mock、fixture、参数化测试。真有需要时应换成成熟框架，
// 而不是把这个文件越写越大。
#pragma once

#include <functional>
#include <sstream>
#include <string>
#include <type_traits>
#include <vector>

namespace pcx::test {

struct Case {
    std::string name;
    std::function<void()> fn;
};

// registry 返回全局用例表。
std::vector<Case>& registry();

// Registrar 在静态初始化期把用例登记进 registry。
struct Registrar {
    Registrar(const char* name, std::function<void()> fn);
};

// Fail 记录一次断言失败。
void Fail(const char* file, int line, const std::string& message);

// RunAll 执行全部用例，返回失败数。
int RunAll();

// Describe 把值渲染成简短文本，供断言消息使用。
//
// 对任意类型都可用：不认识的就输出 <value>，不会导致编译失败。
// 这是刻意的取舍——测试框架不该因为某个类型没有 operator<< 而编译不过。
template <typename T>
std::string Describe(const T& value) {
    using Decayed = std::decay_t<T>;
    if constexpr (std::is_same_v<Decayed, std::string>) {
        return "\"" + value + "\"";
    } else if constexpr (std::is_same_v<Decayed, bool>) {
        return value ? "true" : "false";
    } else if constexpr (std::is_same_v<Decayed, const char*> || std::is_same_v<Decayed, char*>) {
        return std::string("\"") + value + "\"";
    } else if constexpr (std::is_arithmetic_v<Decayed>) {
        return std::to_string(value);
    } else if constexpr (std::is_convertible_v<Decayed, std::string>) {
        return "\"" + std::string(value) + "\"";
    } else {
        return "<value>";
    }
}

}  // namespace pcx::test

// PCX_TEST(name) 定义一个测试用例。
#define PCX_TEST(name)                                                    \
    static void name();                                                   \
    static const ::pcx::test::Registrar pcx_registrar_##name(#name, name); \
    static void name()

// PCX_CHECK 断言条件为真。
#define PCX_CHECK(cond)                                                          \
    do {                                                                         \
        if (!(cond)) {                                                           \
            ::pcx::test::Fail(__FILE__, __LINE__, "expected: " #cond);           \
        }                                                                        \
    } while (0)

// PCX_CHECK_FALSE 断言条件为假。
#define PCX_CHECK_FALSE(cond)                                                    \
    do {                                                                         \
        if ((cond)) {                                                            \
            ::pcx::test::Fail(__FILE__, __LINE__, "expected NOT: " #cond);       \
        }                                                                        \
    } while (0)

// PCX_CHECK_EQ 断言两值相等，失败时打印双方实际值。
#define PCX_CHECK_EQ(a, b)                                                              \
    do {                                                                                \
        const auto& pcx_a = (a);                                                        \
        const auto& pcx_b = (b);                                                        \
        if (!(pcx_a == pcx_b)) {                                                        \
            ::pcx::test::Fail(__FILE__, __LINE__,                                       \
                              std::string(#a " == " #b "\n      actual:   ") +          \
                                  ::pcx::test::Describe(pcx_a) +                        \
                                  "\n      expected: " + ::pcx::test::Describe(pcx_b)); \
        }                                                                               \
    } while (0)

// PCX_CHECK_THROWS 断言表达式抛出指定类型的异常。
#define PCX_CHECK_THROWS(expr, exception_type)                                    \
    do {                                                                          \
        bool pcx_threw = false;                                                   \
        try {                                                                     \
            (void)(expr);                                                         \
        } catch (const exception_type&) {                                         \
            pcx_threw = true;                                                     \
        } catch (...) {                                                           \
            ::pcx::test::Fail(__FILE__, __LINE__,                                 \
                              #expr " threw the wrong exception type");            \
            pcx_threw = true;                                                     \
        }                                                                         \
        if (!pcx_threw) {                                                         \
            ::pcx::test::Fail(__FILE__, __LINE__, #expr " did not throw");        \
        }                                                                         \
    } while (0)
