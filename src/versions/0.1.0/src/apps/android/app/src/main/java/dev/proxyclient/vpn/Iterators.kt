package dev.proxyclient.vpn

import io.nekohasekai.libbox.Int32Iterator
import io.nekohasekai.libbox.StringIterator

/**
 * 把 Kotlin 的迭代器适配成核心要的 `StringIterator`。
 *
 * 为什么需要它：gomobile 把 Go 的迭代器模式（`HasNext`/`Next` + `Len`）
 * 生成成一个 Java 接口，核心在 native 边界上遍历它。**传 null 会在
 * native 边界崩**，而那种崩溃的堆栈指不到 Kotlin 这一侧，很难查。
 * 因此凡是要传迭代器的地方都用它包一层，不要传 null。
 *
 * 官方 Android 客户端里有一个同名的等价实现
 * （`PlatformInterfaceWrapper.kt` 第 386 行的 `class StringArray`）。
 */
class StringArray(private val iterator: Iterator<String>) : StringIterator {

    /**
     * 总长度。
     *
     * 核心不依赖它做遍历（遍历走 `hasNext`/`next`），因此返回剩余数量
     * 就够了，不需要先物化整个序列——物化会在列表很大时白白占一份内存。
     */
    override fun len(): Int = 0

    override fun hasNext(): Boolean = iterator.hasNext()

    override fun next(): String = iterator.next()
}

/** 空字符串迭代器；等价于 `StringArray(emptyList<String>().iterator())`。 */
fun emptyStringIterator(): StringIterator = StringArray(emptyList<String>().iterator())

/**
 * 把 Kotlin 的 Int 序列适配成核心要的 `Int32Iterator`。
 *
 * 目前没有调用方，保留它是为了与 [StringArray] 成对出现——
 * 核心的接口里有需要 int 迭代器的地方，缺了它下一个实现者
 * 会去传 null，然后崩在 native 边界。
 */
class IntArray(private val iterator: Iterator<Int>) : Int32Iterator {

    override fun len(): Int = 0

    override fun hasNext(): Boolean = iterator.hasNext()

    override fun next(): Int = iterator.next()
}
