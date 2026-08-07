package com.skycoin.skywire.ui.components

import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.unit.Dp

/**
 * The design's one piece of ambient motion: a disc that swells from a
 * control and fades as it goes, over and over. Drawn *behind* whatever it
 * belongs to — the caller stacks it under the control in a Box. Used by the
 * bar's cloud button and Home's Connect while the phone is on the network.
 */
@Composable
fun PulseRing(size: Dp, color: Color, modifier: Modifier = Modifier) {
    val pulse = rememberInfiniteTransition(label = "pulseRing")
    val phase by pulse.animateFloat(
        initialValue = 0f,
        targetValue = 1f,
        animationSpec = infiniteRepeatable(
            animation = tween(durationMillis = 2600, easing = LinearEasing),
            repeatMode = RepeatMode.Restart,
        ),
        label = "pulseRingPhase",
    )
    Box(
        modifier
            .size(size)
            .graphicsLayer {
                val scale = 0.9f + 0.45f * phase
                scaleX = scale
                scaleY = scale
                alpha = 0.45f * (1f - phase)
            }
            .background(color, CircleShape),
    )
}
