package dev.proxyclient.ui

import dev.proxyclient.options.PerAppMode
import dev.proxyclient.options.RouteMode
import dev.proxyclient.options.RuleAction
import dev.proxyclient.options.SniffProtocol
import dev.proxyclient.options.TrafficEntry

/**
 * 取值到文案键的映射 / value to message key.
 *
 * 用**显式映射**而不是 `"ui.policy." + value` 这样的前缀拼接，尽管后者更短：
 * 拼接出来的键在静态检查里看不见（tools/scripts/check-i18n-keys.ps1 只能识别
 * 完整的字面量键），一个拼错的键会一路走到界面上才被发现。
 * 显式映射写起来长一点，但它让每一个键都能被检查脚本核对。
 *
 * 取值不认识时返回**取值本身**：那样界面上显示的就是服务端给的原值，
 * 用户至少能看到「这里是它」，而不是一片空白或一个像模像样的错误选项。
 *
 * 只有**确实有译文**的取值才在这里映射。TUN 栈、日志等级这类技术取值直接
 * 原样显示（system / gvisor / mixed），文案表里的标签本来就把这些取值列在
 * 括号里（见 ui.options.tun_stack）——给它们各造一个中文名反而会让用户
 * 对不上核心文档里的写法。
 */

fun routeModeLabelKey(mode: String): String = when (mode) {
    RouteMode.RULE -> "ui.policy.rule"
    RouteMode.GLOBAL -> "ui.policy.global"
    RouteMode.DIRECT -> "ui.policy.direct"
    else -> mode
}

fun perAppModeLabelKey(mode: String): String = when (mode) {
    PerAppMode.ALL -> "ui.perapp.all"
    PerAppMode.EXCLUDE -> "ui.perapp.exclude"
    PerAppMode.INCLUDE -> "ui.perapp.include"
    else -> mode
}

fun trafficEntryLabelKey(entry: String): String = when (entry) {
    TrafficEntry.SYSTEM_PROXY -> "ui.entry.system_proxy"
    TrafficEntry.TUN -> "ui.entry.tun"
    // 线协议里的取值是 local_port，而文案表里的键叫 ui.entry.local_proxy。
    // 两者不一致是历史原因，映射在这里一次说清楚，而不是把文案表的键
    // 改成与协议同名——那会同时改动桌面端。
    TrafficEntry.LOCAL_PORT -> "ui.entry.local_proxy"
    // PAC 不在 Go 的取值说明里（只有 system_proxy / tun / local_port），
    // 但桌面端的接入方式里出现过它，因此认识它、不丢失它。
    "pac" -> "ui.entry.pac"
    else -> entry
}

fun finalActionLabelKey(action: String): String = when (action) {
    RuleAction.PROXY -> "ui.options.final_proxy"
    RuleAction.DIRECT -> "ui.options.final_direct"
    else -> action
}

fun sniffProtocolLabelKey(protocol: String): String = when (protocol) {
    SniffProtocol.TLS -> "ui.options.sniff_tls"
    SniffProtocol.HTTP -> "ui.options.sniff_http"
    SniffProtocol.QUIC -> "ui.options.sniff_quic"
    else -> protocol
}

/**
 * 构造一组单选选项，并在当前取值不认识时把它原样补进去。
 *
 * 补这一项是必要的：服务端将来新增一个取值时，界面若只列出已知项，
 * 会表现为「一个都没选中」，而用户点任意一项都会把那个值覆盖掉——
 * 一次「打开界面改点别的」就把不认识的配置弄丢了。
 */
fun labelledChoices(
    messages: Messages,
    known: List<String>,
    current: String,
    labelKey: (String) -> String,
): List<Choice> {
    val choices = ArrayList<Choice>(known.size + 1)
    for (value in known) choices.add(Choice(value, messages[labelKey(value)]))
    if (current.isNotEmpty() && !known.contains(current)) {
        choices.add(Choice(current, messages[labelKey(current)]))
    }
    return choices
}

/**
 * 取值即标签的一组选项（不做翻译的技术取值）。
 *
 * 与 [labelledChoices] 分开写，是为了让「这里没有译文」是**显式**的：
 * 否则混在一起时，一个本该有译文的取值会静默地以原文出现。
 */
fun rawChoices(known: List<String>, current: String): List<Choice> {
    val choices = ArrayList<Choice>(known.size + 1)
    for (value in known) choices.add(Choice(value, value))
    if (current.isNotEmpty() && !known.contains(current)) choices.add(Choice(current, current))
    return choices
}
