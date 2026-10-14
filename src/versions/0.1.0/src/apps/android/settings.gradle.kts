// Android 工程定义 / Android project settings.
//
// 版本策略：所有插件与依赖都固定到确切版本，不使用 latest 或动态版本号。
// 理由见设计基线：动态版本会让「昨天能编、今天编不过」变成常态，
// 而且构建结果不可复现——同一份源码在不同时间产出不同产物。
pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}

dependencyResolutionManagement {
    // 只允许在 settings 里声明仓库，模块内不得再各自声明，
    // 否则依赖来源会随模块增加而失控。
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
    }
}

rootProject.name = "proxyclient-android"

// 单一模块。拆分模块的前提是有真实的编译隔离需求，
// 为了「看起来架构清晰」提前拆分只会让构建变慢、配置变复杂。
include(":app")
