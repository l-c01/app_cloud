#include "app/app_context.hpp"

#include <chrono>

namespace pcx::app {

namespace {

// kServiceCallTimeout 是界面等待服务响应的上限。
//
// 取 5 秒而不是更长：界面每次刷新都会调用，超时过长会让定时器堆积；
// 而本地 IPC 的正常响应时间在毫秒级，5 秒已经是很宽松的界。
constexpr std::chrono::milliseconds kServiceCallTimeout{5000};

// kRemoteCallTimeout 是**会走出本机**的操作的等待上限。
//
// 订阅刷新要经服务进程访问面板：慢面板、需要代理才能到达的面板、
// 以及上百个节点逐一解析，都会让耗时远超本地调用。
// 用 5 秒会让正常的刷新被误判为失败，而用户重试只会让情况更糟。
constexpr std::chrono::milliseconds kRemoteCallTimeout{30000};

}  // namespace

std::chrono::milliseconds AppContext::RemoteCallTimeout() { return kRemoteCallTimeout; }

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

bool AppContext::Supports(const std::string& operation) const {
    if (!has_capabilities) {
        // 能力表还没读到：倾向「可能有」。
        //
        // 反过来（默认不可用）会让界面在服务刚起来的那几百毫秒里
        // 把登录按钮藏起来，用户看到的是一个点不动的界面，
        // 而真正的原因（能力表尚未返回）没有任何地方显示出来。
        return true;
    }
    return capabilities.Supports(operation);
}

bool AppContext::RefreshSession(std::string* error) {
    if (!ipc.IsConnected()) {
        if (error != nullptr) {
            *error = ipc.LastError().empty() ? "not connected to the local service"
                                             : ipc.LastError();
        }
        return false;
    }
    // 服务端不认识这个操作时，不代表会话状态是「未登录」。
    // 这里如实报「不可用」，而不是把 has_session 置成 false 后
    // 让界面弹出一个永远登不上去的登录页。
    if (!Supports(ipc::op::kGetSessionState)) {
        if (error != nullptr) {
            error->clear();
        }
        return false;
    }

    std::string inner;
    const auto state = ipc.GetSessionState(kServiceCallTimeout, &inner);
    if (!state.has_value()) {
        if (error != nullptr) {
            *error = inner;
        }
        return false;
    }
    session = *state;
    has_session = true;
    if (error != nullptr) {
        error->clear();
    }
    return true;
}

bool AppContext::RefreshNodes(std::string* error) {
    if (!ipc.IsConnected()) {
        if (error != nullptr) {
            *error = ipc.LastError().empty() ? "not connected to the local service"
                                             : ipc.LastError();
        }
        return false;
    }
    // 未登录时订阅里没有节点可列。这一步不走 IPC 是有意的：
    // 服务端对未登录的 ListNodes 会回一个错误，而那不是用户需要看到的
    // 失败——他需要看到的是登录页。
    if (has_session && !session.logged_in) {
        nodes = ipc::NodeList{};
        if (error != nullptr) {
            error->clear();
        }
        return true;
    }
    if (!Supports(ipc::op::kListNodes)) {
        if (error != nullptr) {
            error->clear();
        }
        return false;
    }

    std::string inner;
    const auto list = ipc.ListNodes(kServiceCallTimeout, &inner);
    if (!list.has_value()) {
        if (error != nullptr) {
            *error = inner;
        }
        return false;
    }
    nodes = *list;
    if (error != nullptr) {
        error->clear();
    }
    return true;
}

bool AppContext::RefreshOptions(std::string* error) {
    if (!ipc.IsConnected()) {
        if (error != nullptr) {
            *error = ipc.LastError().empty() ? "not connected to the local service"
                                             : ipc.LastError();
        }
        return false;
    }
    if (!Supports(ipc::op::kGetOptions)) {
        if (error != nullptr) {
            error->clear();
        }
        return false;
    }

    std::string inner;
    const auto loaded = ipc.GetOptions(kServiceCallTimeout, &inner);
    if (!loaded.has_value()) {
        if (error != nullptr) {
            *error = inner;
        }
        return false;
    }
    options = *loaded;
    has_options = true;
    if (error != nullptr) {
        error->clear();
    }
    return true;
}

}  // namespace pcx::app
