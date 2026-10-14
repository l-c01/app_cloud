// 本地 IPC 传输层 / local IPC transport.
//
// 设计约束（设计基线 §6.2）：只走本地通道，不开放公网管理端口。
//
// 支持的通道与 Go 端 handle.network 的取值对应：
//   "unix"   POSIX 域套接字（Linux / macOS）
//   "npipe"  Windows 命名管道（\\.\pipe\...）
//   "tcp"    仅限环回地址；用于不支持前两者的环境
//
// "tcp" 只接受环回地址：这是一个刻意收紧的限制。
// 本地管理接口一旦监听到非环回地址，同网络的任何人都能控制这个代理进程，
// 而这类配置错误（把 127.0.0.1 写成 0.0.0.0）在实践中非常常见。
#pragma once

#include <chrono>
#include <cstddef>
#include <cstdint>
#include <memory>
#include <optional>
#include <string>
#include <vector>

namespace pcx::ipc {

// Transport 是一条已建立的字节流通道。
//
// 语义刻意做成「字节流 + 显式长度」而不是「消息」：
// 报文以换行分隔，一次 Read 可能返回半条或多条报文，
// 由上层负责组帧。这样传输层不需要知道协议，各平台实现也更简单。
class Transport {
public:
    virtual ~Transport() = default;

    // Read 读取至多 length 字节。
    //
    // 返回读取到的字节数。返回 0 且 ok 为 true 表示对端正常关闭；
    // ok 为 false 表示出错（error 填入原因）。
    virtual std::size_t Read(void* buffer, std::size_t length, bool* ok,
                             std::string* error) = 0;

    // Write 写入全部字节。
    //
    // 必须写满或失败：部分写入会让对端收到半条报文并卡在等待换行上。
    virtual bool Write(const void* data, std::size_t length, std::string* error) = 0;

    // Close 关闭通道，可重复调用。
    virtual void Close() = 0;

    // SetReadTimeout 设置读取超时。
    //
    // 返回 false 表示该平台/实现不支持超时，调用方需改用「关闭通道」
    // 来打断阻塞读取——这是必须知道的，否则退出时线程会挂住。
    virtual bool SetReadTimeout(std::chrono::milliseconds /*timeout*/) { return false; }
};

// Connect 按 network/address 建立连接。
//
// 失败时返回 nullptr 并填入 error。
std::unique_ptr<Transport> Connect(const std::string& network,
                                   const std::string& address,
                                   std::string* error);

// IsLoopbackAddress 判断 "host:port" 是否指向环回地址。
//
// 导出它是为了能被单独测试：这是本模块唯一的安全相关判定。
bool IsLoopbackAddress(const std::string& address);

// DescribeAddress 返回**适合写进日志**的地址描述。
//
// 命名管道名与套接字路径可能含用户名等本地信息，
// 这里只保留可辨识的部分，其余省略。
std::string DescribeAddress(const std::string& network, const std::string& address);

}  // namespace pcx::ipc
