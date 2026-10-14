#include "app/app_context.hpp"

#include <chrono>

namespace pcx::app {

namespace {

// kServiceCallTimeout 是界面等待服务响应的上限。
//
// 取 5 秒而不是更长：界面每次刷新都会调用，超时过长会让定时器堆积；
// 而本地 IPC 的正常响应时间在毫秒级，5 秒已经是很宽松的界。
constexpr std::chrono::milliseconds kServiceCallTimeout{5000};

}  // namespace

bool AppContext::RefreshFromService(std::string* error) {
    if (!ipc.IsConnected()) {
        if (error != nullptr) {
            *error = ipc.LastError().empty() ? "not connected to the local service"
                                             : ipc.LastError();
        }
        return false;
    }

    std::string inner;
    const auto state = ipc.GetRuntimeState(kServiceCallTimeout, &inner);
    if (!state.has_value()) {
        // 运行状态读不到就当作服务不可用：这比显示一份过期状态更安全，
        // 用户看到「已连接」而去访问网络，实际代理可能早就断了。
        if (error != nullptr) {
            *error = inner;
        }
        return false;
    }
    runtime_state = *state;

    // 能力只需成功读取一次：它描述的是服务端支持什么，不会随运行状态变化。
    if (!has_capabilities) {
        const auto caps = ipc.GetCapabilities(kServiceCallTimeout, &inner);
        if (caps.has_value()) {
            capabilities = *caps;
            has_capabilities = true;
        }
        // 能力读不到不算失败：运行状态才是界面最需要的。
    }

    if (error != nullptr) {
        error->clear();
    }
    return true;
}

bool AppContext::ReconnectService(std::string* error) {
    std::string inner;
    const auto handle = ipc.LoadHandle(&inner);
    if (!handle.has_value()) {
        if (error != nullptr) {
            // 服务没起来的提示必须能指导用户行动，而不只是陈述事实。
            *error = inner + " (start the background service, then try again)";
        }
        return false;
    }

    if (!ipc.Connect(*handle, &inner)) {
        if (error != nullptr) {
            *error = inner;
        }
        return false;
    }
    has_capabilities = false;
    if (error != nullptr) {
        error->clear();
    }
    return true;
}

}  // namespace pcx::app
