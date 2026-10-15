package dev.proxyclient.options

import dev.proxyclient.ipc.JsonRead
import org.json.JSONArray
import org.json.JSONObject

/**
 * 客户端选项模型 / the client option model.
 *
 * **这是 Go 端 shared/domain/options.go 的镜像。** 每个 JSON 字段名都与那边的
 * json tag 逐字相同；两侧必须一起改，否则表现是「改了没反应」——
 * 界面发出一个服务端不认识的键，服务端按零值处理，而核心可能因为
 * 多了个空字段而拒绝启动。这类错误不会在编译期暴露。
 *
 * 设计依据：design/architecture/ui-options.md。三条原则照抄自那边，因为它们
 * 同样约束界面代码：
 *
 *  1. **两种模式共用同一份配置**：简易模式是高级模式的子集视图，不是另一套
 *     配置。因此这里只有一个 [ClientOptions]，简易模式只显示其中两项。
 *  2. 每个选项都对应一个真实的 sing-box 字段。
 *  3. 默认值必须能直接连上——见 [ClientOptions.defaults]。
 *
 * 解析与序列化的两条约定（都有具体理由，不是风格问题）：
 *
 *  - **解析时不补默认值。** 服务端省略某个字段，就是它那边的零值；
 *    这里补一个「看起来更合理」的默认值，界面显示的值与保存回去的值
 *    就不再是服务端本来的值——一次「打开界面再保存」会静默改掉配置。
 *    界面上缺少默认值的场景只有一处：还没连上服务端时用
 *    [ClientOptions.defaults]（那正是服务端在无会话时返回的东西）。
 *  - **序列化时严格照抄 Go 的 omitempty。** 多写一个 `"mtu": 0` 会被核心
 *    当成真实的 0 处理，而 Go 侧本来不会发出这个键，于是「界面保存过的
 *    配置」与「原生保存的配置」生成的 sing-box 配置不同。
 */
data class ClientOptions(
    val routeMode: String = RouteMode.RULE,

    /** 代理组。用户很少手改，界面目前只读显示。 */
    val groups: List<ProxyGroup> = emptyList(),
    /** 分流规则。**切片顺序即优先级**，因此顺序本身就是数据。 */
    val rules: List<Rule> = emptyList(),
    val ruleSets: List<RuleSet> = emptyList(),
    /** 兜底动作。默认走代理：回落直连会静默泄漏真实流量。 */
    val finalAction: String = RuleAction.PROXY,

    val dns: DnsSettings = DnsSettings(),
    val inbound: InboundSettings = InboundSettings(),
    val subscription: SubscriptionSettings = SubscriptionSettings(),
    val app: AppSettings = AppSettings(),

    val delayTestUrl: String = "",
    val delayTestTimeout: Int = 0,
) {

    fun toJson(): JSONObject {
        val json = JSONObject()
        json.put("route_mode", routeMode)

        if (groups.isNotEmpty()) json.put("groups", JSONArray(groups.map { it.toJson() }))
        if (rules.isNotEmpty()) json.put("rules", JSONArray(rules.map { it.toJson() }))
        if (ruleSets.isNotEmpty()) json.put("rule_sets", JSONArray(ruleSets.map { it.toJson() }))
        json.put("final_action", finalAction)

        json.put("dns", dns.toJson())
        json.put("inbound", inbound.toJson())
        json.put("subscription", subscription.toJson())
        json.put("app", app.toJson())

        putIfNotEmpty("delay_test_url", delayTestUrl)
        putIfNonZero("delay_test_timeout", delayTestTimeout)
        return json
    }

    /** 替换路由模式。简易模式的三选一走这条路，保证只改一个字段。 */
    fun withRouteMode(mode: String): ClientOptions = copy(routeMode = mode)

    /**
     * 替换分应用模式。
     *
     * 模式**变化时清空名单**：同一个 [PerApp.packages] 在排除模式下是黑名单、
     * 在仅代理模式下是白名单。原样留着的后果是把用户「不想代理的应用」
     * 变成「唯一被代理的应用」——正好相反，而界面上看不出发生过什么。
     */
    fun withPerAppMode(mode: String): ClientOptions {
        val current = inbound.perApp
        val packages = if (mode == current.mode) current.packages else emptyList()
        return copy(
            inbound = inbound.copy(perApp = current.copy(mode = mode, packages = packages)),
        )
    }

    companion object {

        fun parse(source: JSONObject): ClientOptions = ClientOptions(
            // route_mode 与 final_action 在 Go 侧没有 omitempty，因此「缺失」
            // 意味着服务端那边的值就是空串。这里不补默认值：
            // 补了的话界面显示的是我们编的值，而保存回去会把服务端的
            // 零值悄悄改成那个值（见本文件开头的两条约定）。
            routeMode = JsonRead.string(source, "route_mode"),
            groups = JsonRead.objects(source, "groups").map { ProxyGroup.parse(it) },
            rules = JsonRead.objects(source, "rules").map { Rule.parse(it) },
            ruleSets = JsonRead.objects(source, "rule_sets").map { RuleSet.parse(it) },
            finalAction = JsonRead.string(source, "final_action"),
            dns = JsonRead.objectOrNull(source, "dns")?.let { DnsSettings.parse(it) }
                ?: DnsSettings(),
            inbound = JsonRead.objectOrNull(source, "inbound")?.let { InboundSettings.parse(it) }
                ?: InboundSettings(),
            subscription = JsonRead.objectOrNull(source, "subscription")
                ?.let { SubscriptionSettings.parse(it) } ?: SubscriptionSettings(),
            app = JsonRead.objectOrNull(source, "app")?.let { AppSettings.parse(it) }
                ?: AppSettings(),
            delayTestUrl = JsonRead.string(source, "delay_test_url"),
            delayTestTimeout = JsonRead.int(source, "delay_test_timeout"),
        )

        /**
         * 与 Go 的 domain.DefaultOptions() 逐项一致的那套默认值。
         *
         * 只在**还没有连上服务端**时用作界面初值。每一项都不是随手取的：
         *
         *  - 兜底动作用「走代理」而不是「直连」：回落直连会在规则没覆盖时
         *    静默泄漏真实流量——用户以为在走代理，实际没有。
         *  - IPv6 关：多数代理链路支持不完整，症状是「某些网站慢」，
         *    难以联想到 IPv6。
         *  - 允许局域网关：公共 Wi-Fi 下等于把出口分享出去。
         *  - 严格路由关、覆写目的地址关：两者都会让实际行为与用户看到的
         *    配置不一致，排查成本高。
         *  - fake-ip 开，且过滤列表非空：规则分流的常见配置依赖它，
         *    而过滤列表配不全就会让某些应用「莫名其妙打不开」。
         */
        fun defaults(): ClientOptions = ClientOptions(
            routeMode = RouteMode.RULE,
            finalAction = RuleAction.PROXY,
            dns = DnsSettings(
                enabled = true,
                fakeIp = true,
                fakeIpRange = "198.18.0.1/16",
                nameservers = listOf(
                    "https://doh.pub/dns-query",
                    "https://dns.alidns.com/dns-query",
                    "114.114.114.114",
                ),
                splitResolution = true,
                fakeIpFilter = listOf(
                    "localhost.ptlogin2.qq.com",
                    "localhost.sec.qq.com",
                    "localhost.work.weixin.qq.com",
                    "+.weixin.qq.com",
                    "+.wechat.com",
                    "time.windows.com",
                    "+.msftconnecttest.com",
                    "+.msftncsi.com",
                    "+.lan",
                    "+.ntp.org",
                ),
                useHosts = true,
                ipv6 = false,
            ),
            inbound = InboundSettings(
                listen = "127.0.0.1",
                mixedPort = 7890,
                allowLan = false,
                trafficEntry = TrafficEntry.SYSTEM_PROXY,
                perApp = PerApp(mode = PerAppMode.ALL),
                tun = TunSettings(
                    enabled = false,
                    stack = TunStack.MIXED,
                    mtu = 1500,
                    autoRoute = true,
                    strictRoute = false,
                    dnsMode = TunDnsMode.HIJACK,
                ),
                sniffer = SniffSettings(
                    enabled = true,
                    protocols = listOf(SniffProtocol.TLS, SniffProtocol.HTTP, SniffProtocol.QUIC),
                    overrideDestination = false,
                ),
            ),
            subscription = SubscriptionSettings(
                autoUpdate = true,
                updateInterval = 6,
                updateViaProxy = true,
            ),
            app = AppSettings(
                autoStart = false,
                startMinimized = false,
                autoConnectOnStart = false,
                logLevel = LogLevel.WARNING,
                logBufferLines = 1000,
            ),
            delayTestUrl = "https://www.gstatic.com/generate_204",
            delayTestTimeout = 5000,
        )
    }
}

/** DNS 覆写设置，对应 ui-options.md 2.3 节。 */
data class DnsSettings(
    /** 关闭时服务端**不应生成 dns 段**，而不是生成一个空的。 */
    val enabled: Boolean = false,
    val fakeIp: Boolean = false,
    val fakeIpRange: String = "",
    val nameservers: List<String> = emptyList(),
    val splitResolution: Boolean = false,
    val fakeIpFilter: List<String> = emptyList(),
    val useHosts: Boolean = false,
    /** 默认关（见 [ClientOptions.defaults] 的说明）。 */
    val ipv6: Boolean = false,
) {
    fun toJson(): JSONObject {
        val json = JSONObject()
        json.put("enabled", enabled)
        json.put("fake_ip", fakeIp)
        putIfNotEmpty("fake_ip_range", fakeIpRange)
        putStrings("nameservers", nameservers)
        json.put("split_resolution", splitResolution)
        putStrings("fake_ip_filter", fakeIpFilter)
        json.put("use_hosts", useHosts)
        json.put("ipv6", ipv6)
        return json
    }

    companion object {
        fun parse(source: JSONObject): DnsSettings = DnsSettings(
            enabled = JsonRead.bool(source, "enabled"),
            fakeIp = JsonRead.bool(source, "fake_ip"),
            fakeIpRange = JsonRead.string(source, "fake_ip_range"),
            nameservers = JsonRead.strings(source, "nameservers"),
            splitResolution = JsonRead.bool(source, "split_resolution"),
            fakeIpFilter = JsonRead.strings(source, "fake_ip_filter"),
            useHosts = JsonRead.bool(source, "use_hosts"),
            ipv6 = JsonRead.bool(source, "ipv6"),
        )
    }
}

/**
 * TUN 入站设置，对应 ui-options.md 2.4 节。
 *
 * 注意 [dnsMode] 与 [dnsAddress]：这里曾经写作 dns_hijack（一个字符串列表），
 * 那是 sing-box 里**不存在**的字段，会让每一份开了 TUN 的配置都起不来
 * （见 ui-options.md 的说明）。界面照模型显示即可，不要再造回那个名字。
 */
data class TunSettings(
    val enabled: Boolean = false,
    val stack: String = "",
    val device: String = "",
    val mtu: Int = 0,
    val autoRoute: Boolean = false,
    val strictRoute: Boolean = false,
    /** 取 disabled / native / hijack。 */
    val dnsMode: String = "",
    /** 通告给系统的 DNS 地址，**只接受 IP，不含端口**。 */
    val dnsAddress: List<String> = emptyList(),
) {
    fun toJson(): JSONObject {
        val json = JSONObject()
        json.put("enabled", enabled)
        putIfNotEmpty("stack", stack)
        putIfNotEmpty("device", device)
        putIfNonZero("mtu", mtu)
        json.put("auto_route", autoRoute)
        json.put("strict_route", strictRoute)
        putIfNotEmpty("dns_mode", dnsMode)
        putStrings("dns_address", dnsAddress)
        return json
    }

    companion object {
        fun parse(source: JSONObject): TunSettings = TunSettings(
            enabled = JsonRead.bool(source, "enabled"),
            stack = JsonRead.string(source, "stack"),
            device = JsonRead.string(source, "device"),
            mtu = JsonRead.int(source, "mtu"),
            autoRoute = JsonRead.bool(source, "auto_route"),
            strictRoute = JsonRead.bool(source, "strict_route"),
            dnsMode = JsonRead.string(source, "dns_mode"),
            dnsAddress = JsonRead.strings(source, "dns_address"),
        )
    }
}

/** 协议嗅探设置。 */
data class SniffSettings(
    val enabled: Boolean = false,
    val protocols: List<String> = emptyList(),
    /** 默认关：开着会让连接的实际目的地与用户看到的域名不一致。 */
    val overrideDestination: Boolean = false,
) {
    fun toJson(): JSONObject {
        val json = JSONObject()
        json.put("enabled", enabled)
        putStrings("protocols", protocols)
        json.put("override_destination", overrideDestination)
        return json
    }

    companion object {
        fun parse(source: JSONObject): SniffSettings = SniffSettings(
            enabled = JsonRead.bool(source, "enabled"),
            protocols = JsonRead.strings(source, "protocols"),
            overrideDestination = JsonRead.bool(source, "override_destination"),
        )
    }
}

/**
 * 分应用代理设置，**仅 Android 有意义**。
 *
 * [packages] 的**含义随 [mode] 变化**：排除模式下它是排除名单，
 * 仅代理模式下它是白名单。用一个字段承载两种含义是有意的——
 * 界面上的选择器与列表是一个整体，拆成两个字段会允许
 * 「排除了 3 个应用、同时只代理 2 个应用」这种自相矛盾的状态。
 *
 * 在 sing-box 里 include 与 exclude 是**互斥**的，同时给出会被核心拒绝，
 * 因此生成配置时必须按模式二选一（Go 侧用取值方法保证这一点）。
 */
data class PerApp(
    val mode: String = PerAppMode.ALL,
    val packages: List<String> = emptyList(),
) {
    fun toJson(): JSONObject {
        val json = JSONObject()
        json.put("mode", mode)
        putStrings("packages", packages)
        return json
    }

    companion object {
        fun parse(source: JSONObject): PerApp = PerApp(
            mode = JsonRead.string(source, "mode"),
            packages = JsonRead.strings(source, "packages"),
        )
    }
}

/** 流量接入设置。 */
data class InboundSettings(
    val listen: String = "",
    val mixedPort: Int = 0,
    /** 默认关：公共 Wi-Fi 下等于把自己的网络身份分享出去。 */
    val allowLan: Boolean = false,
    val trafficEntry: String = "",
    val perApp: PerApp = PerApp(),
    val tun: TunSettings = TunSettings(),
    val sniffer: SniffSettings = SniffSettings(),
) {
    fun toJson(): JSONObject {
        val json = JSONObject()
        json.put("listen", listen)
        json.put("mixed_port", mixedPort)
        json.put("allow_lan", allowLan)
        json.put("traffic_entry", trafficEntry)
        json.put("per_app", perApp.toJson())
        json.put("tun", tun.toJson())
        json.put("sniffer", sniffer.toJson())
        return json
    }

    companion object {
        fun parse(source: JSONObject): InboundSettings = InboundSettings(
            listen = JsonRead.string(source, "listen"),
            mixedPort = JsonRead.int(source, "mixed_port"),
            allowLan = JsonRead.bool(source, "allow_lan"),
            trafficEntry = JsonRead.string(source, "traffic_entry"),
            perApp = JsonRead.objectOrNull(source, "per_app")?.let { PerApp.parse(it) } ?: PerApp(),
            tun = JsonRead.objectOrNull(source, "tun")?.let { TunSettings.parse(it) }
                ?: TunSettings(),
            sniffer = JsonRead.objectOrNull(source, "sniffer")?.let { SniffSettings.parse(it) }
                ?: SniffSettings(),
        )
    }
}

/** 订阅相关设置。 */
data class SubscriptionSettings(
    val url: String = "",
    val autoUpdate: Boolean = false,
    /** 更新间隔（小时）。 */
    val updateInterval: Int = 0,
    /** 更新订阅时走代理：面板本身可能需要代理才能访问，因此默认开。 */
    val updateViaProxy: Boolean = false,
    val namePrefix: String = "",
) {
    fun toJson(): JSONObject {
        val json = JSONObject()
        putIfNotEmpty("url", url)
        json.put("auto_update", autoUpdate)
        putIfNonZero("update_interval", updateInterval)
        json.put("update_via_proxy", updateViaProxy)
        putIfNotEmpty("name_prefix", namePrefix)
        return json
    }

    companion object {
        fun parse(source: JSONObject): SubscriptionSettings = SubscriptionSettings(
            url = JsonRead.string(source, "url"),
            autoUpdate = JsonRead.bool(source, "auto_update"),
            updateInterval = JsonRead.int(source, "update_interval"),
            updateViaProxy = JsonRead.bool(source, "update_via_proxy"),
            namePrefix = JsonRead.string(source, "name_prefix"),
        )
    }
}

/**
 * 应用行为设置。
 *
 * 这些选项**不产生任何 sing-box 字段**：它们是应用自身的行为。
 * 「开机自启」与「启动最小化」在 Android 上没有意义（见界面上的说明）。
 */
data class AppSettings(
    val autoStart: Boolean = false,
    val startMinimized: Boolean = false,
    val autoConnectOnStart: Boolean = false,
    val language: String = "",
    val theme: String = "",
    val logLevel: String = "",
    val logBufferLines: Int = 0,
) {
    fun toJson(): JSONObject {
        val json = JSONObject()
        json.put("auto_start", autoStart)
        json.put("start_minimized", startMinimized)
        json.put("auto_connect_on_start", autoConnectOnStart)
        putIfNotEmpty("language", language)
        putIfNotEmpty("theme", theme)
        putIfNotEmpty("log_level", logLevel)
        putIfNonZero("log_buffer_lines", logBufferLines)
        return json
    }

    companion object {
        fun parse(source: JSONObject): AppSettings = AppSettings(
            autoStart = JsonRead.bool(source, "auto_start"),
            startMinimized = JsonRead.bool(source, "start_minimized"),
            autoConnectOnStart = JsonRead.bool(source, "auto_connect_on_start"),
            language = JsonRead.string(source, "language"),
            theme = JsonRead.string(source, "theme"),
            logLevel = JsonRead.string(source, "log_level"),
            logBufferLines = JsonRead.int(source, "log_buffer_lines"),
        )
    }
}

/**
 * 一个代理组。
 *
 * 「负载均衡」在 sing-box 里没有直接等价物（见 ui-options.md 第 3 节），
 * 因此选择 [ProxyGroupType.BALANCE] 时界面必须注明**不保证会话保持**。
 */
data class ProxyGroup(
    val name: String = "",
    val type: String = "",
    val members: List<String> = emptyList(),
    val includeAll: Boolean = false,
    val testUrl: String = "",
    val testInterval: Int = 0,
    val maxFailures: Int = 0,
) {
    fun toJson(): JSONObject {
        val json = JSONObject()
        json.put("name", name)
        json.put("type", type)
        putStrings("members", members)
        json.put("include_all", includeAll)
        putIfNotEmpty("test_url", testUrl)
        putIfNonZero("test_interval", testInterval)
        putIfNonZero("max_failures", maxFailures)
        return json
    }

    companion object {
        fun parse(source: JSONObject): ProxyGroup = ProxyGroup(
            name = JsonRead.string(source, "name"),
            type = JsonRead.string(source, "type"),
            members = JsonRead.strings(source, "members"),
            includeAll = JsonRead.bool(source, "include_all"),
            testUrl = JsonRead.string(source, "test_url"),
            testInterval = JsonRead.int(source, "test_interval"),
            maxFailures = JsonRead.int(source, "max_failures"),
        )
    }
}

/** 一条分流规则。 */
data class Rule(
    val type: String = "",
    val value: String = "",
    val action: String = "",
    /** [action] 为 [RuleAction.GROUP] 时必须非空。 */
    val groupName: String = "",
) {
    fun toJson(): JSONObject {
        val json = JSONObject()
        json.put("type", type)
        json.put("value", value)
        json.put("action", action)
        putIfNotEmpty("group_name", groupName)
        return json
    }

    companion object {
        fun parse(source: JSONObject): Rule = Rule(
            type = JsonRead.string(source, "type"),
            value = JsonRead.string(source, "value"),
            action = JsonRead.string(source, "action"),
            groupName = JsonRead.string(source, "group_name"),
        )
    }
}

/**
 * 一个规则集。
 *
 * [format] 必须与核心期望的格式一致：mihomo 的 .mrs 与 sing-box 的 .srs
 * 不通用，混用会加载失败。留空表示由服务端按 URL 后缀推断
 * （.srs 为 binary、.json 为 source），推断不出来时由服务端报明确警告，
 * 界面**不要**在这里替它猜一个。
 */
data class RuleSet(
    val name: String = "",
    val url: String = "",
    /** 更新间隔（小时）。 */
    val updateInterval: Int = 0,
    /** 取 source / binary。 */
    val format: String = "",
) {
    fun toJson(): JSONObject {
        val json = JSONObject()
        json.put("name", name)
        putIfNotEmpty("url", url)
        putIfNonZero("update_interval", updateInterval)
        putIfNotEmpty("format", format)
        return json
    }

    companion object {
        fun parse(source: JSONObject): RuleSet = RuleSet(
            name = JsonRead.string(source, "name"),
            url = JsonRead.string(source, "url"),
            updateInterval = JsonRead.int(source, "update_interval"),
            format = JsonRead.string(source, "format"),
        )
    }
}

// ---------------------------------------------------------------------------
// 取值常量
//
// 全部用字符串常量而不是 enum，理由与桌面端一致：enum 遇到服务端新增的取值
// 会在解析阶段失败，而字符串会让界面照原样显示并原样保存回去——
// 「不认识的值」比「丢失的值」安全得多。
// ---------------------------------------------------------------------------

/** 路由模式，对应简易模式的三选一。取值与 Clash 的 mode 一致。 */
object RouteMode {
    /** 按规则分流。 */
    const val RULE = "rule"
    /** 全部走代理，忽略规则。 */
    const val GLOBAL = "global"
    /** 全部直连。选中它时必须给出提示：表现是「连上了但打不开墙外网站」。 */
    const val DIRECT = "direct"

    val choices = listOf(RULE, GLOBAL, DIRECT)
}

/** 分应用代理模式（仅 Android 有意义）。 */
object PerAppMode {
    const val ALL = "all"
    const val EXCLUDE = "exclude"
    const val INCLUDE = "include"

    val choices = listOf(ALL, EXCLUDE, INCLUDE)
}

/** 流量接入方式。 */
object TrafficEntry {
    const val SYSTEM_PROXY = "system_proxy"
    const val TUN = "tun"
    const val LOCAL_PORT = "local_port"

    val choices = listOf(SYSTEM_PROXY, TUN, LOCAL_PORT)
}

/** TUN 栈。 */
object TunStack {
    const val SYSTEM = "system"
    const val GVISOR = "gvisor"
    const val MIXED = "mixed"

    val choices = listOf(SYSTEM, GVISOR, MIXED)
}

/** TUN 的 DNS 处理方式。取值来自 sing-box 的 TunInboundOptions.dns_mode。 */
object TunDnsMode {
    const val DISABLED = "disabled"
    const val NATIVE = "native"
    const val HIJACK = "hijack"

    val choices = listOf(DISABLED, NATIVE, HIJACK)
}

/** 嗅探协议。注意：这份列表目前**不生效**，见界面上的说明与 ui-options.md。 */
object SniffProtocol {
    const val TLS = "tls"
    const val HTTP = "http"
    const val QUIC = "quic"

    val choices = listOf(TLS, HTTP, QUIC)
}

/** 代理组类型。 */
object ProxyGroupType {
    const val SELECT = "select"
    const val URL_TEST = "url_test"
    const val FALLBACK = "fallback"
    /** sing-box 无直接等价物，只作近似：**不保证会话保持**。 */
    const val BALANCE = "balance"
}

/** 规则的匹配方式。 */
object RuleType {
    const val DOMAIN = "domain"
    const val DOMAIN_SUFFIX = "domain_suffix"
    const val DOMAIN_KEYWORD = "domain_keyword"
    const val IP_CIDR = "ip_cidr"
    /** 桌面端可用，**Android 不可用**：系统不允许按包名匹配进程。 */
    const val PROCESS_NAME = "process_name"
    const val RULE_SET = "rule_set"
    const val PORT = "port"
}

/** 命中规则后的动作。 */
object RuleAction {
    const val PROXY = "proxy"
    const val DIRECT = "direct"
    const val REJECT = "reject"
    /** 走指定的代理组；group_name 必须同时给出。 */
    const val GROUP = "group"

    val finalChoices = listOf(PROXY, DIRECT)
}

/** 规则集格式。 */
object RuleSetFormat {
    const val SOURCE = "source"
    const val BINARY = "binary"
}

/** 日志等级，对应 sing-box 的 log.level。 */
object LogLevel {
    const val TRACE = "trace"
    const val DEBUG = "debug"
    const val INFO = "info"
    const val WARNING = "warning"
    const val ERROR = "error"

    val choices = listOf(TRACE, DEBUG, INFO, WARNING, ERROR)
}

/** 主题。 */
object AppTheme {
    const val SYSTEM = "system"
    const val LIGHT = "light"
    const val DARK = "dark"

    val choices = listOf(SYSTEM, LIGHT, DARK)
}

// ---------------------------------------------------------------------------
// 序列化辅助：与 Go 的 omitempty 一一对应
// ---------------------------------------------------------------------------

private fun JSONObject.putIfNotEmpty(key: String, value: String) {
    if (value.isNotEmpty()) put(key, value)
}

private fun JSONObject.putIfNonZero(key: String, value: Int) {
    if (value != 0) put(key, value)
}

private fun JSONObject.putStrings(key: String, values: List<String>) {
    if (values.isNotEmpty()) put(key, JSONArray(values))
}
