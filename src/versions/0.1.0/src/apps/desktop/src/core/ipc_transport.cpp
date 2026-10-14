#include "core/ipc_transport.hpp"

#include <algorithm>
#include <cstring>

#if defined(_WIN32)
#include <windows.h>
#else
#include <arpa/inet.h>
#include <cerrno>
#include <netdb.h>
#include <netinet/in.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <unistd.h>
#include <chrono>
#include <cstddef>
#include <memory>
#endif

namespace pcx::ipc {

namespace {

#if defined(_WIN32)

std::string LastErrorMessage(const char* prefix) {
    return std::string(prefix) + " (win32 error " +
           std::to_string(static_cast<unsigned long>(GetLastError())) + ")";
}

// WindowsNamedPipe 是命名管道传输。
class WindowsNamedPipe final : public Transport {
public:
    explicit WindowsNamedPipe(HANDLE handle) : handle_(handle) {}
    ~WindowsNamedPipe() override { Close(); }

    std::size_t Read(void* buffer, std::size_t length, bool* ok,
                     std::string* error) override {
        if (handle_ == INVALID_HANDLE_VALUE) {
            if (ok != nullptr) { *ok = false; }
            if (error != nullptr) { *error = "pipe is closed"; }
            return 0;
        }
        DWORD read = 0;
        const DWORD want = static_cast<DWORD>(std::min<std::size_t>(length, 0xFFFFFFFFu));
        if (::ReadFile(handle_, buffer, want, &read, nullptr) == 0) {
            const DWORD last = GetLastError();
            if (ok != nullptr) { *ok = false; }
            if (error != nullptr) {
                // 对端关闭管道时表现为 ERROR_BROKEN_PIPE。
                *error = (last == ERROR_BROKEN_PIPE)
                             ? std::string("the service closed the connection")
                             : LastErrorMessage("read from pipe failed");
            }
            return 0;
        }
        if (ok != nullptr) { *ok = true; }
        return static_cast<std::size_t>(read);   // 0 means the peer closed cleanly
    }

    bool Write(const void* data, std::size_t length, std::string* error) override {
        if (handle_ == INVALID_HANDLE_VALUE) {
            if (error != nullptr) { *error = "pipe is closed"; }
            return false;
        }
        const char* cursor = static_cast<const char*>(data);
        std::size_t remaining = length;
        while (remaining > 0) {
            DWORD written = 0;
            const DWORD chunk = static_cast<DWORD>(std::min<std::size_t>(remaining, 0xFFFFFFFFu));
            if (::WriteFile(handle_, cursor, chunk, &written, nullptr) == 0) {
                if (error != nullptr) { *error = LastErrorMessage("write to pipe failed"); }
                return false;
            }
            if (written == 0) {
                if (error != nullptr) { *error = "write to pipe made no progress"; }
                return false;
            }
            cursor += written;
            remaining -= written;
        }
        return true;
    }

    void Close() override {
        if (handle_ != INVALID_HANDLE_VALUE) {
            // 关闭句柄会打断另一个线程里阻塞的 ReadFile，
            // 这是 Windows 上打断阻塞管道的唯一可靠方式。
            ::CloseHandle(handle_);
            handle_ = INVALID_HANDLE_VALUE;
        }
    }

    // SetReadTimeout 返回 false：命名管道不支持 SO_RCVTIMEO 那样的读超时。
    // 调用方必须依赖 Close() 来打断阻塞读取，否则退出时线程会挂住。
    bool SetReadTimeout(std::chrono::milliseconds) override { return false; }

private:
    HANDLE handle_ = INVALID_HANDLE_VALUE;
};

#else  // POSIX

std::string LastErrorMessage(const char* prefix) {
    return std::string(prefix) + ": " + std::strerror(errno);
}

// PosixSocket 是套接字传输（unix 或 tcp）。
class PosixSocket final : public Transport {
public:
    explicit PosixSocket(int fd) : fd_(fd) {}
    ~PosixSocket() override { Close(); }

    std::size_t Read(void* buffer, std::size_t length, bool* ok,
                     std::string* error) override {
        if (fd_ < 0) {
            if (ok != nullptr) { *ok = false; }
            if (error != nullptr) { *error = "socket is closed"; }
            return 0;
        }
        for (;;) {
            const ssize_t got = ::read(fd_, buffer, length);
            if (got > 0) {
                if (ok != nullptr) { *ok = true; }
                return static_cast<std::size_t>(got);
            }
            if (got == 0) {
                // 对端正常关闭。
                if (ok != nullptr) { *ok = true; }
                return 0;
            }
            if (errno == EINTR) {
                continue;   // 被信号打断不是错误。
            }
            if (ok != nullptr) { *ok = false; }
            if (error != nullptr) {
                if (errno == EAGAIN || errno == EWOULDBLOCK) {
                    *error = "read timed out";
                } else {
                    *error = LastErrorMessage("read from socket failed");
                }
            }
            return 0;
        }
    }

    bool Write(const void* data, std::size_t length, std::string* error) override {
        if (fd_ < 0) {
            if (error != nullptr) { *error = "socket is closed"; }
            return false;
        }
        const char* cursor = static_cast<const char*>(data);
        std::size_t remaining = length;
        while (remaining > 0) {
            const ssize_t written = ::write(fd_, cursor, remaining);
            if (written > 0) {
                cursor += written;
                remaining -= static_cast<std::size_t>(written);
                continue;
            }
            if (written < 0 && errno == EINTR) {
                continue;
            }
            if (error != nullptr) { *error = LastErrorMessage("write to socket failed"); }
            return false;
        }
        return true;
    }

    void Close() override {
        if (fd_ >= 0) {
            // shutdown 先于 close：它会唤醒正在阻塞的 read，
            // 否则关闭一个正被读的 fd 在部分平台上行为未定义。
            (void)::shutdown(fd_, SHUT_RDWR);
            ::close(fd_);
            fd_ = -1;
        }
    }

    bool SetReadTimeout(std::chrono::milliseconds timeout) override {
        if (fd_ < 0) {
            return false;
        }
        timeval tv{};
        tv.tv_sec = static_cast<time_t>(timeout.count() / 1000);
        tv.tv_usec = static_cast<suseconds_t>((timeout.count() % 1000) * 1000);
        // socklen_t 的底层宽度随平台不同（可能是 int 或 unsigned int），
        // 直接传 sizeof 会在 -Wconversion 下告警，CI 里开着 -Werror。
        return ::setsockopt(fd_, SOL_SOCKET, SO_RCVTIMEO, &tv,
                            static_cast<socklen_t>(sizeof(tv))) == 0;
    }

private:
    int fd_ = -1;
};

#endif

}  // namespace

// ---------------------------------------------------------------------------
// 连接
// ---------------------------------------------------------------------------

#if defined(_WIN32)

std::unique_ptr<Transport> Connect(const std::string& network,
                                   const std::string& address,
                                   std::string* error) {
    if (network == "tcp") {
        // Windows 上的 tcp 传输未实现。
        //
        // 刻意在这里直接拒绝，而不是「连上去再报不支持」：
        // 后者会先建立一个到管理接口的连接，白白暴露一次访问，
        // 也让人误以为传输是可用的。服务端在 Windows 上使用命名管道。
        if (error != nullptr) {
            *error = "tcp transport is not implemented on Windows; the service uses a named pipe";
        }
        return nullptr;
    }

    if (network != "npipe") {
        if (error != nullptr) {
            *error = "unsupported transport network on Windows: " + network;
        }
        return nullptr;
    }

    if (address.empty()) {
        if (error != nullptr) { *error = "pipe name is empty"; }
        return nullptr;
    }

    // Go 端写入的地址形如 \\.\pipe\name 或裸名字；两种都接受。
    std::wstring wide;
    if (address.rfind("\\\\.\\pipe\\", 0) == 0) {
        wide.assign(address.begin(), address.end());
    } else {
        const std::wstring prefix(L"\\\\.\\pipe\\");
        wide = prefix + std::wstring(address.begin(), address.end());
    }

    HANDLE handle = ::CreateFileW(wide.c_str(), GENERIC_READ | GENERIC_WRITE,
                                  0, nullptr, OPEN_EXISTING, 0, nullptr);
    if (handle == INVALID_HANDLE_VALUE) {
        if (error != nullptr) {
            *error = LastErrorMessage("cannot open the service pipe (is the service running?)");
        }
        return nullptr;
    }
    return std::make_unique<WindowsNamedPipe>(handle);
}

#else  // POSIX

std::unique_ptr<Transport> Connect(const std::string& network,
                                   const std::string& address,
                                   std::string* error) {
    if (network == "unix") {
        if (address.empty()) {
            if (error != nullptr) { *error = "socket path is empty"; }
            return nullptr;
        }
        // sun_path 有固定长度；超长路径必须在连接前报错，
        // 否则会得到一个被静默截断的路径，连到别的地方或直接失败。
        sockaddr_un addr{};
        if (address.size() + 1 > sizeof(addr.sun_path)) {
            if (error != nullptr) {
                *error = "socket path is too long for this platform";
            }
            return nullptr;
        }
        addr.sun_family = AF_UNIX;
        std::memcpy(addr.sun_path, address.c_str(), address.size());

        const int fd = ::socket(AF_UNIX, SOCK_STREAM, 0);
        if (fd < 0) {
            if (error != nullptr) { *error = LastErrorMessage("socket failed"); }
            return nullptr;
        }
        if (::connect(fd, reinterpret_cast<sockaddr*>(&addr),
                      static_cast<socklen_t>(sizeof(addr))) != 0) {
            const std::string detail = LastErrorMessage("connect failed");
            ::close(fd);
            if (error != nullptr) { *error = detail; }
            return nullptr;
        }
        return std::make_unique<PosixSocket>(fd);
    }

    if (network == "tcp") {
        if (!IsLoopbackAddress(address)) {
            // 本地管理接口监听到非环回地址意味着同网络的任何人都能控制它。
            if (error != nullptr) {
                *error = "refusing to connect to a non-loopback management address";
            }
            return nullptr;
        }
        const auto colon = address.rfind(':');
        if (colon == std::string::npos) {
            if (error != nullptr) { *error = "address must be host:port"; }
            return nullptr;
        }
        const std::string host = address.substr(0, colon);
        const std::string port = address.substr(colon + 1);

        addrinfo hints{};
        hints.ai_family = AF_UNSPEC;
        hints.ai_socktype = SOCK_STREAM;
        addrinfo* result = nullptr;
        if (::getaddrinfo(host.c_str(), port.c_str(), &hints, &result) != 0) {
            if (error != nullptr) { *error = "cannot resolve the management address"; }
            return nullptr;
        }
        int fd = -1;
        for (addrinfo* it = result; it != nullptr; it = it->ai_next) {
            fd = ::socket(it->ai_family, it->ai_socktype, it->ai_protocol);
            if (fd < 0) {
                continue;
            }
            if (::connect(fd, it->ai_addr, it->ai_addrlen) == 0) {
                break;
            }
            ::close(fd);
            fd = -1;
        }
        ::freeaddrinfo(result);
        if (fd < 0) {
            if (error != nullptr) { *error = "cannot connect to the management address"; }
            return nullptr;
        }
        return std::make_unique<PosixSocket>(fd);
    }

    if (error != nullptr) {
        *error = "unsupported transport network: " + network;
    }
    return nullptr;
}

#endif

// ---------------------------------------------------------------------------
// 地址判定与描述
// ---------------------------------------------------------------------------

bool IsLoopbackAddress(const std::string& address) {
    std::string host;

    if (!address.empty() && address.front() == '[') {
        // 方括号形式：[IPv6] 后面可以再跟 ":port"。
        const auto close = address.find(']');
        if (close == std::string::npos) {
            return false;  // 方括号不闭合，按不安全处理。
        }
        host = address.substr(1, close - 1);
    } else {
        // 只有**一个**冒号时才按 host:port 切分。
        //
        // 按最后一个冒号切分对 IPv6 是错的："::1" 会被切成 ":"，
        // "::ffff:127.0.0.1" 会被切成 "::ffff"，两者都被漏判为「非环回」。
        // 而这个判定的作用正是拒绝把管理接口暴露给同网络的其他人，
        // 漏判的代价是任何人都能控制本进程。
        const auto first = address.find(':');
        const auto last = address.rfind(':');
        if (first != std::string::npos && first == last) {
            host = address.substr(0, first);
        } else {
            // 没有冒号（纯主机名 / IPv4），或含多个冒号（裸 IPv6）：
            // 整串就是主机部分，没有端口。
            host = address;
        }
    }

    if (host.empty()) {
        // 空主机名在不同平台上可能被解释为「任意地址」，一律按不安全处理。
        return false;
    }
    if (host == "localhost" || host == "127.0.0.1" || host == "::1") {
        return true;
    }
    if (host == "0:0:0:0:0:0:0:1") {
        return true;  // ::1 的完整写法
    }
    // 127.0.0.0/8 全部属于环回。
    if (host.rfind("127.", 0) == 0) {
        return true;
    }
    // IPv4-mapped IPv6：::ffff:127.x.y.z。
    if (host.rfind("::ffff:127.", 0) == 0) {
        return true;
    }
    // 注意 "::"（未指定地址）**不**在此列：它表示监听所有接口。
    return false;
}

std::string DescribeAddress(const std::string& network, const std::string& address) {
    if (network == "unix" || network == "npipe") {
        // 套接字路径与管道名可能含用户名等本地信息，
        // 日志里只保留类型，不写具体路径。
        return network + " (local path omitted)";
    }
    if (network == "tcp") {
        return "tcp " + address;
    }
    return network;
}

}  // namespace pcx::ipc
