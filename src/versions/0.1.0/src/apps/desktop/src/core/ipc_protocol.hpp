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

// 会话与订阅操作。
//
// 这一组此前完全不存在：界面只能连接一份已经构造好的配置，
// 却没有办法登录、取订阅或列出节点。见 Go 端 protocol.go 的同一组常量。
inline constexpr const char* kLogin = "Login";
inline constexpr const char* kLogout = "Logout";
inline constexpr const char* kGetSessionState = "GetSessionState";
inline constexpr const char* kRefreshSubscription = "RefreshSubscription";
inline constexpr const char* kListNodes = "ListNodes";
inline constexpr const char* kSelectNode = "SelectNode";
inline constexpr const char* kGetOptions = "GetOptions";
inline constexpr const char* kSetOptions = "SetOptions";
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
// 会话与订阅的载荷
//
// 安全约定（与 Go 端同一节一致）：这些结构会被服务端序列化后交给界面进程，
// 因此**一律不得**包含面板会话凭据、订阅链接或节点密码。
// 界面侧同样不得把它们写进日志——日志可能被用户复制后公开。
// ---------------------------------------------------------------------------

// LoginParams 是 Login 的参数。
//
// password 只在内存与这一次请求里存在：**不写日志、不回显、不入 preferences**。
// 是否持久化由服务端按 remember_password 决定（默认 false，
// 也就是「关了程序就要重新登录」）。
struct LoginParams {
    // panel_url 为空表示沿用当前会话的地址。
    std::string panel_url;
    std::string email;
    std::string password;
    // allow_insecure_http 允许 http:// 面板地址，仅用于本地测试。
    bool allow_insecure_http = false;
    // remember_password 为真时才请服务端把密码写进安全存储。
    bool remember_password = false;

    // ToJson 生成请求参数。
    //
    // 空字段不写出（与 Go 的 omitempty 一致）：panelUrl 留空时省略它，
    // 服务端才会沿用当前地址；写成空串会被当成「把地址改成空」。
    Json ToJson() const;
};

// AccountView 是登录/恢复后返回的账户摘要。
struct AccountView {
    std::string email;
    std::string uuid;
    bool banned = false;
    bool expired = false;
    std::int64_t expired_at_unix = 0;
    std::int64_t transfer_enable = 0;
    std::int64_t uploaded_bytes = 0;
    std::int64_t downloaded_bytes = 0;
    double balance = 0.0;
    std::string plan_name;
    std::vector<std::string> warnings;

    static AccountView Parse(const Json& source);

    // UploadedAndDownloaded 返回已用流量。
    //
    // 单独一个方法是因为「已用」有两个来源：账户摘要里的
    // uploaded + downloaded，以及订阅用量里的 upload + download。
    // 把加法写在一个地方，可以避免两处算出不同的数字。
    std::int64_t UsedBytes() const;
};

// UsageView 是账户用量。
//
// present 为 false 时界面必须**隐藏**用量，而不是显示 0——
// 「用量 0」与「不知道用量」是两件事。
struct UsageView {
    bool present = false;
    std::int64_t upload = 0;
    std::int64_t download = 0;
    std::int64_t total = 0;
    std::int64_t expire_unix = 0;
    std::int64_t remaining = 0;

    static UsageView Parse(const Json& source);

    // UsedBytes 返回已用流量（上传 + 下载）。
    std::int64_t UsedBytes() const;
};

// SubscriptionView 描述订阅状态。
struct SubscriptionView {
    int node_count = 0;
    // rejected_count 是被拒绝的节点数。**必须显示出来**：
    // 服务端会把协议不支持或字段缺失的节点丢掉，静默丢弃会让用户
    // 以为「订阅里就是少了几个节点」。
    int rejected_count = 0;
    std::vector<std::string> warnings;
    std::string selected_node_id;
    std::int64_t last_sync_unix = 0;
    std::string plan_name;
    UsageView usage;

    static SubscriptionView Parse(const Json& source);

    // EffectivePlanName 返回应当显示的套餐名。
    //
    // 订阅与账户两处都带套餐名，取值可能一个有一个没有。
    // 判定写在这里，避免界面各写一遍而给出不一致的答案。
    std::string EffectivePlanName() const;
};

// SessionState 是 GetSessionState 的返回值。
//
// 它刻意不返回任何凭据。界面据此决定显示登录页还是主界面。
struct SessionState {
    // logged_in 表示是否持有可用会话。
    bool logged_in = false;
    // panel_url 是当前面板地址（用户填的，不是凭据）。
    std::string panel_url;
    std::optional<AccountView> account;
    std::optional<SubscriptionView> subscription;
    // secure_storage 表示本平台是否具备安全存储。
    //
    // 为 false 时界面**必须**提示「本次可用，但不会记住登录」——
    // 否则用户会以为下次启动仍是登录状态。
    bool secure_storage = false;

    static SessionState Parse(const Json& source);

    // RememberWarningRequired 报告是否需要显示「不会记住登录」的提示。
    //
    // 条件写在这里而不是散在界面代码里：登录页与专业模式都要显示它，
    // 两处各写一遍必然漂移。
    bool RememberWarningRequired() const { return !secure_storage; }
};

// NodeView 是一个节点的界面表示。
//
// **不含 password / uuid 等连接凭据**：节点的连接参数是秘密，
// 界面只需要能显示和选择它。
struct NodeView {
    std::string id;
    std::string name;
    std::string protocol;
    std::string server;
    int port = 0;
    bool selected = false;

    static NodeView Parse(const Json& source);

    // Label 返回列表里显示的一行文字。
    //
    // 协议与地址都放进去：同名节点在面板里很常见，
    // 只显示名字会让用户无法区分自己选的是哪一个。
    std::string Label() const;
};

// NodeList 是 ListNodes 的返回值。
struct NodeList {
    std::vector<NodeView> nodes;

    static NodeList Parse(const Json& source);

    // SelectedIndex 返回被标记为选中的节点下标；没有时返回 -1。
    //
    // 返回 -1 而不是 0：服务端可能一个都没标记（比如选中的节点
    // 已被订阅淘汰），此时界面应当保持不选中，而不是跳到第一个。
    int SelectedIndex() const;
};

// SelectNodeParams 是 SelectNode 的参数。
//
// 允许按名字或 ID（二选一）：界面习惯用名字，而名字可能重名，
// 此时服务端返回 node_name_ambiguous 并要求改用 ID。
struct SelectNodeParams {
    std::string id;
    std::string name;

    // ToJson 生成请求参数；两者都为空时返回空对象，
    // 由服务端按 invalid_params 拒绝，而不是在这里编造一个取值。
    Json ToJson() const;

    // IsUsable 报告参数里至少有一个标识。
    bool IsUsable() const { return !id.empty() || !name.empty(); }
};

// SelectNodeResult 返回最终选定的节点 ID。
//
// 回传 ID 是必要的：界面按名字请求，而实际生效的是 ID；
// 不回传的话界面只能自己猜解析结果。
struct SelectNodeResult {
    std::string selected_node_id;

    static SelectNodeResult Parse(const Json& source);
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
