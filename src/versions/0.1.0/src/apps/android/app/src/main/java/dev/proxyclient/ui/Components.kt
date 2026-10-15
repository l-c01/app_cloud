package dev.proxyclient.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Card
import androidx.compose.material3.Checkbox
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp

/**
 * 界面的公共零件 / shared UI pieces.
 *
 * 集中在一处而不是每个屏幕各写一套：各写一套时最先分叉的总是「说明文字
 * 用什么颜色」和「开关放在标签的哪一侧」，而这类分叉没人会去统一。
 */

/** 一个取值 + 它的文案。文案在这里就已经是渲染好的字符串，零件不认识语言表。 */
data class Choice(val value: String, val label: String)

/** 一张分组卡片。标题用 titleMedium：分组标题与字段标签必须能一眼分开。 */
@Composable
fun SectionCard(title: String, content: @Composable ColumnScope.() -> Unit) {
    Card(modifier = Modifier.fillMaxWidth()) {
        Column(
            modifier = Modifier.padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            if (title.isNotEmpty()) {
                Text(
                    text = title,
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.SemiBold,
                )
            }
            content()
        }
    }
}

/** 只读的一行「标签 — 值」。 */
@Composable
fun LabelledValue(label: String, value: String) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.SpaceBetween,
    ) {
        Text(label, style = MaterialTheme.typography.bodyMedium)
        Text(value, style = MaterialTheme.typography.bodyMedium, fontWeight = FontWeight.Medium)
    }
}

/** 开关一行。[note] 用来解释「为什么默认是这样」，不是装饰。 */
@Composable
fun ToggleRow(
    label: String,
    checked: Boolean,
    enabled: Boolean = true,
    note: String = "",
    onCheckedChange: (Boolean) -> Unit,
) {
    Column(modifier = Modifier.fillMaxWidth()) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(label, style = MaterialTheme.typography.bodyMedium)
            Switch(checked = checked, onCheckedChange = onCheckedChange, enabled = enabled)
        }
        if (note.isNotEmpty()) NoteText(note)
    }
}

/**
 * 多选一。用单选按钮而不是下拉框：
 * 下拉框把「有哪几个选项」藏起来了，而这里的每一项都对应一个真实行为
 * （尤其是「全局直连」——它必须被看见）。
 */
@Composable
fun ChoiceRow(
    label: String,
    choices: List<Choice>,
    selected: String,
    enabled: Boolean = true,
    note: String = "",
    onSelect: (String) -> Unit,
) {
    Column(modifier = Modifier.fillMaxWidth()) {
        Text(label, style = MaterialTheme.typography.bodyMedium)
        for (choice in choices) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                RadioButton(
                    selected = selected == choice.value,
                    onClick = { onSelect(choice.value) },
                    enabled = enabled,
                )
                Text(choice.label, style = MaterialTheme.typography.bodyMedium)
            }
        }
        if (note.isNotEmpty()) NoteText(note)
    }
}

/** 勾选一行（与开关区分：勾选用于「这次操作的一个选项」，开关用于常驻状态）。 */
@Composable
fun CheckRow(
    label: String,
    checked: Boolean,
    enabled: Boolean = true,
    note: String = "",
    onCheckedChange: (Boolean) -> Unit,
) {
    Column(modifier = Modifier.fillMaxWidth()) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Checkbox(checked = checked, onCheckedChange = onCheckedChange, enabled = enabled)
            Text(label, style = MaterialTheme.typography.bodyMedium)
        }
        if (note.isNotEmpty()) NoteText(note)
    }
}

/** 单行文本输入。[isPassword] 为真时用密码遮罩，并且不进 saved state。 */
@Composable
fun TextInputRow(
    label: String,
    value: String,
    enabled: Boolean = true,
    isPassword: Boolean = false,
    onValueChange: (String) -> Unit,
) {
    OutlinedTextField(
        value = value,
        onValueChange = onValueChange,
        label = { Text(label) },
        enabled = enabled,
        singleLine = true,
        visualTransformation = if (isPassword) {
            PasswordVisualTransformation()
        } else {
            VisualTransformation.None
        },
        modifier = Modifier.fillMaxWidth(),
    )
}

/**
 * 数字输入。
 *
 * 文本状态留在本地：输入过程中必然出现「7」「78」「780」这样的中间值，
 * 每次按键都提交会把「7」当成真实端口发给服务端。空串不提交——
 * 清空输入框是一个中间状态，不是「改成 0」。
 */
@Composable
fun NumberInputRow(
    label: String,
    value: Int,
    enabled: Boolean = true,
    onValueChange: (Int) -> Unit,
) {
    var text by remember(value) { mutableStateOf(value.toString()) }
    OutlinedTextField(
        value = text,
        onValueChange = { next ->
            text = next
            next.trim().toIntOrNull()?.let(onValueChange)
        },
        label = { Text(label) },
        enabled = enabled,
        singleLine = true,
        modifier = Modifier.fillMaxWidth(),
    )
}

/**
 * 列表输入：一行一项。
 *
 * 用多行文本而不是「加号 / 删除按钮」的行列表：这两类值
 * （名称服务器、fake-ip 过滤、DNS 通告地址）经常是成批粘贴进来的，
 * 逐条点击添加会让「粘贴 10 个域名」变成 20 次操作。
 */
@Composable
fun TextListInputRow(
    label: String,
    values: List<String>,
    enabled: Boolean = true,
    note: String = "",
    onValueChange: (List<String>) -> Unit,
) {
    var text by remember(values) { mutableStateOf(values.joinToString("\n")) }
    OutlinedTextField(
        value = text,
        onValueChange = { next ->
            text = next
            onValueChange(
                next.split('\n').map { line -> line.trim() }.filter { it.isNotEmpty() },
            )
        },
        label = { Text(label) },
        enabled = enabled,
        minLines = 3,
        maxLines = 8,
        modifier = Modifier.fillMaxWidth(),
    )
    if (note.isNotEmpty()) NoteText(note)
}

/** 小字说明。用于「为什么是这样」与「这一项在什么条件下才生效」。 */
@Composable
fun NoteText(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.secondary,
    )
}

/** 警告。用于「开了会出问题」与「这一项可能不符合你的预期」。 */
@Composable
fun WarningText(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.error,
    )
}
