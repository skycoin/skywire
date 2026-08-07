package com.skycoin.skywire.ui.theme

import androidx.compose.material3.Typography
import androidx.compose.ui.text.font.Font
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import com.skycoin.skywire.R

/**
 * Two families, both variable-weight single files (minSdk 26, so the wght
 * axis is always honoured and each [Font] entry below is a real instance,
 * not a faux weight):
 *
 *  - **Quicksand** carries every display/headline/title role — the rounded
 *    geometric voice of the redesign. Titles are Bold throughout; the family
 *    has no meaningful hierarchy below 600 at title sizes.
 *  - **Nunito** carries body and label text: the same roundness, but drawn
 *    for small sizes and long lines, which Quicksand is not.
 */
val QuicksandFamily = FontFamily(
    Font(R.font.quicksand_variable, FontWeight.Medium),
    Font(R.font.quicksand_variable, FontWeight.SemiBold),
    Font(R.font.quicksand_variable, FontWeight.Bold),
)

val NunitoFamily = FontFamily(
    Font(R.font.nunito_variable, FontWeight.Normal),
    Font(R.font.nunito_variable, FontWeight.Medium),
    Font(R.font.nunito_variable, FontWeight.SemiBold),
    Font(R.font.nunito_variable, FontWeight.Bold),
)

private val Base = Typography()

/**
 * Quicksand for the roles that name things (display/headline/title), Nunito
 * for the roles that explain them (body) and operate them (label). Body sits
 * at SemiBold and labels at Bold — the design's text is deliberately chunky,
 * and anything lighter reads faint the moment it lands on the blue-tinted
 * cards.
 */
val SkywireTypography = Base.copy(
    displayLarge = Base.displayLarge.copy(fontFamily = QuicksandFamily, fontWeight = FontWeight.Bold),
    displayMedium = Base.displayMedium.copy(fontFamily = QuicksandFamily, fontWeight = FontWeight.Bold),
    displaySmall = Base.displaySmall.copy(fontFamily = QuicksandFamily, fontWeight = FontWeight.Bold),
    headlineLarge = Base.headlineLarge.copy(fontFamily = QuicksandFamily, fontWeight = FontWeight.Bold),
    headlineMedium = Base.headlineMedium.copy(fontFamily = QuicksandFamily, fontWeight = FontWeight.Bold),
    headlineSmall = Base.headlineSmall.copy(fontFamily = QuicksandFamily, fontWeight = FontWeight.Bold),
    titleLarge = Base.titleLarge.copy(fontFamily = QuicksandFamily, fontWeight = FontWeight.Bold),
    titleMedium = Base.titleMedium.copy(fontFamily = QuicksandFamily, fontWeight = FontWeight.Bold),
    titleSmall = Base.titleSmall.copy(fontFamily = QuicksandFamily, fontWeight = FontWeight.Bold),
    bodyLarge = Base.bodyLarge.copy(fontFamily = NunitoFamily, fontWeight = FontWeight.SemiBold),
    bodyMedium = Base.bodyMedium.copy(fontFamily = NunitoFamily, fontWeight = FontWeight.SemiBold),
    bodySmall = Base.bodySmall.copy(fontFamily = NunitoFamily, fontWeight = FontWeight.SemiBold),
    labelLarge = Base.labelLarge.copy(fontFamily = NunitoFamily, fontWeight = FontWeight.Bold),
    labelMedium = Base.labelMedium.copy(fontFamily = NunitoFamily, fontWeight = FontWeight.Bold),
    labelSmall = Base.labelSmall.copy(fontFamily = NunitoFamily, fontWeight = FontWeight.Bold),
)
