package dev.proxyclient.ui

import dev.proxyclient.config.UiMode
import dev.proxyclient.ipc.NodeView
import dev.proxyclient.ipc.SessionState
import dev.proxyclient.options.ClientOptions

/**
 * 连接状态 / connection state.
 *
 * 刻意与桌面端 coordinator 的状态名保持一致：同一个人在两个平台上
 * 看到的词应当是同一套，否则「已连接」和「connected」会被当成两回事。
 */
enum class ConnectionState {
    Disconnected,
    Connecting,
    Connected,
    Disconnecting,
    Failed;

    /** 对应的文案键。未知状态由调用方原样显示状态码，不在这里猜。 */
    fun messageKey(): String = when (this) {
        Disconnected -> "ui.state.disconnected"
        Connecting -> "ui.state.connecting"
        Connected -> "ui.state.connected"
        Disconnecting -> "ui.state.disconnecting"
        Failed -> "ui.state.failed"
    }
}

/**
 * 与后台服务的连接状态 / backend availability.
 *
 * 与 [ConnectionState] 是两件事，必须分开：前者是「隧道通不通」，
 * 后者是「界面能不能控制服务」。合成一个状态的表现是——服务没启动时
 * 界面显示「未连接」，用户于是去点连接，而连接按钮根本不可能成功，
 * 因为没有任何东西可以连。
 */
enum class BackendState {
    Connecting,
    Ready,
    Unavailable,
}

/**
 * 界面状态 / UI state.
 *
 * 两种模式共用同一份状态（设计基线 §4.1）。若各持一份，
 * 很快就会出现「简单模式显示已连接、专业模式显示未连接」。
 *
 * [options] 是唯一的配置来源：简易模式与专业模式改的都是它，
 * 只是各自暴露的字段不同。任何「简易模式专用字段」都会破坏这条性质。
 */
data class ProxyUiState(
    val connection: ConnectionState = ConnectionState.Disconnected,
    val uiMode: UiMode = UiMode.Simple,
    val rememberUiMode: Boolean = false,
    val core: String = "sing-box",

    /** 与后台服务的连接状态。 */
    val backend: BackendState = BackendState.Connecting,
    /** 正在进行的操作名；空串表示空闲。 */
    val busy: String = "",

    val session: SessionState = SessionState(),
    val nodes: List<NodeView> = emptyList(),
    /** 当前节点名；空串表示未选中（或选中的节点已不在订阅里）。 */
    val activeNode: String = "",
    /**
     * 选项目前**总是**本地与服务端共用的那一份。
     *
     * 未连上服务端时它是 [ClientOptions.defaults]（与服务端在无会话时
     * 返回的是同一套值），因此界面永远有东西可画，而不是一片空白。
     */
    val options: ClientOptions = ClientOptions.defaults(),

    /** 需要用户看到的错误，已本地化。 */
    val message: String = "",
    /** 成功类提示，已本地化。与 [message] 分开：颜色与位置都不同。 */
    val notice: String = "",
    /** 诊断细节（原文，可能来自异常或服务端）。只作为小字补充，不含凭据。 */
    val detail: String = "",
)
