#include "core/ipc_client.hpp"

#include <cstdlib>
#include <filesystem>
#include <fstream>
#include <sstream>

#include "core/json_util.hpp"

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
    auto transport = Connect(handle.network, handle.address, &connect_error);
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
