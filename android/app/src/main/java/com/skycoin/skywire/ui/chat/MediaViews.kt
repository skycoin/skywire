package com.skycoin.skywire.ui.chat

import android.graphics.Bitmap
import android.graphics.SurfaceTexture
import android.media.MediaPlayer
import android.net.Uri
import android.view.Surface
import android.view.TextureView
import android.widget.MediaController
import android.widget.VideoView
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.gestures.detectTransformGestures
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.rounded.Fullscreen
import androidx.compose.material.icons.rounded.Pause
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import com.skycoin.skywire.R
import com.skycoin.skywire.chat.AudioPeaks
import com.skycoin.skywire.chat.ChatAudioPlayer
import com.skycoin.skywire.chat.ChatFile
import com.skycoin.skywire.chat.ChatImages
import com.skycoin.skywire.chat.ChatMessage
import com.skycoin.skywire.chat.humanSize
import com.skycoin.skywire.chat.thumbPath
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.withContext
import java.io.File

/** `file:<path>` for our own local copy, else the served `/files/…` path. */
fun mediaSource(f: ChatFile): String =
    if (f.localPath.isNotEmpty() && File(f.localPath).isFile) "file:" + f.localPath else f.url

/** The bytes of [source] on disk: our own copy, or a cached download. */
suspend fun ChatViewModel.mediaFile(source: String): File? =
    if (source.startsWith("file:")) File(source.removePrefix("file:")).takeIf { it.isFile } else repo.media.get(source)

private val peakCache = HashMap<String, Pair<FloatArray, Long>>()

/** Only one recorded video plays at a time. */
private val roundNow = MutableStateFlow<String?>(null)

@Composable
fun ImageThumb(vm: ChatViewModel, f: ChatFile) {
    val source = mediaSource(f)
    val cacheKey = "thumb:$source"
    var bmp by remember(source) { mutableStateOf(ChatImages.cached(cacheKey)) }
    LaunchedEffect(source) {
        if (bmp != null) return@LaunchedEffect
        val loaded = withContext(Dispatchers.IO) {
            val file = if (source.startsWith("file:")) {
                File(source.removePrefix("file:"))
            } else {
                f.thumbPath()?.let { vm.repo.media.get(it) } ?: vm.repo.media.get(source)
            }
            file?.let { ChatImages.decode(it.readBytes(), THUMB_EDGE) }
                ?: if (!source.startsWith("file:")) vm.repo.media.get(source)?.let { ChatImages.decode(it.readBytes(), THUMB_EDGE) } else null
        }
        if (loaded != null) {
            ChatImages.remember(cacheKey, loaded)
            bmp = loaded
        }
    }
    val b = bmp
    val ratio = b?.let { (it.width.toFloat() / it.height).coerceIn(0.5f, 2f) } ?: 1.3f
    Box(
        Modifier
            .width(240.dp)
            .height((240 / ratio).dp)
            .clip(RoundedCornerShape(16.dp))
            .background(MaterialTheme.colorScheme.surfaceContainerHighest)
            .clickable { vm.push(ChatPage.Image(source, f.name)) },
        contentAlignment = Alignment.Center,
    ) {
        if (b != null) {
            Image(b.asImageBitmap(), f.name, Modifier.fillMaxSize(), contentScale = ContentScale.Crop)
        } else {
            CircularProgressIndicator(Modifier.size(24.dp), strokeWidth = 2.dp)
        }
    }
}

/** Voice messages get bars; attached audio gets its name over the bar. */
@Composable
fun VoicePlayer(vm: ChatViewModel, m: ChatMessage, f: ChatFile, colors: BubbleColors, voice: Boolean) {
    val source = mediaSource(f)
    val playback by ChatAudioPlayer.state.collectAsState()
    val mine = playback.key == m.key
    var file by remember(source) { mutableStateOf<File?>(null) }
    var peaks by remember(source) { mutableStateOf(peakCache[source]) }
    var loading by remember { mutableStateOf(false) }
    var wantPlay by remember { mutableStateOf(false) }

    LaunchedEffect(source, wantPlay) {
        if (file == null && (voice || wantPlay)) {
            loading = wantPlay
            file = vm.mediaFile(source)
            loading = false
        }
        val fl = file ?: return@LaunchedEffect
        if (voice && peaks == null) {
            peaks = withContext(Dispatchers.Default) { AudioPeaks.compute(fl, BARS) }?.also { peakCache[source] = it }
        }
        if (wantPlay) {
            wantPlay = false
            startPlayback(vm, m, f, voice, fl)
        }
    }
    val duration = if (mine && playback.durationMs > 0) playback.durationMs else peaks?.second ?: 0L
    val progress = if (mine && duration > 0) playback.positionMs.toFloat() / duration else 0f
    Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.widthIn(min = 200.dp)) {
        Box(
            Modifier.size(42.dp).clip(CircleShape).background(colors.accent).clickable {
                val fl = file
                if (fl == null) wantPlay = true else startPlayback(vm, m, f, voice, fl)
            },
            contentAlignment = Alignment.Center,
        ) {
            if (loading) {
                CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp, color = colors.bg)
            } else {
                Icon(
                    if (mine && playback.playing) Icons.Rounded.Pause else Icons.Rounded.PlayArrow,
                    null,
                    tint = if (colors.accent == colors.fg) colors.bg else Color.White,
                )
            }
        }
        Spacer(Modifier.width(10.dp))
        Column(Modifier.width(170.dp)) {
            if (!voice) {
                Text(f.name, color = colors.fg, style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            Waveform(
                peaks = if (voice) peaks?.first else FloatArray(BARS) { 0.35f },
                progress = progress,
                played = colors.fg,
                rest = colors.fg.copy(alpha = 0.35f),
                modifier = Modifier.fillMaxWidth().height(if (voice) 30.dp else 10.dp),
                onSeek = { frac -> if (mine) ChatAudioPlayer.seek(m.key, frac) else file?.let { ChatAudioPlayer.play(m.key, it, frac) } },
            )
            Row(verticalAlignment = Alignment.CenterVertically) {
                val shown = if (mine && (playback.playing || playback.positionMs > 0)) playback.positionMs else duration
                Text(
                    clock(shown) + " · " + humanSize(f.size),
                    color = colors.meta,
                    style = MaterialTheme.typography.labelSmall,
                )
                Spacer(Modifier.weight(1f))
                SpeedPill(vm, colors)
            }
        }
    }
}

/** Start (or pause) a clip, telling the media notification what it is and whose. */
private fun startPlayback(vm: ChatViewModel, m: ChatMessage, f: ChatFile, voice: Boolean, file: File) {
    val app = vm.getApplication<android.app.Application>()
    val key = vm.repo.openKey.value.orEmpty()
    val chat = when {
        key == com.skycoin.skywire.chat.SAVED_KEY -> app.getString(R.string.chat_saved_messages)
        vm.repo.isGroupKey(key) -> vm.repo.groupInfo(key)?.name.orEmpty()
        else -> vm.repo.displayName(key)
    }
    ChatAudioPlayer.describe(app, if (voice) app.getString(R.string.chat_voice_message) else f.name, chat)
    ChatAudioPlayer.setSpeed(vm.repo.store.state.value.speed)
    ChatAudioPlayer.toggle(m.key, file)
}

@Composable
fun SpeedPill(vm: ChatViewModel, colors: BubbleColors) {
    val local by vm.repo.store.state.collectAsState()
    val speed = local.speed
    val label = (if (speed % 1f == 0f) speed.toInt().toString() else speed.toString()) + "×"
    Text(
        label,
        style = MaterialTheme.typography.labelSmall,
        color = if (speed != 1f) colors.bg else colors.fg,
        modifier = Modifier
            .clip(RoundedCornerShape(8.dp))
            .background(if (speed != 1f) colors.fg else colors.fg.copy(alpha = 0.15f))
            .clickable {
                val next = ChatAudioPlayer.speeds[(ChatAudioPlayer.speeds.indexOfFirst { it == speed } + 1) % ChatAudioPlayer.speeds.size]
                vm.repo.store.update { it.copy(speed = next) }
                ChatAudioPlayer.setSpeed(next)
            }
            .padding(horizontal = 6.dp, vertical = 1.dp),
    )
}

fun clock(ms: Long): String {
    val s = (ms / 1000).coerceAtLeast(0)
    return "%d:%02d".format(s / 60, s % 60)
}

/** A round (or square) video message: plays in place on tap, the ring is its progress. */
@Composable
fun RoundVideo(vm: ChatViewModel, m: ChatMessage, f: ChatFile, square: Boolean) {
    val source = mediaSource(f)
    var file by remember(source) { mutableStateOf<File?>(null) }
    val now by roundNow.collectAsState()
    var player by remember { mutableStateOf<MediaPlayer?>(null) }
    var playing by remember { mutableStateOf(false) }
    var progress by remember { mutableFloatStateOf(0f) }
    var position by remember { mutableLongStateOf(0L) }
    var duration by remember { mutableLongStateOf(0L) }
    val ring = MaterialTheme.colorScheme.primary
    LaunchedEffect(source) { file = vm.mediaFile(source) }
    LaunchedEffect(now) {
        if (now != m.key && playing) {
            player?.pause()
            playing = false
        }
    }
    LaunchedEffect(playing) {
        while (playing && isActive) {
            player?.let {
                position = it.currentPosition.toLong()
                if (duration > 0) progress = position.toFloat() / duration
            }
            delay(50)
        }
    }
    val shape = if (square) RoundedCornerShape(18.dp) else CircleShape
    Box(Modifier.size(VIDEO_NOTE.dp), contentAlignment = Alignment.Center) {
        val fl = file
        if (fl == null) {
            Box(Modifier.size(VIDEO_NOTE.dp).clip(shape).background(Color.Black), contentAlignment = Alignment.Center) {
                CircularProgressIndicator(Modifier.size(28.dp), color = Color.White, strokeWidth = 2.dp)
            }
        } else {
            AndroidView(
                factory = { ctx ->
                    TextureView(ctx).apply {
                        surfaceTextureListener = object : TextureView.SurfaceTextureListener {
                            override fun onSurfaceTextureAvailable(st: SurfaceTexture, w: Int, h: Int) {
                                val mp = MediaPlayer()
                                runCatching {
                                    mp.setDataSource(fl.absolutePath)
                                    mp.setSurface(Surface(st))
                                    // Centre-crop the clip into the square view instead of stretching it.
                                    mp.setOnVideoSizeChangedListener { _, vw, vh ->
                                        if (vw > 0 && vh > 0) {
                                            val sx = maxOf(1f, vw.toFloat() / vh)
                                            val sy = maxOf(1f, vh.toFloat() / vw)
                                            setTransform(android.graphics.Matrix().apply { setScale(sx, sy, width / 2f, height / 2f) })
                                        }
                                    }
                                    mp.prepare()
                                    mp.seekTo(1)
                                    duration = mp.duration.toLong()
                                    mp.setOnCompletionListener {
                                        playing = false
                                        progress = 0f
                                        position = 0
                                        it.seekTo(1)
                                    }
                                    player = mp
                                }.onFailure { mp.release() }
                            }

                            override fun onSurfaceTextureSizeChanged(st: SurfaceTexture, w: Int, h: Int) = Unit
                            override fun onSurfaceTextureDestroyed(st: SurfaceTexture): Boolean {
                                player?.release()
                                player = null
                                playing = false
                                return true
                            }

                            override fun onSurfaceTextureUpdated(st: SurfaceTexture) = Unit
                        }
                    }
                },
                modifier = Modifier
                    .size((VIDEO_NOTE - 8).dp)
                    .clip(shape)
                    .clickable {
                        val p = player ?: return@clickable
                        if (playing) {
                            p.pause()
                            playing = false
                        } else {
                            ChatAudioPlayer.stop()
                            roundNow.value = m.key
                            runCatching { p.playbackParams = p.playbackParams.setSpeed(vm.repo.store.state.value.speed) }
                            p.start()
                            playing = true
                        }
                    },
            )
            if (!square) {
                Canvas(Modifier.size(VIDEO_NOTE.dp)) {
                    val stroke = 3.dp.toPx()
                    drawArc(
                        color = ring,
                        startAngle = -90f,
                        sweepAngle = 360f * progress,
                        useCenter = false,
                        style = Stroke(stroke, cap = StrokeCap.Round),
                        topLeft = Offset(stroke / 2, stroke / 2),
                        size = androidx.compose.ui.geometry.Size(size.width - stroke, size.height - stroke),
                    )
                }
            }
            if (!playing) {
                Icon(
                    Icons.Rounded.PlayArrow, null, tint = Color.White,
                    modifier = Modifier.size(40.dp).clip(CircleShape).background(Color.Black.copy(alpha = 0.45f)).padding(6.dp),
                )
            }
            Text(
                clock(if (playing || position > 0) position else duration),
                color = Color.White,
                style = MaterialTheme.typography.labelSmall,
                modifier = Modifier.align(Alignment.BottomStart).padding(start = 14.dp, bottom = 10.dp)
                    .clip(RoundedCornerShape(8.dp)).background(Color.Black.copy(alpha = 0.45f)).padding(horizontal = 5.dp, vertical = 1.dp),
            )
            Icon(
                Icons.Rounded.Fullscreen, stringResource(R.string.chat_full_screen), tint = Color.White,
                modifier = Modifier.align(Alignment.BottomEnd).padding(end = 10.dp, bottom = 6.dp).size(30.dp)
                    .clip(CircleShape).background(Color.Black.copy(alpha = 0.45f))
                    .clickable {
                        player?.pause()
                        playing = false
                        vm.push(ChatPage.Video("file:" + fl.absolutePath, f.name, position))
                    }
                    .padding(4.dp),
            )
        }
    }
    DisposableEffect(Unit) {
        onDispose {
            player?.release()
            player = null
        }
    }
}

/** An attached film: a card that opens the full-screen player. */
@Composable
fun VideoCard(vm: ChatViewModel, f: ChatFile, colors: BubbleColors) {
    Row(
        Modifier.clip(RoundedCornerShape(12.dp)).clickable { vm.push(ChatPage.Video(mediaSource(f), f.name, 0)) }.widthIn(min = 200.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(Modifier.size(54.dp).clip(RoundedCornerShape(12.dp)).background(Color.Black), contentAlignment = Alignment.Center) {
            Icon(Icons.Rounded.PlayArrow, null, tint = Color.White, modifier = Modifier.size(32.dp))
        }
        Spacer(Modifier.width(10.dp))
        Column(Modifier.weight(1f, fill = false)) {
            Text(f.name, color = colors.fg, style = MaterialTheme.typography.bodyMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
            Text(humanSize(f.size), color = colors.meta, style = MaterialTheme.typography.labelSmall)
        }
    }
}

/** Full image with pinch and double-tap zoom (index.html lightbox: 1×–6×, double tap 2.5×). */
@Composable
fun ImageViewer(vm: ChatViewModel, source: String, name: String, onBack: () -> Unit) {
    var bmp by remember { mutableStateOf<Bitmap?>(null) }
    var failed by remember { mutableStateOf(false) }
    LaunchedEffect(source) {
        val f = vm.mediaFile(source)
        bmp = f?.let { withContext(Dispatchers.IO) { ChatImages.decode(it.readBytes(), 2560) } }
        failed = bmp == null
    }
    var scale by remember { mutableFloatStateOf(1f) }
    var offset by remember { mutableStateOf(Offset.Zero) }
    Box(Modifier.fillMaxSize().background(Color.Black)) {
        val b = bmp
        if (b != null) {
            Image(
                b.asImageBitmap(), name,
                contentScale = ContentScale.Fit,
                modifier = Modifier
                    .fillMaxSize()
                    .pointerInput(Unit) {
                        detectTapGestures(
                            onDoubleTap = { at ->
                                if (scale > 1f) {
                                    scale = 1f
                                    offset = Offset.Zero
                                } else {
                                    scale = 2.5f
                                    offset = (Offset(size.width / 2f, size.height / 2f) - at) * 1.5f
                                }
                            },
                            onTap = { if (scale == 1f) onBack() },
                        )
                    }
                    .pointerInput(Unit) {
                        detectTransformGestures { _, pan, zoom, _ ->
                            scale = (scale * zoom).coerceIn(1f, 6f)
                            offset = if (scale == 1f) Offset.Zero else offset + pan
                        }
                    }
                    .graphicsLayer {
                        scaleX = scale
                        scaleY = scale
                        translationX = offset.x
                        translationY = offset.y
                    },
            )
        } else if (failed) {
            Text(stringResource(R.string.chat_cannot_open), color = Color.White, modifier = Modifier.align(Alignment.Center))
        } else {
            CircularProgressIndicator(Modifier.align(Alignment.Center), color = Color.White)
        }
        ViewerBar(name, onBack)
    }
}

/** Full-screen player: our own copies play from disk, served ones stream with Range and our credential. */
@Composable
fun VideoViewer(vm: ChatViewModel, source: String, name: String, startMs: Long, onBack: () -> Unit) {
    var auth by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(Unit) {
        ChatAudioPlayer.stop()
        auth = vm.repo.http.authorization()
    }
    Box(Modifier.fillMaxSize().background(Color.Black)) {
        val a = auth
        if (a != null) {
            AndroidView(
                factory = { ctx ->
                    VideoView(ctx).apply {
                        val controller = MediaController(ctx)
                        controller.setAnchorView(this)
                        setMediaController(controller)
                        if (source.startsWith("file:")) {
                            setVideoPath(source.removePrefix("file:"))
                        } else {
                            setVideoURI(Uri.parse(vm.repo.http.url(source)), mapOf("Authorization" to a))
                        }
                        setOnPreparedListener { mp ->
                            runCatching { mp.playbackParams = mp.playbackParams.setSpeed(vm.repo.store.state.value.speed) }
                            if (startMs > 0) seekTo(startMs.toInt())
                            start()
                        }
                        setOnErrorListener { _, _, _ ->
                            vm.repo.note(R.string.chat_cannot_play)
                            true
                        }
                    }
                },
                modifier = Modifier.fillMaxSize().align(Alignment.Center),
                onRelease = { it.stopPlayback() },
            )
        }
        ViewerBar(name, onBack)
    }
}

@Composable
private fun ViewerBar(name: String, onBack: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().statusBarsPadding().background(Color.Black.copy(alpha = 0.4f)).padding(4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, stringResource(R.string.chat_close), tint = Color.White) }
        Text(name, color = Color.White, maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.titleSmall)
    }
}

private const val THUMB_EDGE = 640
private const val BARS = 48
private const val VIDEO_NOTE = 200
