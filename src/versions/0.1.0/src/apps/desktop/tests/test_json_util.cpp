// json_util 的单元测试。
#include "core/json_util.hpp"
#include "test_framework.hpp"

using pcx::json::Envelope;
using pcx::json::Json;

// ---------------------------------------------------------------------------
// 安全取值
// ---------------------------------------------------------------------------

PCX_TEST(GetStringReturnsTheValueWhenPresent) {
    const Json obj = {{"name", "proxy"}};
    PCX_CHECK_EQ(pcx::json::GetString(obj, "name"), std::string("proxy"));
}

PCX_TEST(GetStringReturnsFallbackForMissingOrWrongType) {
    const Json obj = {{"count", 3}, {"flag", true}};
    PCX_CHECK_EQ(pcx::json::GetString(obj, "missing", "dflt"), std::string("dflt"));
    // 类型不符不抛异常：服务端字段类型变化不该让界面崩掉。
    PCX_CHECK_EQ(pcx::json::GetString(obj, "count", "dflt"), std::string("dflt"));
    PCX_CHECK_EQ(pcx::json::GetString(Json::array(), "count", "dflt"), std::string("dflt"));
}

PCX_TEST(GetIntAcceptsIntegers) {
    const Json obj = {{"port", 7890}, {"negative", -12}};
    PCX_CHECK_EQ(pcx::json::GetInt(obj, "port"), std::int64_t(7890));
    PCX_CHECK_EQ(pcx::json::GetInt(obj, "negative"), std::int64_t(-12));
}

PCX_TEST(GetIntAcceptsFloatsOnlyWhenIntegral) {
    const Json obj = {{"whole", 8080.0}, {"fractional", 2.7}};
    PCX_CHECK_EQ(pcx::json::GetInt(obj, "whole"), std::int64_t(8080));
    // 2.7 显然不是端口号，应视为类型不符而不是截断成 2。
    PCX_CHECK_EQ(pcx::json::GetInt(obj, "fractional", -1), std::int64_t(-1));
}

PCX_TEST(GetIntRejectsNumericStrings) {
    // 不自动转换字符串里的数字："12abc" 之类的脏数据不该悄悄通过。
    const Json obj = {{"port", "7890"}};
    PCX_CHECK_EQ(pcx::json::GetInt(obj, "port", -1), std::int64_t(-1));
}

PCX_TEST(GetBoolOnlyAcceptsBooleans) {
    const Json obj = {{"on", true}, {"off", false}, {"one", 1}};
    PCX_CHECK(pcx::json::GetBool(obj, "on"));
    PCX_CHECK_FALSE(pcx::json::GetBool(obj, "off"));
    PCX_CHECK_FALSE(pcx::json::GetBool(obj, "one"));
    PCX_CHECK(pcx::json::GetBool(obj, "missing", true));
}

PCX_TEST(GetObjectReturnsEmptyForWrongType) {
    const Json obj = {{"nested", {{"a", 1}}}, {"list", Json::array()}};
    PCX_CHECK_EQ(pcx::json::GetObject(obj, "nested").size(), std::size_t(1));
    PCX_CHECK_EQ(pcx::json::GetObject(obj, "list").size(), std::size_t(0));
    PCX_CHECK_EQ(pcx::json::GetObject(obj, "missing").size(), std::size_t(0));
}

// ---------------------------------------------------------------------------
// 脱敏
// ---------------------------------------------------------------------------

PCX_TEST(SensitiveKeysAreRecognised) {
    PCX_CHECK(pcx::json::IsSensitiveKey("password"));
    PCX_CHECK(pcx::json::IsSensitiveKey("Password"));
    PCX_CHECK(pcx::json::IsSensitiveKey("sessionToken"));
    PCX_CHECK(pcx::json::IsSensitiveKey("Authorization"));
    PCX_CHECK(pcx::json::IsSensitiveKey("subscribe_url"));
    PCX_CHECK(pcx::json::IsSensitiveKey("node_uuid"));
    PCX_CHECK_FALSE(pcx::json::IsSensitiveKey("host"));
    PCX_CHECK_FALSE(pcx::json::IsSensitiveKey("port"));
}

PCX_TEST(RedactReplacesSensitiveValuesRecursively) {
    // 显式构造每层对象：隐式初始化列表在嵌套场景下容易产生歧义，
    // 而这段代码无法在本机编译验证，因此宁可写啰嗦一点。
    Json nested = Json::object();
    nested["auth_data"] = "Bearer secret";
    nested["port"] = 443;

    Json first = Json::object();
    first["token"] = "abc";
    Json second = Json::object();
    second["name"] = "ok";
    Json list = Json::array();
    list.push_back(first);
    list.push_back(second);

    Json input = Json::object();
    input["host"] = "example.com";
    input["password"] = "hunter2";
    input["nested"] = nested;
    input["list"] = list;

    const Json out = pcx::json::Redact(input);

    // 非敏感字段保持原样。
    PCX_CHECK_EQ(pcx::json::GetString(out, "host"), std::string("example.com"));
    // 敏感字段被替换。
    PCX_CHECK_EQ(pcx::json::GetString(out, "password"), std::string("<redacted>"));
    // 嵌套对象里的敏感字段同样被替换。
    PCX_CHECK_EQ(pcx::json::GetString(pcx::json::GetObject(out, "nested"), "auth_data"),
                 std::string("<redacted>"));
    PCX_CHECK_EQ(pcx::json::GetInt(pcx::json::GetObject(out, "nested"), "port"),
                 std::int64_t(443));
    // 数组元素也会被处理。
    const Json out_list = out.at("list");
    PCX_CHECK_EQ(pcx::json::GetString(out_list.at(0), "token"), std::string("<redacted>"));
    PCX_CHECK_EQ(pcx::json::GetString(out_list.at(1), "name"), std::string("ok"));

    // 输入不被修改。
    PCX_CHECK_EQ(pcx::json::GetString(input, "password"), std::string("hunter2"));
}

PCX_TEST(RedactStopsAtDepthLimit) {
    // 构造超过深度上限的嵌套，验证不会无限递归。
    Json deep = Json::object();
    Json* cursor = &deep;
    for (int i = 0; i < 40; ++i) {
        (*cursor)["child"] = Json::object();
        cursor = &(*cursor)["child"];
    }
    (*cursor)["password"] = "secret";

    const Json out = pcx::json::Redact(deep, 8);
    // 不崩溃即达成目的；同时确认深处的秘密没有以明文残留。
    PCX_CHECK_EQ(pcx::json::Compact(out).find("secret"), std::string::npos);
}

PCX_TEST(RedactUrlStripsQueryValuesAndUserInfo) {
    // 订阅链接的 token 就在查询串里，整条链接即凭据。
    const std::string got = pcx::json::RedactUrl(
        "https://host/api/v1/client/subscribe?token=SECRET123&flag=general");
    PCX_CHECK_EQ(got.find("SECRET123"), std::string::npos);
    PCX_CHECK_EQ(got, std::string(
        "https://host/api/v1/client/subscribe?token=<redacted>&flag=<redacted>"));
}

PCX_TEST(RedactUrlStripsUserInfo) {
    const std::string got = pcx::json::RedactUrl("https://user:pass@host/path");
    PCX_CHECK_EQ(got.find("pass"), std::string::npos);
    PCX_CHECK_EQ(got, std::string("https://<redacted>@host/path"));
}

PCX_TEST(RedactUrlLeavesPlainUrlsAlone) {
    PCX_CHECK_EQ(pcx::json::RedactUrl("https://example.com/a/b"),
                 std::string("https://example.com/a/b"));
}

PCX_TEST(RedactUrlKeepsTheFragment) {
    const std::string got = pcx::json::RedactUrl("https://host/p?token=x#frag");
    PCX_CHECK_EQ(got, std::string("https://host/p?token=<redacted>#frag"));
}

// ---------------------------------------------------------------------------
// 信封
// ---------------------------------------------------------------------------

PCX_TEST(ParseEnvelopeAcceptsBothKnownStatuses) {
    Json payload = Json::object();
    payload["a"] = 1;
    Json ok_root = Json::object();
    ok_root["status"] = "success";
    ok_root["message"] = "";
    ok_root["data"] = payload;

    const auto ok = pcx::json::ParseEnvelope(ok_root);
    PCX_CHECK(ok.has_value());
    PCX_CHECK(pcx::json::IsSuccess(*ok));
    PCX_CHECK_EQ(pcx::json::GetInt(ok->data, "a"), std::int64_t(1));

    Json fail_root = Json::object();
    fail_root["status"] = "fail";
    fail_root["message"] = "nope";
    fail_root["data"] = nullptr;

    const auto fail = pcx::json::ParseEnvelope(fail_root);
    PCX_CHECK(fail.has_value());
    PCX_CHECK_FALSE(pcx::json::IsSuccess(*fail));
    PCX_CHECK_EQ(fail->message, std::string("nope"));
}

PCX_TEST(ParseEnvelopeRejectsNonEnvelopes) {
    // 这些情况都意味着「地址不是 Xboard」或中间有拦截页，
    // 必须与「业务失败」区分开。
    PCX_CHECK_FALSE(pcx::json::ParseEnvelope(Json::array()).has_value());
    PCX_CHECK_FALSE(pcx::json::ParseEnvelope(Json::object()).has_value());
    PCX_CHECK_FALSE(pcx::json::ParseEnvelope({{"status", 200}}).has_value());
    PCX_CHECK_FALSE(pcx::json::ParseEnvelope({{"status", "ok"}}).has_value());
    PCX_CHECK_FALSE(pcx::json::ParseEnvelope("plain text").has_value());
}

PCX_TEST(SuccessIsDecidedByStatusNotMessage) {
    // message 可能被面板插件改写或本地化，拿它做判断会静默出错。
    const auto env = pcx::json::ParseEnvelope(
        {{"status", "fail"}, {"message", "success"}});
    PCX_CHECK(env.has_value());
    PCX_CHECK_FALSE(pcx::json::IsSuccess(*env));
}

// ---------------------------------------------------------------------------
// 解析
// ---------------------------------------------------------------------------

PCX_TEST(TryParseHandlesValidAndInvalidInput) {
    const auto valid = pcx::json::TryParse(R"({"a":1})");
    PCX_CHECK(valid.has_value());
    PCX_CHECK_EQ(pcx::json::GetInt(*valid, "a"), std::int64_t(1));

    // 解析失败是常态（用户填错地址、拦截页），不该抛异常。
    PCX_CHECK_FALSE(pcx::json::TryParse("{not json").has_value());
    PCX_CHECK_FALSE(pcx::json::TryParse("").has_value());
    PCX_CHECK_FALSE(pcx::json::TryParse("<html></html>").has_value());
}

PCX_TEST(CompactProducesSingleLineOutput) {
    const Json value = {{"a", 1}, {"b", "two"}};
    const std::string text = pcx::json::Compact(value);
    PCX_CHECK_EQ(text.find('\n'), std::string::npos);
}

PCX_TEST(CompactDoesNotThrowOnInvalidUtf8) {
    // 日志里出现非法 UTF-8 很常见（来自核心输出），
    // 序列化时抛异常会让「记录日志」这件事本身失败。
    Json value = {{"junk", std::string("\xff\xfe")}};
    const std::string text = pcx::json::Compact(value);
    PCX_CHECK_FALSE(text.empty());
}
