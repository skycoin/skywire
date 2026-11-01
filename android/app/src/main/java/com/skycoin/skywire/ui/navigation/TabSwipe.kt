package com.skycoin.skywire.ui.navigation

import androidx.compose.animation.AnimatedContentTransitionScope
import androidx.compose.animation.core.animate
import androidx.compose.animation.core.tween
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.systemGestures
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.State
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.input.pointer.positionChangeIgnoreConsumed
import androidx.compose.ui.input.pointer.positionChanged
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.unit.dp
import androidx.navigation.NavBackStackEntry
import kotlinx.coroutines.launch
import kotlin.math.abs
import kotlin.math.max
import kotlin.math.sign

/** The bar's tabs left to right, the order a swipe walks them in. */
val TAB_ORDER = listOf(Routes.HOME, Routes.CHAT, Routes.HUB, Routes.WALLET, Routes.SETTINGS)

/** How long the next tab takes to slide in, and the page to settle after a drag. */
const val TAB_SLIDE_MS = 250

/** 1 moving to a tab on the right, -1 to one on the left, null unless both ends are tabs. */
fun AnimatedContentTransitionScope<NavBackStackEntry>.tabStep(): Int? {
    val from = TAB_ORDER.indexOf(initialState.destination.route)
    val to = TAB_ORDER.indexOf(targetState.destination.route)
    return if (from < 0 || to < 0 || from == to) null else (to - from).sign
}

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
 * A horizontal swipe calls [onSwipe] with true for the next tab, either quick or
 * past most of the width, and the page follows the finger until then. It runs
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
    // Where the page is while it follows the finger. It eases back to 0 as the
    // next tab slides in, which keeps the two moves one motion.
    var dragX by remember { mutableFloatStateOf(0f) }
    val scope = rememberCoroutineScope()
    if (!enabled) return this
    // The layer goes inside the pointer input, so moving the page does not move the finger's coordinates.
    return pointerInput(edgeLeft, edgeRight) {
        val minDistance = 72.dp.toPx()
        val slop = viewConfiguration.touchSlop
        awaitEachGesture {
            val down = awaitFirstDown(requireUnconsumed = false)
            if (down.position.x < edgeLeft || down.position.x > size.width - edgeRight) return@awaitEachGesture
            var travel = Offset.Zero
            var next: Boolean? = null
            try {
                while (true) {
                    val event = awaitPointerEvent()
                    if (event.changes.size > 1) return@awaitEachGesture
                    val change = event.changes.firstOrNull { it.id == down.id } ?: return@awaitEachGesture
                    if (change.positionChanged() && change.isConsumed) return@awaitEachGesture
                    travel += change.positionChangeIgnoreConsumed()
                    val sideways = abs(travel.x) > 2 * abs(travel.y)
                    if (sideways && abs(travel.x) > slop) dragX = travel.x
                    if (!change.pressed) {
                        // A long press that then drags is selecting text, not swiping.
                        val quick = change.uptimeMillis - down.uptimeMillis < SWIPE_MAX_MS
                        val far = abs(travel.x) > max(minDistance, size.width * 0.2f)
                        if (sideways && (quick && far || abs(travel.x) > size.width * 0.4f)) next = travel.x < 0
                        return@awaitEachGesture
                    }
                }
            } finally {
                next?.let(action)
                val from = dragX
                if (from != 0f) scope.launch { animate(from, 0f, animationSpec = tween(TAB_SLIDE_MS)) { v, _ -> dragX = v } }
            }
        }
    }.graphicsLayer { translationX = dragX }
}

private const val SWIPE_MAX_MS = 600L
