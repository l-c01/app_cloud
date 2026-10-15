package dev.proxyclient.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import dev.proxyclient.ipc.NodeView

/**
 * 节点列表 / the node list.
 *
 * 点按即切换（设计基线 §1：节点「点击即切换」）。没有单独的「确认」按钮：
 * 选错节点的代价只是再点一次，而多一步确认会让每次切换都变慢。
 *
 * **用 Column 而不是 LazyColumn**：外层已经是一个 verticalScroll 的 Column，
 * 在它里面再放一个竖向可滚动组件会得到「无限高约束」并在测量阶段抛异常。
 * 节点很多时这样的列表不是最优，要改成 LazyColumn 必须先重构外层的滚动结构
 * （把外层换成 LazyColumn，各分组作为 item）——那是另一件事，见 README。
 */
@Composable
fun NodeListPanel(
    state: ProxyUiState,
    messages: Messages,
    onSelectNode: (String) -> Unit,
    onRefreshSubscription: () -> Unit,
) {
    val busy = state.busy.isNotEmpty()
    val ready = state.backend == BackendState.Ready

    SectionCard(messages["ui.label.node_list"]) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = if (state.activeNode.isBlank()) {
                    messages["ui.label.no_node"]
                } else {
                    messages["ui.label.current_node", state.activeNode]
                },
                style = MaterialTheme.typography.bodyMedium,
                fontWeight = FontWeight.Medium,
            )
            OutlinedButton(
                onClick = onRefreshSubscription,
                enabled = ready && !busy && state.session.loggedIn,
            ) {
                Text(messages["ui.action.refresh"])
            }
        }

        if (state.nodes.isEmpty()) {
            NoteText(messages["ui.detail.no_nodes"])
        } else {
            NoteText(messages["ui.detail.tap_to_select"])
            for (node in state.nodes) {
                NodeRow(
                    node = node,
                    enabled = ready && !busy,
                    onSelect = { onSelectNode(node.id) },
                )
            }
        }
    }
}

@Composable
private fun NodeRow(
    node: NodeView,
    enabled: Boolean,
    onSelect: () -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            // 整行可点：只让单选钮可点会让「点节点名」变成没反应。
            .clickable(enabled = enabled) { onSelect() }
            .padding(vertical = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        RadioButton(selected = node.selected, onClick = { onSelect() }, enabled = enabled)
        Column(modifier = Modifier.fillMaxWidth()) {
            Text(node.name, style = MaterialTheme.typography.bodyMedium)
            // 协议与服务器地址用于分辨同名节点，不含任何凭据。
            NoteText("${node.protocol} · ${node.server}:${node.port}")
        }
    }
}
