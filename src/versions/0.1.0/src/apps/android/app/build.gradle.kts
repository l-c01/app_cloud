// app 模块配置 / app module configuration.
//
// 三条硬规则（设计基线 §10.2 与 ADR-0003）：
//   1. 包名来自配置，未配置时使用**明确标注的内部测试身份**，
//      而不是悄悄用一个看起来正式的名字；
//   2. release 构建缺少签名密钥时**必须失败**，绝不临时生成随机密钥；
//   3. 语言资源与桌面端共用仓库里的 locales/，不各存一份。

import java.util.Properties

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
}

// -----------------------------------------------------------------------------
// 定位仓库根目录
//
// 不用固定层数的相对路径：目录一调整就会静默指向错误位置，
// 而 Copy 任务找不到源目录时不会报错，只是什么都不拷。
// -----------------------------------------------------------------------------
val repoRoot: File = run {
    var dir: File? = rootProject.projectDir
    while (dir != null && !File(dir, ".git").exists()) {
        dir = dir.parentFile
    }
    dir ?: rootProject.projectDir
}

// -----------------------------------------------------------------------------
// 应用身份
//
// androidPackageId 在 design/client-config.json 里默认留空。留空时使用一个
// **明确标注为内部测试**的包名，而不是猜一个正式名字：
// 正式包名一旦发布就无法更改，猜错等于把用户永久绑定在一个占位身份上。
// -----------------------------------------------------------------------------
val configuredApplicationId: String = run {
    val configFile = File(repoRoot, "design/client-config.json")
    if (configFile.exists()) {
        // 只做最小解析：不引入 JSON 库，因为这里只需要一个字段。
        val text = configFile.readText()
        val match = Regex("\"androidPackageId\"\\s*:\\s*\"([^\"]*)\"").find(text)
        val value = match?.groupValues?.get(1).orEmpty()
        if (value.isNotBlank()) value else "dev.proxyclient.internaltest"
    } else {
        "dev.proxyclient.internaltest"
    }
}

// -----------------------------------------------------------------------------
// 签名
//
// 密钥来自环境变量（CI 由仓库 Secrets 注入）。缺任何一项都不配置 release 签名，
// 并在 release 构建时显式失败——见文件末尾的检查任务。
// -----------------------------------------------------------------------------
val keystorePath: String? = System.getenv("ANDROID_KEYSTORE_PATH")
val keystorePassword: String? = System.getenv("ANDROID_KEYSTORE_PASSWORD")
val keyAlias: String? = System.getenv("ANDROID_KEY_ALIAS")
val keyPassword: String? = System.getenv("ANDROID_KEY_PASSWORD")
val hasReleaseSigning = listOf(keystorePath, keystorePassword, keyAlias, keyPassword)
    .all { !it.isNullOrBlank() } && File(keystorePath!!).exists()

android {
    namespace = "dev.proxyclient"
    compileSdk = 35

    defaultConfig {
        applicationId = configuredApplicationId
        minSdk = 26
        targetSdk = 35
        versionCode = 1
        versionName = "0.1.0"

        // ABI 过滤。
        //
        // 默认声明全部三个目标 ABI；CI 按目标逐个构建时用
        // -Ppcx.abiFilter=<abi> 收窄到单个，否则三个 ABI 的构建任务会
        // 各自打包出三份 APK，与「一个目标一份产物」的约定不符。
        val requestedAbi = (project.findProperty("pcx.abiFilter") as String?)
            ?.takeIf { it.isNotBlank() }
        ndk {
            abiFilters += requestedAbi?.let { listOf(it) }
                ?: listOf("arm64-v8a", "armeabi-v7a", "x86")
        }
    }

    signingConfigs {
        if (hasReleaseSigning) {
            create("release") {
                storeFile = File(keystorePath!!)
                storePassword = keystorePassword
                this.keyAlias = keyAlias
                keyPassword = keyPassword
            }
        }
    }

    buildTypes {
        debug {
            // 调试包用独立后缀：否则调试包与正式包无法共存，
            // 测试时不得不先卸载正式包（会丢数据）。
            applicationIdSuffix = ".debug"
        }
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro",
            )
            if (hasReleaseSigning) {
                signingConfig = signingConfigs.getByName("release")
            }
        }
    }

    buildFeatures {
        compose = true
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    kotlinOptions {
        jvmTarget = "17"
        // 警告是否当错误，由 pcx.strictWarnings 控制，默认关闭。
        //
        // 与桌面端 CMakeLists 里的 PCX_STRICT_CONVERSION 是同一条理由：
        // 在代码从未编译过的情况下把全部警告当错误，得到的是
        // 「改一处、推一次、再报一处」的长循环，而不是更干净的代码——
        // 每轮都要消耗一次 CI 构建。
        //
        // 警告仍然照常输出（看得见问题），只是不挡住第一次构建成功。
        // 等功能验证过之后，用 -Ppcx.strictWarnings=true 收紧。
        val strictWarnings = (project.findProperty("pcx.strictWarnings") as String?)
            ?.toBooleanStrictOrNull() ?: false
        allWarningsAsErrors = strictWarnings
    }

    packaging {
        resources {
            excludes += "/META-INF/{AL2.0,LGPL2.1}"
        }
    }
}

// -----------------------------------------------------------------------------
// 语言资源
//
// 与桌面端**共用仓库里的 locales/**：两份翻译文件必然漂移，
// 最后表现为同一个错误在两个平台上显示不同的话。
// -----------------------------------------------------------------------------
val copyLocales by tasks.registering(Copy::class) {
    val source = File(repoRoot, "locales")
    // 先断言源目录存在：Copy 找不到源目录时不报错，只是什么都不拷，
    // 结果是应用里没有语言资源而构建依然「成功」。
    doFirst {
        require(source.isDirectory) { "locales directory not found at $source" }
    }
    from(source)
    into(layout.projectDirectory.dir("src/main/assets/locales"))
}

// 出厂配置模板同样随包分发，供用户在无法修改安装目录时参考。
val copyConfig by tasks.registering(Copy::class) {
    val source = File(repoRoot, "design/client-config.json")
    doFirst {
        require(source.isFile) { "client-config.json not found at $source" }
    }
    from(source)
    into(layout.projectDirectory.dir("src/main/assets/config"))
}

tasks.named("preBuild") {
    dependsOn(copyLocales, copyConfig)
}

// -----------------------------------------------------------------------------
// release 构建必须签名
//
// 没有这道检查时，Gradle 会产出一个未签名的 APK，文件名看起来完全正常。
// 用户拿到后无法安装，而错误信息与真正的原因（缺少密钥）相距很远。
// -----------------------------------------------------------------------------
tasks.matching { it.name.contains("Release") && it.name.startsWith("assemble") }.configureEach {
    doFirst {
        if (!hasReleaseSigning) {
            throw GradleException(
                "Refusing to build a release APK without signing keys. " +
                    "Provide ANDROID_KEYSTORE_PATH, ANDROID_KEYSTORE_PASSWORD, " +
                    "ANDROID_KEY_ALIAS and ANDROID_KEY_PASSWORD. " +
                    "Never generate a random release key: it would make updates impossible.",
            )
        }
    }
}

dependencies {
    // 版本全部固定。Compose BOM 也是固定版本，而不是 latest。
    implementation(platform("androidx.compose:compose-bom:2024.12.01"))
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.ui:ui-tooling-preview")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.activity:activity-compose:1.9.3")
    implementation("androidx.lifecycle:lifecycle-runtime-compose:2.8.7")

    debugImplementation("androidx.compose.ui:ui-tooling")

    testImplementation("junit:junit:4.13.2")
}
