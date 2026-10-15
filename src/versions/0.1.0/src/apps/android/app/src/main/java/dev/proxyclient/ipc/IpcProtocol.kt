package dev.proxyclient.ipc

import android.content.Context
import org.json.JSONArray
import org.json.JSONObject
import java.io.File

/**
 * 本地 IPC 的线协议 / the local IPC wire protocol.
 *
 * **这是 Go 端 runtime/ipc/protocol.go 与桌面端 core/ipc_protocol.hpp 的镜像。**
 * 三侧的 JSON 字段名必须逐字一致；改动任何一侧都要同时改另外两侧，
 * 否则表现是「服务端说成功、界面拿不到字段」这类难查的问题。
 *
 * 版本号、操作名、错误码集中在这里而不是散落在调用点：
 * 它们属于**契约**，契约值一旦散开，一次不完整的改动不会被任何编译器发现。
 */
object IpcProtocol {

    /** 线协议版本，必须与 Go 端 ProtocolVersion 相同。 */
    const val PROTOCOL_VERSION: Int = 1

    /** 载荷结构版本，必须与 Go 端 SchemaVersion 相同。 */
    const val SCHEMA_VERSION: String = "1.0.0"

    /**
     * 操作名 / operation names.
     *
     * 用字符串常量而不是 enum：服务端新增操作时，enum 会因为无法解析而
     * 让整条消息失败，而字符串可以让界面明确地报「不支持该操作」。
     * 这一点与桌面端 core/ipc_protocol.hpp 的处理一致。
     */
    object Op {
        const val GET_CAPABILITIES = "GetCapabilities"
        const val VALIDATE_PROFILE = "ValidateProfile"
        const val COMPILE_PROFILE = "CompileProfile"
        const val START = "Start"
        const val STOP = "Stop"
        const val GET_RUNTIME_STATE = "GetRuntimeState"
        const val SWITCH_NODE = "SwitchNode"
        const val SET_NETWORK_MODE = "SetNetworkMode"
        const val WATCH_EVENTS = "WatchEvents"
        const val RECOVER_NETWORK = "RecoverNetwork"

        // 会话与订阅操作。这一组是界面真正可用的前提：
        // 没有它们就只能连接一份已经构造好的配置，无法登录、取订阅或选节点。
        const val LOGIN = "Login"
        const val LOGOUT = "Logout"
        const val GET_SESSION_STATE = "GetSessionState"
        const val REFRESH_SUBSCRIPTION = "RefreshSubscription"
        const val LIST_NODES = "ListNodes"
        const val SELECT_NODE = "SelectNode"
        const val GET_OPTIONS = "GetOptions"
        const val SET_OPTIONS = "SetOptions"
    }

    /**
     * 稳定错误码，与 Go 端保持一致。
     *
     * 界面按码分支，**不解析 message**：message 是给人看的诊断文本，
     * 可能出现任意语言或后续被改写，用它做判断的代码会在服务端换文案时静默失效。
     */
    object Code {
        const val MALFORMED = "malformed_request"
        const val VERSION_MISMATCH = "version_mismatch"
        const val UNAUTHORIZED = "unauthorized"
        const val UNKNOWN_OPERATION = "unknown_operation"
        const val INVALID_PARAMS = "invalid_params"
        const val BUSY = "busy"
        const val NOT_RUNNING = "not_running"
        const val INTERNAL = "internal_error"
        const val TIMEOUT = "timeout"
        const val BACKPRESSURE = "backpressure"

        // 以下是客户端本地产生的码，服务端不会发出（与桌面端同样处理）。
        const val TRANSPORT = "transport_error"
        const val INCOMPATIBLE = "incompatible_client"
        const val BAD_REPLY = "bad_reply"
    }

    /**
     * 句柄里的 network 取值 / transport kinds.
     *
     * unix 与 tcp 与 Go 端 net.Listener 的 Addr().Network() 对应；
     * IN_PROCESS 是 Android 侧预留的第三种：设计基线规定 Android 用
     * 「绑定服务 / AAR」而不是本地套接字，那条路径没有监听地址，
     * 因此它不进 Handle，而由宿主注册一条进程内传输（见 IpcTransports）。
     */
    object Network {
        const val UNIX = "unix"
        const val TCP = "tcp"
        const val NPIPE = "npipe"
        const val IN_PROCESS = "inproc"
    }
}

// ---------------------------------------------------------------------------
// JSON 读取辅助
//
// 不用 org.json 的 optString(key, fallback) 等重载：它们对 JSON 的 null
// 处理在不同实现下并不一致（可能返回字面量 "null"），而配置里出现一个
// 字符串 "null" 会一路走进核心配置，报错却指向别处。这里统一显式判空。
// ---------------------------------------------------------------------------

internal object JsonRead {

    private fun raw(source: JSONObject, key: String): Any? {
        if (!source.has(key)) return null
        val value = source.opt(key) ?: return null
        return if (value === JSONObject.NULL) null else value
    }

    fun string(source: JSONObject, key: String, fallback: String = ""): String {
        val value = raw(source, key) ?: return fallback
        return if (value is String) value else value.toString()
    }

    fun int(source: JSONObject, key: String, fallback: Int = 0): Int {
        val value = raw(source, key) ?: return fallback
        if (value is Number) return value.toInt()
        return value.toString().trim().toIntOrNull() ?: fallback
    }

    fun long(source: JSONObject, key: String, fallback: Long = 0L): Long {
        val value = raw(source, key) ?: return fallback
        if (value is Number) return value.toLong()
        return value.toString().trim().toLongOrNull() ?: fallback
    }

    fun double(source: JSONObject, key: String, fallback: Double = 0.0): Double {
        val value = raw(source, key) ?: return fallback
        if (value is Number) return value.toDouble()
        return value.toString().trim().toDoubleOrNull() ?: fallback
    }

    fun bool(source: JSONObject, key: String, fallback: Boolean = false): Boolean {
        val value = raw(source, key) ?: return fallback
        if (value is Boolean) return value
        // 兼容 "true" 这类字符串：服务端不应发出它，但读成 false 会把
        // 「开关打开了」显示成「没打开」，比多写两行更危险。
        return value.toString().trim().equals("true", ignoreCase = true)
    }

    fun strings(source: JSONObject, key: String): List<String> {
        val value = raw(source, key)
        val array = when (value) {
            is JSONArray -> value
            else -> return emptyList()
        }
        val out = ArrayList<String>(array.length())
        for (i in 0 until array.length()) {
            val item = array.opt(i) ?: continue
            if (item === JSONObject.NULL) continue
            out += item.toString()
        }
        return out
    }

    fun objectOrNull(source: JSONObject, key: String): JSONObject? {
        val value = raw(source, key)
        return value as? JSONObject
    }

    /** 读取对象数组；非对象元素被跳过而不是猜一个空对象。 */
    fun objects(source: JSONObject, key: String): List<JSONObject> {
        val value = raw(source, key)
        val array = when (value) {
            is JSONArray -> value
            else -> return emptyList()
        }
        val out = ArrayList<JSONObject>(array.length())
        for (i in 0 until array.length()) {
            (array.opt(i) as? JSONObject)?.let { out += it }
        }
        return out
    }
}

// ---------------------------------------------------------------------------
// 连接句柄
// ---------------------------------------------------------------------------

/**
 * 连接句柄 / connection handle.
 *
 * 由服务端写在状态文件里，客户端据此建立连接。
 *
 * **Token 是本地访问控制凭据**，不是用户凭据：它只用于证明「调用方与
 * 服务在同一台机器、同一用户下」。即便如此也必须当成秘密对待——
 * 因此 [toString] 里被隐去，任何日志、崩溃报告或诊断包都不会带出它。
 */
data class Handle(
    val network: String,
    val address: String,
    val token: String,
    val protocolVersion: Int,
    val schemaVersion: String,
    val serverPid: Int,
) {

    /** 返回空串表示句柄可用。 */
    fun validationError(): String = when {
        network.isBlank() -> "the handle has no transport kind"
        address.isBlank() -> "the handle has no address"
        token.isBlank() -> "the handle has no access token"
        else -> ""
    }

    /** 报告句柄声明的版本是否与本客户端一致。 */
    fun isCompatible(): Boolean =
        protocolVersion == IpcProtocol.PROTOCOL_VERSION &&
            schemaVersion == IpcProtocol.SCHEMA_VERSION

    /**
     * 刻意隐去 token 与地址。
     *
     * data class 自动生成的 toString 会把 token 原样写出来，而
     * 「把对象打进日志」是排查问题时最自然的动作——默认安全的做法是
     * 让那次打印什么都不泄露。
     */
    override fun toString(): String =
        "Handle(network=$network, address=<omitted>, token=<redacted>, " +
            "protocolVersion=$protocolVersion, schemaVersion=$schemaVersion, serverPid=$serverPid)"

    companion object {
        fun parse(source: JSONObject): Handle = Handle(
            network = JsonRead.string(source, "network"),
            address = JsonRead.string(source, "address"),
            token = JsonRead.string(source, "token"),
            protocolVersion = JsonRead.int(source, "protocolVersion"),
            schemaVersion = JsonRead.string(source, "schemaVersion"),
            serverPid = JsonRead.int(source, "serverPid"),
        )
    }
}

/** 读取句柄文件的结果。用 sealed 而不是可空值：失败原因是界面要显示的文案。 */
sealed class HandleLoad {
    data class Loaded(val handle: Handle, val path: String) : HandleLoad()

    /** 状态文件不存在：最常见的情况是服务还没启动。 */
    data object Missing : HandleLoad()

    /** 文件存在但读不出可用句柄。detail 只用于诊断展示，不含凭据。 */
    data class Corrupt(val detail: String) : HandleLoad()

    /** 句柄可用但版本不匹配。 */
    data class Incompatible(
        val serverProtocol: Int,
        val serverSchema: String,
    ) : HandleLoad()
}

/**
 * 状态文件的位置 / where the handle file lives.
 *
 * 与桌面端的规则**不可能相同**：桌面端用 XDG 运行期目录或 APPDATA，
 * Android 上没有这些概念，唯一的等价物是应用私有目录。这里读
 * filesDir 与 noBackupFilesDir 两处，是因为服务可能被以
 * -state-dir <其中之一> 启动，而两个目录都是应用私有的（同一 uid 才能读），
 * 因此多试一处不会放宽访问控制。
 *
 * **这一条尚未与 Android 侧的打包方式核对过**（服务目前根本没有随 APK 分发）：
 * 具体用哪一个目录，必须由决定「Android 服务是进程内 AAR 还是独立进程」
 * 的探针一并定下来，否则界面会稳定地报「服务未运行」。
 */
object ServiceHandleStore {

    private const val HANDLE_FILE_NAME = "service.json"

    fun defaultPaths(context: Context): List<File> = listOf(
        File(context.filesDir, HANDLE_FILE_NAME),
        File(context.noBackupFilesDir, HANDLE_FILE_NAME),
    )

    fun load(context: Context): HandleLoad {
        var sawFile = false
        var lastDetail = ""

        for (file in defaultPaths(context)) {
            if (!file.isFile) continue
            sawFile = true

            // 读文件与解析 JSON 都用 runCatching，并且**不用**
            // `continue` 跳出内联 lambda：那需要 Kotlin 2.2 的
            // "break continue in inline lambdas"，而本工程的语言版本更低，
            // 用它会直接编译失败（报的是「only available since language
            // version 2.2」，与代码逻辑无关）。
            //
            // 因此这里改成把结果包成可空值，失败时记下原因并跳过本次循环。
            val text = runCatching { file.readText() }.getOrNull()
            if (text == null) {
                lastDetail = "the state file is unreadable"
                continue
            }
            val json = runCatching { JSONObject(text) }.getOrNull()
            if (json == null) {
                lastDetail = "the state file is not valid JSON"
                continue
            }

            val handle = Handle.parse(json)
            val validation = handle.validationError()
            if (validation.isNotEmpty()) {
                lastDetail = validation
                continue
            }
            if (!handle.isCompatible()) {
                return HandleLoad.Incompatible(handle.protocolVersion, handle.schemaVersion)
            }
            return HandleLoad.Loaded(handle, file.absolutePath)
        }

        if (!sawFile) return HandleLoad.Missing
        return HandleLoad.Corrupt(lastDetail.ifEmpty { "the state file is unreadable" })
    }
}
