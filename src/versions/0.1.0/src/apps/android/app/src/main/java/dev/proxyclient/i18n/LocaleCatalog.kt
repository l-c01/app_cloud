package dev.proxyclient.i18n

import android.content.Context
import org.json.JSONObject

/**
 * 多语言目录 / localisation catalog.
 *
 * 与桌面端和 Go 端**共用仓库里的 locales/*.json**，不另存一份翻译。
 * 理由：两份翻译文件必然漂移，最后表现为同一个错误在不同平台上显示不同的话。
 * 键集合与占位符的一致性由 tools/scripts/check-i18n-keys.ps1 统一校验。
 *
 * 约定（见 design/architecture/i18n-convention.md）：
 *  - 位置参数 {0}、{1}；
 *  - 缺键返回**键名本身**，而不是空串——界面会显示 panel.unauthorized，
 *    问题立刻可见；返回空串则会变成难以察觉的空白；
 *  - 越界或非法占位符**原样保留**，同样是为了让缺陷可见；
 *  - 语言协商顺序：请求 → 精确 → 主语言 → 兜底 → 任意可用。
 */
class LocaleCatalog private constructor(
    private val catalogs: Map<String, Map<String, String>>,
    val fallback: String,
) {

    /**
     * 语言协商 / negotiate the language to use.
     *
     * @param requested 用户选择；"system" 或空表示跟随系统。
     */
    fun negotiate(requested: String): String {
        if (catalogs.isEmpty()) return ""

        val want = when (val normalized = normalize(requested)) {
            "", "system" -> normalize(java.util.Locale.getDefault().toString())
            else -> normalized
        }

        // 1) 精确匹配
        catalogs[want]?.let { return want }

        // 2) 主语言匹配（zh-Hans → zh-CN；en-US → en）
        val primary = want.substringBefore('-')
        if (primary.isNotEmpty()) {
            catalogs[primary]?.let { return primary }
            catalogs.keys.sorted().firstOrNull { it.substringBefore('-') == primary }
                ?.let { return it }
        }

        // 3) 兜底语言
        catalogs[fallback]?.let { return fallback }
        val fallbackPrimary = fallback.substringBefore('-')
        catalogs.keys.sorted().firstOrNull { it.substringBefore('-') == fallbackPrimary }
            ?.let { return it }

        // 4) 任意可用语言：保证总能返回一个能用的语言，而不是空串
        return catalogs.keys.sorted().first()
    }

    /**
     * 取一条文案并按位置参数格式化。
     *
     * @param language 已协商的语言。
     * @param key 文案键。
     * @param args 位置参数。
     */
    fun translate(language: String, key: String, vararg args: Any): String {
        // 注意不要把这个局部变量命名为 format：它会遮蔽伴生对象里的
        // format() 函数，导致下面那一行被解析成「调用一个字符串」。
        val template = lookup(language, key) ?: lookup(fallback, key) ?: return key
        return format(template, args)
    }

    fun languages(): List<String> = catalogs.keys.sorted()

    private fun lookup(language: String, key: String): String? =
        catalogs[normalize(language)]?.get(key)?.takeIf { it.isNotEmpty() }

    companion object {
        private const val ASSET_DIR = "locales"

        /**
         * 从 assets 加载语言目录。
         *
         * 单个文件失败**不中断**整体加载：中文加载失败而英文可用，
         * 远好于整个界面没有文案。
         */
        fun load(context: Context, fallback: String = "en"): LocaleCatalog {
            val catalogs = mutableMapOf<String, Map<String, String>>()
            val assets = context.assets
            val files = runCatching { assets.list(ASSET_DIR)?.toList().orEmpty() }
                .getOrDefault(emptyList())

            for (name in files.filter { it.endsWith(".json") }.sorted()) {
                val text = runCatching {
                    assets.open("$ASSET_DIR/$name").bufferedReader().use { it.readText() }
                }.getOrNull() ?: continue

                val parsed = runCatching { JSONObject(text) }.getOrNull() ?: continue
                val messages = parsed.optJSONObject("messages") ?: continue
                val language = parsed.optString("language")
                    .takeIf { it.isNotBlank() }
                    ?: name.removeSuffix(".json")

                val map = mutableMapOf<String, String>()
                for (key in messages.keys()) {
                    map[key] = messages.optString(key)
                }
                if (map.isNotEmpty()) {
                    catalogs[normalize(language)] = map
                }
            }

            return LocaleCatalog(catalogs, normalize(fallback))
        }

        /** 规范化语言码：小写、下划线转连字符、去空白。 */
        fun normalize(language: String): String =
            language.trim().replace('_', '-').lowercase()

        /**
         * 按位置参数替换 {0}、{1} 等占位符。
         *
         * 越界或非数字占位符原样保留：让翻译缺陷在界面上可见。
         */
        fun format(template: String, args: Array<out Any>): String {
            if (args.isEmpty() || '{' !in template) return template

            val out = StringBuilder(template.length + 16)
            var i = 0
            while (i < template.length) {
                val ch = template[i]
                if (ch != '{') {
                    out.append(ch)
                    i++
                    continue
                }
                val close = template.indexOf('}', i)
                if (close < 0) {
                    out.append(ch)
                    i++
                    continue
                }
                val inner = template.substring(i + 1, close)
                val index = inner.toIntOrNull()
                if (index == null || index < 0 || index >= args.size) {
                    out.append(template, i, close + 1)
                } else {
                    out.append(args[index])
                }
                i = close + 1
            }
            return out.toString()
        }
    }
}
