// 界面共享上下文 / shared UI context.
//
// 两种界面模式（简单 / 专业）**共用同一份上下文**，这是设计基线 §4.1 的硬要求：
// 切换模式只改变呈现与可见选项，不改变已经建立的行为，也不产生第二份状态。
//
// 若每种模式各持一份状态，很快就会出现「简单模式显示已连接、专业模式显示未连接」
// 这类互相矛盾的情况。
#pragma once

#include <chrono>
#include <string>
#include <vector>

#include "app/settings.hpp"
#include "core/i18n.hpp"
#include "core/ipc_client.hpp"
#include "core/ipc_protocol.hpp"
#include "core/options.hpp"

namespace pcx::app {

// AppContext 是整个界面共享的状态。
//
// 生命周期由 wxApp 持有，窗口与面板只拿引用。
struct AppContext {
    // 出厂配置（只读）。
    Config config;
    // 用户偏好（可写；关闭窗口时保存）。
    Preferences preferences;
    // 语言包。
    i18n::Bundle bundle;
    // 协商后的实际语言码。
    std::string language;
    // 与后台服务的连接。
    ipc::IpcClient ipc;

    // 最近一次从服务读到的能力与运行状态。
    ipc::Capabilities capabilities;
    ipc::RuntimeState runtime_state;
    bool has_capabilities = false;

    // -----------------------------------------------------------------------
    // 会话、订阅与选项
    //
    // 这三份状态刻意都放在这里而不是各个面板里：两种模式共用同一份上下文
    // 是设计基线 §4.1 的硬要求。各持一份的话很快就会出现
    // 「简单模式显示已登录、专业模式显示未登录」这类互相矛盾的情况。
    // -----------------------------------------------------------------------

    // 最近一次读到的会话状态。has_session 为 false 表示**还不知道**
    // （服务不可用或尚未查询），与「确定未登录」不是一回事。
    ipc::SessionState session;
    bool has_session = false;

    // 订阅节点列表。刷新订阅或选择节点后由调用方重新拉取。
    ipc::NodeList nodes;

    // 完整选项模型。has_options 为 false 时界面显示「尚未读取」，
    // 而不是拿一份 DefaultOptions 冒充服务端的真实取值——
    // 那会让用户以为看到的就是当前的配置。
    domain::Options options;
    bool has_options = false;

    // app_dir 是可执行文件所在目录，用于定位配置与语言资源。
    std::string app_dir;

    // T 取一条本地化文案。
    std::string T(const std::string& key,
                  const std::vector<std::string>& args = {}) const {
        return bundle.Translate(language, key, args);
    }

    // -----------------------------------------------------------------------
    // 服务交互
    // -----------------------------------------------------------------------

    // RefreshFromService 读取能力与运行状态。
    //
    // 返回 false 时 error 填入原因；调用方应当**照常显示界面**并提示服务不可用，
    // 而不是弹一个模态框然后什么都不显示——后台服务没起来是完全正常的情况。
    bool RefreshFromService(std::string* error);

    // RefreshSession 读取会话与账户摘要。
    bool RefreshSession(std::string* error);

    // RefreshNodes 读取订阅节点列表。
    bool RefreshNodes(std::string* error);

    // RefreshOptions 读取完整选项模型。
    //
    // 返回 false 且 error 非空时表示真的失败了；服务端不支持该操作时
    // 返回 false 但 error 为空——界面据此显示「不可用」而不是一条错误。
    bool RefreshOptions(std::string* error);

    // Supports 报告服务端是否声明支持某操作。
    //
    // 能力表尚未读到时返回 true：把一个可能可用的按钮藏起来，
    // 比让用户点了之后收到「不支持该操作」更难排查。
    bool Supports(const std::string& operation) const;

    // ServiceAvailable 报告后台服务是否可用。
    bool ServiceAvailable() const { return ipc.IsConnected(); }

    // ReconnectService 尝试按默认状态文件重新连接。
    bool ReconnectService(std::string* error);

    // RemoteCallTimeout 返回「会走出本机」的操作（登录、刷新订阅）的等待上限。
    //
    // 导出它是因为调用发生在界面层。让界面自己写一个数字，
    // 两处必然漂移：一边改成 30 秒、另一边还是 5 秒，
    // 表现就是「登录按钮总是超时，而刷新订阅正常」。
    static std::chrono::milliseconds RemoteCallTimeout();
};

}  // namespace pcx::app
