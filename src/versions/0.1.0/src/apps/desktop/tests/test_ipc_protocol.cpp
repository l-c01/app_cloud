// ipc_protocol 的单元测试。
//
// 这些用例同时是对 **Go 端协议定义的核对**：字段名一旦不一致，
// 这里会因为取不到值而失败。
#include "core/ipc_protocol.hpp"
#include "core/json_util.hpp"
#include "test_framework.hpp"

using pcx::ipc::Capabilities;
using pcx::ipc::Handle;
using pcx::ipc::Json;
using pcx::ipc::Request;
using pcx::ipc::Response;
using pcx::ipc::RpcError;
using pcx::ipc::RuntimeState;

// ---------------------------------------------------------------------------
// 版本常量
// ---------------------------------------------------------------------------

PCX_TEST(ProtocolConstantsMatchTheGoSide) {
    // 这两个值必须与 runtime/ipc/protocol.go 的 ProtocolVersion 与
    // SchemaVersion 完全相同；不一致时握手会被对端拒绝。
    PCX_CHECK_EQ(pcx::ipc::kProtocolVersion, 1);
    PCX_CHECK_EQ(std::string(pcx::ipc::kSchemaVersion), std::string("1.0.0"));
}

PCX_TEST(KnownOperationsMatchTheGoSide) {
    const auto operations = pcx::ipc::KnownOperations();
    // Go 端 KnownOperations 返回 10 个操作。
    PCX_CHECK_EQ(operations.size(), std::size_t(10));

    const std::vector<std::string> expected = {
        "GetCapabilities", "ValidateProfile", "CompileProfile", "Start",
        "Stop",            "GetRuntimeState", "SwitchNode",    "SetNetworkMode",
        "WatchEvents",     "RecoverNetwork"};
    for (const auto& name : expected) {
        bool found = false;
        for (const auto& actual : operations) {
            if (actual == name) {
                found = true;
                break;
            }
        }
        PCX_CHECK(found);
    }
}

// ---------------------------------------------------------------------------
// Request 序列化
// ---------------------------------------------------------------------------

PCX_TEST(RequestSerialisesWithGoFieldNames) {
    Request request;
    request.id = "abc-1";
    request.operation = pcx::ipc::op::kStart;
    request.token = "secret-token";

    const Json out = request.ToJson();

    // 字段名与 Go 的 json tag 一致。
    PCX_CHECK_EQ(pcx::json::GetString(out, "id"), std::string("abc-1"));
    PCX_CHECK_EQ(pcx::json::GetString(out, "operation"), std::string("Start"));
    PCX_CHECK_EQ(pcx::json::GetInt(out, "protocolVersion"), std::int64_t(1));
    PCX_CHECK_EQ(pcx::json::GetString(out, "schemaVersion"), std::string("1.0.0"));
    PCX_CHECK_EQ(pcx::json::GetString(out, "token"), std::string("secret-token"));
    // 没有参数时不写 params（对应 Go 的 omitempty）。
    PCX_CHECK(out.find("params") == out.end());
}

PCX_TEST(RequestSerialisesParamsWhenPresent) {
    Request request;
    request.id = "abc-2";
    request.operation = pcx::ipc::op::kSwitchNode;
    request.token = "t";
    request.params = Json{{"nodeId", "node-7"}};

    const Json out = request.ToJson();
    const Json params = pcx::json::GetObject(out, "params");
    PCX_CHECK_EQ(pcx::json::GetString(params, "nodeId"), std::string("node-7"));
}

PCX_TEST(RequestOmitsTokenWhenEmpty) {
    Request request;
    request.id = "abc-3";
    request.operation = pcx::ipc::op::kGetCapabilities;
    // 匿名请求（理论上服务端会拒绝，但序列化行为要正确）。
    const Json out = request.ToJson();
    PCX_CHECK(out.find("token") == out.end());
}

// ---------------------------------------------------------------------------
// Response 解析
// ---------------------------------------------------------------------------

PCX_TEST(ResponseParsesSuccess) {
    Json payload = Json::object();
    payload["platform"] = "windows";
    Json source = Json::object();
    source["id"] = "abc-1";
    source["ok"] = true;
    source["result"] = payload;
    source["protocolVersion"] = 1;
    source["schemaVersion"] = "1.0.0";

    const Response response = Response::Parse(source);
    PCX_CHECK(response.ok);
    PCX_CHECK_EQ(response.id, std::string("abc-1"));
    PCX_CHECK_EQ(response.protocol_version, 1);
    PCX_CHECK_FALSE(response.error.has_value());
    PCX_CHECK_EQ(pcx::json::GetString(response.result, "platform"),
                 std::string("windows"));
}

PCX_TEST(ResponseParsesErrorEnvelope) {
    Json details = Json::object();
    details["serverProtocol"] = "1";
    details["clientProtocol"] = "2";

    Json error = Json::object();
    error["code"] = "version_mismatch";
    error["messageKey"] = "ipc.version_mismatch.protocol";
    error["message"] = "internal text";
    error["retryable"] = false;
    error["detailsRedacted"] = details;

    Json source = Json::object();
    source["id"] = "abc-2";
    source["ok"] = false;
    source["error"] = error;
    source["protocolVersion"] = 1;
    source["schemaVersion"] = "1.0.0";

    const Response response = Response::Parse(source);
    PCX_CHECK_FALSE(response.ok);
    PCX_CHECK(response.error.has_value());
    PCX_CHECK_EQ(response.error->error_code, std::string("version_mismatch"));
    PCX_CHECK_EQ(response.error->message_key,
                 std::string("ipc.version_mismatch.protocol"));
    PCX_CHECK_EQ(response.error->details_redacted.at("clientProtocol"),
                 std::string("2"));
}

PCX_TEST(ErrorDescribeNeverLeaksTheServerMessage) {
    // message 来自服务端，可能包含未脱敏内容；日志里不该带它。
    Json error = Json::object();
    error["code"] = "internal_error";
    error["messageKey"] = "ipc.not_ok";
    error["message"] = "token=SUPERSECRET";
    error["retryable"] = true;

    const RpcError parsed = RpcError::Parse(error);
    const std::string described = parsed.Describe();
    PCX_CHECK_EQ(described.find("SUPERSECRET"), std::string::npos);
    PCX_CHECK(described.find("internal_error") != std::string::npos);
    PCX_CHECK(described.find("retryable=true") != std::string::npos);
}

PCX_TEST(ResponseParsesEvent) {
    Json payload = Json::object();
    payload["state"] = "connected";

    Json event = Json::object();
    event["seq"] = 42;
    event["kind"] = "state_changed";
    event["payload"] = payload;
    event["dropped"] = 3;

    Json source = Json::object();
    source["ok"] = true;
    source["event"] = event;
    source["protocolVersion"] = 1;
    source["schemaVersion"] = "1.0.0";

    const Response response = Response::Parse(source);
    PCX_CHECK(response.event.has_value());
    PCX_CHECK_EQ(response.event->seq, std::uint64_t(42));
    PCX_CHECK_EQ(response.event->kind, std::string("state_changed"));
    // 丢弃计数必须能读到：界面要据此告诉用户「你看到的不是全部」。
    PCX_CHECK_EQ(response.event->dropped, std::uint64_t(3));
    PCX_CHECK_EQ(pcx::json::GetString(response.event->payload, "state"),
                 std::string("connected"));
}

// ---------------------------------------------------------------------------
// Handle
// ---------------------------------------------------------------------------

PCX_TEST(HandleParsesAndValidates) {
    Json source = Json::object();
    source["network"] = "unix";
    source["address"] = "/run/user/1000/proxyclient.sock";
    source["token"] = "abc123";
    source["protocolVersion"] = 1;
    source["schemaVersion"] = "1.0.0";
    source["serverPid"] = 4242;

    const auto handle = Handle::Parse(source);
    PCX_CHECK(handle.has_value());
    PCX_CHECK_EQ(handle->network, std::string("unix"));
    PCX_CHECK_EQ(handle->server_pid, 4242);
    PCX_CHECK(handle->IsCompatible());

    std::string error;
    PCX_CHECK(handle->Validate(&error));
    PCX_CHECK(error.empty());
}

PCX_TEST(HandleValidationRejectsIncompleteHandles) {
    std::string error;

    Handle missing_token;
    missing_token.network = "unix";
    missing_token.address = "/tmp/x.sock";
    PCX_CHECK_FALSE(missing_token.Validate(&error));
    PCX_CHECK_FALSE(error.empty());

    PCX_CHECK_FALSE(Handle{}.Validate(nullptr));
}

PCX_TEST(HandleCompatibilityIsExact) {
    Handle handle;
    handle.protocol_version = pcx::ipc::kProtocolVersion;
    handle.schema_version = pcx::ipc::kSchemaVersion;
    PCX_CHECK(handle.IsCompatible());

    // 结构版本不同也必须拒绝：不做「大版本相同就试试」这种尽力解析。
    handle.schema_version = "2.0.0";
    PCX_CHECK_FALSE(handle.IsCompatible());

    handle.schema_version = pcx::ipc::kSchemaVersion;
    handle.protocol_version = 99;
    PCX_CHECK_FALSE(handle.IsCompatible());
}

PCX_TEST(ParseHandleJsonHandlesBadInput) {
    PCX_CHECK_FALSE(pcx::ipc::ParseHandleJson("").has_value());
    PCX_CHECK_FALSE(pcx::ipc::ParseHandleJson("{not json").has_value());
    PCX_CHECK_FALSE(pcx::ipc::ParseHandleJson("[]").has_value());
    PCX_CHECK(pcx::ipc::ParseHandleJson(R"({"network":"unix"})").has_value());
}

// ---------------------------------------------------------------------------
// Capabilities 与 RuntimeState
// ---------------------------------------------------------------------------

PCX_TEST(CapabilitiesParseCoresAndOperations) {
    Json core = Json::object();
    core["name"] = "sing-box";
    core["version"] = "1.9.0";
    core["installed"] = true;
    core["verifiedAgainst"] = "unverified";

    Json cores = Json::array();
    cores.push_back(core);

    Json entries = Json::array();
    entries.push_back("system_proxy");
    entries.push_back("tun");

    Json operations = Json::array();
    operations.push_back("Start");
    operations.push_back("Stop");

    Json source = Json::object();
    source["platform"] = "windows";
    source["protocolVersion"] = 1;
    source["schemaVersion"] = "1.0.0";
    source["operations"] = operations;
    source["cores"] = cores;
    source["trafficEntries"] = entries;
    source["networkTakeover"] = true;
    source["coreUpdate"] = false;

    const Capabilities capabilities = Capabilities::Parse(source);
    PCX_CHECK_EQ(capabilities.platform, std::string("windows"));
    PCX_CHECK_EQ(capabilities.cores.size(), std::size_t(1));
    PCX_CHECK_EQ(capabilities.cores[0].name, std::string("sing-box"));
    // 界面必须能把「未实测」如实展示出来。
    PCX_CHECK_EQ(capabilities.cores[0].verified_against, std::string("unverified"));
    PCX_CHECK_EQ(capabilities.traffic_entries.size(), std::size_t(2));
    PCX_CHECK(capabilities.network_takeover);
    PCX_CHECK_FALSE(capabilities.core_update);
    PCX_CHECK(capabilities.Supports("Start"));
    PCX_CHECK_FALSE(capabilities.Supports("RecoverNetwork"));
}

PCX_TEST(RuntimeStateParsesConflicts) {
    Json conflict = Json::object();
    conflict["key"] = "system_proxy.server";
    conflict["expected"] = "127.0.0.1:7890";
    conflict["actual"] = "10.0.0.9:8080";

    Json conflicts = Json::array();
    conflicts.push_back(conflict);

    Json source = Json::object();
    source["state"] = "recovery_required";
    source["generation"] = 7;
    source["core"] = "xray";
    source["activeNode"] = "node-3";
    source["mode"] = "system_proxy";
    source["lastError"] = "network recovery failed";
    source["conflicts"] = conflicts;

    const RuntimeState state = RuntimeState::Parse(source);
    PCX_CHECK_EQ(state.state, std::string("recovery_required"));
    PCX_CHECK_EQ(state.generation, std::uint64_t(7));
    PCX_CHECK_EQ(state.active_node, std::string("node-3"));
    PCX_CHECK_EQ(state.conflicts.size(), std::size_t(1));
    PCX_CHECK_EQ(state.conflicts[0].actual, std::string("10.0.0.9:8080"));
    // 有冲突或处于恢复状态时必须让界面提示用户介入。
    PCX_CHECK(state.NeedsAttention());
    PCX_CHECK_FALSE(state.IsConnected());
}

PCX_TEST(RuntimeStateHealthyIsNotFlagged) {
    Json source = Json::object();
    source["state"] = "connected";
    source["generation"] = 1;

    const RuntimeState state = RuntimeState::Parse(source);
    PCX_CHECK(state.IsConnected());
    PCX_CHECK_FALSE(state.NeedsAttention());
}

// ---------------------------------------------------------------------------
// 辅助
// ---------------------------------------------------------------------------

PCX_TEST(RequestIdsAreUniqueWithinAProcess) {
    const std::string first = pcx::ipc::NewRequestId();
    const std::string second = pcx::ipc::NewRequestId();
    PCX_CHECK(first != second);
    PCX_CHECK_FALSE(first.empty());
}

PCX_TEST(SeverityMappingIsStable) {
    using pcx::ipc::Severity;
    // 用户自己改不了的问题要按最严重级别展示。
    PCX_CHECK(pcx::ipc::SeverityFor(pcx::ipc::code::kVersionMismatch) == Severity::Fatal);
    PCX_CHECK(pcx::ipc::SeverityFor(pcx::ipc::code::kUnauthorized) == Severity::Fatal);
    // 重试可能成功的算警告。
    PCX_CHECK(pcx::ipc::SeverityFor(pcx::ipc::code::kBusy) == Severity::Warning);
    PCX_CHECK(pcx::ipc::SeverityFor(pcx::ipc::code::kTransport) == Severity::Warning);
    // 两端对不齐属于要修代码的问题。
    PCX_CHECK(pcx::ipc::SeverityFor(pcx::ipc::code::kMalformed) == Severity::Error);
    PCX_CHECK(pcx::ipc::SeverityFor("something_new") == Severity::Error);
}
