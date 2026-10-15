package dev.proxyclient.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import dev.proxyclient.options.PerAppMode
import dev.proxyclient.options.RouteMode

/**
 * 简易模式 / simple mode.
 *
 * 只有两项（ui-options.md 第 1 节）：**路由模式决定「什么走代理」，
 * 分应用代理决定「谁走代理」**。其余选项（DNS、TUN 栈、嗅探、规则集）
 * 改错了的后果是「打不开网页」，而用户不会知道自己改了什么，因此不放在这里。
 *
 * 这两项在 Android 上都是真正生效的：VPN 本身就是按应用建立的，
 * 因此分应用不是装饰性选项。
 */
@Composable
fun SimpleModePanel(
    state: ProxyUiState,
    messages: Messages,
    onRouteMode: (String) -> Unit,
    onPerAppMode: (String) -> Unit,
) {
    val options = state.options
    val busy = state.busy.isNotEmpty()
    // 改选项需要服务端有会话：SetOptions 在无会话时会明确拒绝。
    // 与其让用户点一个必然失败的按钮，不如直接禁用并说明原因。
    val editable = state.backend == BackendState.Ready && !busy && state.session.loggedIn

    Column(
        modifier = Modifier.fillMaxWidth(),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        SectionCard(messages["ui.label.routing_policy"]) {
            ChoiceRow(
                label = messages["ui.label.routing_policy"],
                // 三项与 Clash 的 mode 一一对应，文案也复用同一组键。
                choices = labelledChoices(
                    messages,
                    RouteMode.choices,
                    options.routeMode,
                    ::routeModeLabelKey,
                ),
                selected = options.routeMode,
                enabled = editable,
                onSelect = onRouteMode,
            )
            if (options.routeMode == RouteMode.DIRECT) {
                // 选中「直连」必须给提示：它的表现是「连上了但打不开墙外网站」。
                // 那不是故障，是这一项的字面含义，而用户不会这么理解。
                WarningText(messages["ui.warn.direct_mode"])
            }
            if (!state.session.loggedIn) NoteText(messages["session.error.not_authenticated"])
        }

        SectionCard(messages["ui.label.per_app"]) {
            ChoiceRow(
                label = messages["ui.label.per_app"],
                choices = labelledChoices(
                    messages,
                    PerAppMode.choices,
                    options.inbound.perApp.mode,
                    ::perAppModeLabelKey,
                ),
                selected = options.inbound.perApp.mode,
                enabled = editable,
                onSelect = onPerAppMode,
            )
            // Android 的 VPN 是按应用建立的，因此这一项在这里真的有效；
            // 但名单本身还需要一个应用选择器，而那是本版本没有的东西。
            NoteText(messages["ui.detail.per_app_applies"])
            NoteText(messages["ui.detail.per_app_needs_apps"])
            NoteText(messages["ui.detail.per_app_permission"])
            if (options.inbound.perApp.packages.isNotEmpty()) {
                NoteText(
                    messages[
                        "ui.detail.per_app_packages_count",
                        options.inbound.perApp.packages.size,
                    ],
                )
            }
        }
    }
}
