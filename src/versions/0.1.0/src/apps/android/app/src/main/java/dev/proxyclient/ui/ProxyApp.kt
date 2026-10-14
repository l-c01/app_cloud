package dev.proxyclient.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.Divider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import dev.proxyclient.config.AppConfig
import dev.proxyclient.config.UiMode
import dev.proxyclient.i18n.LocaleCatalog

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
 * 界面状态 / UI state.
 *
 * 两种模式共用同一份状态（设计基线 §4.1）。若各持一份，
 * 很快就会出现「简单模式显示已连接、专业模式显示未连接」。
 */
data class ProxyUiState(
    val connection: ConnectionState = ConnectionState.Disconnected,
    val uiMode: UiMode = UiMode.Simple,
    val rememberUiMode: Boolean = false,
    val core: String = "sing-box",
    val routingPolicy: String = "rule",
    val activeNode: String = "",
    val message: String = "",
    /** 应用范围：null 表示全部应用，否则为排除列表。 */
    val excludedPackages: Set<String> = emptySet(),
)

/**
 * 主界面 / main screen.
 *
 * 简单模式刻意**不显示**核心选择、路由策略与应用范围。
 * 这不是隐藏次要功能，而是会让人做错决定的选项：
 * 不懂分流规则的用户选了「全局直连」，表现就是「连上了但打不开网页」，
 * 而他不会知道原因。
 */
@Composable
fun ProxyApp(
    config: AppConfig,
    catalog: LocaleCatalog,
    state: ProxyUiState,
    onToggleConnection: () -> Unit,
    onToggleUiMode: () -> Unit,
    onToggleRememberMode: (Boolean) -> Unit,
    onOpenPanel: () -> Unit,
) {
    val language = catalog.negotiate(config.defaultLanguage)
    fun t(key: String, vararg args: Any) = catalog.translate(language, key, *args)

    Column(
        modifier = Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(24.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        // ---- 状态 ----
        Text(
            text = t(state.connection.messageKey()),
            style = MaterialTheme.typography.headlineMedium,
            fontWeight = FontWeight.Bold,
        )

        // 出错或需要介入时把原因说出来，而不是只显示「失败」。
        if (state.message.isNotBlank()) {
            Text(
                text = state.message,
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.error,
            )
        }

        Text(
            text = if (state.activeNode.isBlank()) {
                t("ui.label.no_node")
            } else {
                t("ui.label.current_node", state.activeNode)
            },
            style = MaterialTheme.typography.bodyMedium,
        )

        Spacer(Modifier.height(8.dp))

        // ---- 主操作 ----
        Button(
            onClick = onToggleConnection,
            enabled = state.connection != ConnectionState.Connecting &&
                state.connection != ConnectionState.Disconnecting,
            modifier = Modifier.fillMaxWidth(),
        ) {
            Text(
                text = if (state.connection == ConnectionState.Connected) {
                    t("ui.action.disconnect")
                } else {
                    t("ui.action.connect")
                },
                style = MaterialTheme.typography.titleMedium,
            )
        }

        OutlinedButton(
            onClick = onOpenPanel,
            enabled = config.webBaseUrl.isNotBlank(),
            modifier = Modifier.fillMaxWidth(),
        ) {
            Text(t("ui.action.open_panel"))
        }

        Spacer(Modifier.height(16.dp))
        Divider()
        Spacer(Modifier.height(16.dp))

        // ---- 模式切换 ----
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = t(
                    if (state.uiMode == UiMode.Simple) "ui.mode.simple" else "ui.mode.professional",
                ),
                style = MaterialTheme.typography.titleMedium,
            )
            Switch(checked = state.uiMode == UiMode.Professional, onCheckedChange = { onToggleUiMode() })
        }

        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(t("ui.menu.remember_mode"), style = MaterialTheme.typography.bodyMedium)
            Switch(checked = state.rememberUiMode, onCheckedChange = onToggleRememberMode)
        }

        // ---- 专业模式才展开的部分 ----
        if (state.uiMode == UiMode.Professional) {
            Spacer(Modifier.height(8.dp))
            Card(modifier = Modifier.fillMaxWidth()) {
                Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    LabelledValue(t("ui.label.core"), state.core)
                    LabelledValue(t("ui.label.routing_policy"), t("ui.policy.${state.routingPolicy}"))
                    LabelledValue(
                        t("ui.label.network_mode"),
                        t("ui.entry.tun"),
                    )
                }
            }

            // 能力未实测时如实提示：假装某些选项可用比不显示更糟。
            Text(
                text = t("ui.warn.unverified_capability", state.core),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.secondary,
            )
        }

        // ---- 配置问题 ----
        // 出厂配置有问题时提示用户，但不阻止使用：
        // 用户改不了安装包里的配置，说了也未必能解决，但至少不是被蒙在鼓里。
        if (config.warnings.isNotEmpty()) {
            Spacer(Modifier.height(8.dp))
            Text(
                text = config.warnings.joinToString("\n"),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
        }
    }
}

@Composable
private fun LabelledValue(label: String, value: String) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.SpaceBetween,
    ) {
        Text(label, style = MaterialTheme.typography.bodyMedium)
        Text(value, style = MaterialTheme.typography.bodyMedium, fontWeight = FontWeight.Medium)
    }
}
