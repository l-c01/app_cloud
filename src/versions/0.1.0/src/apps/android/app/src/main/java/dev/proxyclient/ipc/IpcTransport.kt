package dev.proxyclient.ipc

import android.net.LocalSocket
import android.net.LocalSocketAddress
import java.io.InputStream
import java.io.OutputStream
import java.net.InetSocketAddress
import java.net.Socket
import java.util.concurrent.ConcurrentHashMap

/**
 * 本地 IPC 传输层 / local IPC transport.
 *
 * 语义与桌面端 core/ipc_transport.hpp 的 Transport 一致：
 * **字节流 + 由上层按换行组帧**。传输层不需要知道协议，各平台实现因此都很短。
 * 差别只有一处：C++ 用「返回值 + 出参」报告错误，Kotlin 用异常
 * （IOException）。语义一一对应：读返回 0 表示对端正常关闭，抛异常表示出错。
 *
 * Android 与桌面的平台差异（这是本文件存在的理由）：
 *
 *  - 桌面端连的是 Unix 域套接字（POSIX）或命名管道（Windows）。
 *  - Android 是 Linux，**Unix 域套接字本身可用**，但 Java 侧只能用
 *    android.net.LocalSocket 访问（它默认走抽象命名空间，必须显式指定
 *    FILESYSTEM 才能连到一个文件系统路径，而 Go 的 net.Listen("unix", path)
 *    正是文件系统路径）。
 *  - 命名管道在 Android 上不存在，因此 npipe 明确拒绝而不是「尽力尝试」。
 *  - 设计基线规定 Android 用「绑定服务 / AAR」承载同一套抽象操作。
 *    那条路径**没有监听地址**，也不是字节流。因此这里把传输做成可注册的：
 *    宿主（将来的 AAR 桥）注册一个把字节回环到进程内调度器的实现即可，
 *    协议编解码与 [IpcClient] 完全不用改。
 *
 * 两条路径都留着而不是先猜一个：**ADR-0006 已经决定核心走 AAR 内嵌**
 * （sing-box 的 PlatformInterface 提供 fd 入口），因此「进程内回环」那条
 * 是接下来的方向；但 Unix 域套接字这条在当前阶段仍然可用——
 * 服务端只需让同一个 handler 跑在套接字后面，界面无需改动。
 * 顺序是先让界面能跑通，再按 ADR-0006 把核心接进同一个进程。
 */
interface IpcTransport {

    /**
     * 读取至多 buffer.size 字节。
     *
     * 返回 0 表示对端正常关闭；抛 IOException 表示出错。
     */
    fun read(buffer: ByteArray): Int

    /** 写出全部字节。写不满即失败：部分写入会让对端卡在等换行上。 */
    fun write(data: ByteArray)

    /** 关闭通道，可重复调用。关闭必须能打断另一线程里阻塞中的读取。 */
    fun close()
}

/** 传输层错误：消息只用于诊断，不含凭据。 */
class IpcTransportException(message: String) : Exception(message)

/**
 * Unix 域套接字（Android 的 Java 实现）。
 *
 * 必须指定 FILESYSTEM 命名空间：LocalSocket 的默认命名空间是 Android 自己
 * 的抽象套接字，与 Go 端 net.Listen("unix", <路径>) 创建的文件系统套接字
 * 互不可见。用默认值时的表现是「连接被拒绝」，而服务其实正在监听。
 */
class LocalSocketTransport private constructor(
    private val socket: LocalSocket,
    private val input: InputStream,
    private val output: OutputStream,
) : IpcTransport {

    override fun read(buffer: ByteArray): Int = input.read(buffer)

    override fun write(data: ByteArray) {
        output.write(data)
        // 不 flush 的话报文可能一直留在缓冲区里，界面表现为「服务不响应」。
        output.flush()
    }

    override fun close() {
        // 先关流再关套接字：顺序反了会在部分实现上抛异常，
        // 而那时的异常已经没人能处理（close 是在收尾路径上被调用的）。
        runCatching { input.close() }
        runCatching { output.close() }
        runCatching { socket.close() }
    }

    companion object {
        fun connect(path: String): LocalSocketTransport {
            if (path.isBlank()) {
                throw IpcTransportException("the handle has an empty socket path")
            }
            val socket = LocalSocket()
            try {
                socket.connect(LocalSocketAddress(path, LocalSocketAddress.Namespace.FILESYSTEM))
                return LocalSocketTransport(socket, socket.inputStream, socket.outputStream)
            } catch (error: Exception) {
                runCatching { socket.close() }
                val detail = error.message ?: error.javaClass.simpleName
                throw IpcTransportException("cannot open the local socket: $detail")
            }
        }
    }
}

/**
 * TCP 环回传输。
 *
 * **只接受环回地址**，与桌面端 core/ipc_transport.cpp 的同一条限制一致：
 * 本地管理接口一旦监听到非环回地址，同网络的任何人都能控制这个代理进程，
 * 而「把 127.0.0.1 写成 0.0.0.0」在实践中非常常见。
 */
class TcpLoopbackTransport private constructor(
    private val socket: Socket,
    private val input: InputStream,
    private val output: OutputStream,
) : IpcTransport {

    override fun read(buffer: ByteArray): Int = input.read(buffer)

    override fun write(data: ByteArray) {
        output.write(data)
        output.flush()
    }

    override fun close() {
        runCatching { input.close() }
        runCatching { output.close() }
        runCatching { socket.close() }
    }

    companion object {
        fun connect(address: String): TcpLoopbackTransport {
            val endpoint = parseHostPort(address)
                ?: throw IpcTransportException("the management address must be host:port")
            if (!isLoopbackHost(endpoint.first)) {
                throw IpcTransportException(
                    "refusing to connect to a non-loopback management address",
                )
            }
            val socket = Socket()
            try {
                socket.connect(InetSocketAddress(endpoint.first, endpoint.second), CONNECT_TIMEOUT_MS)
                return TcpLoopbackTransport(socket, socket.getInputStream(), socket.getOutputStream())
            } catch (error: Exception) {
                runCatching { socket.close() }
                val detail = error.message ?: error.javaClass.simpleName
                throw IpcTransportException("cannot connect to the management address: $detail")
            }
        }

        private const val CONNECT_TIMEOUT_MS = 5000
    }
}

/**
 * 判断地址是否指向环回。这是本模块**唯一**的安全相关判定，因此与桌面端
 * 逐条对齐（含括号形式与 IPv4-mapped IPv6）。
 */
fun isLoopbackAddress(address: String): Boolean {
    val endpoint = parseHostPort(address) ?: return false
    return isLoopbackHost(endpoint.first)
}

private fun isLoopbackHost(host: String): Boolean {
    if (host.isEmpty()) {
        // 空主机名在不同平台上可能被解释为「任意地址」，一律按不安全处理。
        return false
    }
    if (host == "localhost" || host == "127.0.0.1" || host == "::1") return true
    if (host == "0:0:0:0:0:0:0:1") return true
    if (host.startsWith("127.")) return true
    if (host.startsWith("::ffff:127.")) return true
    // "::"（未指定地址）不在此列：它表示监听所有接口。
    return false
}

/**
 * 拆分 "host:port"。
 *
 * 括号形式先取方括号内容；否则**只有恰好一个冒号**时才按 host:port 切分。
 * 按最后一个冒号切分对 IPv6 是错的："::1" 会被切成 ":"，
 * 而漏判的代价是「拒绝连接」——那还好；反过来把非环回判成环回的代价
 * 才是任何人控制本进程。因此这里宁可漏判。
 */
internal fun parseHostPort(address: String): Pair<String, Int>? {
    if (address.isEmpty()) return null

    if (address.startsWith("[")) {
        val close = address.indexOf(']')
        if (close < 0) return null
        val host = address.substring(1, close)
        val rest = address.substring(close + 1)
        if (!rest.startsWith(":")) return null
        val port = rest.substring(1).toIntOrNull() ?: return null
        return host to port
    }

    val first = address.indexOf(':')
    val last = address.lastIndexOf(':')
    if (first < 0 || first != last) return null
    val host = address.substring(0, first)
    val port = address.substring(first + 1).toIntOrNull() ?: return null
    return host to port
}

/**
 * 传输注册表 / transport registry.
 *
 * 默认注册 Android 真正可用的两种通道；第三种（进程内绑定）留给宿主注册。
 * 用注册表而不是 when(network) 分支，是为了让「Android 服务是进程内还是
 * 独立进程」这个未决问题**不必现在回答**：那个决定只影响这里多一行注册，
 * 不影响协议、模型与界面。
 */
object IpcTransports {

    private val factories = ConcurrentHashMap<String, (Handle) -> IpcTransport>()

    init {
        register(IpcProtocol.Network.UNIX) { handle ->
            LocalSocketTransport.connect(handle.address)
        }
        register(IpcProtocol.Network.TCP) { handle ->
            TcpLoopbackTransport.connect(handle.address)
        }
    }

    fun register(network: String, factory: (Handle) -> IpcTransport) {
        factories[network] = factory
    }

    /**
     * 按句柄建立一条通道。
     *
     * 不支持的通道**明确拒绝**而不是回退到别的通道：回退会让「配置写错了」
     * 表现成「连上了但连的是别的东西」，那比直接失败难查得多。
     */
    fun connect(handle: Handle): IpcTransport {
        val network = handle.network
        factories[network]?.let { factory -> return factory(handle) }

        val reason = when (network) {
            IpcProtocol.Network.NPIPE ->
                "named pipes do not exist on Android; the service must use a unix socket " +
                    "or an in-process binding"
            IpcProtocol.Network.IN_PROCESS ->
                "the in-process binding is not wired yet: the Go service is not bundled " +
                    "in this build, so there is nothing to call (TODO: register an " +
                    "in-process transport from the AAR binding)"
            else -> "unsupported transport kind: $network"
        }
        throw IpcTransportException(reason)
    }
}
