package dev.proxyclient.ipc

import org.json.JSONObject

/**
 * 会话与订阅的载荷 / session and subscription payloads.
 *
 * 与 Go 端 runtime/ipc/protocol.go 的同名结构逐字对应。
 *
 * 安全约定（贯穿本节，来自 Go 端）：这些结构会被序列化后交给界面，
 * 因此**一律不含**面板会话凭据、订阅链接或节点密码。界面拿不到凭据
 * 是有意的，不是缺功能——界面上少一个能泄露凭据的地方。
 */

/** Login 的参数。 */
data class LoginParams(
    /** 面板地址；为空表示沿用服务端当前会话的地址。 */
    val panelUrl: String = "",
    val email: String = "",
    val password: String = "",
    /** 允许 http 面板地址，仅供本地测试。 */
    val allowInsecureHttp: Boolean = false,
    /** 为真时才把密码交给安全存储；默认不保存。 */
    val rememberPassword: Boolean = false,
) {
    /**
     * 注意：**服务端目前还没有使用** [rememberPassword]。
     *
     * 契约里有这个字段、往返测试也覆盖了它，但
     * cmd/proxyclient-service/main.go 的 login 只调用了
     * sess.Login(ctx, email, password)，没有把这一项传下去。
     * 界面仍然照发（契约就是契约），但「勾了记住密码下次仍要重新登录」
     * 是当前的真实行为——在这一点被接上之前，界面上不要承诺相反的结论。
     */
    fun toJson(): JSONObject {
        val json = JSONObject()
        // 与 Go 的 omitempty 对齐：空串与 false 不写。
        // 多写一个 "panelUrl": "" 会让服务端把「沿用当前地址」理解成
        // 「把地址清空」，而那个错误只在真正登录时才暴露。
        if (panelUrl.isNotBlank()) json.put("panelUrl", panelUrl)
        json.put("email", email)
        json.put("password", password)
        if (allowInsecureHttp) json.put("allowInsecureHttp", true)
        if (rememberPassword) json.put("rememberPassword", true)
        return json
    }
}

/** 账户摘要。不含密码、不含原始 auth_data。 */
data class AccountView(
    val email: String = "",
    val uuid: String = "",
    val banned: Boolean = false,
    val expired: Boolean = false,
    val expiredAtUnix: Long = 0L,
    val transferEnable: Long = 0L,
    val uploadedBytes: Long = 0L,
    val downloadedBytes: Long = 0L,
    val balance: Double = 0.0,
    val planName: String = "",
    val warnings: List<String> = emptyList(),
) {
    companion object {
        fun parse(source: JSONObject): AccountView = AccountView(
            email = JsonRead.string(source, "email"),
            uuid = JsonRead.string(source, "uuid"),
            banned = JsonRead.bool(source, "banned"),
            expired = JsonRead.bool(source, "expired"),
            expiredAtUnix = JsonRead.long(source, "expiredAtUnix"),
            transferEnable = JsonRead.long(source, "transferEnable"),
            uploadedBytes = JsonRead.long(source, "uploadedBytes"),
            downloadedBytes = JsonRead.long(source, "downloadedBytes"),
            balance = JsonRead.double(source, "balance"),
            planName = JsonRead.string(source, "planName"),
            warnings = JsonRead.strings(source, "warnings"),
        )
    }
}

/**
 * 账户用量。
 *
 * [present] 为 false 时界面**必须隐藏**用量，而不是显示 0——
 * 「用量 0」与「不知道用量」是两件事，前者会让用户以为流量没被统计。
 */
data class UsageView(
    val present: Boolean = false,
    val upload: Long = 0L,
    val download: Long = 0L,
    val total: Long = 0L,
    val expireUnix: Long = 0L,
    val remaining: Long = 0L,
) {
    fun used(): Long = upload + download

    companion object {
        fun parse(source: JSONObject): UsageView = UsageView(
            present = JsonRead.bool(source, "present"),
            upload = JsonRead.long(source, "upload"),
            download = JsonRead.long(source, "download"),
            total = JsonRead.long(source, "total"),
            expireUnix = JsonRead.long(source, "expireUnix"),
            remaining = JsonRead.long(source, "remaining"),
        )
    }
}

/** 订阅状态。 */
data class SubscriptionView(
    val nodeCount: Int = 0,
    val rejectedCount: Int = 0,
    val warnings: List<String> = emptyList(),
    val selectedNodeId: String = "",
    val lastSyncUnix: Long = 0L,
    val planName: String = "",
    val usage: UsageView = UsageView(),
) {
    companion object {
        fun parse(source: JSONObject): SubscriptionView = SubscriptionView(
            nodeCount = JsonRead.int(source, "nodeCount"),
            rejectedCount = JsonRead.int(source, "rejectedCount"),
            warnings = JsonRead.strings(source, "warnings"),
            selectedNodeId = JsonRead.string(source, "selectedNodeId"),
            lastSyncUnix = JsonRead.long(source, "lastSyncUnix"),
            planName = JsonRead.string(source, "planName"),
            usage = JsonRead.objectOrNull(source, "usage")?.let { UsageView.parse(it) } ?: UsageView(),
        )
    }
}

/**
 * GetSessionState / Login / Logout / RefreshSubscription 的返回值。
 *
 * 它刻意不返回任何凭据。界面据此决定显示登录页还是主界面。
 */
data class SessionState(
    val loggedIn: Boolean = false,
    val panelUrl: String = "",
    val account: AccountView? = null,
    val subscription: SubscriptionView? = null,
    /**
     * 本平台是否具备安全存储。
     *
     * 为 false 时界面必须提示「本次可用，但不会记住登录」——
     * 否则用户会以为下次启动仍是登录状态。
     *
     * 注意：服务端只在**存在会话对象**时才回填这个字段，
     * 因此未登录时看到的 false 是缺省值而不是结论，界面不该据此报警。
     */
    val secureStorage: Boolean = false,
) {
    companion object {
        fun parse(source: JSONObject): SessionState = SessionState(
            loggedIn = JsonRead.bool(source, "loggedIn"),
            panelUrl = JsonRead.string(source, "panelUrl"),
            account = JsonRead.objectOrNull(source, "account")?.let { AccountView.parse(it) },
            subscription = JsonRead.objectOrNull(source, "subscription")
                ?.let { SubscriptionView.parse(it) },
            secureStorage = JsonRead.bool(source, "secureStorage"),
        )
    }
}

/**
 * 一个节点的界面表示。
 *
 * **不含 password / uuid 等连接凭据**：节点的连接参数是秘密，
 * 界面只需要能显示和选择它。
 */
data class NodeView(
    val id: String,
    val name: String,
    val protocol: String,
    val server: String,
    val port: Int,
    val selected: Boolean,
) {
    companion object {
        fun parse(source: JSONObject): NodeView = NodeView(
            id = JsonRead.string(source, "id"),
            name = JsonRead.string(source, "name"),
            protocol = JsonRead.string(source, "protocol"),
            server = JsonRead.string(source, "server"),
            port = JsonRead.int(source, "port"),
            selected = JsonRead.bool(source, "selected"),
        )
    }
}

/** ListNodes 的返回值。 */
data class NodeList(val nodes: List<NodeView>) {
    companion object {
        fun parse(source: JSONObject): NodeList =
            NodeList(JsonRead.objects(source, "nodes").map { NodeView.parse(it) })
    }
}

/** SelectNode 的结果：服务端回传最终生效的节点 ID。 */
data class SelectNodeResult(val selectedNodeId: String) {
    companion object {
        fun parse(source: JSONObject): SelectNodeResult =
            SelectNodeResult(JsonRead.string(source, "selectedNodeId"))
    }
}

/**
 * GetOptions / SetOptions 的返回值。
 *
 * 返回值里的 options 原样保留为 JSON：选项模型在 domain 层定义，
 * 由 [dev.proxyclient.options.ClientOptions] 解析，不在这里再抄一份字段。
 */
data class OptionsResult(val options: JSONObject) {
    companion object {
        /** options 字段缺失或不是对象时返回 null，由调用方报「返回内容无法解析」。 */
        fun parse(source: JSONObject): OptionsResult? {
            val options = JsonRead.objectOrNull(source, "options") ?: return null
            return OptionsResult(options)
        }
    }
}

/** SelectNode 的参数：id 与 name 二选一，ID 优先。 */
internal fun selectNodeParams(id: String?, name: String?): JSONObject {
    val json = JSONObject()
    if (!id.isNullOrBlank()) json.put("id", id)
    if (!name.isNullOrBlank()) json.put("name", name)
    return json
}

/** SetOptions 的参数：与返回值同形，即 `{"options": {...}}`。 */
internal fun setOptionsParams(options: JSONObject): JSONObject {
    val json = JSONObject()
    json.put("options", options)
    return json
}
