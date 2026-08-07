package com.skycoin.skywire.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Shapes
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp

/**
 * Brand tokens: blue-dominant, no dynamic (Material You) color — the palette
 * is brand-locked on both themes. Light is the design's native theme (white
 * surfaces, faint blue-tinted cards with hairline borders); dark is the same
 * hue family moved onto deep navy rather than grey or black, so the blue
 * still reads as the brand and not as decoration.
 */
val SkywireBlue = Color(0xFF0F7BF4)

/** The gradient's deep stops — hero cards and the raised nav button. */
val SkywireBlueDeep = Color(0xFF0B57C9)
val SkywireNavy = Color(0xFF0A3F97)

/**
 * Accents with no Material role of their own, identical on both themes: the
 * greens hold up on white and on navy alike.
 */
object SkyAccents {
    /** Running / healthy / gains. */
    val success = Color(0xFF22C275)

    /** The success green's bright form — dots and toggle tracks ON blue. */
    val successBright = Color(0xFF5CF2A8)

    /** Starting / restarting / degraded. */
    val warning = Color(0xFFF59E0B)

    /** Off / unprotected, where Material's error is too dark for blue. */
    val dangerBright = Color(0xFFFF7B6E)
}

/** Hero surface fill: the deep three-stop diagonal from the design. */
val SkyHeroGradient = Brush.linearGradient(
    colors = listOf(Color(0xFF1685FA), SkywireBlueDeep, SkywireNavy),
    start = Offset.Zero,
    end = Offset.Infinite,
)

/** Primary round action (Connect, the raised nav cloud): lighter, punchier. */
val SkyButtonGradient = Brush.linearGradient(
    colors = listOf(Color(0xFF3D9BFF), SkywireBlue, Color(0xFF0B4FBC)),
    start = Offset.Zero,
    end = Offset.Infinite,
)

private val LightColors = lightColorScheme(
    primary = SkywireBlue,
    onPrimary = Color.White,
    secondary = SkywireBlue,
    onSecondary = Color.White,
    background = Color.White,
    onBackground = Color(0xFF0B1526),
    surface = Color.White,
    onSurface = Color(0xFF0B1526),
    // Cards: a blue-tinted near-white, kept just off pure white — the
    // hairline border from outlineVariant is what draws the card's edge,
    // so the fill can stay bright and let the ink carry the contrast.
    surfaceVariant = Color(0xFFFAFCFF),
    // Darker than the mock's caption grey on purpose: this role carries
    // real sentences (Settings bodies, subtitles), and at the mock's value
    // they read faint on white. The mock keeps its washier greys for
    // decoration (outline below), not for prose.
    onSurfaceVariant = Color(0xFF44536B),
    // Darker than a decorative grey, for the same reason as the role above:
    // this one is not only hairlines. It tints the bottom bar's resting
    // icons and every "off" glyph in the hub, and at the mock's value those
    // read as switched-off rather than as not-currently-selected. ~4.9:1 on
    // white, which is the floor for something you are meant to aim at.
    outline = Color(0xFF56657C),
    outlineVariant = Color(0xFFE7EEF9),
    primaryContainer = Color(0xFFE4EEFD),
    onPrimaryContainer = SkywireBlueDeep,
    // Container roles drive tonal buttons and selection pills — without
    // these, M3 baseline purple leaks in.
    secondaryContainer = Color(0xFFEEF3FB),
    onSecondaryContainer = Color(0xFF101A2B),
    surfaceContainerLowest = Color.White,
    surfaceContainerLow = Color(0xFFFBFCFE),
    surfaceContainer = Color(0xFFF6F9FD),
    surfaceContainerHigh = Color(0xFFF0F5FC),
    surfaceContainerHighest = Color(0xFFE9EFF8),
)

private val DarkColors = darkColorScheme(
    // Brighter than the brand blue: on navy, #0F7BF4 sinks. Dark ink on it
    // rather than white — the button should read as a light source.
    primary = Color(0xFF4AA3FF),
    onPrimary = Color(0xFF04264D),
    secondary = Color(0xFF4AA3FF),
    onSecondary = Color(0xFF04264D),
    background = Color(0xFF0A101C),
    onBackground = Color(0xFFECF2FB),
    surface = Color(0xFF0A101C),
    onSurface = Color(0xFFECF2FB),
    surfaceVariant = Color(0xFF121C2F),
    onSurfaceVariant = Color(0xFF93A3BD),
    outline = Color(0xFF64758F),
    outlineVariant = Color(0xFF1E2B45),
    primaryContainer = Color(0xFF0E3D77),
    onPrimaryContainer = Color(0xFFCFE4FF),
    secondaryContainer = Color(0xFF16233B),
    onSecondaryContainer = Color(0xFFD7E4F6),
    surfaceContainerLowest = Color(0xFF070D17),
    surfaceContainerLow = Color(0xFF0C1424),
    surfaceContainer = Color(0xFF101A2E),
    surfaceContainerHigh = Color(0xFF16223A),
    surfaceContainerHighest = Color(0xFF1C2A46),
)

/**
 * The design's radius scale. Generous on purpose: chips at 12, buttons and
 * icon tiles at 16, cards at 22, sheets and the nav shell at 28. Material
 * pulls small/medium/large for its own components, so most of the app picks
 * these up without asking.
 */
private val SkywireShapes = Shapes(
    extraSmall = RoundedCornerShape(8.dp),
    small = RoundedCornerShape(12.dp),
    medium = RoundedCornerShape(16.dp),
    large = RoundedCornerShape(22.dp),
    extraLarge = RoundedCornerShape(28.dp),
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
            shapes = SkywireShapes,
            content = content,
        )
    }
}
