package com.skycoin.skywire.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.graphics.Color

/**
 * Brand tokens: blue-dominant, no dynamic (Material You) color — the palette
 * is brand-locked on both themes.
 */
val SkywireBlue = Color(0xFF0072FF)

private val LightColors = lightColorScheme(
    primary = SkywireBlue,
    onPrimary = Color.White,
    secondary = SkywireBlue,
    onSecondary = Color.White,
    background = Color.White,
    onBackground = Color.Black,
    surface = Color.White,
    onSurface = Color.Black,
    surfaceVariant = Color(0xFFF2F6FC),
    onSurfaceVariant = Color(0xFF44474E),
    primaryContainer = Color(0xFFD9E8FF),
    onPrimaryContainer = Color(0xFF00264D),
    // Container roles drive the NavigationBar surface + its selection pill —
    // without these, M3 baseline purple leaks into the bar.
    secondaryContainer = Color(0xFFD9E8FF),
    onSecondaryContainer = Color(0xFF00264D),
    surfaceContainerLowest = Color.White,
    surfaceContainerLow = Color(0xFFF7FAFE),
    surfaceContainer = Color(0xFFF2F6FC),
    surfaceContainerHigh = Color(0xFFECF1FA),
    surfaceContainerHighest = Color(0xFFE6EDF8),
)

private val DarkColors = darkColorScheme(
    primary = SkywireBlue,
    onPrimary = Color.White,
    secondary = SkywireBlue,
    onSecondary = Color.White,
    background = Color.Black,
    onBackground = Color.White,
    // Surfaces near-black per spec, not Material's default dark grey.
    surface = Color(0xFF0A0A0A),
    onSurface = Color.White,
    surfaceVariant = Color(0xFF15181D),
    onSurfaceVariant = Color(0xFFC4C6CF),
    primaryContainer = Color(0xFF003B80),
    onPrimaryContainer = Color(0xFFD9E8FF),
    // Same container-role overrides as light: near-black blue-tinted bar,
    // blue selection pill — no baseline purple.
    secondaryContainer = Color(0xFF003B80),
    onSecondaryContainer = Color(0xFFD9E8FF),
    surfaceContainerLowest = Color(0xFF060708),
    surfaceContainerLow = Color(0xFF0C0E11),
    surfaceContainer = Color(0xFF101216),
    surfaceContainerHigh = Color(0xFF15181D),
    surfaceContainerHighest = Color(0xFF1A1E24),
)

/**
 * Whether the app resolved to its dark half. `isSystemInDarkTheme()` is not
 * the same question — the user's own Light/Dark override sits on top of it —
 * and screens that hand a colour scheme to something outside Compose need the
 * answer after that override, not before. The embedded SkyChat page is the
 * one caller today: it has a matching pair of themes and no way to know which
 * one the app is in.
 */
val LocalDarkTheme = staticCompositionLocalOf { true }

@Composable
fun SkywireTheme(
    darkTheme: Boolean = isSystemInDarkTheme(),
    content: @Composable () -> Unit,
) {
    CompositionLocalProvider(LocalDarkTheme provides darkTheme) {
        MaterialTheme(
            colorScheme = if (darkTheme) DarkColors else LightColors,
            typography = SkywireTypography,
            content = content,
        )
    }
}
