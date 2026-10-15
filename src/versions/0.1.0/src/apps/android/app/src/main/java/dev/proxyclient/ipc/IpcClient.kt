package dev.proxyclient.ipc

import org.json.JSONObject
import java.io.ByteArrayOutputStream
import java.util.ArrayDeque
import java.util.concurrent.ArrayBlockingQueue
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicLong

/**
 * 调用结果 / the outcome of a call.
 *
 * 失败用**结构化的** [IpcRpcError] 而不是字符串：界面要按 code 分支、
 * 按 messageKey 取文案。把错误折成一个字符串会逼着界面去解析它，
 * 而那个解析在服务端换一次文案之后就会静默失效。
 */
sealed class IpcResult<out T> {
    data class Ok<out T>(val value: T) : IpcResult<T>()
    data class Err(val error: IpcRpcError) : IpcResult<Nothing>()
}

/**
 * JSON-RPC 客户端 / JSON-RPC client over a pluggable byte stream.
 *
 * 线程模型（镜像桌面端 core/ipc_client.cpp 的决定）：
 *
 *   一条后台读取线程负责收报文并按 id 配对；**它绝不调用界面代码**。
 *   [call] 会阻塞到响应到达或超时，因此**必须在工作线程上调用**；
 *   界面线程只读状态、不直接调它。
 *
 * 为什么不回调界面：跨线程直接改 Compose 的状态会让重组发生在任意线程上，
 * 表现是随机的界面错乱与崩溃，而且只在负载高时出现，极难复现。
 * 让线程边界只有一处（读取线程 → 队列 → 工作线程 → 主线程状态）
 * 是这里唯一的复杂度来源，也是唯一值得付出的那部分。
 *
 * 事件同样进队列并带上限与丢弃计数：慢消费者不该把内存吃光，
 * 也不该静默地少几条日志。服务端的丢弃数与本地丢弃数分开统计，
 * 两者相加才是真正丢失量。
 */
class IpcClient(
    private val transport: IpcTransport,
    private val token: String,
) {

    private val pending = ConcurrentHashMap<String, ArrayBlockingQueue<IpcResponse>>()

    private val eventLock = Any()
    private val events = ArrayDeque<IpcEvent>()

    private val droppedLocally = AtomicLong()
    private val droppedByServer = AtomicLong()
    private val lastEventSeq = AtomicLong()
    private val requestSeq = AtomicLong()

    private val closed = AtomicBoolean(false)
    private val closedReasonText = AtomicReferenceValue("")

    private val reader: Thread

    init {
        // 守护线程：即使界面进程准备退出，它也不该把进程留住。
        reader = Thread({ readerLoop() }, "proxyclient-ipc-reader").apply {
            isDaemon = true
            start()
        }
    }

    fun isClosed(): Boolean = closed.get()

    // -----------------------------------------------------------------------
    // 调用
    // -----------------------------------------------------------------------

    /**
     * 发起一次调用并等待配对响应。
     *
     * 超时返回 timeout 错误，并把该请求的占位清掉：迟到的响应会被当作
     * 无人认领而丢弃，**不会串到下一次调用上**——串号的表现是「点了 A
     * 却拿到了 B 的结果」，那比超时难查得多。
     */
    fun call(
        operation: String,
        params: JSONObject? = null,
        timeoutMs: Long = DEFAULT_CALL_TIMEOUT_MS,
    ): IpcResult<JSONObject?> {
        if (closed.get()) {
            return IpcResult.Err(transportError())
        }

        val id = "a${requestSeq.incrementAndGet()}"
        val queue = ArrayBlockingQueue<IpcResponse>(1)
        pending[id] = queue

        val payload = buildRequestJson(id, operation, token, params).toString() + "\n"
        try {
            transport.write(payload.toByteArray(Charsets.UTF_8))
        } catch (error: Exception) {
            pending.remove(id)
            finish(error.message ?: error.javaClass.simpleName)
            return IpcResult.Err(transportError())
        }

        val waitMs = if (timeoutMs > 0) timeoutMs else DEFAULT_CALL_TIMEOUT_MS
        val response = try {
            queue.poll(waitMs, TimeUnit.MILLISECONDS)
        } catch (interrupted: InterruptedException) {
            // 恢复中断标志：吞掉它会让上层的取消逻辑永远等不到。
            Thread.currentThread().interrupt()
            null
        }
        pending.remove(id)

        if (response == null) {
            if (closed.get()) return IpcResult.Err(transportError())
            return IpcResult.Err(
                IpcRpcError(
                    code = IpcProtocol.Code.TIMEOUT,
                    messageKey = "ui.detail.service_timeout",
                    message = "the local service did not respond within ${waitMs}ms",
                    operationId = operation,
                    retryable = true,
                ),
            )
        }

        val lateEvent = response.event
        if (lateEvent != null) {
            // 服务端不会在响应位置上发事件。真出现时保留它、报一次异常，
            // 而不是丢掉——少了这条事件会让后面的序号检查无解。
            enqueue(lateEvent)
            return IpcResult.Err(badReply("an event arrived where a response was expected"))
        }

        if (!response.ok) {
            return IpcResult.Err(
                response.error
                    ?: IpcRpcError(code = IpcProtocol.Code.INTERNAL, messageKey = "ipc.not_ok"),
            )
        }
        return IpcResult.Ok(response.result)
    }

    // -----------------------------------------------------------------------
    // 会话与订阅（便捷封装，内部都是 call）
    //
    // 每一条都只做两件事：发起调用、把结果解析成强类型。解析集中在这些
    // 函数里，是为了让界面层完全不必接触 JSONObject——界面里出现字段名字符串
    // 就意味着「字段名写错了不会被任何编译器发现」。
    // -----------------------------------------------------------------------

    fun getSessionState(timeoutMs: Long = DEFAULT_CALL_TIMEOUT_MS): IpcResult<SessionState> =
        parse("GetSessionState", call(IpcProtocol.Op.GET_SESSION_STATE, null, timeoutMs)) {
            SessionState.parse(it)
        }

    fun login(
        params: LoginParams,
        timeoutMs: Long = LONG_CALL_TIMEOUT_MS,
    ): IpcResult<SessionState> =
        parse("Login", call(IpcProtocol.Op.LOGIN, params.toJson(), timeoutMs)) {
            SessionState.parse(it)
        }

    fun logout(timeoutMs: Long = DEFAULT_CALL_TIMEOUT_MS): IpcResult<SessionState> =
        parse("Logout", call(IpcProtocol.Op.LOGOUT, null, timeoutMs)) {
            SessionState.parse(it)
        }

    fun refreshSubscription(
        timeoutMs: Long = LONG_CALL_TIMEOUT_MS,
    ): IpcResult<SessionState> =
        parse("RefreshSubscription", call(IpcProtocol.Op.REFRESH_SUBSCRIPTION, null, timeoutMs)) {
            SessionState.parse(it)
        }

    fun listNodes(timeoutMs: Long = DEFAULT_CALL_TIMEOUT_MS): IpcResult<NodeList> =
        parse("ListNodes", call(IpcProtocol.Op.LIST_NODES, null, timeoutMs)) {
            NodeList.parse(it)
        }

    fun selectNode(
        id: String? = null,
        name: String? = null,
        timeoutMs: Long = DEFAULT_CALL_TIMEOUT_MS,
    ): IpcResult<SelectNodeResult> =
        parse(
            "SelectNode",
            call(IpcProtocol.Op.SELECT_NODE, selectNodeParams(id, name), timeoutMs),
        ) { SelectNodeResult.parse(it) }

    /** 返回选项对象本身（`{"options": {...}}` 里的那一层）。 */
    fun getOptions(timeoutMs: Long = DEFAULT_CALL_TIMEOUT_MS): IpcResult<JSONObject> =
        parse("GetOptions", call(IpcProtocol.Op.GET_OPTIONS, null, timeoutMs)) { result ->
            OptionsResult.parse(result)?.options
                ?: throw IllegalStateException("the reply has no options object")
        }

    /**
     * 整体替换选项，返回**服务端规范化之后**的选项。
     *
     * 采纳返回值而不是本地那份：服务端可能补默认值或做归一化，
     * 界面若继续显示自己发出去的那份，显示的就会与实际生效的配置不同。
     */
    fun setOptions(
        options: JSONObject,
        timeoutMs: Long = DEFAULT_CALL_TIMEOUT_MS,
    ): IpcResult<JSONObject> =
        parse(
            "SetOptions",
            call(IpcProtocol.Op.SET_OPTIONS, setOptionsParams(options), timeoutMs),
        ) { result ->
            OptionsResult.parse(result)?.options
                ?: throw IllegalStateException("the reply has no options object")
        }

    // -----------------------------------------------------------------------
    // 事件
    // -----------------------------------------------------------------------

    /**
     * 取出一个待处理事件；队列为空时返回 null。
     *
     * 目前 Android 界面还没有消费事件的屏幕（连接状态仍由本地状态机表达）。
     * 读取线程仍然必须收集它们：一条也收不到与收到了但没人看，
     * 在后端诊断上不是同一件事，而丢弃计数正是为此存在的。
     */
    fun pollEvent(): IpcEvent? = synchronized(eventLock) {
        if (events.isEmpty()) null else events.pollFirst()
    }

    /** 因本地队列满而被丢弃的事件数。 */
    fun droppedLocally(): Long = droppedLocally.get()

    /** 服务端报告的累计丢弃数。 */
    fun droppedByServer(): Long = droppedByServer.get()

    /** 最近收到的事件的序号，用于发现跳号。 */
    fun lastEventSeq(): Long = lastEventSeq.get()

    // -----------------------------------------------------------------------
    // 生命周期
    // -----------------------------------------------------------------------

    /**
     * 关闭连接。可重复调用。
     *
     * 关闭通道是**唯一**能打断阻塞读取的手段（与桌面端一致的结论）：
     * 那些不支持读超时的通道上，读取会一直挂着，退出时线程会留到最后。
     */
    fun close() {
        finish("the connection was closed")
        reader.join(READER_JOIN_TIMEOUT_MS)
    }

    // -----------------------------------------------------------------------
    // 内部
    // -----------------------------------------------------------------------

    private fun readerLoop() {
        val buffer = ByteArray(READ_BUFFER_SIZE)
        val line = ByteArrayOutputStream(INITIAL_LINE_BYTES)
        var failure = ""

        try {
            while (!closed.get()) {
                val got = transport.read(buffer)
                if (got <= 0) {
                    // 0 表示对端正常关闭；负值不该出现，一并按关闭处理。
                    failure = "the service closed the connection"
                    break
                }
                for (index in 0 until got) {
                    val value = buffer[index].toInt() and 0xFF
                    when (value) {
                        BYTE_LF -> {
                            if (line.size() > 0) {
                                dispatchLine(String(line.toByteArray(), Charsets.UTF_8))
                                line.reset()
                            }
                        }
                        BYTE_CR -> Unit
                        else -> {
                            line.write(value)
                            if (line.size() > MAX_LINE_BYTES) {
                                // 超长报文按不可解析处理并计数：继续缓冲会让
                                // 一条坏报文把内存吃光，而断开连接更没必要。
                                droppedLocally.incrementAndGet()
                                line.reset()
                            }
                        }
                    }
                }
            }
        } catch (error: Exception) {
            failure = error.message ?: error.javaClass.simpleName
        } finally {
            finish(failure.ifEmpty { "the connection was lost" })
        }
    }

    private fun dispatchLine(text: String) {
        val response = IpcResponse.parse(text)
        if (response == null) {
            // 无法解析的报文不该让读取线程退出：服务端将来可能推送
            // 本客户端不认识的报文，丢弃并计数比断开更有韧性。
            droppedLocally.incrementAndGet()
            return
        }

        val event = response.event
        if (event != null) {
            droppedByServer.addAndGet(event.dropped)
            lastEventSeq.set(event.seq)
            enqueue(event)
            return
        }

        if (response.id.isEmpty()) {
            droppedLocally.incrementAndGet()
            return
        }
        // 无人认领（已经超时）时 offer 返回 false：静默丢弃是对的，
        // 那是我们自己已经放弃的请求。
        pending[response.id]?.offer(response)
    }

    private fun enqueue(event: IpcEvent) {
        synchronized(eventLock) {
            if (events.size >= EVENT_QUEUE_LIMIT) {
                // 队列满时丢弃**最旧**的事件，保留最新的：界面最需要知道的是
                // 当前状态，而不是几分钟前的状态。
                events.pollFirst()
                droppedLocally.incrementAndGet()
            }
            events.addLast(event)
        }
    }

    /** 标记关闭并唤醒所有等待中的调用方。 */
    private fun finish(reason: String) {
        if (!closed.compareAndSet(false, true)) return
        closedReasonText.set(reason)
        val syntheticError = IpcRpcError(
            code = IpcProtocol.Code.TRANSPORT,
            messageKey = "ui.detail.service_unavailable",
            message = reason,
            retryable = true,
        )
        val snapshot = pending.values.toList()
        for (queue in snapshot) {
            queue.offer(
                IpcResponse(
                    id = "",
                    ok = false,
                    result = null,
                    error = syntheticError,
                    event = null,
                    protocolVersion = IpcProtocol.PROTOCOL_VERSION,
                    schemaVersion = IpcProtocol.SCHEMA_VERSION,
                ),
            )
        }
    }

    private fun transportError(): IpcRpcError = IpcRpcError(
        code = IpcProtocol.Code.TRANSPORT,
        messageKey = "ui.detail.service_unavailable",
        message = closedReasonText.get(),
        retryable = true,
    )

    private fun badReply(detail: String): IpcRpcError = IpcRpcError(
        code = IpcProtocol.Code.BAD_REPLY,
        messageKey = "ui.detail.bad_reply",
        message = detail,
        retryable = false,
    )

    private fun <T> parse(
        what: String,
        result: IpcResult<JSONObject?>,
        block: (JSONObject) -> T,
    ): IpcResult<T> = when (result) {
        is IpcResult.Err -> result
        is IpcResult.Ok -> {
            val source = result.value
            if (source == null) {
                IpcResult.Err(badReply("$what returned no result object"))
            } else {
                try {
                    IpcResult.Ok(block(source))
                } catch (error: Exception) {
                    IpcResult.Err(
                        badReply("$what: ${error.message ?: error.javaClass.simpleName}"),
                    )
                }
            }
        }
    }

    companion object {
        /** 普通调用的默认上限，与桌面端 kDefaultCallTimeout 一致。 */
        const val DEFAULT_CALL_TIMEOUT_MS: Long = 15_000

        /** 登录与拉订阅要走公网，15 秒常常不够；超时会被用户当成「登录失败」。 */
        const val LONG_CALL_TIMEOUT_MS: Long = 45_000

        private const val READ_BUFFER_SIZE = 8 * 1024
        private const val INITIAL_LINE_BYTES = 4 * 1024
        private const val MAX_LINE_BYTES = 8 * 1024 * 1024
        private const val EVENT_QUEUE_LIMIT = 256
        private const val READER_JOIN_TIMEOUT_MS = 1000L
        private const val BYTE_LF = 0x0A
        private const val BYTE_CR = 0x0D
    }
}

/**
 * 一个可原子读写的字符串。
 *
 * 不用 @Volatile 字段而是包一层：`closedReason` 只在关闭时写一次、
 * 由任意线程读，裸字段需要显式注解才正确，而漏掉注解不会有任何提示。
 */
private class AtomicReferenceValue(initial: String) {
    private val value = java.util.concurrent.atomic.AtomicReference(initial)
    fun get(): String = value.get()
    fun set(next: String) {
        value.set(next)
    }
}
