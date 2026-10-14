// IPC 客户端 / IPC client.
//
// 线程模型（这是本模块最重要的设计决定）：
//
//   一条后台读取线程负责读取与解析；**它绝不调用界面代码**。
//   响应按 ID 配对后唤醒等待的调用方；事件进入内部队列，
//   由界面线程按自己的节奏 PollEvent 取走。
//
// 为什么不直接回调界面：跨线程调用 wxWidgets 会导致随机崩溃与界面卡死，
// 而且这类 bug 只在负载高时出现，极难复现。排队 + 轮询让线程边界只有一处，
// 代价是界面需要定时轮询（本来也要为状态刷新做定时器）。
//
// 队列有上限并统计丢弃数：慢消费者不该把内存吃光，也不该静默丢事件。
// 服务端的丢弃计数与本地丢弃数分开统计，两者相加才是真正丢失量。
#pragma once

#include <atomic>
#include <chrono>
#include <condition_variable>
#include <cstdint>
#include <deque>
#include <functional>
#include <map>
#include <memory>
#include <mutex>
#include <optional>
#include <string>
#include <thread>
#include <vector>

#include "core/ipc_protocol.hpp"
#include "core/ipc_transport.hpp"

namespace pcx::ipc {

// kDefaultEventQueueSize 是本地事件队列上限。
//
// 取 256 而不是更大：界面每次轮询都会把队列取空，
// 堆积到几百条说明界面已经卡住，此时继续缓冲只会推迟问题。
inline constexpr std::size_t kDefaultEventQueueSize = 256;

class IpcClient {
public:
    IpcClient();
    ~IpcClient();

    // 不可拷贝：持有线程与互斥量。
    IpcClient(const IpcClient&) = delete;
    IpcClient& operator=(const IpcClient&) = delete;

    // -----------------------------------------------------------------------
    // 连接
    // -----------------------------------------------------------------------

    // Connect 按句柄建立连接并启动读取线程。
    //
    // 会先校验句柄版本：不兼容时**不连接**，直接给出明确原因，
    // 而不是连上去再收到一个语焉不详的握手失败。
    bool Connect(const Handle& handle, std::string* error);
    bool Connect(const Handle& handle) { return Connect(handle, nullptr); }

    // Disconnect 关闭连接并等待读取线程退出。可重复调用。
    void Disconnect();

    // IsConnected 报告当前是否有可用连接。
    bool IsConnected() const;

    // -----------------------------------------------------------------------
    // 调用
    // -----------------------------------------------------------------------

    // Call 发起一次调用并等待配对响应。
    //
    // 超时返回 std::nullopt 并填入 error。超时后该请求的迟到响应会被丢弃，
    // 不会串到下一次调用上。
    std::optional<Response> Call(const std::string& operation,
                                 const std::optional<Json>& params,
                                 std::chrono::milliseconds timeout,
                                 std::string* error);

    // -----------------------------------------------------------------------
    // 事件
    // -----------------------------------------------------------------------

    // PollEvent 取出一个待处理事件；队列为空时返回 false。
    //
    // **必须由界面线程调用**。
    bool PollEvent(Event* out);

    // DroppedLocally 返回因本地队列满而被丢弃的事件数。
    std::uint64_t DroppedLocally() const;

    // DroppedByServer 返回服务端报告的累计丢弃数。
    //
    // 与服务端的 seq 跳号一起用于判断「界面看到的是不是全部事件」。
    std::uint64_t DroppedByServer() const;

    // LastEventSeq 返回最近收到的事件的序号。
    //
    // 界面可以用它检测跳号：若相邻两次收到的 seq 差值大于 1，
    // 说明中间有事件丢失（服务端背压丢弃或本地队列溢出）。
    // 这比只看丢弃计数更直接。
    std::uint64_t LastEventSeq() const;

    // -----------------------------------------------------------------------
    // 便捷封装（内部都是 Call）
    // -----------------------------------------------------------------------

    std::optional<Capabilities> GetCapabilities(std::chrono::milliseconds timeout,
                                                std::string* error);
    std::optional<RuntimeState> GetRuntimeState(std::chrono::milliseconds timeout,
                                                std::string* error);
    bool Start(std::chrono::milliseconds timeout, std::string* error);
    bool Stop(std::chrono::milliseconds timeout, std::string* error);
    bool SetNetworkMode(const std::string& mode, std::chrono::milliseconds timeout,
                        std::string* error);
    bool SwitchNode(const std::string& node_id, std::chrono::milliseconds timeout,
                    std::string* error);
    bool RecoverNetwork(std::chrono::milliseconds timeout, std::string* error);

    // -----------------------------------------------------------------------
    // 状态文件
    // -----------------------------------------------------------------------

    // DefaultStatePath 返回服务的连接句柄文件路径。
    //
    // 与 Go 端 writeHandleFile 的位置保持一致；不一致会导致界面永远
    // 连不上服务，且看起来像是服务没启动。
    static std::string DefaultStatePath();

    // LoadHandle 读取并校验句柄文件。
    std::optional<Handle> LoadHandle(std::string* error) const;

    // LastError 返回最近一次连接层失败的原因（供界面展示）。
    std::string LastError() const;

private:
    // ReaderLoop 在后台线程读取并分发报文。
    void ReaderLoop();
    // FailPending 用同一个原因唤醒所有等待中的调用方。
    void FailPending(const std::string& reason);
    void SetConnected(bool connected, const std::string& reason);
    // HandleLine 处理一条按行分割的报文。
    void HandleLine(const std::string& line);
    void EnqueueEvent(const Event& event);

    std::unique_ptr<Transport> transport_;

    mutable std::mutex mutex_;                 // 保护下面全部成员
    std::condition_variable response_ready_;   // 响应到达或连接断开

    std::map<std::string, Response> responses_;
    std::deque<Event> events_;
    std::size_t event_queue_limit_ = kDefaultEventQueueSize;

    std::atomic<std::uint64_t> dropped_locally_{0};
    std::atomic<std::uint64_t> dropped_by_server_{0};
    std::atomic<std::uint64_t> last_event_seq_{0};
    std::atomic<bool> connected_{false};
    std::atomic<bool> stopping_{false};

    std::string token_;
    std::string last_error_;
    std::thread reader_;
};

}  // namespace pcx::ipc
