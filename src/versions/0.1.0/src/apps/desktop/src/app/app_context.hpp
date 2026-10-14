// 界面共享上下文 / shared UI context.
//
// 两种界面模式（简单 / 专业）**共用同一份上下文**，这是设计基线 §4.1 的硬要求：
// 切换模式只改变呈现与可见选项，不改变已经建立的行为，也不产生第二份状态。
//
// 若每种模式各持一份状态，很快就会出现「简单模式显示已连接、专业模式显示未连接」
// 这类互相矛盾的情况。
#pragma once

#include <string>
#include <vector>

#include "app/settings.hpp"
#include "core/i18n.hpp"
#include "core/ipc_client.hpp"
#include "core/ipc_protocol.hpp"

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

    // ServiceAvailable 报告后台服务是否可用。
    bool ServiceAvailable() const { return ipc.IsConnected(); }

    // ReconnectService 尝试按默认状态文件重新连接。
    bool ReconnectService(std::string* error);
};

}  // namespace pcx::app
