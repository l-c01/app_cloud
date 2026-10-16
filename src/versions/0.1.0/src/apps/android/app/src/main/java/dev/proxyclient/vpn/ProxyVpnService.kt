package dev.proxyclient.vpn

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Intent
import android.net.VpnService
import android.os.ParcelFileDescriptor
import android.util.Log
import dev.proxyclient.MainActivity
import dev.proxyclient.R
import io.nekohasekai.libbox.CommandServer
import io.nekohasekai.libbox.OverrideOptions


/**
 * Android 上的流量接管 / traffic takeover on Android.
 *
 * ## 它现在做什么
 *
 *   1. 建立 `VpnService`；
 *   2. **实现核心的 `PlatformInterface`**（见 [VpnPlatformInterface]），
 *      由 `openTun` 把 VpnService 的 fd 交给核心，由
 *      `autoDetectInterfaceControl` 调 `protect` 防止回环；
 *   3. 启动核心（`CommandServer.startOrReloadService`），把生成的配置
 *      交给它跑。
 *
 * ## 此前的状态与本轮的变化
 *
 * 此前这个类是**骨架**：它能建立 VPN、能拿到 TUN 的 `ParcelFileDescriptor`，
 * 但那个 fd **没有任何人使用**——核心会自己去创建 TUN，而未 root 的
 * Android 不允许。结果就是「VPN 能建立，但一点流量都不代理」。
 *
 * 现在 fd 经 `openTun` 进核心，核心由本服务启动并持有配置。
 *
 * ## 仍然未完成的部分（必须如实标注）
 *
 * - **没有在真机上验证过**：本仓库的 CI 只能证明「构建成功」与
 *   「APK 里带着核心」，证明不了流量真的走了代理；
 * - 配置由调用方通过 [startWithConfig] 下发。界面（`ProxyController`）
 *   目前仍走进程内 IPC 那条链，**尚未改成「编译好的配置下发给本服务」**。
 *   也就是说：核心能跑、fd 能交，但「点连接之后把配置送进来」这一步
 *   还没接上。这是下一步要做的。
 */
class ProxyVpnService : VpnService() {

    private var tunnel: ParcelFileDescriptor? = null

    /** 核心的平台接口。fd 的交接全在它里面完成。 */
    private var platform: VpnPlatformInterface? = null

    /** 核心的命令服务；它持有正在运行的 sing-box 实例。 */
    private var commandServer: CommandServer? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_START -> startTunnel()
            ACTION_START_WITH_CONFIG -> {
                val config = intent.getStringExtra(EXTRA_CONFIG)
                if (config.isNullOrEmpty()) {
                    // 没有配置就启动核心等于让它跑一份空配置——
                    // 那会让「已连接」变成一个假象。
                    Log.e(TAG, "ACTION_START_WITH_CONFIG without a config; ignoring")
                } else {
                    pendingConfig = config
                    startTunnel()
                }
            }
            ACTION_STOP -> {
                stopTunnel()
                stopSelf()
            }
            else -> {
                // 未知动作时不做事，也不崩溃：系统在某些情况下会以 null intent
                // 重启服务（例如进程被回收后），那不是错误。
            }
        }
        // START_STICKY 会让系统在内存紧张时重启服务并传入 null intent。
        // 对代理来说这反而危险：用户以为已经断开，服务却又起来了。
        // 因此用 START_NOT_STICKY——用户没点连接，就不该有连接。
        return START_NOT_STICKY
    }

    /**
     * 创建一份配好 session 与 MTU 的 `VpnService.Builder`。
     *
     * **为什么放在这里而不是 `VpnPlatformInterface` 里**：`Builder` 是
     * `VpnService` 的内部类，只能由 VpnService 的实例创建。在平台接口里写
     * `VpnService.Builder()` 会报「Constructor of the inner class
     * 'inner class Builder' can only be called with a receiver」。
     */
    fun newTunBuilder(session: String, mtu: Int): Builder {
        val builder = Builder()
        // setSession/setMtu 就是方法。此前我按「Kotlin 合成属性」写成
        // builder.session = ... 是错的——当时真正的问题是 Builder 是内部类，
        // 属性化改法掩盖了它，于是错误从「Builder 无法构造」变成了
        // 「session 未解析」，看起来像另一回事。
        builder.setSession(session)
        builder.setMtu(mtu)
        return builder
    }
    private fun startTunnel() {
        if (tunnel != null) {
            return
        }

        // prepare() 返回非 null 表示用户尚未授权，必须由 Activity 发起授权流程。
        // 这里直接返回而不是静默失败：静默失败会表现为「点了连接但没反应」。
        if (prepare(this) != null) {
            // 授权请求必须从 Activity 发起，服务无法弹系统对话框。
            val intent = Intent(this, MainActivity::class.java).apply {
                action = MainActivity.ACTION_REQUEST_VPN_PERMISSION
                addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
            }
            startActivity(intent)
            return
        }

        startForeground(NOTIFICATION_ID, buildNotification())

        // 核心启动时会调用 platform.openTun()，由那里完成 VpnService 的
        // 建立与 fd 交接。因此**我们不再自己 establish()**：两处都建立
        // 会得到两条隧道，而系统只会把流量送到其中一条。
        val platformInterface = VpnPlatformInterface(this)
        platform = platformInterface

        val config = pendingConfig
        if (config == null) {
            // 还没有配置：先把平台接口准备好，等配置到达再启动核心。
            // 不在这里编一份空配置去启动核心——那会掩盖「配置没送到」。
            Log.i(TAG, "tunnel prepared; waiting for a compiled config before starting the core")
            return
        }

        startCore(platformInterface, config)
    }

    private fun startCore(platformInterface: VpnPlatformInterface, config: String) {
        try {
            val server = CommandServer(
                ProxyCommandServerHandler(onStop = {
                    // 核心要求停止时，必须真的把隧道拆掉（见 handler 的注释）。
                    stopTunnel()
                    stopSelf()
                }),
                platformInterface,
            )
            server.start()
            commandServer = server

            val overrides = OverrideOptions().apply {
                // 让核心知道哪些应用在隧道内。空列表表示「全部应用」，
                // 由平台接口在 openTun 里按 include/exclude 表达。
                includePackage = emptyStringIterator()
                excludePackage = emptyStringIterator()
            }
            server.startOrReloadService(config, overrides)

            tunnel = platformInterface.tunnel
            if (tunnel == null) {
                // 核心没要 TUN（例如配置里没启用 TUN 入站）。
                // 这时候「已连接」是假的：没有 TUN，流量一点都不会被接管。
                Log.w(TAG, "the core did not request a TUN interface; traffic will not be captured")
            }
        } catch (e: Exception) {
            // 启动失败必须让用户知道，而不是留下一个「已连接」的假象。
            Log.e(TAG, "failed to start the core", e)
            stopTunnel()
            stopSelf()
        }
    }

    private fun stopTunnel() {
        // 先停核心再关隧道：反过来会让核心在一个已经消失的 fd 上收包。
        runCatching { commandServer?.closeService() }
            .onFailure { Log.w(TAG, "closing the core service failed", it) }
        runCatching { commandServer?.close() }
        commandServer = null

        // 关闭 TUN 设备。不关的话系统会保留一条指向已死进程的路由，
        // 用户表现为「断开之后完全没网」。
        platform?.closeTunnel()
        platform = null
        runCatching { tunnel?.close() }
        tunnel = null
        pendingConfig = null
        stopForeground(STOP_FOREGROUND_REMOVE)
    }

    override fun onRevoke() {
        // 用户在系统设置里撤销了 VPN 授权，或另一个 VPN 应用接管了。
        // 必须清理，而不是继续假装在运行。
        stopTunnel()
        stopSelf()
    }

    override fun onDestroy() {
        stopTunnel()
        super.onDestroy()
    }

    private fun buildNotification(): Notification {
        val manager = getSystemService(NotificationManager::class.java)
        // minSdk 是 26，通知渠道一定可用，因此不做版本判断。
        val channel = NotificationChannel(
            CHANNEL_ID,
            getString(R.string.notification_channel_proxy),
            NotificationManager.IMPORTANCE_LOW,
        ).apply {
            // 常驻通知不该响铃或震动：它长期存在，打扰用户是错的。
            setShowBadge(false)
        }
        manager.createNotificationChannel(channel)

        val contentIntent = PendingIntent.getActivity(
            this,
            0,
            Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE,
        )

        return Notification.Builder(this, CHANNEL_ID)
            .setContentTitle(getString(R.string.notification_title))
            // 用应用自己的图标，而不是 android.R.drawable.stat_sys_vpn_ic。
            //
            // 后者是系统隐藏资源：它不在公开 SDK 里，编译期直接报
            // "Unresolved reference"。而且同一行的失败会让后续链式调用的
            // 类型推断失效，连带下一行的 setContentIntent 也报未解析——
            // 一个错误看起来像两个，容易修错地方。
            .setSmallIcon(R.drawable.ic_launcher_foreground)
            .setContentIntent(contentIntent)
            .setOngoing(true)
            .build()
    }

    /** 尚未交给核心的配置；`startTunnel` 会把它送进去。 */
    @Volatile
    private var pendingConfig: String? = null

    companion object {
        const val ACTION_START = "dev.proxyclient.action.START"
        const val ACTION_STOP = "dev.proxyclient.action.STOP"

        /**
         * 带着编译好的配置启动。
         *
         * 配置是编译器（Go 侧 `shared/compiler`）生成的 sing-box JSON，
         * 原样交给核心——**不要在 Kotlin 侧再拼一遍配置**：
         * 两份实现必然分叉，而分叉的症状是「界面上改了但没生效」。
         */
        const val ACTION_START_WITH_CONFIG = "dev.proxyclient.action.START_WITH_CONFIG"
        const val EXTRA_CONFIG = "dev.proxyclient.extra.CONFIG"

        private const val TAG = "proxyclient/vpn"
        private const val CHANNEL_ID = "proxy_status"
        private const val NOTIFICATION_ID = 1001
    }
}
