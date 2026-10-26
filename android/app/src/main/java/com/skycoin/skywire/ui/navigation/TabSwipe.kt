package com.skycoin.skywire.ui.navigation

import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.systemGestures
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.State
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.input.pointer.positionChangeIgnoreConsumed
import androidx.compose.ui.input.pointer.positionChanged
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.unit.dp
import kotlin.math.abs
import kotlin.math.max

/** The bar's tabs left to right, the order a swipe walks them in. */
val TAB_ORDER = listOf(Routes.HOME, Routes.CHAT, Routes.HUB, Routes.WALLET, Routes.SETTINGS)

/** True while a tab's screen owns horizontal drags, so a swipe does not change tabs. */
val tabSwipeHeld: State<Boolean> get() = held

private val held = mutableStateOf(false)

/** Holds tab swipes while [hold] is true, and lets them go when the screen leaves. */
@Composable
fun HoldTabSwipe(hold: Boolean) {
    DisposableEffect(hold) {
        held.value = hold
        onDispose { held.value = false }
    }
}

/**
 * A quick horizontal swipe calls [onSwipe] with true for the next tab. It runs
 * after the children, so a drag a child scrolls with is theirs, and a swipe
 * from the system back-gesture edges is left to the system.
 */
@Composable
fun Modifier.tabSwipe(enabled: Boolean, onSwipe: (next: Boolean) -> Unit): Modifier {
    val action by rememberUpdatedState(onSwipe)
    val density = LocalDensity.current
    val direction = LocalLayoutDirection.current
    val gestures = WindowInsets.systemGestures
    val edgeLeft = gestures.getLeft(density, direction)
    val edgeRight = gestures.getRight(density, direction)
    if (!enabled) return this
    return pointerInput(edgeLeft, edgeRight) {
        val minDistance = 72.dp.toPx()
        awaitEachGesture {
            val down = awaitFirstDown(requireUnconsumed = false)
            if (down.position.x < edgeLeft || down.position.x > size.width - edgeRight) return@awaitEachGesture
            var travel = Offset.Zero
            while (true) {
                val event = awaitPointerEvent()
                if (event.changes.size > 1) return@awaitEachGesture
                val change = event.changes.firstOrNull { it.id == down.id } ?: return@awaitEachGesture
                if (change.positionChanged() && change.isConsumed) return@awaitEachGesture
                travel += change.positionChangeIgnoreConsumed()
                if (!change.pressed) {
                    // A long press that then drags is selecting text, not swiping.
                    val quick = change.uptimeMillis - down.uptimeMillis < SWIPE_MAX_MS
                    val far = abs(travel.x) > max(minDistance, size.width * 0.2f)
                    if (quick && far && abs(travel.x) > 2 * abs(travel.y)) action(travel.x < 0)
                    return@awaitEachGesture
                }
            }
        }
    }
}

private const val SWIPE_MAX_MS = 600L
