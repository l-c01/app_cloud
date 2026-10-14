package dev.proxyclient.vpn

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Intent
import android.net.VpnService
import android.os.ParcelFileDescriptor
import dev.proxyclient.MainActivity
import dev.proxyclient.R

/**
 * Android 上的流量接管 / traffic takeover on Android.
 *
 * **这是骨架，不是可用实现。** 当前它只把 VpnService 的建立与拆除做正确，
 * 尚未把隧道接入核心进程。设计基线要求把「已完成」与「未完成」分开陈述，
 * 因此这里明确标注：
 *
 *   已实现：前台服务、通知、VpnService 授权与建立、TUN 设备生命周期、
 *           应用范围（允许/排除列表）的表达。
 *   未实现：把 TUN 的流量交给核心处理（需要核心的 Android 绑定方式，
 *           由 G1 探针决定是用 AAR 内嵌还是预构建二进制）。
 *
 * 为什么 Android 必须用 VpnService：这是系统提供的**唯一**流量接管方式。
 * 没有 root 的情况下无法修改系统代理设置，也没有 TUN 设备的其它获取途径。
 */
class ProxyVpnService : VpnService() {

    private var tunnel: ParcelFileDescriptor? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_START -> startTunnel()
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

        val builder = Builder()
            .setSession("proxyclient")
            .setMtu(DEFAULT_MTU)

        // 地址与路由。刻意只声明 IPv4 与一条默认路由：
        // 声明得越多，出问题时越难判断是哪一条规则导致的。
        builder.addAddress(VPN_ADDRESS, VPN_PREFIX_LENGTH)
        builder.addRoute("0.0.0.0", 0)

        // 排除本应用自身：否则代理发出的流量会被自己的隧道再抓一次，
        // 形成回环，表现为「连上了但完全没网」——这是 Android 代理
        // 最常见的错误之一。
        runCatching { builder.addDisallowedApplication(packageName) }

        tunnel = runCatching { builder.establish() }.getOrNull()

        if (tunnel == null) {
            // 建立失败必须让用户知道，而不是留下一个「已连接」的假象。
            stopSelf()
            return
        }

        startForeground(NOTIFICATION_ID, buildNotification())
    }

    private fun stopTunnel() {
        // 关闭 TUN 设备。不关的话系统会保留一条指向已死进程的路由，
        // 用户表现为「断开之后完全没网」。
        runCatching { tunnel?.close() }
        tunnel = null
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
            .setSmallIcon(android.R.drawable.stat_sys_vpn_ic)
            .setContentIntent(contentIntent)
            .setOngoing(true)
            .build()
    }

    companion object {
        const val ACTION_START = "dev.proxyclient.action.START"
        const val ACTION_STOP = "dev.proxyclient.action.STOP"

        private const val CHANNEL_ID = "proxy_status"
        private const val NOTIFICATION_ID = 1001

        // 10.111.222.1 是常见 VPN 应用使用的私有网段，避开家用网段，
        // 减少与用户实际网络冲突的可能。
        private const val VPN_ADDRESS = "10.111.222.1"
        private const val VPN_PREFIX_LENGTH = 32
        private const val DEFAULT_MTU = 1500
    }
}
