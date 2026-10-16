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
 * ## 返回类型：`Unit`，不是 `Int`
 *
 * Go 侧这些方法的签名是 `func (...) error`。gomobile 把 Go 的 `error`
 * 返回值映射成**抛出的 Java 异常**，因此生成出来的 Kotlin 接口是
 * `fun serviceStop(): Unit`（不是返回错误码）。
 *
 * 这一点踩过一次：按「Go 里返回 error 就是返回状态码」的直觉写成
 * `override fun serviceStop(): Int = 0` 会得到
 * 「Return type of 'serviceStop' is not a subtype of ... 'Unit'」。
 * **失败要靠抛异常表达，不是靠返回码。**
 *
 * ## 每个返回默认值的方法都写明了理由
 *
 * 一个「看起来成功但其实没实现」的回调会让核心据此做出错误判断，
 * 而症状与回调本身看起来毫无关系。
 */
class ProxyCommandServerHandler(
    private val onStop: () -> Unit,
) : CommandServerHandler {

    override fun serviceStop() {
        // 核心要求停止（例如配置重载失败、或用户从核心侧断开）。
        // 必须真的停掉隧道：什么都不做会让用户看到「已连接」，
        // 而核心其实已经不在跑了。
        onStop()
    }

    override fun serviceReload() {
        // 本应用的重载流程由 Kotlin 侧驱动（换节点/改选项后重新下发配置），
        // 核心侧没有需要自己重载的东西。
    }

    /**
     * 系统代理状态。
     *
     * **如实报告「不可用」。** Android 上没有可供普通应用写入的全局系统代理
     * （只有 `VpnService.Builder.setHttpProxy`，而那是隧道内的设置，
     * 不是系统设置）。报成可用会让核心以为它能用系统代理接管流量，
     * 从而不去用 TUN——结果就是「VPN 建立了但没流量」。
     */
    override fun getSystemProxyStatus(): SystemProxyStatus {
        val status = SystemProxyStatus()
        status.available = false
        status.enabled = false
        return status
    }

    override fun setSystemProxyEnabled(enabled: Boolean) {
        // 见上：Android 上没有这个能力。
        // 不抛异常也不假装成功——抛异常会让核心把整次配置重载判为失败，
        // 而这件事本来就不该被尝试。这里留下记录以便诊断。
        Log.w(TAG, "setSystemProxyEnabled($enabled) is not supported on Android; ignored")
    }

    override fun triggerNativeCrash() {
        // 只给调试用的入口。不实现，避免误触发。
        throw UnsupportedOperationException("triggerNativeCrash is debug-only and not wired up")
    }

    override fun writeDebugMessage(message: String) {
        // 核心的调试输出进 logcat。用 WARN 级别而不是 DEBUG：
        // 这些消息通常是异常路径上的诊断，过滤掉就失去了意义。
        Log.w(TAG, message)
    }

    override fun connectSSHAgent() {
        // 本应用不使用 SSH agent。
        throw UnsupportedOperationException("the SSH agent is not supported")
    }

    private companion object {
        const val TAG = "proxyclient/core"
    }
}
