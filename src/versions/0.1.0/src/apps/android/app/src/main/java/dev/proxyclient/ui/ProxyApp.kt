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
import androidx.compose.material3.Divider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import dev.proxyclient.config.AppConfig
import dev.proxyclient.config.UiMode
import dev.proxyclient.options.ClientOptions

/**
 * 主界面 / main screen.
 *
 * 两种模式共用同一份状态与同一份选项（设计基线 §4.1）：
 * 切换模式只改变呈现与可见选项，不改变已经建立的行为。
 *
 * 本文件只做**组装**：每一项功能都在自己的文件里
 * （登录、节点列表、简易模式、专业模式），这样「哪个屏幕改了什么」
 * 不需要在一个上千行的文件里找。
 *
 * 滚动结构：这里是最外层的 verticalScroll Column，因此**任何子屏幕都不能
 * 再放一个竖向可滚动组件**（LazyColumn 等）——嵌套会在测量阶段拿到无限高
 * 约束并抛异常。节点列表因此是普通 Column，理由写在 NodeListPanel 里。
 */
@Composable
fun ProxyApp(
    config: AppConfig,
    messages: Messages,
    state: ProxyUiState,
    onToggleConnection: () -> Unit,
    onToggleUiMode: () -> Unit,
    onToggleRememberMode: (Boolean) -> Unit,
    onOpenPanel: () -> Unit,
    onSignIn: (
        panelUrl: String,
        email: String,
        password: String,
        rememberPassword: Boolean,
        allowInsecureHttp: Boolean,
    ) -> Unit,
    onSignOut: () -> Unit,
    onRefreshSubscription: () -> Unit,
    onSelectNode: (String) -> Unit,
    onReloadOptions: () -> Unit,
    onSaveOptions: (ClientOptions) -> Unit,
    onRouteMode: (String) -> Unit,
    onPerAppMode: (String) -> Unit,
) {
    val busy = state.busy.isNotEmpty()

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
            text = messages[state.connection.messageKey()],
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
        if (state.notice.isNotBlank()) {
            Text(
                text = state.notice,
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.secondary,
            )
        }
        // 诊断细节只在它比上面那句话更有信息量时显示。
        // 服务端错误与网络异常都能在这里看到原文，便于定位问题。
        if (state.detail.isNotBlank() && state.detail != state.message) {
            NoteText(state.detail)
        }
        if (busy) {
            NoteText(messages["ui.detail.busy"])
        }
        if (state.backend == BackendState.Ready) {
            NoteText(messages["ui.detail.service_ok"])
        }

        Spacer(Modifier.height(4.dp))

        if (!state.session.loggedIn) {
            // 未登录：登录页就是主界面。这里不显示连接按钮——
            // 没有订阅就没有东西可以连，而一个点了没反应的按钮比隐藏它更糟。
            LoginScreen(
                state = state,
                messages = messages,
                defaultPanelUrl = config.apiBaseUrl,
                openPanelEnabled = config.webBaseUrl.isNotBlank(),
                onSignIn = onSignIn,
                onOpenPanel = onOpenPanel,
            )
        } else {
            SessionPanel(state = state, messages = messages, onSignOut = onSignOut)

            NodeListPanel(
                state = state,
                messages = messages,
                onSelectNode = onSelectNode,
                onRefreshSubscription = onRefreshSubscription,
            )

            // ---- 主操作 ----
            Button(
                onClick = onToggleConnection,
                enabled = state.connection != ConnectionState.Connecting &&
                    state.connection != ConnectionState.Disconnecting &&
                    !busy,
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text(
                    text = if (state.connection == ConnectionState.Connected) {
                        messages["ui.action.disconnect"]
                    } else {
                        messages["ui.action.connect"]
                    },
                    style = MaterialTheme.typography.titleMedium,
                )
            }
        }

        Spacer(Modifier.height(8.dp))
        Divider()
        Spacer(Modifier.height(8.dp))

        // ---- 模式切换 ----
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = messages[
                    if (state.uiMode == UiMode.Simple) "ui.mode.simple" else "ui.mode.professional",
                ],
                style = MaterialTheme.typography.titleMedium,
            )
            Switch(
                checked = state.uiMode == UiMode.Professional,
                onCheckedChange = { onToggleUiMode() },
            )
        }

        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(messages["ui.menu.remember_mode"], style = MaterialTheme.typography.bodyMedium)
            Switch(checked = state.rememberUiMode, onCheckedChange = onToggleRememberMode)
        }

        // ---- 简易模式：路由模式 + 分应用代理 ----
        if (state.session.loggedIn && state.uiMode == UiMode.Simple) {
            SimpleModePanel(
                state = state,
                messages = messages,
                onRouteMode = onRouteMode,
                onPerAppMode = onPerAppMode,
            )
        }

        // ---- 专业模式：完整选项 ----
        if (state.session.loggedIn && state.uiMode == UiMode.Professional) {
            ProModePanel(
                state = state,
                messages = messages,
                onSave = onSaveOptions,
                onReloadOptions = onReloadOptions,
            )
        }

        // ---- 能力提示 ----
        // 能力未实测时如实提示：假装某些选项可用比不显示更糟。
        // 这里仍然报出内核名，因为「哪张能力表没实测过」是这条提示的一部分。
        Text(
            text = messages["ui.warn.unverified_capability", state.core],
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.secondary,
        )

        // ---- 配置问题 ----
        // 出厂配置有问题时提示用户，但不阻止使用：
        // 用户改不了安装包里的配置，说了也未必能解决，但至少不是被蒙在鼓里。
        if (config.warnings.isNotEmpty()) {
            Text(
                text = config.warnings.joinToString("\n"),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
        }
    }
}
