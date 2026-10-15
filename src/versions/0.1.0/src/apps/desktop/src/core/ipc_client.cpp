#include "core/ipc_client.hpp"

#include <cstdlib>
#include <filesystem>
#include <fstream>
#include <sstream>
#include <chrono>
#include <cstddef>
#include <cstdint>
#include <mutex>
#include <optional>
#include <thread>
#include <utility>

#include "core/json_util.hpp"
#include "core/options.hpp"

namespace pcx::ipc {

namespace {

// kDefaultCallTimeout 是普通调用的默认上限。
constexpr std::chrono::milliseconds kDefaultCallTimeout{15000};

// 读取缓冲区大小。取 8 KiB：单条事件通常远小于它，
// 而它又小到不会让「一次读到多条报文」的处理变得罕见。
constexpr std::size_t kReadBufferSize = 8 * 1024;

std::string StateFileName() { return "service.json"; }

}  // namespace

// ---------------------------------------------------------------------------
// 生命周期
// ---------------------------------------------------------------------------

IpcClient::IpcClient() = default;

IpcClient::~IpcClient() {
    Disconnect();
}

// ---------------------------------------------------------------------------
// 连接
// ---------------------------------------------------------------------------

bool IpcClient::Connect(const Handle& handle, std::string* error) {
    const auto fail = [&](const std::string& reason) {
        {
            std::lock_guard<std::mutex> lock(mutex_);
            last_error_ = reason;
        }
        if (error != nullptr) {
            *error = reason;
        }
        return false;
    };

    // 先断开旧连接，避免同时存在两条通道。
    Disconnect();

    std::string validation_error;
    if (!handle.Validate(&validation_error)) {
        return fail(validation_error);
    }
    if (!handle.IsCompatible()) {
        // 不兼容时不连接：连上去也只会收到语焉不详的握手失败，
        // 不如在这里给出可以照着排查的原因。
        std::ostringstream reason;
        reason << "the service speaks protocol " << handle.protocol_version
               << " / schema " << handle.schema_version << ", this client speaks "
               << kProtocolVersion << " / " << kSchemaVersion
               << "; reinstall or upgrade the matching version";
        return fail(reason.str());
    }

    std::string connect_error;
    // 这里必须写全名 pcx::ipc::Connect。
    //
    // 成员函数 IpcClient::Connect 会遮蔽命名空间里同名的自由函数 Connect：
    // 不加限定的 `Connect(...)` 只会去找成员，然后因为参数个数不符而编译失败。
    // 两者名字相同但职责不同——成员负责整条连接的生命周期，
    // 自由函数只负责按 network/address 建出一条通道。
    auto transport = pcx::ipc::Connect(handle.network, handle.address, &connect_error);
    if (!transport) {
        return fail(connect_error.empty() ? "cannot reach the local service" : connect_error);
    }

    {
        std::lock_guard<std::mutex> lock(mutex_);
        transport_ = std::move(transport);
        token_ = handle.token;
        responses_.clear();
        events_.clear();
        last_error_.clear();
        stopping_.store(false);
        dropped_locally_.store(0);
        dropped_by_server_.store(0);
        last_event_seq_.store(0);
    }

    // 读取超时设为 500ms，让读取线程能定期检查停止标志。
    //
    // 若平台不支持读超时（Windows 命名管道），读取会一直阻塞，
    // 此时靠 Disconnect 里的 Close 打断它——这一点在 Transport 接口里说明了。
    (void)transport_->SetReadTimeout(std::chrono::milliseconds(500));

    {
        std::lock_guard<std::mutex> lock(mutex_);
        reader_ = std::thread([this] { ReaderLoop(); });
    }
    SetConnected(true, "");
    return true;
}

void IpcClient::Disconnect() {
    {
        std::lock_guard<std::mutex> lock(mutex_);
        if (transport_) {
            // 关闭通道以打断阻塞中的读取。
            transport_->Close();
        }
        stopping_.store(true);
    }
    // 唤醒所有等待中的调用方，否则它们会一直等到超时。
    FailPending("the connection was closed");

    std::thread reader;
    {
        std::lock_guard<std::mutex> lock(mutex_);
        if (reader_.joinable()) {
            reader = std::move(reader_);
        }
    }
    if (reader.joinable()) {
        reader.join();
    }

    {
        std::lock_guard<std::mutex> lock(mutex_);
        transport_.reset();
        token_.clear();
        responses_.clear();
        events_.clear();
    }
    SetConnected(false, "");
}

bool IpcClient::IsConnected() const { return connected_.load(); }

// ---------------------------------------------------------------------------
// 读取线程
// ---------------------------------------------------------------------------

void IpcClient::ReaderLoop() {
    std::string buffer;
    buffer.reserve(kReadBufferSize);

    for (;;) {
        if (stopping_.load()) {
            break;
        }

        Transport* transport = nullptr;
        {
            std::lock_guard<std::mutex> lock(mutex_);
            transport = transport_.get();
        }
        if (transport == nullptr) {
            break;
        }

        char chunk[kReadBufferSize];
        bool ok = false;
        std::string read_error;
        const std::size_t got = transport->Read(chunk, sizeof(chunk), &ok, &read_error);

        if (!ok) {
            // 读超时不是故障：它只是让我们有机会检查停止标志，
            // 也是读取线程在 Windows 命名管道之外唯一能定期醒来的方式。
            if (read_error == "read timed out") {
                continue;
            }
            if (stopping_.load()) {
                break;
            }
            SetConnected(false, read_error.empty() ? "the connection was lost" : read_error);
            FailPending(last_error_);
            break;
        }

        if (got == 0) {
            // 对端正常关闭。
            if (stopping_.load()) {
                break;
            }
            SetConnected(false, "the service closed the connection");
            FailPending("the service closed the connection");
            break;
        }

        buffer.append(chunk, got);

        // 按换行组帧。一次读取可能包含半条或多条报文。
        std::size_t pos = 0;
        for (;;) {
            const auto newline = buffer.find('\n', pos);
            if (newline == std::string::npos) {
                break;
            }
            std::string line = buffer.substr(pos, newline - pos);
            pos = newline + 1;
            // 去掉 \r，兼容以 CRLF 结尾的写入方。
            if (!line.empty() && line.back() == '\r') {
                line.pop_back();
            }
            if (!line.empty()) {
                HandleLine(line);
            }
        }
        buffer.erase(0, pos);
    }

    SetConnected(false, last_error_);
}

void IpcClient::HandleLine(const std::string& line) {
    const auto parsed = json::TryParse(line);
    if (!parsed.has_value()) {
        // 无法解析的报文不该让读取线程退出：服务端将来可能推送
        // 本客户端不认识的报文类型，丢弃并计数比断开更有韧性。
        dropped_locally_.fetch_add(1);
        return;
    }

    const Response response = Response::Parse(*parsed);

    if (response.event.has_value()) {
        dropped_by_server_.fetch_add(response.event->dropped);
        last_event_seq_.store(response.event->seq);
        EnqueueEvent(*response.event);
        return;
    }

    // 普通响应：按 ID 配对。
    {
        std::lock_guard<std::mutex> lock(mutex_);
        if (response.id.empty()) {
            // 没有 ID 也不是事件：无法配对，丢弃。
            dropped_locally_.fetch_add(1);
            return;
        }
        responses_[response.id] = response;
    }
    response_ready_.notify_all();
}

void IpcClient::EnqueueEvent(const Event& event) {
    {
        std::lock_guard<std::mutex> lock(mutex_);
        if (events_.size() >= event_queue_limit_) {
            // 队列满时丢弃**最旧**的事件，保留最新的。
            // 理由：界面最需要知道的是当前状态，而不是几分钟前的状态。
            events_.pop_front();
            dropped_locally_.fetch_add(1);
        }
        events_.push_back(event);
    }
}

void IpcClient::FailPending(const std::string& reason) {
    {
        std::lock_guard<std::mutex> lock(mutex_);
        if (!reason.empty()) {
            last_error_ = reason;
        }
    }
    // 等待方靠 connected_ 变 false 判断失败，这里只需唤醒它们。
    response_ready_.notify_all();
}

void IpcClient::SetConnected(bool connected, const std::string& reason) {
    connected_.store(connected);
    if (!connected && !reason.empty()) {
        std::lock_guard<std::mutex> lock(mutex_);
        last_error_ = reason;
    }
    response_ready_.notify_all();
}

// ---------------------------------------------------------------------------
// 调用
// ---------------------------------------------------------------------------

std::optional<Response> IpcClient::Call(const std::string& operation,
                                        const std::optional<Json>& params,
                                        std::chrono::milliseconds timeout,
                                        std::string* error) {
    if (timeout.count() <= 0) {
        timeout = kDefaultCallTimeout;
    }
    if (!IsConnected()) {
        if (error != nullptr) {
            *error = last_error_.empty() ? "not connected to the local service" : last_error_;
        }
        return std::nullopt;
    }

    Request request;
    request.id = NewRequestId();
    request.operation = operation;
    request.params = params;

    Transport* transport = nullptr;
    {
        std::lock_guard<std::mutex> lock(mutex_);
        transport = transport_.get();
        request.token = token_;
        // 预留位置，避免响应比这里的插入更早到达时被当成「无人认领」而丢弃。
        responses_.erase(request.id);
    }
    if (transport == nullptr) {
        if (error != nullptr) {
            *error = "not connected to the local service";
        }
        return std::nullopt;
    }

    std::string payload = json::Compact(request.ToJson());
    payload.push_back('\n');

    std::string write_error;
    if (!transport->Write(payload.data(), payload.size(), &write_error)) {
        if (error != nullptr) {
            *error = write_error.empty() ? "cannot send the request" : write_error;
        }
        return std::nullopt;
    }

    const auto deadline = std::chrono::steady_clock::now() + timeout;
    std::unique_lock<std::mutex> lock(mutex_);

    for (;;) {
        const auto it = responses_.find(request.id);
        if (it != responses_.end()) {
            Response response = it->second;
            responses_.erase(it);
            return response;
        }
        if (!connected_.load()) {
            if (error != nullptr) {
                *error = last_error_.empty() ? "the connection was lost" : last_error_;
            }
            return std::nullopt;
        }
        if (response_ready_.wait_until(lock, deadline) == std::cv_status::timeout) {
            // 超时：把占位清掉，迟到的响应会被当作无人认领而丢弃，
            // 不会串到下一次调用上。
            responses_.erase(request.id);
            if (error != nullptr) {
                *error = "the local service did not respond in time";
            }
            return std::nullopt;
        }
    }
}

// ---------------------------------------------------------------------------
// 事件
// ---------------------------------------------------------------------------

bool IpcClient::PollEvent(Event* out) {
    std::lock_guard<std::mutex> lock(mutex_);
    if (events_.empty()) {
        return false;
    }
    if (out != nullptr) {
        *out = events_.front();
    }
    events_.pop_front();
    return true;
}

std::uint64_t IpcClient::DroppedLocally() const { return dropped_locally_.load(); }

std::uint64_t IpcClient::DroppedByServer() const { return dropped_by_server_.load(); }

std::uint64_t IpcClient::LastEventSeq() const { return last_event_seq_.load(); }

// ---------------------------------------------------------------------------
// 便捷封装
// ---------------------------------------------------------------------------

std::optional<Capabilities> IpcClient::GetCapabilities(std::chrono::milliseconds timeout,
                                                       std::string* error) {
    const auto response = Call(op::kGetCapabilities, std::nullopt, timeout, error);
    if (!response.has_value()) {
        return std::nullopt;
    }
    if (!response->ok) {
        if (error != nullptr) {
            *error = response->error.has_value() ? response->error->Describe()
                                                 : "the service rejected the request";
        }
        return std::nullopt;
    }
    return Capabilities::Parse(response->result);
}

std::optional<RuntimeState> IpcClient::GetRuntimeState(std::chrono::milliseconds timeout,
                                                       std::string* error) {
    const auto response = Call(op::kGetRuntimeState, std::nullopt, timeout, error);
    if (!response.has_value()) {
        return std::nullopt;
    }
    if (!response->ok) {
        if (error != nullptr) {
            *error = response->error.has_value() ? response->error->Describe()
                                                 : "the service rejected the request";
        }
        return std::nullopt;
    }
    return RuntimeState::Parse(response->result);
}

bool IpcClient::Start(std::chrono::milliseconds timeout, std::string* error) {
    const auto response = Call(op::kStart, std::nullopt, timeout, error);
    if (!response.has_value()) {
        return false;
    }
    if (!response->ok) {
        if (error != nullptr) {
            *error = response->error.has_value() ? response->error->Describe()
                                                 : "the service refused to start";
        }
        return false;
    }
    return true;
}

bool IpcClient::Stop(std::chrono::milliseconds timeout, std::string* error) {
    const auto response = Call(op::kStop, std::nullopt, timeout, error);
    if (!response.has_value()) {
        return false;
    }
    if (!response->ok) {
        if (error != nullptr) {
            *error = response->error.has_value() ? response->error->Describe()
                                                 : "the service refused to stop";
        }
        return false;
    }
    return true;
}

bool IpcClient::SetNetworkMode(const std::string& mode, std::chrono::milliseconds timeout,
                               std::string* error) {
    Json params = Json::object();
    params["mode"] = mode;
    const auto response = Call(op::kSetNetworkMode, params, timeout, error);
    if (!response.has_value()) {
        return false;
    }
    if (!response->ok) {
        if (error != nullptr) {
            *error = response->error.has_value() ? response->error->Describe()
                                                 : "the service rejected the network mode";
        }
        return false;
    }
    return true;
}

bool IpcClient::SwitchNode(const std::string& node_id, std::chrono::milliseconds timeout,
                           std::string* error) {
    Json params = Json::object();
    params["nodeId"] = node_id;
    const auto response = Call(op::kSwitchNode, params, timeout, error);
    if (!response.has_value()) {
        return false;
    }
    if (!response->ok) {
        if (error != nullptr) {
            *error = response->error.has_value() ? response->error->Describe()
                                                 : "the service rejected the node switch";
        }
        return false;
    }
    return true;
}

bool IpcClient::RecoverNetwork(std::chrono::milliseconds timeout, std::string* error) {
    const auto response = Call(op::kRecoverNetwork, std::nullopt, timeout, error);
    if (!response.has_value()) {
        return false;
    }
    if (!response->ok) {
        if (error != nullptr) {
            *error = response->error.has_value() ? response->error->Describe()
                                                 : "network recovery failed";
        }
        return false;
    }
    return true;
}

// ---------------------------------------------------------------------------
// 会话与订阅
// ---------------------------------------------------------------------------

std::string IpcClient::DescribeFailure(const std::string& fallback,
                                       const std::optional<Response>& response) {
    // 先把上一次的错误清掉：调用方拿到 error 之后会去读 LastRpcError，
    // 若不清，一次失败会让后续成功操作的提示仍然显示上一条原因。
    {
        std::lock_guard<std::mutex> lock(mutex_);
        ClearRpcErrorLocked();
    }
    if (!response.has_value()) {
        return fallback;
    }
    if (!response->error.has_value()) {
        // 服务端说失败但没给错误结构：这不是正常契约，
        // 用调用方给的兜底文案，而不是编一条原因。
        return fallback;
    }
    const RpcError& rpc = *response->error;
    {
        std::lock_guard<std::mutex> lock(mutex_);
        last_rpc_error_ = rpc;
        has_rpc_error_ = true;
    }
    if (!rpc.message_key.empty()) {
        // 优先返回 i18n 键：界面据此显示本地化文案。
        // 退回 Describe() 之前不返回 message——那是服务端原文，
        // 可能是英文，也可能包含用户不该看到的路径信息。
        return rpc.message_key;
    }
    return rpc.Describe();
}

void IpcClient::ClearRpcErrorLocked() {
    last_rpc_error_ = RpcError{};
    has_rpc_error_ = false;
}

void IpcClient::ClearRpcError() {
    std::lock_guard<std::mutex> lock(mutex_);
    ClearRpcErrorLocked();
}

bool IpcClient::HasRpcError() const {
    std::lock_guard<std::mutex> lock(mutex_);
    return has_rpc_error_;
}

RpcError IpcClient::LastRpcError() const {
    std::lock_guard<std::mutex> lock(mutex_);
    return last_rpc_error_;
}

bool IpcClient::Login(const LoginParams& params, std::chrono::milliseconds timeout,
                      std::string* error) {
    ClearRpcError();
    const auto response = Call(op::kLogin, params.ToJson(), timeout, nullptr);
    if (!response.has_value()) {
        if (error != nullptr) {
            *error = "the local service did not answer the login request";
        }
        return false;
    }
    if (!response->ok) {
        if (error != nullptr) {
            *error = DescribeFailure("the service rejected the login", response);
        }
        return false;
    }
    if (error != nullptr) {
        error->clear();
    }
    return true;
}

bool IpcClient::Logout(std::chrono::milliseconds timeout, std::string* error) {
    ClearRpcError();
    const auto response = Call(op::kLogout, std::nullopt, timeout, nullptr);
    if (!response.has_value()) {
        if (error != nullptr) {
            *error = "the local service did not answer the logout request";
        }
        return false;
    }
    if (!response->ok) {
        if (error != nullptr) {
            *error = DescribeFailure("the service rejected the logout", response);
        }
        return false;
    }
    if (error != nullptr) {
        error->clear();
    }
    return true;
}

std::optional<SessionState> IpcClient::GetSessionState(
    std::chrono::milliseconds timeout, std::string* error) {
    ClearRpcError();
    const auto response = Call(op::kGetSessionState, std::nullopt, timeout, nullptr);
    if (!response.has_value()) {
        if (error != nullptr) {
            *error = "the local service did not report the session state";
        }
        return std::nullopt;
    }
    if (!response->ok) {
        if (error != nullptr) {
            *error = DescribeFailure("the service rejected the session query", response);
        }
        return std::nullopt;
    }
    return SessionState::Parse(response->result);
}

std::optional<SubscriptionView> IpcClient::RefreshSubscription(
    std::chrono::milliseconds timeout, std::string* error) {
    ClearRpcError();
    const auto response = Call(op::kRefreshSubscription, std::nullopt, timeout, nullptr);
    if (!response.has_value()) {
        if (error != nullptr) {
            *error = "the local service did not answer the subscription refresh";
        }
        return std::nullopt;
    }
    if (!response->ok) {
        if (error != nullptr) {
            *error = DescribeFailure("the service refused to refresh the subscription",
                                     response);
        }
        return std::nullopt;
    }
    // 返回值可能是 subscription 对象，也可能是包着它的对象。
    // 两种形态都接受：RefreshSubscription 与 GetSessionState 里的
    // subscription 是同一个结构，服务端把它放在哪一层不该决定界面能不能工作。
    if (const auto it = response->result.find("subscription");
        it != response->result.end() && it->is_object()) {
        return SubscriptionView::Parse(*it);
    }
    return SubscriptionView::Parse(response->result);
}

std::optional<NodeList> IpcClient::ListNodes(std::chrono::milliseconds timeout,
                                             std::string* error) {
    ClearRpcError();
    const auto response = Call(op::kListNodes, std::nullopt, timeout, nullptr);
    if (!response.has_value()) {
        if (error != nullptr) {
            *error = "the local service did not report the node list";
        }
        return std::nullopt;
    }
    if (!response->ok) {
        if (error != nullptr) {
            *error = DescribeFailure("the service rejected the node query", response);
        }
        return std::nullopt;
    }
    return NodeList::Parse(response->result);
}

std::optional<std::string> IpcClient::SelectNode(const SelectNodeParams& params,
                                                 std::chrono::milliseconds timeout,
                                                 std::string* error) {
    ClearRpcError();
    if (!params.IsUsable()) {
        // 本地就挡住：参数里既没有 id 也没有 name 时，服务端会回
        // invalid_params，而那条错误不会告诉用户「界面根本没把选择传出去」。
        if (error != nullptr) {
            *error = "no node was given to select";
        }
        return std::nullopt;
    }
    const auto response = Call(op::kSelectNode, params.ToJson(), timeout, nullptr);
    if (!response.has_value()) {
        if (error != nullptr) {
            *error = "the local service did not answer the node selection";
        }
        return std::nullopt;
    }
    if (!response->ok) {
        if (error != nullptr) {
            *error = DescribeFailure("the service rejected the node selection", response);
        }
        return std::nullopt;
    }
    const SelectNodeResult result = SelectNodeResult::Parse(response->result);
    if (result.selected_node_id.empty()) {
        // 回传空 ID 说明服务端的响应不符合契约。直接当成失败：
        // 否则界面会把「不知道选中了谁」显示成「切换成功」。
        if (error != nullptr) {
            *error = "the service did not report which node it selected";
        }
        return std::nullopt;
    }
    if (error != nullptr) {
        error->clear();
    }
    return result.selected_node_id;
}

std::optional<domain::Options> IpcClient::GetOptions(std::chrono::milliseconds timeout,
                                                     std::string* error) {
    ClearRpcError();
    const auto response = Call(op::kGetOptions, std::nullopt, timeout, nullptr);
    if (!response.has_value()) {
        if (error != nullptr) {
            *error = "the local service did not report the options";
        }
        return std::nullopt;
    }
    if (!response->ok) {
        if (error != nullptr) {
            *error = DescribeFailure("the service rejected the options query", response);
        }
        return std::nullopt;
    }
    // 契约是 {"options": {...}}。这里刻意**只**认这一层：
    // 与 RefreshSubscription 不同，选项模型必须整体一致，
    // 兼容两种形态会让「服务端换了外层结构」被静默吞掉。
    const auto it = response->result.find("options");
    if (it == response->result.end()) {
        if (error != nullptr) {
            *error = "the service returned no options object";
        }
        return std::nullopt;
    }
    const auto options = domain::ParseOptions(*it);
    if (!options.has_value()) {
        if (error != nullptr) {
            *error = "the options object the service returned is not an object";
        }
        return std::nullopt;
    }
    if (error != nullptr) {
        error->clear();
    }
    return options;
}

bool IpcClient::SetOptions(const domain::Options& options,
                           std::chrono::milliseconds timeout, std::string* error) {
    ClearRpcError();
    Json params = Json::object();
    params["options"] = domain::SerializeOptions(options);
    const auto response = Call(op::kSetOptions, params, timeout, nullptr);
    if (!response.has_value()) {
        if (error != nullptr) {
            *error = "the local service did not answer the options update";
        }
        return false;
    }
    if (!response->ok) {
        if (error != nullptr) {
            *error = DescribeFailure("the service rejected the options update", response);
        }
        return false;
    }
    if (error != nullptr) {
        error->clear();
    }
    return true;
}

// ---------------------------------------------------------------------------
// 状态文件
// ---------------------------------------------------------------------------

std::string IpcClient::DefaultStatePath() {
    // 与 Go 端保持一致：状态文件放在用户级配置目录下。
    //
    // 用环境变量而不是平台 API，是为了让三端的路径规则显式且一致；
    // 各平台的具体取值由打包脚本与安装程序保证。
#if defined(_WIN32)
    if (const char* appdata = std::getenv("APPDATA"); appdata != nullptr && *appdata != '\0') {
        return (std::filesystem::path(appdata) / "proxyclient" / StateFileName()).string();
    }
#else
    if (const char* xdg = std::getenv("XDG_RUNTIME_DIR"); xdg != nullptr && *xdg != '\0') {
        // 优先运行期目录：它随会话创建、权限收紧，且重启后自动清理，
        // 适合放含访问令牌的文件。
        return (std::filesystem::path(xdg) / "proxyclient" / StateFileName()).string();
    }
    if (const char* xdg_config = std::getenv("XDG_CONFIG_HOME");
        xdg_config != nullptr && *xdg_config != '\0') {
        return (std::filesystem::path(xdg_config) / "proxyclient" / StateFileName()).string();
    }
    if (const char* home = std::getenv("HOME"); home != nullptr && *home != '\0') {
        return (std::filesystem::path(home) / ".config" / "proxyclient" / StateFileName()).string();
    }
#endif
    return {};
}

std::optional<Handle> IpcClient::LoadHandle(std::string* error) const {
    const std::string path = DefaultStatePath();
    if (path.empty()) {
        if (error != nullptr) {
            *error = "cannot determine the service state file location";
        }
        return std::nullopt;
    }

    std::ifstream in(path, std::ios::binary);
    if (!in) {
        // 文件不存在是最常见的情况：服务还没启动。
        if (error != nullptr) {
            *error = "the local service is not running (no state file at " + path + ")";
        }
        return std::nullopt;
    }
    std::ostringstream buffer;
    buffer << in.rdbuf();

    const auto handle = ParseHandleJson(buffer.str());
    if (!handle.has_value()) {
        if (error != nullptr) {
            *error = "the service state file is unreadable or malformed";
        }
        return std::nullopt;
    }
    std::string validation_error;
    if (!handle->Validate(&validation_error)) {
        if (error != nullptr) {
            *error = validation_error;
        }
        return std::nullopt;
    }
    return handle;
}

std::string IpcClient::LastError() const {
    std::lock_guard<std::mutex> lock(mutex_);
    return last_error_;
}

}  // namespace pcx::ipc
