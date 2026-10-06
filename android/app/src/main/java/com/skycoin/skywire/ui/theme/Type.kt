package com.skycoin.skywire.ui.theme

import androidx.compose.material3.Typography
import androidx.compose.ui.text.font.Font
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import com.skycoin.skywire.R

/**
 * Static instances cut from the Quicksand and Nunito variable fonts with fontTools
 * (varLib.instancer). Compose did not apply a variable font's weight, so every
 * weight drew at the file's thin default (Quicksand 300, Nunito 200).
 *
 *  - **Quicksand** carries every display/headline/title role — the rounded
 *    geometric voice of the redesign. Titles are Bold throughout.
 *  - **Nunito** carries body and label text: the same roundness, but drawn
 *    for small sizes and long lines, which Quicksand is not.
 */
val QuicksandFamily = FontFamily(
    Font(R.font.quicksand_bold, FontWeight.Bold),
)

val NunitoFamily = FontFamily(
    Font(R.font.nunito_semibold, FontWeight.SemiBold),
    Font(R.font.nunito_bold, FontWeight.Bold),
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
