#include "core/ipc_protocol.hpp"

#include <atomic>
#include <chrono>
#include <random>
#include <sstream>
#include <cstdint>
#include <optional>
#include <string_view>
#include <vector>

#include "core/json_util.hpp"

namespace pcx::ipc {

using json::GetBool;
using json::GetInt;
using json::GetObject;
using json::GetString;

namespace {

// GetStringArray 取字符串数组；缺失或类型不符时返回空数组。
std::vector<std::string> GetStringArray(const Json& obj, std::string_view key) {
    std::vector<std::string> out;
    if (!obj.is_object()) {
        return out;
    }
    const auto it = obj.find(std::string(key));
    if (it == obj.end() || !it->is_array()) {
        return out;
    }
    out.reserve(it->size());
    for (const auto& item : *it) {
        if (item.is_string()) {
            out.push_back(item.get<std::string>());
        }
        // 非字符串元素跳过而不是报错：服务端可能将来加入结构化元素，
        // 那时旧客户端应当仍然能用。
    }
    return out;
}

std::string GetStringField(const Json& obj, std::string_view key) {
    return GetString(obj, key);
}

}  // namespace

// ---------------------------------------------------------------------------
// 操作名
// ---------------------------------------------------------------------------

std::vector<std::string> KnownOperations() {
    return {op::kGetCapabilities, op::kValidateProfile, op::kCompileProfile,
            op::kStart,           op::kStop,           op::kGetRuntimeState,
            op::kSwitchNode,      op::kSetNetworkMode, op::kWatchEvents,
            op::kRecoverNetwork,
            // 会话与订阅
            op::kLogin,           op::kLogout,         op::kGetSessionState,
            op::kRefreshSubscription, op::kListNodes,  op::kSelectNode,
            op::kGetOptions,      op::kSetOptions};
}

// ---------------------------------------------------------------------------
// Request
// ---------------------------------------------------------------------------

Json Request::ToJson() const {
    Json out = Json::object();
    out["id"] = id;
    out["operation"] = operation;
    out["protocolVersion"] = kProtocolVersion;
    out["schemaVersion"] = kSchemaVersion;
    if (!token.empty()) {
        // omitempty：token 为空时不写该字段。
        // 注意 token 是本地访问控制凭据，绝不能出现在日志里；
        // 因此这个函数的结果不允许直接落盘或打印。
        out["token"] = token;
    }
    if (params.has_value()) {
        out["params"] = *params;
    }
    return out;
}

// ---------------------------------------------------------------------------
// RpcError
// ---------------------------------------------------------------------------

RpcError RpcError::Parse(const Json& source) {
    RpcError out;
    out.error_code = GetStringField(source, "code");
    out.message_key = GetStringField(source, "messageKey");
    out.message = GetStringField(source, "message");
    out.field_path = GetStringField(source, "fieldPath");
    out.retryable = GetBool(source, "retryable");
    out.operation_id = GetStringField(source, "operationId");

    const Json details = GetObject(source, "detailsRedacted");
    for (auto it = details.begin(); it != details.end(); ++it) {
        if (it->is_string()) {
            out.details_redacted[it.key()] = it->get<std::string>();
        } else {
            // 非字符串值转成紧凑 JSON，避免整条信息丢失。
            out.details_redacted[it.key()] = json::Compact(*it);
        }
    }
    return out;
}

std::string RpcError::Describe() const {
    // 只输出码、键与字段路径。message 来自服务端，可能包含未脱敏内容，
    // 因此这里刻意不带它——日志泄漏凭据的代价远高于排障便利。
    std::ostringstream out;
    out << "ipc error code=" << (error_code.empty() ? "<none>" : error_code);
    if (!message_key.empty()) {
        out << " key=" << message_key;
    }
    if (!field_path.empty()) {
        out << " field=" << field_path;
    }
    if (retryable) {
        out << " retryable=true";
    }
    return out.str();
}

// ---------------------------------------------------------------------------
// Event
// ---------------------------------------------------------------------------

Event Event::Parse(const Json& source) {
    Event out;
    out.seq = static_cast<std::uint64_t>(GetInt(source, "seq"));
    out.kind = GetStringField(source, "kind");
    out.dropped = static_cast<std::uint64_t>(GetInt(source, "dropped"));
    if (const auto it = source.find("payload");
        it != source.end() && !it->is_null()) {
        out.payload = *it;
    }
    return out;
}

// ---------------------------------------------------------------------------
// Response
// ---------------------------------------------------------------------------

Response Response::Parse(const Json& source) {
    Response out;
    out.id = GetStringField(source, "id");
    out.ok = GetBool(source, "ok");
    out.protocol_version = static_cast<int>(GetInt(source, "protocolVersion"));
    out.schema_version = GetStringField(source, "schemaVersion");

    if (const auto it = source.find("result");
        it != source.end() && !it->is_null()) {
        out.result = *it;
    }
    if (const auto it = source.find("error");
        it != source.end() && it->is_object()) {
        out.error = RpcError::Parse(*it);
    }
    if (const auto it = source.find("event");
        it != source.end() && it->is_object()) {
        out.event = Event::Parse(*it);
    }
    return out;
}

// ---------------------------------------------------------------------------
// Handle
// ---------------------------------------------------------------------------

std::optional<Handle> Handle::Parse(const Json& source) {
    if (!source.is_object()) {
        return std::nullopt;
    }
    Handle out;
    out.network = GetStringField(source, "network");
    out.address = GetStringField(source, "address");
    out.token = GetStringField(source, "token");
    out.protocol_version = static_cast<int>(GetInt(source, "protocolVersion"));
    out.schema_version = GetStringField(source, "schemaVersion");
    out.server_pid = static_cast<int>(GetInt(source, "serverPid"));
    return out;
}

bool Handle::Validate(std::string* error) const {
    const auto fail = [error](const char* message) {
        if (error != nullptr) {
            *error = message;
        }
        return false;
    };
    if (network.empty()) {
        return fail("ipc: handle has no network");
    }
    if (address.empty()) {
        return fail("ipc: handle has no address");
    }
    if (token.empty()) {
        return fail("ipc: handle has no access token");
    }
    if (error != nullptr) {
        error->clear();
    }
    return true;
}

bool Handle::IsCompatible() const {
    // 精确相等才算兼容：不做「大版本相同就试试」这种尽力解析，
    // 那会把协议不匹配变成难以定位的字段缺失。
    return protocol_version == kProtocolVersion && schema_version == kSchemaVersion;
}

// ---------------------------------------------------------------------------
// Capabilities
// ---------------------------------------------------------------------------

CoreCapability CoreCapability::Parse(const Json& source) {
    CoreCapability out;
    out.name = GetStringField(source, "name");
    out.version = GetStringField(source, "version");
    out.installed = GetBool(source, "installed");
    out.verified_against = GetStringField(source, "verifiedAgainst");
    return out;
}

Capabilities Capabilities::Parse(const Json& source) {
    Capabilities out;
    out.platform = GetStringField(source, "platform");
    out.protocol_version = static_cast<int>(GetInt(source, "protocolVersion"));
    out.schema_version = GetStringField(source, "schemaVersion");
    out.operations = GetStringArray(source, "operations");
    out.traffic_entries = GetStringArray(source, "trafficEntries");
    out.network_takeover = GetBool(source, "networkTakeover");
    out.core_update = GetBool(source, "coreUpdate");

    if (const auto it = source.find("cores");
        it != source.end() && it->is_array()) {
        out.cores.reserve(it->size());
        for (const auto& item : *it) {
            if (item.is_object()) {
                out.cores.push_back(CoreCapability::Parse(item));
            }
        }
    }
    return out;
}

bool Capabilities::Supports(const std::string& operation) const {
    for (const auto& known : operations) {
        if (known == operation) {
            return true;
        }
    }
    return false;
}

// ---------------------------------------------------------------------------
// RuntimeState
// ---------------------------------------------------------------------------

ConflictInfo ConflictInfo::Parse(const Json& source) {
    ConflictInfo out;
    out.key = GetStringField(source, "key");
    out.expected = GetStringField(source, "expected");
    out.actual = GetStringField(source, "actual");
    return out;
}

RuntimeState RuntimeState::Parse(const Json& source) {
    RuntimeState out;
    out.state = GetStringField(source, "state");
    out.generation = static_cast<std::uint64_t>(GetInt(source, "generation"));
    out.core = GetStringField(source, "core");
    out.active_node = GetStringField(source, "activeNode");
    out.mode = GetStringField(source, "mode");
    out.last_error = GetStringField(source, "lastError");

    if (const auto it = source.find("conflicts");
        it != source.end() && it->is_array()) {
        out.conflicts.reserve(it->size());
        for (const auto& item : *it) {
            if (item.is_object()) {
                out.conflicts.push_back(ConflictInfo::Parse(item));
            }
        }
    }
    return out;
}

bool RuntimeState::NeedsAttention() const {
    // 这三个状态都需要用户或维护者介入，不能当作普通状态展示。
    return state == "recovery_required" || state == "failed" ||
           !conflicts.empty();
}

// ---------------------------------------------------------------------------
// 辅助
// ---------------------------------------------------------------------------

std::string NewRequestId() {
    // 进程内单调序号 + 随机前缀：既保证同一连接内不重复，
    // 也避免多个客户端实例撞 ID。
    static std::atomic<std::uint64_t> counter{0};
    static const std::string prefix = [] {
        std::random_device device;
        std::mt19937_64 engine(device());
        std::uniform_int_distribution<std::uint64_t> distribution;
        std::ostringstream out;
        out << std::hex << distribution(engine);
        return out.str();
    }();

    std::ostringstream out;
    out << prefix << "-" << counter.fetch_add(1);
    return out.str();
}

std::optional<Handle> ParseHandleJson(const std::string& text) {
    const auto parsed = json::TryParse(text);
    if (!parsed.has_value()) {
        return std::nullopt;
    }
    return Handle::Parse(*parsed);
}

Severity SeverityFor(const std::string& error_code) {
    if (error_code == code::kUnauthorized || error_code == code::kVersionMismatch ||
        error_code == code::kIncompatible) {
        // 这类问题用户自己改不了，需要重装或升级。
        return Severity::Fatal;
    }
    if (error_code == code::kMalformed || error_code == code::kInvalidParams ||
        error_code == code::kUnknownOperation) {
        // 客户端与服务端对不齐，属于应当修代码的问题。
        return Severity::Error;
    }
    if (error_code == code::kBusy || error_code == code::kNotRunning ||
        error_code == code::kTimeout || error_code == code::kBackpressure ||
        error_code == code::kTransport) {
        // 重试或换时机就可能成功。
        return Severity::Warning;
    }
    return Severity::Error;
}

}  // namespace pcx::ipc
