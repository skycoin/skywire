package com.skycoin.skywire.ui.theme

import androidx.compose.material3.Typography
import androidx.compose.ui.text.font.Font
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import com.skycoin.skywire.R

/**
 * The Skycoin brand typeface (res/font/): Light / Regular / Bold + italics.
 * Applied across the whole Material 3 type scale so every screen inherits it.
 */
val SkycoinFontFamily = FontFamily(
    Font(R.font.skycoin_light, FontWeight.Light),
    Font(R.font.skycoin_light_italic, FontWeight.Light, FontStyle.Italic),
    Font(R.font.skycoin_regular, FontWeight.Normal),
    Font(R.font.skycoin_regular_italic, FontWeight.Normal, FontStyle.Italic),
    Font(R.font.skycoin_bold, FontWeight.Bold),
    Font(R.font.skycoin_bold_italic, FontWeight.Bold, FontStyle.Italic),
)

private fun Typography.withFontFamily(family: FontFamily): Typography = copy(
    displayLarge = displayLarge.copy(fontFamily = family),
    displayMedium = displayMedium.copy(fontFamily = family),
    displaySmall = displaySmall.copy(fontFamily = family),
    headlineLarge = headlineLarge.copy(fontFamily = family),
    headlineMedium = headlineMedium.copy(fontFamily = family),
    headlineSmall = headlineSmall.copy(fontFamily = family),
    titleLarge = titleLarge.copy(fontFamily = family),
    titleMedium = titleMedium.copy(fontFamily = family),
    titleSmall = titleSmall.copy(fontFamily = family),
    bodyLarge = bodyLarge.copy(fontFamily = family),
    bodyMedium = bodyMedium.copy(fontFamily = family),
    bodySmall = bodySmall.copy(fontFamily = family),
    labelLarge = labelLarge.copy(fontFamily = family),
    labelMedium = labelMedium.copy(fontFamily = family),
    labelSmall = labelSmall.copy(fontFamily = family),
)

private val Base = Typography().withFontFamily(SkycoinFontFamily)

/**
 * Roles whose Material default weight is Medium (500) are pinned to Bold: the
 * family ships no 500 cut, and unpinned they would silently resolve to Regular
 * and flatten the title/label emphasis hierarchy.
 */
val SkywireTypography = Base.copy(
    titleMedium = Base.titleMedium.copy(fontWeight = FontWeight.Bold),
    titleSmall = Base.titleSmall.copy(fontWeight = FontWeight.Bold),
    labelLarge = Base.labelLarge.copy(fontWeight = FontWeight.Bold),
    labelMedium = Base.labelMedium.copy(fontWeight = FontWeight.Bold),
    labelSmall = Base.labelSmall.copy(fontWeight = FontWeight.Bold),
)
