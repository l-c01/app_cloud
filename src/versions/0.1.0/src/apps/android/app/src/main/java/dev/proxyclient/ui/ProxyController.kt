package dev.proxyclient.ui

import android.content.Context
import android.os.Handler
import android.os.Looper
import dev.proxyclient.ipc.HandleLoad
import dev.proxyclient.ipc.IpcClient
import dev.proxyclient.ipc.IpcProtocol
import dev.proxyclient.ipc.IpcResult
import dev.proxyclient.ipc.IpcRpcError
import dev.proxyclient.ipc.IpcTransportException
import dev.proxyclient.ipc.IpcTransports
import dev.proxyclient.ipc.LoginParams
import dev.proxyclient.ipc.NodeView
import dev.proxyclient.ipc.ServiceHandleStore
import dev.proxyclient.ipc.SessionState
import dev.proxyclient.options.ClientOptions
import java.util.concurrent.Executors

/**
 * 界面与后台服务之间的唯一出入口 / the single entry point to the backend.
 *
 * 线程模型（这是本文件最重要的约定）：
 *
 *   - **所有 IPC 调用都在一条后台线程上串行执行**。单线程而不是线程池：
 *     这些操作彼此依赖（登录之后才知道订阅、改完选项要拿回规范化结果），
 *     并发执行只会让「先登录还是先取选项」变成随机顺序。
 *   - **状态只在主线程上改**。Compose 的状态不是线程安全的，从工作线程写
 *     会表现为随机的界面错乱，且只在负载高时出现。
 *   - 工作线程只读一份快照（[snapshot]）用于决定下一步做什么，
 *     不直接改状态。
 *
 * 因此本类的每个公开方法都从主线程调用，内部把工作丢给后台线程，
 * 再通过 [onState] 把新状态交回主线程。
 */
class ProxyController(
    private val context: Context,
    private val messages: Messages,
    initial: ProxyUiState,
    private val onState: (ProxyUiState) -> Unit,
) {

    private val io = Executors.newSingleThreadExecutor { runnable ->
        Thread(runnable, "proxyclient-ipc").apply { isDaemon = true }
    }
    private val main = Handler(Looper.getMainLooper())

    @Volatile
    private var current: ProxyUiState = initial

    @Volatile
    private var client: IpcClient? = null

    private var stopped = false

    // -----------------------------------------------------------------------
    // 生命周期
    // -----------------------------------------------------------------------

    /** 打开界面时调用：尝试连上服务并取一份完整状态。 */
    fun start() {
        submit("") { connectAndLoad() }
    }

    /** 界面销毁时调用。 */
    fun stop() {
        if (stopped) return
        stopped = true
        io.execute {
            client?.close()
            client = null
        }
        io.shutdown()
    }

    /**
     * 纯界面本地状态的更新（界面模式、记住模式这类偏好）。
     *
     * **必须经过这里**，而不是在 Activity 里直接写 uiState：控制器持有一份
     * 状态快照，绕过它的话，下一次「后台操作完成」会用那份旧快照把本地改动
     * 刷回去。表现是「切到专业模式，过几秒自己跳回简单模式」——
     * 而且只在后台恰好有操作时出现，极难复现。
     */
    fun updateLocal(transform: (ProxyUiState) -> ProxyUiState) = update(transform)

    // -----------------------------------------------------------------------
    // 操作
    // -----------------------------------------------------------------------

    /** 登录。密码只在这一次调用里存在，不进状态、不落盘。 */
    fun login(
        panelUrl: String,
        email: String,
        password: String,
        rememberPassword: Boolean,
        allowInsecureHttp: Boolean,
    ) {
        if (email.isBlank() || password.isBlank()) {
            // 本地就能判断的输入错误不必往返一次：往返的结果是一样的，
            // 只是慢一点、还会在服务端留下一串失败的登录记录。
            reportLocal("session.error.credentials_required")
            return
        }
        if (panelUrl.isBlank() && snapshot().session.panelUrl.isBlank()) {
            reportLocal("session.error.panel_url_required")
            return
        }

        submit(IpcProtocol.Op.LOGIN) {
            val active = ensureClient() ?: return@submit
            val params = LoginParams(
                panelUrl = panelUrl,
                email = email,
                password = password,
                allowInsecureHttp = allowInsecureHttp,
                rememberPassword = rememberPassword,
            )
            when (val result = active.login(params)) {
                is IpcResult.Ok -> {
                    applySession(result.value)
                    loadNodes(active)
                    loadOptions(active)
                }
                is IpcResult.Err -> report(result.error)
            }
        }
    }

    fun logout() {
        submit(IpcProtocol.Op.LOGOUT) {
            val active = ensureClient() ?: return@submit
            when (val result = active.logout()) {
                is IpcResult.Ok -> {
                    applySession(result.value)
                    // 退出登录后节点列表必须清空：留着上一份订阅的节点，
                    // 界面会显示一个已经不存在的可选项。
                    update { it.copy(nodes = emptyList(), activeNode = "") }
                }
                is IpcResult.Err -> report(result.error)
            }
        }
    }

    /** 重新拉取订阅（刷新按钮）。 */
    fun refreshSubscription() {
        submit(IpcProtocol.Op.REFRESH_SUBSCRIPTION) {
            val active = ensureClient() ?: return@submit
            when (val result = active.refreshSubscription()) {
                is IpcResult.Ok -> {
                    applySession(result.value)
                    loadNodes(active)
                }
                is IpcResult.Err -> report(result.error)
            }
        }
    }

    /** 只刷新会话与节点（不重新拉订阅）。 */
    fun refresh() {
        submit(IpcProtocol.Op.GET_SESSION_STATE) {
            val active = ensureClient() ?: return@submit
            loadSession(active)
            loadNodes(active)
        }
    }

    /** 点按节点即选中。传 ID 而不是名字：同名节点会被选错。 */
    fun selectNode(id: String) {
        submit(IpcProtocol.Op.SELECT_NODE) {
            val active = ensureClient() ?: return@submit
            when (val result = active.selectNode(id = id)) {
                is IpcResult.Ok -> {
                    loadSession(active)
                    loadNodes(active)
                }
                is IpcResult.Err -> report(result.error)
            }
        }
    }

    /** 保存整套选项；界面拿回的是服务端规范化之后的那份。 */
    fun saveOptions(options: ClientOptions) {
        submit(IpcProtocol.Op.SET_OPTIONS) {
            val active = ensureClient() ?: return@submit
            when (val result = active.setOptions(options.toJson())) {
                is IpcResult.Ok -> {
                    // 先把值取出来再进 lambda：lambda 是稍后在主线程执行的，
                    // 依赖智能转换在其中仍然成立会把「能编译」变成一件碰运气的事。
                    val saved = result.value
                    update {
                        it.copy(
                            options = ClientOptions.parse(saved),
                            notice = messages["ui.detail.options_saved"],
                            message = "",
                            detail = "",
                        )
                    }
                }
                is IpcResult.Err -> report(result.error)
            }
        }
    }

    /** 重新从服务端读选项，丢弃本地改动。 */
    fun reloadOptions() {
        submit(IpcProtocol.Op.GET_OPTIONS) {
            val active = ensureClient() ?: return@submit
            loadOptions(active)
        }
    }

    /**
     * 简易模式的路由模式：立即生效。
     *
     * 这里不做「先改再保存」的两段式：它只有一个取值，没有输入到一半的
     * 中间状态，因此立即提交既没有歧义也不会产生半个配置。
     */
    fun setRouteMode(mode: String) {
        saveOptions(snapshot().options.withRouteMode(mode))
    }

    /** 简易模式的分应用模式。理由同上。 */
    fun setPerAppMode(mode: String) {
        saveOptions(snapshot().options.withPerAppMode(mode))
    }

    // -----------------------------------------------------------------------
    // 内部：调度
    // -----------------------------------------------------------------------

    /**
     * 把一次操作放到后台线程，并在开始与结束时维护 [ProxyUiState.busy]。
     *
     * 两个状态更新都经过主线程的同一个 Handler，因此它们的先后顺序
     * 一定是「开始」在前——用别的同步方式会出现「忙」还没置上就被清掉。
     */
    private fun submit(operation: String, body: () -> Unit) {
        if (stopped) return
        update { it.copy(busy = operation, message = "", detail = "") }
        io.execute {
            try {
                body()
            } catch (error: Throwable) {
                // 这里捕获的是本类自己的缺陷（解析、空值等），不是服务端的错误。
                // 吞掉它会让界面永远停在「上一个操作还在进行中」。
                reportLocal("ui.error.unexpected", error.message ?: error.javaClass.simpleName)
            } finally {
                update { it.copy(busy = "") }
            }
        }
    }

    private fun snapshot(): ProxyUiState = current

    /** **只在主线程执行** [transform]。 */
    private fun update(transform: (ProxyUiState) -> ProxyUiState) {
        main.post {
            val next = transform(current)
            current = next
            onState(next)
        }
    }

    // -----------------------------------------------------------------------
    // 内部：连接与加载
    // -----------------------------------------------------------------------

    /**
     * 返回一个可用的客户端；连不上时报告原因并返回 null。
     *
     * 每次调用都会检查旧连接是否已经断开：服务重启会让连接变成死的，
     * 而一个「看起来还在」的死连接会让之后每次调用都超时。
     */
    private fun ensureClient(): IpcClient? {
        client?.let { existing ->
            if (!existing.isClosed()) return existing
            client = null
        }

        val handle = when (val load = ServiceHandleStore.load(context)) {
            is HandleLoad.Loaded -> load.handle
            HandleLoad.Missing -> {
                fail(
                    "ui.detail.service_unavailable",
                    "no state file, looked in: " +
                        ServiceHandleStore.defaultPaths(context).joinToString(", ") { it.name },
                )
                return null
            }
            is HandleLoad.Corrupt -> {
                fail("ui.detail.service_unavailable", load.detail)
                return null
            }
            is HandleLoad.Incompatible -> {
                // 版本不匹配要说清是哪一侧的版本，否则用户只能看到「连不上」。
                val key = if (load.serverProtocol != IpcProtocol.PROTOCOL_VERSION) {
                    "ipc.version_mismatch.protocol"
                } else {
                    "ipc.version_mismatch.schema"
                }
                fail(key, "", load.serverProtocol.toString(), load.serverSchema)
                return null
            }
        }

        val transport = try {
            IpcTransports.connect(handle)
        } catch (error: IpcTransportException) {
            fail("ui.detail.service_unavailable", error.message ?: "")
            return null
        }

        val created = IpcClient(transport, handle.token)
        client = created
        update { it.copy(backend = BackendState.Ready, message = "", detail = "") }
        return created
    }

    private fun connectAndLoad() {
        val active = ensureClient() ?: return
        loadSession(active)
        loadNodes(active)
        loadOptions(active)
    }

    private fun loadSession(active: IpcClient) {
        when (val result = active.getSessionState()) {
            is IpcResult.Ok -> applySession(result.value)
            is IpcResult.Err -> report(result.error)
        }
    }

    private fun loadNodes(active: IpcClient) {
        when (val result = active.listNodes()) {
            is IpcResult.Ok -> {
                val nodes = result.value.nodes
                update { withSelectedNode(it, nodes) }
            }
            is IpcResult.Err -> report(result.error)
        }
    }

    private fun loadOptions(active: IpcClient) {
        when (val result = active.getOptions()) {
            is IpcResult.Ok -> {
                val parsed = ClientOptions.parse(result.value)
                update { it.copy(options = parsed) }
            }
            is IpcResult.Err -> report(result.error)
        }
    }

    private fun applySession(next: SessionState) {
        update { withSelectedNode(it.copy(session = next), it.nodes) }
    }

    /**
     * 让「当前节点」这个名字与节点列表保持一致。
     *
     * 优先用列表里标记为 selected 的那一项；用不了时回退到会话里的
     * selectedNodeId。两者都取不到就留空——**不保留上一次的名字**：
     * 切换账号后显示上一个账号的节点名，比什么都不显示更糟。
     */
    private fun withSelectedNode(state: ProxyUiState, nodes: List<NodeView>): ProxyUiState {
        val selectedId = state.session.subscription?.selectedNodeId.orEmpty()
        val name = nodes.firstOrNull { it.selected }?.name
            ?: nodes.firstOrNull { it.id == selectedId }?.name
            ?: ""
        return state.copy(nodes = nodes, activeNode = name)
    }

    // -----------------------------------------------------------------------
    // 内部：错误呈现
    // -----------------------------------------------------------------------

    /**
     * 呈现服务端错误。
     *
     * 文案优先用 messageKey 查表；查不到时**显示键名本身**（LocaleCatalog 的
     * 约定），而不是显示英文诊断文本——键名能直接指向缺失的翻译，
     * 而诊断文本看起来像正常文案，会被当成「界面就是这样」。
     */
    private fun report(error: IpcRpcError) {
        val shown = if (error.messageKey.isNotEmpty()) {
            messages[error.messageKey]
        } else {
            error.describe()
        }
        update { it.copy(message = shown, notice = "", detail = error.describe()) }
    }

    private fun reportLocal(key: String, vararg args: Any) {
        update { it.copy(message = messages[key, *args], notice = "", detail = "") }
    }

    private fun fail(messageKey: String, detail: String, vararg args: Any) {
        update {
            it.copy(
                backend = BackendState.Unavailable,
                message = messages[messageKey, *args],
                notice = "",
                detail = detail,
            )
        }
    }
}
