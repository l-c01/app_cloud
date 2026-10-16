package dev.proxyclient.vpn

import android.util.Log
import io.nekohasekai.libbox.CommandServerHandler
import io.nekohasekai.libbox.SystemProxyStatus

/**
 * 核心回调到应用的入口 / the core's callbacks into the app.
 *
 * `libbox.CommandServerHandler` 只有 7 个方法（依据 1.14.3 的
 * `command_server.go`），因此这里完整实现而不是留一部分抛异常。
 *
 * **每个返回默认值的方法都写明了理由。** 一个「返回 false 但其实没实现」的
 * 回调会让核心据此做出错误判断，而症状与回调本身看起来毫无关系。
 */
class ProxyCommandServerHandler(
    private val onStop: () -> Unit,
) : CommandServerHandler {

    override fun serviceStop(): Int {
        // 核心要求停止（例如配置重载失败、或用户从核心侧断开）。
        // 必须真的停掉隧道：只返回 0 而不动隧道会让用户看到「已连接」
        // 但核心已经不在跑了。
        runCatching { onStop() }
            .onFailure { Log.e(TAG, "failed to stop the tunnel", it) }
        return 0
    }

    override fun serviceReload(): Int {
        // 本应用的重载流程由 Kotlin 侧驱动（换节点/改选项后重新下发配置），
        // 核心侧没有需要自己重载的东西。
        return 0
    }

    /**
     * 系统代理状态。
     *
     * **如实报告「不可用」。** Android 上没有可供普通应用写入的全局系统代理
     * （只有 VpnService 的 `setHttpProxy`，而那是隧道内的设置，不是系统设置）。
     * 报成可用会让核心以为它能用系统代理接管流量，从而不去用 TUN——
     * 结果就是「VPN 建立了但没流量」。
     */
    override fun getSystemProxyStatus(): SystemProxyStatus {
        val status = SystemProxyStatus()
        status.available = false
        status.enabled = false
        return status
    }

    override fun setSystemProxyEnabled(enabled: Boolean): Int {
        // 见上：Android 上没有这个能力，因此拒绝而不是假装设置成功。
        return -1
    }

    override fun triggerNativeCrash(): Int {
        // 只给调试用的入口。不实现，避免误触发。
        return -1
    }

    override fun writeDebugMessage(message: String) {
        // 核心的调试输出进 logcat。用 WARN 级别而不是 DEBUG：
        // 这些消息通常是异常路径上的诊断，过滤掉就失去了意义。
        Log.w(TAG, message)
    }

    override fun connectSSHAgent(): Int {
        // 本应用不使用 SSH agent。
        return -1
    }

    private companion object {
        const val TAG = "proxyclient/core"
    }
}
