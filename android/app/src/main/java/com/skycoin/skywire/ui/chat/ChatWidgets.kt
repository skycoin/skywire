package com.skycoin.skywire.ui.chat

import android.graphics.Bitmap
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Bookmark
import androidx.compose.material.icons.rounded.Campaign
import androidx.compose.material.icons.rounded.Groups
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.skycoin.skywire.chat.ChatImages
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/** Stable colour for a key, so a person keeps the same tint everywhere. */
fun keyColor(key: String): Color {
    var h = 0
    for (c in key) h = h * 31 + c.code
    val hue = ((h ushr 1) % 360).toFloat()
    return Color.hsv(hue, 0.55f, 0.78f)
}

/** First letter of a name, or the first two hex digits of a key. */
fun initials(label: String, key: String): String {
    val name = label.trim()
    if (name.isNotEmpty() && !name.startsWith(key.take(8))) {
        return name.split(' ').filter { it.isNotEmpty() }.take(2).joinToString("") { it.take(1) }.uppercase()
    }
    return key.take(2).uppercase()
}

enum class AvatarKind { PERSON, GROUP, CHANNEL, SAVED }

/**
 * A round avatar: [load] supplies the picture (local first, then published),
 * and until it does — or when there is none — a tinted disc with initials.
 */
@Composable
fun ChatAvatar(
    key: String,
    label: String,
    size: Dp,
    kind: AvatarKind = AvatarKind.PERSON,
    version: Any? = null,
    online: Boolean? = null,
    load: (suspend () -> ByteArray?)? = null,
) {
    val cacheKey = "avatar:$key:$version"
    var bitmap by remember(cacheKey) { mutableStateOf(ChatImages.cached(cacheKey)) }
    LaunchedEffect(cacheKey) {
        if (bitmap != null || load == null) return@LaunchedEffect
        val bytes = runCatching { load() }.getOrNull() ?: return@LaunchedEffect
        val decoded = withContext(Dispatchers.Default) { ChatImages.decode(bytes, 256) } ?: return@LaunchedEffect
        ChatImages.remember(cacheKey, decoded)
        bitmap = decoded
    }
    Box(Modifier.size(size)) {
        val b = bitmap
        if (b != null) {
            Image(
                bitmap = b.asImageBitmap(),
                contentDescription = label,
                contentScale = ContentScale.Crop,
                modifier = Modifier.size(size).clip(CircleShape),
            )
        } else {
            val tint = if (kind == AvatarKind.SAVED) MaterialTheme.colorScheme.primary else keyColor(key)
            Box(
                Modifier.size(size).clip(CircleShape).background(tint),
                contentAlignment = Alignment.Center,
            ) {
                when (kind) {
                    AvatarKind.SAVED -> Icon(Icons.Rounded.Bookmark, null, tint = Color.White, modifier = Modifier.size(size * 0.5f))
                    AvatarKind.GROUP -> Icon(Icons.Rounded.Groups, null, tint = Color.White, modifier = Modifier.size(size * 0.55f))
                    AvatarKind.CHANNEL -> Icon(Icons.Rounded.Campaign, null, tint = Color.White, modifier = Modifier.size(size * 0.55f))
                    AvatarKind.PERSON -> Text(
                        initials(label, key),
                        color = Color.White,
                        fontWeight = FontWeight.Bold,
                        fontSize = (size.value * 0.38f).sp,
                    )
                }
            }
        }
        if (online != null) {
            val dot = size * 0.28f
            Box(
                Modifier
                    .align(Alignment.BottomEnd)
                    .size(dot)
                    .clip(CircleShape)
                    .background(if (online) Color(0xFF22C275) else Color(0xFF9AA5B5))
                    .border(2.dp, MaterialTheme.colorScheme.surface, CircleShape),
            )
        }
    }
}

/** Bars of a voice message; [progress] 0..1 colours the played part. Tap or drag seeks. */
@Composable
fun Waveform(
    peaks: FloatArray?,
    progress: Float,
    played: Color,
    rest: Color,
    modifier: Modifier = Modifier,
    onSeek: ((Float) -> Unit)? = null,
) {
    val bars = peaks ?: FALLBACK_PEAKS
    Canvas(
        modifier.then(
            if (onSeek != null) {
                Modifier.pointerInput(onSeek) {
                    detectTapGestures { offset -> onSeek((offset.x / size.width).coerceIn(0f, 1f)) }
                }
            } else {
                Modifier
            },
        ),
    ) {
        val n = bars.size
        if (n == 0) return@Canvas
        val gap = 2.dp.toPx()
        val w = ((size.width - gap * (n - 1)) / n).coerceAtLeast(1f)
        for (i in 0 until n) {
            val h = (bars[i].coerceIn(0.06f, 1f) * size.height).coerceAtLeast(w)
            val x = i * (w + gap)
            val c = if ((i + 0.5f) / n <= progress) played else rest
            drawRoundRect(c, Offset(x, (size.height - h) / 2), Size(w, h), CornerRadius(w / 2, w / 2))
        }
    }
}

private val FALLBACK_PEAKS = FloatArray(36) { i -> 0.25f + 0.2f * kotlin.math.abs(kotlin.math.sin(i * 0.7f)) }

fun Bitmap.ratio(): Float = if (height == 0) 1f else width.toFloat() / height
