package dev.proxyclient.ui

import android.os.Build
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.dynamicDarkColorScheme
import androidx.compose.material3.dynamicLightColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext

/**
 * 配色 / colour scheme.
 *
 * 在 Android 12 及以上使用系统的动态取色，让应用看起来像系统的一部分；
 * 更低版本回退到固定配色。这不是装饰性选择：用户对代理类应用的信任
 * 主要来自「看起来是否像个正经应用」，而配色不一致是最直观的减分项。
 */
private val LightColors = lightColorScheme(
    primary = Color(0xFF1B4D3E),
    onPrimary = Color.White,
    secondary = Color(0xFF4A6360),
    surface = Color(0xFFFFFBFE),
)

private val DarkColors = darkColorScheme(
    primary = Color(0xFF9CD6C0),
    onPrimary = Color(0xFF00382A),
    secondary = Color(0xFFB1CCC7),
    surface = Color(0xFF191C1B),
)

@Composable
fun ProxyClientTheme(
    darkTheme: Boolean = isSystemInDarkTheme(),
    content: @Composable () -> Unit,
) {
    val colorScheme = when {
        // 动态取色只在 Android 12 及以上可用。
        Build.VERSION.SDK_INT >= Build.VERSION_CODES.S -> {
            val context = LocalContext.current
            if (darkTheme) dynamicDarkColorScheme(context) else dynamicLightColorScheme(context)
        }
        darkTheme -> DarkColors
        else -> LightColors
    }

    MaterialTheme(colorScheme = colorScheme, content = content)
}
