package dev.proxyclient

import android.app.Activity
import android.content.Intent
import android.content.SharedPreferences
import android.net.VpnService
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import dev.proxyclient.config.AppConfig
import dev.proxyclient.config.UiMode
import dev.proxyclient.i18n.LocaleCatalog
import dev.proxyclient.options.ClientOptions
import dev.proxyclient.options.RouteMode
import dev.proxyclient.ui.ConnectionState
import dev.proxyclient.ui.Messages
import dev.proxyclient.ui.ProxyApp
import dev.proxyclient.ui.ProxyClientTheme
import dev.proxyclient.ui.ProxyController
import dev.proxyclient.ui.ProxyUiState
import dev.proxyclient.vpn.ProxyVpnService

/**
 * 主活动 / main activity.
 *
 * 职责：
 *  - 加载出厂配置与语言资源；
 *  - 持有界面状态（两种模式共用一份）并创建唯一的 [ProxyController]；
 *  - 处理 VPN 授权流程，并启动/停止 [ProxyVpnService]。
 *
 * 界面状态放在这里而不是各屏幕里：两种模式必须看到同一份状态，
 * 否则很快就会出现「简单模式显示已连接、专业模式显示未连接」。
 *
 * 状态**只有一个写入者**：后台线程改状态一律经由 ProxyController，
 * Activity 只改「纯界面偏好」那一部分，而且也要经过它——理由见
 * [ProxyController.updateLocal]。
 */
class MainActivity : ComponentActivity() {

    private lateinit var preferences: SharedPreferences
    private lateinit var catalog: LocaleCatalog
    private lateinit var messages: Messages

    private var controller: ProxyController? = null

    private var uiState by mutableStateOf(ProxyUiState())

    /**
     * VPN 授权结果。
     *
     * 作为属性初始化而不是在 onCreate 里注册：registerForActivityResult
     * 必须在 Activity 进入 STARTED 之前调用，放在 onCreate 之后注册会抛异常。
     */
    private val vpnPermissionLauncher = registerForActivityResult(
        ActivityResultContracts.StartActivityForResult(),
    ) { result ->
        if (result.resultCode == Activity.RESULT_OK) {
            startVpnService()
        } else {
            // 用户拒绝授权。必须如实回到「未连接」，
            // 而不是停在「正在连接」让用户一直等。
            applyLocal {
                it.copy(
                    connection = ConnectionState.Disconnected,
                    message = messages["ui.detail.vpn_permission_denied"],
                )
            }
        }
    }

    private val config: AppConfig by lazy { AppConfig.load(this) }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        preferences = getSharedPreferences(PREFERENCES_NAME, MODE_PRIVATE)
        catalog = LocaleCatalog.load(this, config.fallbackLanguage)
        val language = catalog.negotiate(config.defaultLanguage)
        messages = Messages(catalog, language)

        uiState = ProxyUiState(
            uiMode = restoreUiMode(),
            rememberUiMode = preferences.getBoolean(KEY_REMEMBER_MODE, false),
            core = config.defaultCore,
            options = initialOptions(),
        )

        // 用 applicationContext 而不是 this：控制器活到界面销毁之后，
        // 持有 Activity 会让它无法被回收。
        val created = ProxyController(
            context = applicationContext,
            messages = messages,
            initial = uiState,
            onState = { next -> uiState = next },
        )
        controller = created
        // 打开界面即尝试连上服务并取一份完整状态。连不上时会明确提示，
        // 而不是让界面停在一片空白上。
        created.start()

        // 服务通过这个 action 请求授权（服务自身无法弹系统对话框）。
        if (intent?.action == ACTION_REQUEST_VPN_PERMISSION) {
            requestVpnPermission()
        }

        setContent {
            ProxyClientTheme {
                ProxyApp(
                    config = config,
                    messages = messages,
                    state = uiState,
                    onToggleConnection = ::toggleConnection,
                    onToggleUiMode = ::toggleUiMode,
                    onToggleRememberMode = ::setRememberUiMode,
                    onOpenPanel = ::openPanel,
                    onSignIn = ::signIn,
                    onSignOut = { controller?.logout() },
                    onRefreshSubscription = { controller?.refreshSubscription() },
                    onSelectNode = { nodeId -> controller?.selectNode(nodeId) },
                    onReloadOptions = { controller?.reloadOptions() },
                    onSaveOptions = { options -> controller?.saveOptions(options) },
                    onRouteMode = { mode -> controller?.setRouteMode(mode) },
                    onPerAppMode = { mode -> controller?.setPerAppMode(mode) },
                )
            }
        }
    }

    override fun onDestroy() {
        controller?.stop()
        controller = null
        super.onDestroy()
    }

    // -------------------------------------------------------------------------
    // 后端操作
    // -------------------------------------------------------------------------

    private fun signIn(
        panelUrl: String,
        email: String,
        password: String,
        rememberPassword: Boolean,
        allowInsecureHttp: Boolean,
    ) {
        controller?.login(panelUrl, email, password, rememberPassword, allowInsecureHttp)
    }

    // -------------------------------------------------------------------------
    // 连接
    // -------------------------------------------------------------------------

    private fun toggleConnection() {
        when (uiState.connection) {
            ConnectionState.Connected, ConnectionState.Connecting -> stopVpnService()
            else -> requestVpnPermission()
        }
    }

    private fun requestVpnPermission() {
        applyLocal { it.copy(connection = ConnectionState.Connecting, message = "") }

        // prepare() 返回非 null 表示尚未授权，需要走系统流程。
        // 返回 null 表示已授权，可以直接启动。
        val consent = VpnService.prepare(this)
        if (consent == null) {
            startVpnService()
        } else {
            vpnPermissionLauncher.launch(consent)
        }
    }

    private fun startVpnService() {
        val intent = Intent(this, ProxyVpnService::class.java).apply {
            action = ProxyVpnService.ACTION_START
        }
        startForegroundService(intent)
        applyLocal { it.copy(connection = ConnectionState.Connected, message = "") }
    }

    private fun stopVpnService() {
        applyLocal { it.copy(connection = ConnectionState.Disconnecting) }
        val intent = Intent(this, ProxyVpnService::class.java).apply {
            action = ProxyVpnService.ACTION_STOP
        }
        startService(intent)
        applyLocal { it.copy(connection = ConnectionState.Disconnected, message = "") }
    }

    // -------------------------------------------------------------------------
    // 偏好
    // -------------------------------------------------------------------------

    private fun toggleUiMode() {
        val next = if (uiState.uiMode == UiMode.Simple) UiMode.Professional else UiMode.Simple
        applyLocal { it.copy(uiMode = next) }
        // 只有勾选了「记住模式」才写盘。
        // 否则用户勾选前的临时切换会被持久化，下次启动看到的模式
        // 和他以为的不一样。
        if (uiState.rememberUiMode) {
            preferences.edit().putString(KEY_UI_MODE, next.toPersisted()).apply()
        }
    }

    private fun setRememberUiMode(remember: Boolean) {
        applyLocal { it.copy(rememberUiMode = remember) }
        preferences.edit().apply {
            putBoolean(KEY_REMEMBER_MODE, remember)
            if (remember) {
                putString(KEY_UI_MODE, uiState.uiMode.toPersisted())
            } else {
                // 取消记住时清掉已保存的值，避免下次开启时恢复到一个
                // 用户早已忘记的旧选择。
                remove(KEY_UI_MODE)
            }
        }.apply()
    }

    private fun restoreUiMode(): UiMode {
        val remember = preferences.getBoolean(KEY_REMEMBER_MODE, false)
        if (!remember) {
            return config.defaultUiMode
        }
        val saved = preferences.getString(KEY_UI_MODE, null)
        return saved?.let { UiMode.fromString(it) } ?: config.defaultUiMode
    }

    /**
     * 还没连上服务端时的选项初值。
     *
     * 用 [ClientOptions.defaults]（与服务端在无会话时返回的是同一套值），
     * 并把出厂配置里的 routingPolicy 套上去。**连上之后一律以服务端返回的
     * 选项为准**：否则简单模式与专业模式会对「当前路由模式」给出两个答案，
     * 而用户不知道该信哪个。
     */
    private fun initialOptions(): ClientOptions {
        val defaults = ClientOptions.defaults()
        val policy = config.defaultRoutingPolicy
        return if (RouteMode.choices.contains(policy)) defaults.withRouteMode(policy) else defaults
    }

    /** 本地偏好也走控制器的状态通道，理由见 [ProxyController.updateLocal]。 */
    private fun applyLocal(transform: (ProxyUiState) -> ProxyUiState) {
        val active = controller
        if (active == null) {
            uiState = transform(uiState)
        } else {
            active.updateLocal(transform)
        }
    }

    // -------------------------------------------------------------------------

    private fun openPanel() {
        if (config.webBaseUrl.isBlank()) {
            return
        }
        runCatching {
            startActivity(Intent(Intent.ACTION_VIEW, android.net.Uri.parse(config.webBaseUrl)))
        }
    }

    companion object {
        const val ACTION_REQUEST_VPN_PERMISSION = "dev.proxyclient.action.REQUEST_VPN_PERMISSION"

        private const val PREFERENCES_NAME = "proxyclient_preferences"
        private const val KEY_UI_MODE = "ui_mode"
        private const val KEY_REMEMBER_MODE = "remember_ui_mode"
    }
}
