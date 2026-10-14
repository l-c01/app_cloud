// 根构建脚本 / root build script.
//
// 只做两件事：声明插件版本、把仓库级配置集中在一处。
// 具体模块配置在 app/build.gradle.kts。
//
// 版本固定策略：见 settings.gradle.kts 的说明。

plugins {
    // AGP 与 Kotlin 版本必须匹配：两者不匹配时的报错往往出现在
    // 毫不相关的地方（例如资源合并），因此这里并排声明便于核对。
    id("com.android.application") version "8.7.3" apply false
    id("org.jetbrains.kotlin.android") version "2.1.0" apply false
    // Kotlin 2.0 起 Compose 编译器是独立的 Kotlin 插件，
    // 不再通过 composeOptions.kotlinCompilerExtensionVersion 配置。
    id("org.jetbrains.kotlin.plugin.compose") version "2.1.0" apply false
}

tasks.register("clean", Delete::class) {
    delete(rootProject.layout.buildDirectory)
}
