# ProGuard / R8 规则 / shrinker rules.
#
# 目标不是「尽量小」，而是「不要因为混淆而坏掉」。
# 代理客户端的体积主要来自核心二进制，混淆应用代码省下的空间很有限，
# 而混淆导致的运行时崩溃非常难查（堆栈里全是 a.b.c）。

# ---- 保留 Compose 运行时的必要信息 -------------------------------------------
# Compose 依赖反射读取 @Composable 的元数据；被裁剪掉会表现为
# 界面空白或随机崩溃，而不是编译错误。
-keep class androidx.compose.runtime.** { *; }
-dontwarn androidx.compose.**

# ---- 枚举 ----------------------------------------------------------------
# 我们用枚举表示界面模式与连接状态，并按其名称做持久化与显示。
# 混淆名称会让已保存的偏好失效（读出来是未识别的值，
# 表现成「记住界面模式」无效）。
-keepclassmembers enum * {
    public static **[] values();
    public static ** valueOf(java.lang.String);
}

# ---- 序列化与反射 ---------------------------------------------------------
# 保留注解本身，以及带注解的成员的名称。
-keepattributes *Annotation*, InnerClasses, Signature, EnclosingMethod
-keepclassmembers class * {
    @androidx.annotation.Keep <methods>;
}

# ---- 调试信息 -------------------------------------------------------------
# 保留行号：崩溃堆栈里有行号才能定位问题。
# 不保留源文件名是为了不泄漏构建机上的路径。
-keepattributes SourceFile,LineNumberTable
-renamesourcefileattribute SourceFile

# ---- 明确不保留的东西 -----------------------------------------------------
# 不保留全部类：那等于关掉混淆，也就得不到 R8 的优化。
