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
import dev.proxyclient.ui.ConnectionState
import dev.proxyclient.ui.ProxyApp
import dev.proxyclient.ui.ProxyClientTheme
import dev.proxyclient.ui.ProxyUiState
import dev.proxyclient.vpn.ProxyVpnService

/**
 * 主活动 / main activity.
 *
 * 职责：
 *  - 加载出厂配置与语言资源；
 *  - 持有界面状态（两种模式共用一份）；
 *  - 处理 VPN 授权流程，并启动/停止 [ProxyVpnService]。
 *
 * 界面状态放在这里而不是各屏幕里：两种模式必须看到同一份状态，
 * 否则很快就会出现「简单模式显示已连接、专业模式显示未连接」。
 */
class MainActivity : ComponentActivity() {

    private lateinit var preferences: SharedPreferences
    private lateinit var catalog: LocaleCatalog

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
            updateState {
                it.copy(
                    connection = ConnectionState.Disconnected,
                    message = catalog.translate(
                        catalog.negotiate(config.defaultLanguage),
                        "ui.detail.vpn_permission_denied",
                    ),
                )
            }
        }
    }

    private val config: AppConfig by lazy { AppConfig.load(this) }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        preferences = getSharedPreferences(PREFERENCES_NAME, MODE_PRIVATE)
        catalog = LocaleCatalog.load(this, config.fallbackLanguage)

        uiState = ProxyUiState(
            uiMode = restoreUiMode(),
            rememberUiMode = preferences.getBoolean(KEY_REMEMBER_MODE, false),
            core = config.defaultCore,
            routingPolicy = config.defaultRoutingPolicy,
        )

        // 服务通过这个 action 请求授权（服务自身无法弹系统对话框）。
        if (intent?.action == ACTION_REQUEST_VPN_PERMISSION) {
            requestVpnPermission()
        }

        setContent {
            ProxyClientTheme {
                ProxyApp(
                    config = config,
                    catalog = catalog,
                    state = uiState,
                    onToggleConnection = ::toggleConnection,
                    onToggleUiMode = ::toggleUiMode,
                    onToggleRememberMode = ::setRememberUiMode,
                    onOpenPanel = ::openPanel,
                )
            }
        }
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
        updateState { it.copy(connection = ConnectionState.Connecting, message = "") }

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
        updateState { it.copy(connection = ConnectionState.Connected, message = "") }
    }

    private fun stopVpnService() {
        updateState { it.copy(connection = ConnectionState.Disconnecting) }
        val intent = Intent(this, ProxyVpnService::class.java).apply {
            action = ProxyVpnService.ACTION_STOP
        }
        startService(intent)
        updateState { it.copy(connection = ConnectionState.Disconnected, message = "") }
    }

    // -------------------------------------------------------------------------
    // 偏好
    // -------------------------------------------------------------------------

    private fun toggleUiMode() {
        val next = if (uiState.uiMode == UiMode.Simple) UiMode.Professional else UiMode.Simple
        updateState { it.copy(uiMode = next) }
        // 只有勾选了「记住模式」才写盘。
        // 否则用户勾选前的临时切换会被持久化，下次启动看到的模式
        // 和他以为的不一样。
        if (uiState.rememberUiMode) {
            preferences.edit().putString(KEY_UI_MODE, next.toPersisted()).apply()
        }
    }

    private fun setRememberUiMode(remember: Boolean) {
        updateState { it.copy(rememberUiMode = remember) }
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

    // -------------------------------------------------------------------------

    private fun openPanel() {
        if (config.webBaseUrl.isBlank()) {
            return
        }
        runCatching {
            startActivity(Intent(Intent.ACTION_VIEW, android.net.Uri.parse(config.webBaseUrl)))
        }
    }

    private inline fun updateState(transform: (ProxyUiState) -> ProxyUiState) {
        uiState = transform(uiState)
    }

    companion object {
        const val ACTION_REQUEST_VPN_PERMISSION = "dev.proxyclient.action.REQUEST_VPN_PERMISSION"

        private const val PREFERENCES_NAME = "proxyclient_preferences"
        private const val KEY_UI_MODE = "ui_mode"
        private const val KEY_REMEMBER_MODE = "remember_ui_mode"
    }
}
