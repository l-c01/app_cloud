package dev.proxyclient.ipc

import org.json.JSONObject

/**
 * 报文层 / the message layer.
 *
 * 与 Go 端 runtime/ipc/protocol.go 的 Request / Response / RPCError / Event 对应。
 * 报文以**一行一条 JSON** 传输（换行分隔），一次读取可能返回半条或多条，
 * 组帧由 [IpcClient] 负责——传输层只管字节，不需要知道协议。
 */

/**
 * 统一错误结构。
 *
 * 约定（与 Go 端一致）：
 *  - [code] 是稳定的机器可读码，界面据此分支；
 *  - [messageKey] 供查语言表，界面文案由客户端本地化，不解析 [message]；
 *  - [fieldPath] 用于字段级错误；
 *  - [detailsRedacted] 只放**已脱敏**的补充信息，禁止放原始凭据。
 */
data class IpcRpcError(
    val code: String,
    val messageKey: String,
    val message: String = "",
    val fieldPath: String = "",
    val retryable: Boolean = false,
    val operationId: String = "",
    val detailsRedacted: Map<String, String> = emptyMap(),
) {

    /** 供日志使用的单行描述。messageKey 存在时优先用它，避免把诊断文本当文案。 */
    fun describe(): String {
        val suffix = if (fieldPath.isNotEmpty()) " ($fieldPath)" else ""
        val text = if (message.isNotEmpty()) message else messageKey
        return "$code$suffix: $text"
    }

    companion object {
        fun parse(source: JSONObject?): IpcRpcError {
            if (source == null) {
                return IpcRpcError(code = IpcProtocol.Code.INTERNAL, messageKey = "ipc.not_ok")
            }
            val details = LinkedHashMap<String, String>()
            JsonRead.objectOrNull(source, "detailsRedacted")?.let { objectValue ->
                for (key in objectValue.keys()) {
                    details[key] = JsonRead.string(objectValue, key)
                }
            }
            return IpcRpcError(
                code = JsonRead.string(source, "code"),
                messageKey = JsonRead.string(source, "messageKey"),
                message = JsonRead.string(source, "message"),
                fieldPath = JsonRead.string(source, "fieldPath"),
                retryable = JsonRead.bool(source, "retryable"),
                operationId = JsonRead.string(source, "operationId"),
                detailsRedacted = details,
            )
        }
    }
}

/**
 * 服务端主动推送的事件。
 *
 * [seq] 单调递增；客户端据此发现丢事件（背压丢弃时 seq 会跳号）。
 * [dropped] 是本次推送前因背压被丢弃的事件数：它让界面知道
 * 「你看到的不是全部」，而不是静默地少几条日志。
 */
data class IpcEvent(
    val seq: Long,
    val kind: String,
    val payload: JSONObject?,
    val dropped: Long,
) {
    companion object {
        fun parse(source: JSONObject): IpcEvent = IpcEvent(
            seq = JsonRead.long(source, "seq"),
            kind = JsonRead.string(source, "kind"),
            payload = JsonRead.objectOrNull(source, "payload"),
            dropped = JsonRead.long(source, "dropped"),
        )
    }
}

/** 一次调用响应，或一条事件推送。事件推送不含 id。 */
data class IpcResponse(
    val id: String,
    val ok: Boolean,
    val result: JSONObject?,
    val error: IpcRpcError?,
    val event: IpcEvent?,
    val protocolVersion: Int,
    val schemaVersion: String,
) {
    companion object {

        /** 无法解析时返回 null：调用方丢弃并计数，而不是让读取线程退出。 */
        fun parse(text: String): IpcResponse? {
            val json = runCatching { JSONObject(text) }.getOrNull() ?: return null
            return parse(json)
        }

        fun parse(source: JSONObject): IpcResponse {
            val eventObject = JsonRead.objectOrNull(source, "event")
            return IpcResponse(
                id = JsonRead.string(source, "id"),
                ok = JsonRead.bool(source, "ok"),
                result = JsonRead.objectOrNull(source, "result"),
                error = JsonRead.objectOrNull(source, "error")?.let { IpcRpcError.parse(it) },
                event = eventObject?.let { IpcEvent.parse(it) },
                protocolVersion = JsonRead.int(source, "protocolVersion"),
                schemaVersion = JsonRead.string(source, "schemaVersion"),
            )
        }
    }
}

/**
 * 构造一次调用请求。
 *
 * token 出现在本地 IPC 报文里是设计的一部分（它只用于本地通道的访问控制，
 * 不是用户凭据），但**绝不能**把本函数的返回值写进日志或诊断包。
 */
internal fun buildRequestJson(
    id: String,
    operation: String,
    token: String,
    params: JSONObject?,
): JSONObject {
    val request = JSONObject()
    request.put("id", id)
    request.put("operation", operation)
    request.put("protocolVersion", IpcProtocol.PROTOCOL_VERSION)
    request.put("schemaVersion", IpcProtocol.SCHEMA_VERSION)
    request.put("token", token)
    if (params != null) {
        request.put("params", params)
    }
    return request
}
