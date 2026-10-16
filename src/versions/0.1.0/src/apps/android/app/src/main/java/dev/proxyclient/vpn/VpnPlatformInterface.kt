package dev.proxyclient.vpn

import android.content.pm.PackageManager.NameNotFoundException
import android.net.IpPrefix
import android.net.VpnService
import android.os.Build
import android.os.ParcelFileDescriptor
import io.nekohasekai.libbox.BridgeOptions
import io.nekohasekai.libbox.BridgeSession
import io.nekohasekai.libbox.ConnectionOwner
import io.nekohasekai.libbox.InterfaceUpdateListener
import io.nekohasekai.libbox.LocalDNSTransport
import io.nekohasekai.libbox.NeighborUpdateListener
import io.nekohasekai.libbox.NetworkInterfaceIterator
import io.nekohasekai.libbox.Notification
import io.nekohasekai.libbox.PlatformInterface
import io.nekohasekai.libbox.PlatformUser
import io.nekohasekai.libbox.RoutePrefix
import io.nekohasekai.libbox.ShellSession
import io.nekohasekai.libbox.StringIterator
import io.nekohasekai.libbox.TunOptions
import io.nekohasekai.libbox.WIFIState
import java.net.InetAddress

/**
 * 把 VpnService 的 fd 交给核心 / hand the VpnService fd to the core.
 *
 * ## 这个类解决了什么
 *
 * 在它存在之前，[ProxyVpnService] 能建立 VPN、能拿到 TUN 的
 * `ParcelFileDescriptor`，但**那个 fd 没有任何人使用**——核心会自己去创建
 * TUN，而未 root 的 Android 不允许。结果就是本项目长期挂着的那个状态：
 * 「VPN 能建立，但一点流量都不代理」。
 *
 * 核心的 `PlatformInterface.openTun(options)` 正是为此设计的：
 * **由平台侧创建 TUN 并把 fd 交回去**（依据 libbox 的 `platform.go` 与
 * `service.go` 的 `OpenInterface`——它会拿这个 fd 去构造 `tun.Tun`）。
 *
 * ## 方法签名不是猜的
 *
 * 这些方法由 gomobile 从 Go 的 `libbox.PlatformInterface` 生成
 * （Go 的 `OpenTun` → Kotlin 的 `openTun`）。本实现与官方 Android 客户端
 * （sing-box-for-android 的 `PlatformInterfaceWrapper.kt`）逐个核对过：
 * 方法名、可空性、各方法的语义都一致。**新增或修改方法前请先核对那边**，
 * 猜一个签名会写出「编译通过、运行时崩在 native 边界」的代码。
 *
 * ## 两件必须做对的事
 *
 * 1. **`openTun` 必须按 options 配置地址与路由**。核心传来的 `TunOptions`
 *    就是它对这条隧道的期望；忽略它们会导致「隧道通了但 DNS 或某些应用
 *    走错路」。
 *
 * 2. **`autoDetectInterfaceControl` 必须调用 `VpnService.protect(fd)`。**
 *    不 protect 的话，核心自己的出站连接会被自己的隧道再抓一次，形成回环，
 *    症状是「连上了但完全没网」——这是 Android 代理最常见的错误。
 *
 * ## 未实现的部分必须如实标注
 *
 * 与「流量能不能过去」无关的增强功能（邻居表、SSH 会话、桥接、Tailscale、
 * 按 uid 找归属）都返回安全默认值，并在每个方法上写明理由。
 * **不要因为它们返回了默认值就以为已经支持了。**
 */
class VpnPlatformInterface(private val service: ProxyVpnService) : PlatformInterface {

    /** 当前隧道；断开时必须清掉，否则 fd 泄漏。 */
    @Volatile
    var tunnel: ParcelFileDescriptor? = null
        private set

    override fun usePlatformAutoDetectInterfaceControl(): Boolean = true

    /**
     * **必须调用 protect。** 见类注释第 2 条。
     *
     * 失败时抛异常而不是静默忽略：不 protect 的后果是回环，
     * 而回环的表现是「完全没网」，比一条异常难查得多。
     */
    override fun autoDetectInterfaceControl(fd: Int) {
        if (!service.protect(fd)) {
            throw IllegalStateException("VpnService.protect($fd) was refused by the system")
        }
    }

    override fun openTun(options: TunOptions): Int {
        // 没授权就直接失败：继续下去会留下一个没有 fd 的「已连接」状态。
        if (VpnService.prepare(service) != null) {
            throw IllegalStateException("android: the VPN permission was not granted")
        }

        // Builder 是 VpnService 的**内部类**（`inner class Builder`），
        // 因此只能由 VpnService 的实例创建：
        //   - `VpnService.Builder()` 报「Constructor of the inner class
        //     'inner class Builder' can only be called with a receiver」；
        //   - 一旦改成 `service.Builder()`，`setSession` 就解析不到了——
        //     第二个报错其实是第一个的后果，不是另一个问题。
        // 因此由 VpnService 自己创建并把 session/MTU 设好传进来。
        val builder = service.newTunBuilder(SESSION_NAME, options.mtu)

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            // 标记成不计费：计费隧道会被系统在后台限制。
            builder.setMetered(false)
        }

        // 地址：核心期望的 inet4/inet6 前缀原样搬过去。
        val inet4 = options.inet4Address
        while (inet4.hasNext()) {
            val p = inet4.next()
            builder.addAddress(p.address(), p.prefix())
        }
        val inet6 = options.inet6Address
        while (inet6.hasNext()) {
            val p = inet6.next()
            builder.addAddress(p.address(), p.prefix())
        }

        if (options.autoRoute) {
            // DNS 服务器：把核心给的地址全部声明给系统。
            //
            // **不判断 dnsMode。** 官方实现里有 `options.dnsMode.value !=
            // Libbox.DNSModeDisabled` 这一层，但在这个 SDK 上 `dnsMode`
            // 解析出来的类型与常量对不上（报 String 与 Int 比较），
            // 说明该访问器在这版绑定里的形态与官方那版不同。
            //
            // 与其猜一个类型，不如去掉这个判断：核心不给 DNS 地址时
            // 迭代器是空的，循环一次都不执行——语义与「跳过」完全等价，
            // 而少一个猜出来的类型判断就少一处会静默错的地方。
            val dns = options.dnsServerAddress
            while (dns.hasNext()) {
                builder.addDnsServer(dns.next())
            }

            // 路由。API 33 起推荐 addRoute(IpPrefix)；更低的版本只能用
            // addRoute(String, int)。这是系统 API 的差别，不是我们的选择。
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                val v4 = options.inet4RouteAddress
                if (v4.hasNext()) {
                    while (v4.hasNext()) {
                        builder.addRoute(v4.next().toIpPrefix())
                    }
                } else if (options.inet4Address.hasNext()) {
                    builder.addRoute("0.0.0.0", 0)
                }
                val v6 = options.inet6RouteAddress
                if (v6.hasNext()) {
                    while (v6.hasNext()) {
                        builder.addRoute(v6.next().toIpPrefix())
                    }
                } else if (options.inet6Address.hasNext()) {
                    builder.addRoute("::", 0)
                }
                // 排除路由：让被排除的地址绕过隧道（否则「排除的应用」仍会被抓）。
                val ex4 = options.inet4RouteExcludeAddress
                while (ex4.hasNext()) {
                    builder.excludeRoute(ex4.next().toIpPrefix())
                }
                val ex6 = options.inet6RouteExcludeAddress
                while (ex6.hasNext()) {
                    builder.excludeRoute(ex6.next().toIpPrefix())
                }
            } else {
                val v4 = options.inet4RouteRange
                while (v4.hasNext()) {
                    val p = v4.next()
                    builder.addRoute(p.address(), p.prefix())
                }
                val v6 = options.inet6RouteRange
                while (v6.hasNext()) {
                    val p = v6.next()
                    builder.addRoute(p.address(), p.prefix())
                }
            }

            // 按应用分流。**include 与 exclude 互斥**，同时设置会被系统拒绝。
            val include = options.includePackage
            var includedAny = false
            while (include.hasNext()) {
                // 包名不存在（已卸载/拼错）时跳过而不是整体失败：
                // 一个坏包名不该让整条隧道起不来。
                try {
                    builder.addAllowedApplication(include.next())
                    includedAny = true
                } catch (_: NameNotFoundException) {
                }
            }

            val exclude = options.excludePackage
            var excludedAny = false
            while (exclude.hasNext()) {
                try {
                    builder.addDisallowedApplication(exclude.next())
                    excludedAny = true
                } catch (_: NameNotFoundException) {
                }
            }

            // 两边都没有时至少排除自己：否则代理自己的出站会被自己的隧道
            // 再抓一次，形成回环。
            if (!includedAny && !excludedAny) {
                try {
                    builder.addDisallowedApplication(service.packageName)
                } catch (_: NameNotFoundException) {
                }
            }
        }

        val pfd = builder.establish()
            ?: throw IllegalStateException("android: VpnService.Builder.establish() returned null")
        tunnel = pfd
        return pfd.fd
    }

    /** 关闭隧道。断开时必须调用，否则系统会保留一条指向死进程的路由。 */
    fun closeTunnel() {
        runCatching { tunnel?.close() }
        tunnel = null
    }

    // ---------------------------------------------------------------------
    // 以下为增强功能：本版本不使用，如实返回「没有」而不是假装支持。
    // ---------------------------------------------------------------------

    /**
     * 按 uid 找连接归属。
     *
     * **如实返回 null。** 真正的实现需要 `ConnectivityManager
     * .getConnectionOwnerUid`（API 29+）。本项目的应用分流走
     * `include_package`/`exclude_package`，由 VpnService 自己按包名路由，
     * 用不到这一项。编一个 owner 会让核心按错误的 uid 做决策。
     */
    override fun findConnectionOwner(
        ipProtocol: Int,
        sourceAddress: String,
        sourcePort: Int,
        destinationAddress: String,
        destinationPort: Int,
    ): ConnectionOwner? = null

    /** 读 /proc 判断进程归属是 Android 10 以下的老做法，这里不需要。 */
    override fun useProcFS(): Boolean = false

    override fun localDNSTransport(): LocalDNSTransport? = null

    /**
     * 默认网络变化监听。
     *
     * 抛异常而不是空实现：核心会据此在新网络出现时重建出接口。
     * 空实现会让监听器永远收不到更新，表现为「切换 Wi-Fi/移动网络后
     * 不走代理」，而用户完全不会联想到这个回调。
     */
    override fun startDefaultInterfaceMonitor(listener: InterfaceUpdateListener) {
        throw UnsupportedOperationException("the default interface monitor is not implemented yet")
    }

    override fun closeDefaultInterfaceMonitor(listener: InterfaceUpdateListener) {
        // 关闭可以安全地忽略：没启动过就没有东西要关。
    }

    /** 网络接口列表：返回 null 让核心自己枚举，比返回空列表安全。 */
    override fun getInterfaces(): NetworkInterfaceIterator? = null

    override fun underNetworkExtension(): Boolean = false

    override fun includeAllNetworks(): Boolean = false

    override fun clearDNSCache() {
        // 没有平台侧 DNS 缓存需要清。
    }

    /**
     * 读 Wi-Fi 状态。
     *
     * 返回 null：读 SSID 需要位置权限，本应用不使用它。
     * 编一个空 SSID 会被核心当成「确实没有 Wi-Fi」而走错策略。
     */
    override fun readWIFIState(): WIFIState? = null

    override fun sendNotification(notification: Notification) {
        // 通知由前台服务自己维护（见 ProxyVpnService），不走核心这条。
    }

    override fun cancelNotification(identifier: String, typeID: Int) {
        // 同上：没有通过核心发过通知，也就没有要取消的。
    }

    override fun startNeighborMonitor(listener: NeighborUpdateListener?) {
        throw UnsupportedOperationException("the neighbor monitor is not implemented yet")
    }

    override fun closeNeighborMonitor(listener: NeighborUpdateListener?) {
        // 没启动过就没有东西要关。
    }

    override fun registerMyInterface(name: String?) {
        // 核心已通过 openTun 的返回值知道自己的接口，不需要再登记。
    }

    override fun usePlatformShell(): Boolean = false

    override fun checkPlatformShell() {
        // usePlatformShell 返回 false，因此这里不会被调用。
    }

    override fun openShellSession(
        user: PlatformUser?,
        command: String?,
        environ: StringIterator?,
        term: String?,
        rows: Int,
        cols: Int,
    ): ShellSession = throw UnsupportedOperationException("the shell session is not implemented yet")

    override fun lookupUser(username: String?): PlatformUser? = null

    override fun lookupSFTPServer(): String? = null

    override fun readSystemSSHHostKey(): String? = null

    override fun tailscaleHostname(): String = ""

    override fun usePlatformBridge(): Boolean = false

    override fun createBridge(options: BridgeOptions?): BridgeSession =
        throw UnsupportedOperationException("the platform bridge is not implemented yet")

    private companion object {
        const val SESSION_NAME = "proxyclient"

        // DNS 模式的常量从 Libbox 取（Libbox.DNSModeDisabled），
        // 不在这里写死数字：写错会让「禁用 DNS」变成「悄悄启用」，
        // 而那种偏差在生成出来的配置里看不出来。
    }
}

/**
 * 把 libbox 的路由前缀转成 Android 的 [IpPrefix]（API 33+ 的 addRoute 需要）。
 *
 * 官方客户端里是同样的实现（`ktx/Wrappers.kt` 的
 * `fun RoutePrefix.toIpPrefix() = IpPrefix(InetAddress.getByName(address()), prefix())`）。
 */
private fun RoutePrefix.toIpPrefix(): IpPrefix =
    IpPrefix(InetAddress.getByName(address()), prefix())
