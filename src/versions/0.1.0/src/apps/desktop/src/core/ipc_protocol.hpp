// UI 与后台服务之间的 IPC 协议 / IPC protocol between the UI and the service.
//
// **这是 Go 端 runtime/ipc/protocol.go 的镜像。**
// 两侧的 JSON 字段名必须逐字一致；改动任何一侧都要同时改另一侧，
// 否则表现是「服务端说成功、界面拿不到字段」这类难查的问题。
//
// 设计约束（设计基线 §6.2）：
//   - 只走本地通道（Unix socket / 命名管道），不开放公网管理端口；
//   - 握手校验 protocolVersion 与 schemaVersion，不匹配就明确拒绝，
//     **不做尽力解析**——尽力解析会把「字段缺失」变成难以定位的运行时错误；
//   - 错误用统一结构，界面按 messageKey 本地化，不解析 message；
//   - 错误里不得包含完整 token 或其它凭据。
#pragma once

#include <cstdint>
#include <map>
#include <optional>
#include <string>
#include <vector>

#include <nlohmann/json.hpp>

namespace pcx::ipc {

using Json = nlohmann::json;

// ---------------------------------------------------------------------------
// 版本
// ---------------------------------------------------------------------------

// kProtocolVersion 是线协议版本，必须与 Go 端 ProtocolVersion 相同。
inline constexpr int kProtocolVersion = PCX_IPC_PROTOCOL_VERSION;

// kSchemaVersion 是载荷结构版本，必须与 Go 端 SchemaVersion 相同。
inline constexpr const char* kSchemaVersion = PCX_IPC_SCHEMA_VERSION;

// ---------------------------------------------------------------------------
// 操作名
//
// 取值与 Go 端 Operation 常量一一对应。
// 用字符串常量而不是 enum：枚举遇到服务端新增操作会解析失败，
// 而字符串可以让界面优雅地报「不支持该操作」。
// ---------------------------------------------------------------------------

namespace op {
inline constexpr const char* kGetCapabilities = "GetCapabilities";
inline constexpr const char* kValidateProfile = "ValidateProfile";
inline constexpr const char* kCompileProfile = "CompileProfile";
inline constexpr const char* kStart = "Start";
inline constexpr const char* kStop = "Stop";
inline constexpr const char* kGetRuntimeState = "GetRuntimeState";
inline constexpr const char* kSwitchNode = "SwitchNode";
inline constexpr const char* kSetNetworkMode = "SetNetworkMode";
inline constexpr const char* kWatchEvents = "WatchEvents";
inline constexpr const char* kRecoverNetwork = "RecoverNetwork";
}  // namespace op

// KnownOperations 返回全部已知操作。
std::vector<std::string> KnownOperations();

// ---------------------------------------------------------------------------
// 稳定错误码（与 Go 端一致）
// ---------------------------------------------------------------------------

namespace code {
inline constexpr const char* kMalformed = "malformed_request";
inline constexpr const char* kVersionMismatch = "version_mismatch";
inline constexpr const char* kUnauthorized = "unauthorized";
inline constexpr const char* kUnknownOperation = "unknown_operation";
inline constexpr const char* kInvalidParams = "invalid_params";
inline constexpr const char* kBusy = "busy";
inline constexpr const char* kNotRunning = "not_running";
inline constexpr const char* kInternal = "internal_error";
inline constexpr const char* kTimeout = "timeout";
inline constexpr const char* kBackpressure = "backpressure";
// 以下是客户端本地产生的码（服务端不会发）。
inline constexpr const char* kTransport = "transport_error";
inline constexpr const char* kIncompatible = "incompatible_client";
}  // namespace code

// ---------------------------------------------------------------------------
// 报文
// ---------------------------------------------------------------------------

// Request 是一次调用请求。
//
// ID 由客户端生成，用于把响应与请求配对。
struct Request {
    std::string id;
    std::string operation;
    std::string token;
    // Params 缺省时不序列化该字段（与 Go 的 omitempty 一致）。
    std::optional<Json> params;

    Json ToJson() const;
};

// RpcError 是统一错误结构。
struct RpcError {
    std::string error_code;
    std::string message_key;
    std::string message;
    std::string field_path;
    bool retryable = false;
    std::string operation_id;
    std::map<std::string, std::string> details_redacted;

    // Parse 从 JSON 解析；缺失字段取零值。
    static RpcError Parse(const Json& source);

    // Describe 返回**已脱敏**的单行描述，供日志使用。
    std::string Describe() const;
};

// Event 是服务端主动推送的事件。
struct Event {
    std::uint64_t seq = 0;
    std::string kind;
    Json payload = Json::object();
    // dropped 是本次推送前因背压被丢弃的事件数。
    // 它让界面知道「你看到的不是全部」，而不是静默地少几条日志。
    std::uint64_t dropped = 0;

    static Event Parse(const Json& source);
};

// Response 是一次调用响应，或一条事件推送。
struct Response {
    std::string id;
    bool ok = false;
    Json result = Json::object();
    std::optional<RpcError> error;
    std::optional<Event> event;
    int protocol_version = 0;
    std::string schema_version;

    static Response Parse(const Json& source);
};

// ---------------------------------------------------------------------------
// 连接句柄
// ---------------------------------------------------------------------------

// Handle 是客户端连接服务所需的全部信息，由服务端写到状态文件。
//
// Token 是**本地访问控制凭据**：状态文件必须以仅当前用户可读的权限创建，
// 且 token 绝不写入日志或诊断包。
struct Handle {
    std::string network;  // "unix" / "tcp" / "npipe"
    std::string address;
    std::string token;
    int protocol_version = 0;
    std::string schema_version;
    int server_pid = 0;

    static std::optional<Handle> Parse(const Json& source);

    // Validate 校验句柄可用性。
    //
    // error 为 nullptr 时只返回布尔值。
    bool Validate(std::string* error = nullptr) const;

    // IsCompatible 报告句柄声明的版本是否与我们一致。
    bool IsCompatible() const;
};

// ---------------------------------------------------------------------------
// 返回值结构
// ---------------------------------------------------------------------------

// CoreCapability 描述单个核心的可用性。
struct CoreCapability {
    std::string name;
    std::string version;
    bool installed = false;
    // verified_against 记录能力表所依据的核心版本；未实测时为 "unverified"。
    std::string verified_against;

    static CoreCapability Parse(const Json& source);
};

// Capabilities 是 GetCapabilities 的返回值。
//
// 界面据此决定哪些元素可用，而不是假设服务一定支持某项能力（设计基线 §4.2）。
struct Capabilities {
    std::string platform;
    int protocol_version = 0;
    std::string schema_version;
    std::vector<std::string> operations;
    std::vector<CoreCapability> cores;
    std::vector<std::string> traffic_entries;
    bool network_takeover = false;
    bool core_update = false;

    static Capabilities Parse(const Json& source);

    // Supports 报告服务端是否声明支持某操作。
    bool Supports(const std::string& operation) const;
};

// ConflictInfo 描述一处网络设置冲突。
struct ConflictInfo {
    std::string key;
    // expected 是我们当初写入的值；actual 是当前实际值。
    // 两者都不含凭据，可以安全展示给用户。
    std::string expected;
    std::string actual;

    static ConflictInfo Parse(const Json& source);
};

// RuntimeState 是 GetRuntimeState 的返回值。
struct RuntimeState {
    std::string state;
    std::uint64_t generation = 0;
    std::string core;
    std::string active_node;
    std::string mode;
    std::string last_error;
    std::vector<ConflictInfo> conflicts;

    static RuntimeState Parse(const Json& source);

    // IsConnected 报告是否处于已连接状态。
    bool IsConnected() const { return state == "connected"; }
    // NeedsAttention 报告是否有需要用户处理的情况。
    bool NeedsAttention() const;
};

// ---------------------------------------------------------------------------
// 辅助
// ---------------------------------------------------------------------------

// NewRequestId 生成一个请求 ID。
//
// 不要求密码学随机：它只用于把响应与请求配对。
std::string NewRequestId();

// ParseHandleJson 解析状态文件内容。
std::optional<Handle> ParseHandleJson(const std::string& text);

// Severity 把错误码映射到界面严重级别，供统一展示逻辑使用。
enum class Severity { Info, Warning, Error, Fatal };
Severity SeverityFor(const std::string& error_code);

}  // namespace pcx::ipc
