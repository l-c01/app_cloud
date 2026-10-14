package dev.proxyclient.config

import android.content.Context
import org.json.JSONObject

/**
 * 出厂配置 / shipped configuration.
 *
 * 只读。与桌面端读取同一个 design/client-config.json（打包进 assets/config）。
 *
 * 与用户偏好的区分（桌面端有同样的设计）：
 *  - Config 描述「这个构建是什么」：软件名、面板地址、默认端口、默认核心；
 *    应用**绝不写它**。
 *  - 用户偏好（界面模式、语言、上次选的核心）单独保存，应用可以写。
 *
 * 混在一起的后果很具体：应用升级会覆盖用户选择；或者用户的选择被写进
 * 出厂配置，于是「重置为默认值」变成不可能——默认值已经被改掉了。
 */
data class AppConfig(
    val applicationName: String = "default",
    val displayNameZh: String = "",
    val displayNameEn: String = "",
    val apiBaseUrl: String = "",
    val webBaseUrl: String = "",
    val timeoutSeconds: Int = 15,
    val defaultUiMode: UiMode = UiMode.Simple,
    val defaultLanguage: String = "system",
    val fallbackLanguage: String = "en",
    val defaultCore: String = "sing-box",
    val defaultRoutingPolicy: String = "rule",
    val localProxyHost: String = "127.0.0.1",
    val httpProxyPort: Int = 7890,
    val socksProxyPort: Int = 7891,
    val tunMtu: Int = 1500,
    val tunEnableIpv6: Boolean = false,
    /** 解析过程中的问题；界面可据此提示配置有误，但不阻止启动。 */
    val warnings: List<String> = emptyList(),
) {
    fun displayName(language: String): String = when {
        language.startsWith("zh") && displayNameZh.isNotEmpty() -> displayNameZh
        language.startsWith("en") && displayNameEn.isNotEmpty() -> displayNameEn
        applicationName.isNotEmpty() -> applicationName
        else -> "default"
    }

    companion object {
        private const val ASSET_PATH = "config/client-config.json"

        /**
         * 从 assets 加载出厂配置。
         *
         * 读不到时返回默认值而不是抛异常：配置不完整不该让用户连界面都看不到。
         */
        fun load(context: Context): AppConfig {
            val text = runCatching {
                context.assets.open(ASSET_PATH).bufferedReader().use { it.readText() }
            }.getOrNull() ?: return AppConfig(
                warnings = listOf("the shipped config file is missing from the package"),
            )

            val root = runCatching { JSONObject(text) }.getOrNull() ?: return AppConfig(
                warnings = listOf("the shipped config file is not valid JSON"),
            )

            val warnings = mutableListOf<String>()
            val branding = root.optJSONObject("branding") ?: JSONObject()
            val names = branding.optJSONObject("displayNames") ?: JSONObject()
            val panel = root.optJSONObject("panel") ?: JSONObject()
            val defaults = root.optJSONObject("clientDefaults") ?: JSONObject()
            val tun = defaults.optJSONObject("tun") ?: JSONObject()

            val uiModeText = defaults.optString("uiMode", "simple")
            val uiMode = UiMode.fromString(uiModeText)
            if (uiMode == null) {
                // 认不出的取值必须被报告：静默回退会让配置错误表现为「设置没生效」。
                warnings += "clientDefaults.uiMode is not 'simple' or 'professional'; using 'simple'"
            }

            // timeoutSeconds 属于 panel 分组（见 client-config 的字段分组）。
            val timeout = panel.optInt("timeoutSeconds", 15)
            val httpPort = defaults.optInt("httpProxyPort", 7890)
            val socksPort = defaults.optInt("socksProxyPort", 7891)
            if (httpPort !in 1..65535) {
                warnings += "clientDefaults.httpProxyPort is out of range; using 7890"
            }
            if (socksPort !in 1..65535) {
                warnings += "clientDefaults.socksProxyPort is out of range; using 7891"
            }
            if (httpPort == socksPort) {
                warnings += "httpProxyPort and socksProxyPort are the same; the core will fail to bind"
            }

            return AppConfig(
                applicationName = branding.optString("applicationName", "default")
                    .ifBlank { "default" },
                displayNameZh = names.optString("zh-CN", ""),
                displayNameEn = names.optString("en", ""),
                apiBaseUrl = panel.optString("apiBaseUrl", ""),
                webBaseUrl = panel.optString("webBaseUrl", ""),
                timeoutSeconds = if (timeout in 1..600) timeout else 15,
                defaultUiMode = uiMode ?: UiMode.Simple,
                defaultLanguage = defaults.optString("language", "system"),
                fallbackLanguage = defaults.optString("fallbackLanguage", "en"),
                defaultCore = defaults.optString("core", "sing-box"),
                defaultRoutingPolicy = defaults.optString("routingPolicy", "rule"),
                localProxyHost = defaults.optString("localProxyHost", "127.0.0.1"),
                httpProxyPort = if (httpPort in 1..65535) httpPort else 7890,
                socksProxyPort = if (socksPort in 1..65535) socksPort else 7891,
                tunMtu = tun.optInt("mtu", 1500),
                tunEnableIpv6 = tun.optBoolean("enableIpv6", false),
                warnings = warnings,
            )
        }
    }
}

/**
 * 界面模式 / interface mode.
 *
 * 两种模式共用同一份配置与节点库（设计基线 §4.1）：
 * 切换模式不改变已经建立的行为，只改变呈现与可见选项。
 */
enum class UiMode {
    Simple,
    Professional;

    companion object {
        /** 持久化取值与 client-config.json 保持一致。 */
        fun fromString(text: String): UiMode? = when (text) {
            "simple" -> Simple
            "professional" -> Professional
            // 不认识的值返回 null 而不是猜一个：静默回退会让「配置写错了」
            // 表现成「我的设置没生效」。
            else -> null
        }
    }

    fun toPersisted(): String = when (this) {
        Simple -> "simple"
        Professional -> "professional"
    }
}
