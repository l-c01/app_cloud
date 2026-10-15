package dev.proxyclient.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp

/**
 * 登录页 / the sign-in screen.
 *
 * 只在**未登录**时显示。输入框的内容放在这个 Composable 的本地状态里，
 * 不进 [ProxyUiState]：密码一旦进了共享状态，它就会出现在
 * 「把状态打进日志」这种最自然的排查动作里。
 *
 * 同样刻意**不用 rememberSaveable**：可保存状态会进系统给的
 * saved instance state Bundle，而那个 Bundle 在进程被回收时可能被写到磁盘。
 * 旋转屏幕后要重新输入密码，比密码落盘可接受得多。
 */
@Composable
fun LoginScreen(
    state: ProxyUiState,
    messages: Messages,
    /** 出厂配置里的面板地址：用户没填过面板地址时作为初值。 */
    defaultPanelUrl: String,
    /** 面板网页地址是否已配置。为假时「打开面板网页」不可用。 */
    openPanelEnabled: Boolean,
    onSignIn: (
        panelUrl: String,
        email: String,
        password: String,
        rememberPassword: Boolean,
        allowInsecureHttp: Boolean,
    ) -> Unit,
    onOpenPanel: () -> Unit,
) {
    // 服务端已有面板地址时以它为准；否则用出厂配置里的那个。
    // remember 的键取 initialPanelUrl 而不是常量：登录成功后服务端会带回
    // 真正的地址，那时这个输入框应当跟着更新。
    val initialPanelUrl = state.session.panelUrl.ifBlank { defaultPanelUrl }
    var panelUrl by remember(initialPanelUrl) { mutableStateOf(initialPanelUrl) }
    var email by remember(state.session.account?.email) {
        mutableStateOf(state.session.account?.email.orEmpty())
    }
    var password by remember { mutableStateOf("") }
    // 「记住密码」默认关（契约也如此）：默认保存密码意味着默认把凭据写盘，
    // 而大多数用户不会去读这个复选框。
    var rememberPassword by remember { mutableStateOf(false) }
    var allowInsecureHttp by remember { mutableStateOf(false) }

    val busy = state.busy.isNotEmpty()
    val ready = state.backend == BackendState.Ready
    val panelUrlMissing = panelUrl.isBlank() && state.session.panelUrl.isBlank()
    val httpLikely = allowInsecureHttp || panelUrl.trim().startsWith("http://")

    Column(
        modifier = Modifier.fillMaxWidth(),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        SectionCard(messages["ui.login.title"]) {
            NoteText(messages["ui.login.hint"])
            TextInputRow(
                label = messages["ui.login.panel_url"],
                value = panelUrl,
                enabled = !busy,
                onValueChange = { panelUrl = it },
            )
            TextInputRow(
                label = messages["ui.login.email"],
                value = email,
                enabled = !busy,
                onValueChange = { email = it },
            )
            TextInputRow(
                label = messages["ui.login.password"],
                value = password,
                enabled = !busy,
                isPassword = true,
                onValueChange = { password = it },
            )

            CheckRow(
                label = messages["ui.login.remember"],
                checked = rememberPassword,
                enabled = !busy,
                onCheckedChange = { rememberPassword = it },
            )
            CheckRow(
                label = messages["ui.login.allow_insecure"],
                checked = allowInsecureHttp,
                enabled = !busy,
                onCheckedChange = { allowInsecureHttp = it },
            )
            if (httpLikely) {
                // http 面板地址意味着账号与订阅在网络上明文可见。允许它是为了
                // 本地联调，因此这句话必须就贴在开关下面，而不是写在文档里。
                WarningText(messages["ui.warn.http_panel"])
            }

            Button(
                onClick = {
                    onSignIn(
                        panelUrl.trim(),
                        email.trim(),
                        password,
                        rememberPassword,
                        allowInsecureHttp,
                    )
                },
                enabled = ready && !busy && !panelUrlMissing &&
                    email.isNotBlank() && password.isNotBlank(),
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text(messages["ui.login.sign_in"])
            }
        }

        OutlinedButton(
            onClick = onOpenPanel,
            enabled = openPanelEnabled && !busy,
            modifier = Modifier.fillMaxWidth(),
        ) {
            Text(messages["ui.action.open_panel"])
        }

        if (!ready) {
            // 服务不可用时把原因说清楚：此时登录按钮不可能成功，
            // 而一个「点了没反应」的按钮会让人以为是密码错了。
            Text(
                text = messages["ui.detail.service_unavailable"],
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
        }

        Spacer(Modifier.height(4.dp))
    }
}
