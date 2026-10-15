package dev.proxyclient.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Button
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import dev.proxyclient.options.AppTheme
import dev.proxyclient.options.ClientOptions
import dev.proxyclient.options.LogLevel
import dev.proxyclient.options.ProxyGroupType
import dev.proxyclient.options.RuleAction
import dev.proxyclient.options.TrafficEntry
import dev.proxyclient.options.TunDnsMode
import dev.proxyclient.options.TunStack

/**
 * 专业模式 / professional mode.
 *
 * 分组照 ui-options.md 第 2 节：代理、规则、DNS、流量接入、嗅探、订阅、应用。
 * 与简易模式共用**同一份** [ClientOptions]（设计基线 §0 原则一）：
 * 这里改的每一项，简易模式选中的路由模式都会跟着变，反之亦然。
 *
 * 输入框与开关的提交方式不同，这是刻意的：
 *
 *  - 选项里的文本/数字字段先落进一份**草稿**，由「保存」一次性提交。
 *    「每敲一个字就发一次 SetOptions」不只是慢：`198` 会被当成真实网段
 *    写进服务端，而用户还没输完 `.18.0.1/16`。
 *  - 简易模式那两项没有中间状态，因此那边是即改即生效。
 *
 * 草稿用 `remember(state.options)`：服务端返回新选项时（保存成功、重新载入、
 * 切换账号）草稿重新初始化为服务端那份。这样界面永远不会显示一份
 * 「服务端并不知道」的配置。
 */
@Composable
fun ProModePanel(
    state: ProxyUiState,
    messages: Messages,
    onSave: (ClientOptions) -> Unit,
    onReloadOptions: () -> Unit,
) {
    var draft by remember(state.options) { mutableStateOf(state.options) }
    val busy = state.busy.isNotEmpty()
    val editable = state.backend == BackendState.Ready && !busy && state.session.loggedIn

    fun update(transform: (ClientOptions) -> ClientOptions) {
        draft = transform(draft)
    }

    Column(
        modifier = Modifier.fillMaxWidth(),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        if (!state.session.loggedIn) {
            NoteText(messages["session.error.not_authenticated"])
        }

        // ---- 2.1 代理 ----
        SectionCard(messages["ui.group.proxy"]) {
            TextInputRow(
                label = messages["ui.label.delay_test_url"],
                value = draft.delayTestUrl,
                enabled = editable,
                onValueChange = { value -> update { it.copy(delayTestUrl = value) } },
            )
            NumberInputRow(
                label = messages["ui.label.delay_test_timeout"],
                value = draft.delayTestTimeout,
                enabled = editable,
                onValueChange = { value -> update { it.copy(delayTestTimeout = value) } },
            )
            NoteText(messages["ui.label.proxy_groups"])
            if (draft.groups.isEmpty()) {
                NoteText(messages["ui.detail.none"])
            } else {
                NoteText(messages["ui.detail.items_count", draft.groups.size])
                for (group in draft.groups) {
                    val members = if (group.members.isEmpty()) "" else " · ${group.members.size}"
                    NoteText("${group.name} · ${group.type}$members")
                }
                if (draft.groups.any { it.type == ProxyGroupType.BALANCE }) {
                    // 参照物提供了负载均衡，而 sing-box 没有直接等价物：
                    // 差异必须写进界面，而不是假装一致。
                    WarningText(messages["ui.detail.balance_no_session"])
                }
            }
            NoteText(messages["ui.detail.groups_readonly"])
        }

        // ---- 2.2 规则 ----
        SectionCard(messages["ui.group.rules"]) {
            ChoiceRow(
                label = messages["ui.label.final_action"],
                choices = labelledChoices(
                    messages,
                    RuleAction.finalChoices,
                    draft.finalAction,
                    ::finalActionLabelKey,
                ),
                selected = draft.finalAction,
                enabled = editable,
                onSelect = { value -> update { it.copy(finalAction = value) } },
            )
            NoteText(messages["ui.detail.rules_order"])
            if (draft.rules.isEmpty()) {
                NoteText(messages["ui.detail.none"])
            } else {
                NoteText(messages["ui.detail.items_count", draft.rules.size])
                for (rule in draft.rules) {
                    val group = if (rule.groupName.isEmpty()) "" else " (${rule.groupName})"
                    NoteText("${rule.type} ${rule.value} → ${rule.action}$group")
                }
            }
            NoteText(messages["ui.detail.rules_readonly"])
            if (draft.ruleSets.isEmpty()) {
                NoteText(messages["ui.label.rule_sets"] + ": " + messages["ui.detail.none"])
            } else {
                NoteText(messages["ui.detail.items_count", draft.ruleSets.size])
                for (ruleSet in draft.ruleSets) {
                    val format = if (ruleSet.format.isEmpty()) "" else " · ${ruleSet.format}"
                    NoteText("${ruleSet.name}$format")
                }
            }
        }

        // ---- 2.3 DNS ----
        SectionCard(messages["ui.group.dns"]) {
            ToggleRow(
                label = messages["ui.label.dns_enabled"],
                checked = draft.dns.enabled,
                enabled = editable,
                onCheckedChange = { value -> update { it.copy(dns = it.dns.copy(enabled = value)) } },
            )
            ToggleRow(
                label = messages["ui.label.dns_fake_ip"],
                checked = draft.dns.fakeIp,
                enabled = editable,
                onCheckedChange = { value -> update { it.copy(dns = it.dns.copy(fakeIp = value)) } },
            )
            TextInputRow(
                label = messages["ui.label.dns_fake_ip_range"],
                value = draft.dns.fakeIpRange,
                enabled = editable && draft.dns.fakeIp,
                onValueChange = { value -> update { it.copy(dns = it.dns.copy(fakeIpRange = value)) } },
            )
            TextListInputRow(
                label = messages["ui.label.dns_nameservers"],
                values = draft.dns.nameservers,
                enabled = editable,
                onValueChange = { value ->
                    update { it.copy(dns = it.dns.copy(nameservers = value)) }
                },
            )
            ToggleRow(
                label = messages["ui.label.dns_split_resolution"],
                checked = draft.dns.splitResolution,
                enabled = editable,
                onCheckedChange = { value ->
                    update { it.copy(dns = it.dns.copy(splitResolution = value)) }
                },
            )
            TextListInputRow(
                label = messages["ui.label.dns_fake_ip_filter"],
                values = draft.dns.fakeIpFilter,
                enabled = editable && draft.dns.fakeIp,
                note = messages["ui.detail.fake_ip_filter_note"],
                onValueChange = { value ->
                    update { it.copy(dns = it.dns.copy(fakeIpFilter = value)) }
                },
            )
            ToggleRow(
                label = messages["ui.label.dns_use_hosts"],
                checked = draft.dns.useHosts,
                enabled = editable,
                onCheckedChange = { value ->
                    update { it.copy(dns = it.dns.copy(useHosts = value)) }
                },
            )
            ToggleRow(
                label = messages["ui.label.dns_ipv6"],
                checked = draft.dns.ipv6,
                enabled = editable,
                note = messages["ui.detail.ipv6_off_note"],
                onCheckedChange = { value -> update { it.copy(dns = it.dns.copy(ipv6 = value)) } },
            )
        }

        // ---- 2.4 流量接入 ----
        SectionCard(messages["ui.group.inbound"]) {
            ChoiceRow(
                label = messages["ui.label.network_mode"],
                choices = labelledChoices(
                    messages,
                    TrafficEntry.choices,
                    draft.inbound.trafficEntry,
                    ::trafficEntryLabelKey,
                ),
                selected = draft.inbound.trafficEntry,
                enabled = editable,
                onSelect = { value -> update { it.copy(inbound = it.inbound.copy(trafficEntry = value)) } },
            )
            NumberInputRow(
                label = messages["ui.label.mixed_port"],
                value = draft.inbound.mixedPort,
                enabled = editable,
                onValueChange = { value -> update { it.copy(inbound = it.inbound.copy(mixedPort = value)) } },
            )
            ToggleRow(
                label = messages["ui.label.allow_lan"],
                checked = draft.inbound.allowLan,
                enabled = editable,
                onCheckedChange = { value -> update { it.copy(inbound = it.inbound.copy(allowLan = value)) } },
            )
            if (draft.inbound.allowLan) {
                WarningText(messages["ui.warn.allow_lan"])
            }

            // TUN：Android 上真正被使用的是它，因此这里的默认值与桌面端不同。
            ToggleRow(
                label = messages["ui.label.tun_enabled"],
                checked = draft.inbound.tun.enabled,
                enabled = editable,
                onCheckedChange = { value ->
                    update { it.copy(inbound = it.inbound.copy(tun = it.inbound.tun.copy(enabled = value))) }
                },
            )
            ChoiceRow(
                label = messages["ui.label.tun_stack"],
                // 技术取值原样显示（system / gvisor / mixed）：文案表的标签
                // 本来就把这些取值列在括号里，另造一套中文名会让用户对不上
                // 核心文档里的写法。理由见 OptionLabels.kt 的模块说明。
                choices = rawChoices(TunStack.choices, draft.inbound.tun.stack),
                selected = draft.inbound.tun.stack,
                enabled = editable && draft.inbound.tun.enabled,
                onSelect = { value ->
                    update { it.copy(inbound = it.inbound.copy(tun = it.inbound.tun.copy(stack = value))) }
                },
            )
            TextInputRow(
                label = messages["ui.label.tun_device"],
                value = draft.inbound.tun.device,
                enabled = editable && draft.inbound.tun.enabled,
                onValueChange = { value ->
                    update { it.copy(inbound = it.inbound.copy(tun = it.inbound.tun.copy(device = value))) }
                },
            )
            NumberInputRow(
                label = messages["ui.label.tun_mtu"],
                value = draft.inbound.tun.mtu,
                enabled = editable && draft.inbound.tun.enabled,
                onValueChange = { value ->
                    update { it.copy(inbound = it.inbound.copy(tun = it.inbound.tun.copy(mtu = value))) }
                },
            )
            ToggleRow(
                label = messages["ui.label.tun_auto_route"],
                checked = draft.inbound.tun.autoRoute,
                enabled = editable && draft.inbound.tun.enabled,
                onCheckedChange = { value ->
                    update { it.copy(inbound = it.inbound.copy(tun = it.inbound.tun.copy(autoRoute = value))) }
                },
            )
            ToggleRow(
                label = messages["ui.label.tun_strict_route"],
                checked = draft.inbound.tun.strictRoute,
                enabled = editable && draft.inbound.tun.enabled,
                onCheckedChange = { value ->
                    update { it.copy(inbound = it.inbound.copy(tun = it.inbound.tun.copy(strictRoute = value))) }
                },
            )
            ChoiceRow(
                label = messages["ui.label.tun_dns_mode"],
                choices = rawChoices(TunDnsMode.choices, draft.inbound.tun.dnsMode),
                selected = draft.inbound.tun.dnsMode,
                enabled = editable && draft.inbound.tun.enabled,
                onSelect = { value ->
                    update { it.copy(inbound = it.inbound.copy(tun = it.inbound.tun.copy(dnsMode = value))) }
                },
            )
            TextListInputRow(
                label = messages["ui.label.tun_dns_address"],
                values = draft.inbound.tun.dnsAddress,
                enabled = editable && draft.inbound.tun.enabled,
                note = messages["ui.detail.tun_dns_address_note"],
                onValueChange = { value ->
                    update {
                        it.copy(inbound = it.inbound.copy(tun = it.inbound.tun.copy(dnsAddress = value)))
                    }
                },
            )

            // 分应用也在这里出现一次：它的数据模型住在 inbound 里，
            // 而专业模式的用户会到「流量接入」下面找它。
            NoteText(messages["ui.detail.per_app_location"])
        }

        // ---- 2.5 嗅探 ----
        SectionCard(messages["ui.group.sniffer"]) {
            ToggleRow(
                label = messages["ui.label.sniff_enabled"],
                checked = draft.inbound.sniffer.enabled,
                enabled = editable,
                onCheckedChange = { value ->
                    update {
                        it.copy(inbound = it.inbound.copy(sniffer = it.inbound.sniffer.copy(enabled = value)))
                    }
                },
            )
            ToggleRow(
                label = messages["ui.label.sniff_override_destination"],
                checked = draft.inbound.sniffer.overrideDestination,
                enabled = editable,
                onCheckedChange = { value ->
                    update {
                        it.copy(
                            inbound = it.inbound.copy(
                                sniffer = it.inbound.sniffer.copy(overrideDestination = value),
                            ),
                        )
                    }
                },
            )
            TextListInputRow(
                label = messages["ui.label.sniff_protocols"],
                values = draft.inbound.sniffer.protocols,
                enabled = editable,
                onValueChange = { value ->
                    update {
                        it.copy(inbound = it.inbound.copy(sniffer = it.inbound.sniffer.copy(protocols = value)))
                    }
                },
            )
            // 「改了没反应」的选项必须标注出来，否则它正是设计文档批评的伪选项。
            WarningText(messages["ui.detail.sniff_protocols_inactive"])
        }

        // ---- 2.6 订阅 ----
        SectionCard(messages["ui.group.subscription"]) {
            TextInputRow(
                label = messages["ui.label.sub_url"],
                value = draft.subscription.url,
                enabled = editable,
                onValueChange = { value -> update { it.copy(subscription = it.subscription.copy(url = value)) } },
            )
            ToggleRow(
                label = messages["ui.label.sub_auto_update"],
                checked = draft.subscription.autoUpdate,
                enabled = editable,
                onCheckedChange = { value ->
                    update { it.copy(subscription = it.subscription.copy(autoUpdate = value)) }
                },
            )
            NumberInputRow(
                label = messages["ui.label.sub_update_interval"],
                value = draft.subscription.updateInterval,
                enabled = editable,
                onValueChange = { value ->
                    update { it.copy(subscription = it.subscription.copy(updateInterval = value)) }
                },
            )
            ToggleRow(
                label = messages["ui.label.sub_update_via_proxy"],
                checked = draft.subscription.updateViaProxy,
                enabled = editable,
                onCheckedChange = { value ->
                    update { it.copy(subscription = it.subscription.copy(updateViaProxy = value)) }
                },
            )
            TextInputRow(
                label = messages["ui.label.sub_name_prefix"],
                value = draft.subscription.namePrefix,
                enabled = editable,
                onValueChange = { value ->
                    update { it.copy(subscription = it.subscription.copy(namePrefix = value)) }
                },
            )
        }

        // ---- 2.7 应用 ----
        SectionCard(messages["ui.group.app"]) {
            ToggleRow(
                label = messages["ui.label.app_auto_start"],
                checked = draft.app.autoStart,
                enabled = editable,
                onCheckedChange = { value -> update { it.copy(app = it.app.copy(autoStart = value)) } },
            )
            ToggleRow(
                label = messages["ui.label.app_start_minimized"],
                checked = draft.app.startMinimized,
                enabled = editable,
                onCheckedChange = { value -> update { it.copy(app = it.app.copy(startMinimized = value)) } },
            )
            ToggleRow(
                label = messages["ui.label.app_auto_connect"],
                checked = draft.app.autoConnectOnStart,
                enabled = editable,
                onCheckedChange = { value ->
                    update { it.copy(app = it.app.copy(autoConnectOnStart = value)) }
                },
            )
            // 开机自启与启动最小化在 Android 上没有意义（没有托盘，也不声明
            // RECEIVE_BOOT_COMPLETED），说清楚比让用户以为它生效了好。
            WarningText(messages["ui.detail.android_autostart_unsupported"])

            TextInputRow(
                label = messages["ui.label.app_language"],
                value = draft.app.language,
                enabled = editable,
                onValueChange = { value -> update { it.copy(app = it.app.copy(language = value)) } },
            )
            NoteText(messages["ui.detail.language_theme_not_applied"])

            ChoiceRow(
                label = messages["ui.label.app_theme"],
                choices = rawChoices(AppTheme.choices, draft.app.theme),
                selected = draft.app.theme,
                enabled = editable,
                onSelect = { value -> update { it.copy(app = it.app.copy(theme = value)) } },
            )
            ChoiceRow(
                label = messages["ui.label.app_log_level"],
                choices = rawChoices(LogLevel.choices, draft.app.logLevel),
                selected = draft.app.logLevel,
                enabled = editable,
                onSelect = { value -> update { it.copy(app = it.app.copy(logLevel = value)) } },
            )
            NumberInputRow(
                label = messages["ui.label.app_log_buffer_lines"],
                value = draft.app.logBufferLines,
                enabled = editable,
                onValueChange = { value -> update { it.copy(app = it.app.copy(logBufferLines = value)) } },
            )
        }

        // ---- 保存 / 重新载入 ----
        Row(
            modifier = Modifier.fillMaxWidth().padding(top = 4.dp),
            horizontalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            Button(
                onClick = { onSave(draft) },
                enabled = editable,
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text(messages["ui.action.save"])
            }
            OutlinedButton(
                onClick = onReloadOptions,
                enabled = editable,
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text(messages["ui.action.reload"])
            }
        }
        NoteText(messages["ui.detail.save_scope"])
    }
}
