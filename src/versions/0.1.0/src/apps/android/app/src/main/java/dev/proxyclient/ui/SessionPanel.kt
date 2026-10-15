package dev.proxyclient.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

/**
 * 账户与订阅 / account and subscription.
 *
 * 界面**拿不到**凭据（服务端刻意不返回），因此这里能显示的只有摘要。
 * 用量按「已用 / 剩余」两个数显示：只显示已用会让用户无法判断还剩多少，
 * 只显示剩余则无法看出用得有多快。
 *
 * 文案用 `ui.session.*` 那一组**整句式**的键（「账号：{0}」），而不是
 * 标签加数值两段：这一屏是只读摘要，整句话比对齐的列更好读，也省掉了
 * 两处需要同步的空格与冒号。
 */
@Composable
fun SessionPanel(
    state: ProxyUiState,
    messages: Messages,
    onSignOut: () -> Unit,
) {
    val session = state.session
    val account = session.account
    val subscription = session.subscription
    val busy = state.busy.isNotEmpty()

    Column(
        modifier = Modifier.fillMaxWidth(),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        SectionCard(messages["ui.label.account"]) {
            if (account == null) {
                // 登录成功但面板没给账户信息：如实说「不知道」，不要显示一片空白。
                NoteText(messages["ui.detail.account_unknown"])
            } else {
                if (account.email.isNotBlank()) {
                    SessionLine(messages["ui.session.email", account.email])
                }
                val plan = account.planName.ifBlank { subscription?.planName.orEmpty() }
                SessionLine(
                    messages["ui.session.plan", plan.ifBlank { messages["ui.value.unknown"] }],
                )

                val usage = subscription?.usage
                if (usage != null && usage.present) {
                    SessionLine(
                        messages[
                            "ui.label.usage_summary",
                            formatBytes(usage.used()),
                            formatBytes(usage.remaining),
                        ],
                    )
                    if (usage.expireUnix > 0) {
                        SessionLine(messages["ui.session.expires", formatUnixTime(usage.expireUnix)])
                    }
                } else {
                    // present 为 false 时**不显示 0**：「用量 0」与「不知道用量」
                    // 是两件事，前者会让用户以为流量没被统计。
                    SessionLine(messages["ui.label.usage_unknown"])
                }

                if (subscription != null) {
                    SessionLine(messages["ui.session.nodes", subscription.nodeCount])
                    if (subscription.rejectedCount > 0) {
                        SessionLine(messages["ui.session.rejected_nodes", subscription.rejectedCount])
                    }
                    SessionLine(
                        messages[
                            "ui.label.last_sync",
                            if (subscription.lastSyncUnix > 0) {
                                formatUnixTime(subscription.lastSyncUnix)
                            } else {
                                messages["ui.value.unknown"]
                            },
                        ],
                    )
                }

                if (account.banned) WarningText(messages["ui.session.banned"])
                if (account.expired) WarningText(messages["ui.session.expired"])

                // 服务端的 warnings 可能是文案键，也可能是诊断文本。
                // messages.server 两种都能处理（查不到就原样返回）。
                for (warning in account.warnings) WarningText(messages.server(warning))
                for (warning in subscription?.warnings.orEmpty()) {
                    WarningText(messages.server(warning))
                }
            }

            if (session.loggedIn && !session.secureStorage) {
                // 只有在**存在会话**时才判断这一条：服务端仅在那种情况下回填
                // secureStorage，未登录时看到的 false 是缺省值而不是结论。
                WarningText(messages["ui.warn.no_secure_storage"])
            }
        }

        Button(
            onClick = onSignOut,
            enabled = session.loggedIn && !busy && state.backend == BackendState.Ready,
            modifier = Modifier.fillMaxWidth(),
        ) {
            Text(messages["ui.action.sign_out"])
        }
    }
}

/** 摘要里的一行。用正文而不是小字：这些是用户要读的数字。 */
@Composable
private fun SessionLine(text: String) {
    Text(text = text, style = MaterialTheme.typography.bodyMedium)
}

/**
 * 人类可读的字节数。
 *
 * 用 1024 进制并保留一位小数：面板给的额度（如 100 GB）通常也是 1024 进制，
 * 用 1000 进制会显示成「93.1 GB」，看起来像是额度少了。
 *
 * 0 **必须如实显示成 0 B**：它是「额度用尽」这个真实状态，
 * 把它显示成「—」会把「没流量了」变成「不知道有多少流量」，
 * 而这两件事该做的事完全不同。只有负值按未知处理。
 */
fun formatBytes(value: Long): String {
    if (value < 0) return "—"
    if (value == 0L) return "0 B"
    val units = arrayOf("B", "KiB", "MiB", "GiB", "TiB", "PiB")
    var size = value.toDouble()
    var unit = 0
    while (size >= 1024.0 && unit < units.size - 1) {
        size /= 1024.0
        unit++
    }
    return if (unit == 0) {
        "${value} ${units[unit]}"
    } else {
        String.format(Locale.US, "%.1f %s", size, units[unit])
    }
}

/** 按本地时区格式化秒级时间戳。 */
fun formatUnixTime(unixSeconds: Long): String {
    val formatter = SimpleDateFormat("yyyy-MM-dd HH:mm", Locale.getDefault())
    return formatter.format(Date(unixSeconds * 1000L))
}
