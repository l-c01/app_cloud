package dev.proxyclient.ui

import dev.proxyclient.i18n.LocaleCatalog

/**
 * 文案渲染器 / message renderer.
 *
 * 把「用哪种语言 + 查表 + 填占位符」收在一处，而不是每个屏幕各写一遍：
 * 各写一遍时最先分叉的总是「用哪个语言」。
 *
 * 缺键返回**键名本身**（LocaleCatalog 的行为）：界面会显示 ui.login.title，
 * 问题立刻可见；返回空串则会变成难以察觉的空白。
 */
class Messages(private val catalog: LocaleCatalog, val language: String) {

    operator fun get(key: String, vararg args: Any): String =
        catalog.translate(language, key, *args)

    /**
     * 渲染服务端给出的文本。
     *
     * 服务端的 warnings 既可能是文案键（界面查表本地化），也可能是已经
     * 成文的诊断文本。查表查不到时原样返回，两种情况都得到正确结果——
     * 因此这里不需要先判断它是哪一种。
     */
    fun server(text: String): String = if (text.isEmpty()) "" else catalog.translate(language, text)
}
