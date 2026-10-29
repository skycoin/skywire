package com.skycoin.skywire.ui.components

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.input.nestedscroll.NestedScrollConnection
import androidx.compose.ui.input.nestedscroll.NestedScrollSource
import androidx.compose.ui.layout.layout
import androidx.compose.ui.platform.LocalConfiguration
import kotlin.math.roundToInt

/** Below this height (a phone in landscape, about 400 dp) screens give their chrome back to content. */
const val SHORT_SCREEN_DP = 480

@Composable
fun isShortScreen(): Boolean = LocalConfiguration.current.screenHeightDp < SHORT_SCREEN_DP

/** Put [modifier] on the header and [connection] on (a parent of) the list it sits above. */
class CollapsingHeader(val modifier: Modifier, val connection: NestedScrollConnection)

/**
 * A header the list scrolls away first and pulls back last. Disabled, the
 * header stays put and the connection consumes nothing.
 */
@Composable
fun rememberCollapsingHeader(enabled: Boolean): CollapsingHeader {
    var height by remember { mutableIntStateOf(0) }
    var offset by remember { mutableFloatStateOf(0f) }
    LaunchedEffect(enabled) { if (!enabled) offset = 0f }
    val connection = remember(enabled) {
        object : NestedScrollConnection {
            // Scrolling up: the header goes before the list moves.
            override fun onPreScroll(available: Offset, source: NestedScrollSource): Offset {
                if (!enabled || available.y >= 0) return Offset.Zero
                val next = (offset + available.y).coerceIn(-height.toFloat(), 0f)
                val used = next - offset
                offset = next
                return Offset(0f, used)
            }

            // Scrolling down: the list reaches its top before the header returns.
            override fun onPostScroll(consumed: Offset, available: Offset, source: NestedScrollSource): Offset {
                if (!enabled || available.y <= 0) return Offset.Zero
                val next = (offset + available.y).coerceIn(-height.toFloat(), 0f)
                val used = next - offset
                offset = next
                return Offset(0f, used)
            }
        }
    }
    val modifier = Modifier
        .clipToBounds()
        .layout { measurable, constraints ->
            val placeable = measurable.measure(constraints)
            height = placeable.height
            val shown = (placeable.height + offset.roundToInt()).coerceAtLeast(0)
            layout(placeable.width, shown) { placeable.place(0, offset.roundToInt()) }
        }
    return CollapsingHeader(modifier, connection)
}
