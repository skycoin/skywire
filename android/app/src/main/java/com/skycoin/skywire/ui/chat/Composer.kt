package com.skycoin.skywire.ui.chat

import android.Manifest
import android.content.pm.PackageManager
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.camera.view.PreviewView
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.gestures.waitForUpOrCancellation
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.Send
import androidx.compose.material.icons.rounded.AttachFile
import androidx.compose.material.icons.rounded.CameraAlt
import androidx.compose.material.icons.rounded.CropSquare
import androidx.compose.material.icons.rounded.Delete
import androidx.compose.material.icons.rounded.FlipCameraAndroid
import androidx.compose.material.icons.rounded.Lock
import androidx.compose.material.icons.rounded.Mic
import androidx.compose.material.icons.rounded.RadioButtonUnchecked
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextField
import androidx.compose.material3.TextFieldDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import com.skycoin.skywire.R
import com.skycoin.skywire.chat.ChatAudioPlayer
import com.skycoin.skywire.chat.VideoNoteRecorder
import com.skycoin.skywire.chat.VoiceRecorder
import com.skycoin.skywire.chat.pick
import com.skycoin.skywire.chat.sendFile
import com.skycoin.skywire.chat.sendText
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlin.math.roundToInt

private enum class RecPhase { IDLE, OPENING, RECORDING }

/** One morphing button: send with text, mic or camera without (index.html composer). */
@Composable
fun Composer(vm: ChatViewModel, key: String) {
    val repo = vm.repo
    val context = LocalContext.current
    val owner = LocalLifecycleOwner.current
    val scope = rememberCoroutineScope()
    val density = LocalDensity.current
    val local by repo.store.state.collectAsState()
    val replies by vm.replies.collectAsState()
    var text by remember(key) { mutableStateOf(vm.drafts[key].orEmpty()) }
    val video = local.mediaMode == "video"

    val voice = remember { VoiceRecorder(context) }
    val camera = remember { VideoNoteRecorder(context) }
    var phase by remember { mutableStateOf(RecPhase.IDLE) }
    var recVideo by remember { mutableStateOf(false) }
    var locked by remember { mutableStateOf(false) }
    var slideX by remember { mutableFloatStateOf(0f) }
    var slideY by remember { mutableFloatStateOf(0f) }
    var elapsed by remember { mutableLongStateOf(0L) }
    val levels = remember { mutableStateListOf<Float>() }
    var preview by remember { mutableStateOf<PreviewView?>(null) }
    var front by remember { mutableStateOf(local.videoFacingFront) }
    var square by remember { mutableStateOf(local.videoSquare) }
    var holdJob by remember { mutableStateOf<Job?>(null) }
    var released by remember { mutableStateOf(false) }

    val cancelPx = with(density) { CANCEL_DX.dp.toPx() }
    val lockPx = with(density) { LOCK_DY.dp.toPx() }

    fun reset() {
        phase = RecPhase.IDLE
        locked = false
        slideX = 0f
        slideY = 0f
        elapsed = 0
        levels.clear()
    }

    fun finish(cancel: Boolean) {
        val wasVideo = recVideo
        val wasLocked = locked
        scope.launch {
            val file = if (wasVideo) camera.stop(discard = cancel) else voice.stop(discard = cancel)
            if (wasVideo) camera.release()
            reset()
            when {
                cancel -> repo.note(if (wasLocked) (if (wasVideo) R.string.chat_rec_video_discarded else R.string.chat_rec_voice_discarded) else (if (wasVideo) R.string.chat_rec_video_cancelled else R.string.chat_rec_voice_cancelled))
                file == null -> repo.note(if (wasVideo) R.string.chat_rec_hold_video else R.string.chat_rec_hold_voice)
                else -> {
                    val mime = when (file.extension) {
                        "oga" -> "audio/ogg"
                        "m4a" -> "audio/mp4"
                        else -> "video/mp4"
                    }
                    val caption = if (wasVideo) "📹 Video message" else "🎤 Voice message"
                    repo.sendFile(key, file, file.name, mime, caption, recorded = if (wasVideo) 2 else 1)
                }
            }
        }
    }

    val permissions = rememberLauncherForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) { grants ->
        if (grants.values.all { it }) {
            repo.note(if (grants.containsKey(Manifest.permission.CAMERA)) R.string.chat_rec_camera_ready else R.string.chat_rec_mic_ready)
        } else {
            repo.note(if (grants.containsKey(Manifest.permission.CAMERA)) R.string.chat_rec_camera_denied else R.string.chat_rec_mic_denied, error = true)
        }
    }

    fun granted(p: String) = ContextCompat.checkSelfPermission(context, p) == PackageManager.PERMISSION_GRANTED

    fun start(asVideo: Boolean) {
        val needed = listOfNotNull(
            Manifest.permission.RECORD_AUDIO.takeIf { !granted(it) },
            Manifest.permission.CAMERA.takeIf { asVideo && !granted(it) },
        )
        if (needed.isNotEmpty()) {
            permissions.launch(needed.toTypedArray())
            return
        }
        ChatAudioPlayer.stop()
        recVideo = asVideo
        if (asVideo) {
            phase = RecPhase.OPENING
        } else {
            runCatching { voice.start() }.onSuccess { phase = RecPhase.RECORDING }.onFailure {
                repo.note(R.string.chat_rec_mic_error, it.message.orEmpty(), error = true)
            }
        }
    }

    // A video take opens the camera first; the take starts once it is bound.
    var bound by remember { mutableStateOf(false) }
    val wantCamera = recVideo && phase != RecPhase.IDLE
    LaunchedEffect(wantCamera, preview, front) {
        bound = false
        if (!wantCamera) return@LaunchedEffect
        val view = preview ?: return@LaunchedEffect
        try {
            camera.bind(owner, view, front)
            bound = true
        } catch (e: kotlinx.coroutines.CancellationException) {
            throw e
        } catch (e: Exception) {
            repo.note(R.string.chat_rec_camera_error, e.message.orEmpty(), error = true)
            camera.release()
            reset()
        }
    }
    LaunchedEffect(bound, phase) {
        if (!bound || phase != RecPhase.OPENING) return@LaunchedEffect
        if (released && !locked) {
            // The finger lifted while the camera opened: nothing to send.
            camera.release()
            reset()
            repo.note(R.string.chat_rec_camera_ready)
            return@LaunchedEffect
        }
        runCatching { camera.start(square) }.onSuccess { phase = RecPhase.RECORDING }.onFailure {
            repo.note(R.string.chat_rec_camera_error, it.message.orEmpty(), error = true)
            camera.release()
            reset()
        }
    }

    LaunchedEffect(phase) {
        while (phase == RecPhase.RECORDING && isActive) {
            elapsed = if (recVideo) camera.elapsedMs else voice.elapsedMs
            if (!recVideo) {
                levels += voice.sample()
                if (levels.size > 120) levels.removeAt(0)
            }
            delay(55)
        }
    }

    // Leaving the app ends an unlocked take and sends what was recorded.
    DisposableEffect(owner) {
        val obs = LifecycleEventObserver { _, e ->
            if (e == Lifecycle.Event.ON_PAUSE && phase != RecPhase.IDLE && !locked) finish(cancel = false)
        }
        owner.lifecycle.addObserver(obs)
        onDispose {
            owner.lifecycle.removeObserver(obs)
            if (phase != RecPhase.IDLE) {
                voice.stop(discard = true)
                camera.release()
            }
        }
    }

    val attach = rememberLauncherForActivityResult(ActivityResultContracts.GetMultipleContents()) { uris ->
        scope.launch {
            for (uri in uris) {
                val p = repo.pick(uri)
                if (p == null) {
                    repo.note(R.string.chat_import_unreadable, error = true)
                    continue
                }
                repo.sendFile(key, p.file, p.name, p.mime)
            }
        }
        vm.setReply(key, null)
    }

    Column(Modifier.fillMaxWidth().background(MaterialTheme.colorScheme.surface).navigationBarsPadding()) {
        if (phase != RecPhase.IDLE && recVideo) {
            Row(Modifier.fillMaxWidth().padding(top = 8.dp), horizontalArrangement = Arrangement.Center, verticalAlignment = Alignment.CenterVertically) {
                IconButton(onClick = {
                    front = !front
                    repo.store.update { it.copy(videoFacingFront = front) }
                }) { Icon(Icons.Rounded.FlipCameraAndroid, stringResource(R.string.chat_switch_camera)) }
                AndroidView(
                    factory = { ctx -> PreviewView(ctx).apply { scaleType = PreviewView.ScaleType.FILL_CENTER }.also { preview = it } },
                    modifier = Modifier.size(220.dp).clip(if (square) RoundedCornerShape(24.dp) else CircleShape),
                )
                IconButton(onClick = {
                    square = !square
                    repo.store.update { it.copy(videoSquare = square) }
                }) {
                    Icon(if (square) Icons.Rounded.RadioButtonUnchecked else Icons.Rounded.CropSquare, stringResource(if (square) R.string.chat_rec_round else R.string.chat_rec_square))
                }
            }
        }
        Row(
            Modifier.fillMaxWidth().padding(horizontal = 6.dp, vertical = 6.dp),
            verticalAlignment = Alignment.Bottom,
        ) {
            if (phase == RecPhase.IDLE) {
                IconButton(onClick = { attach.launch("*/*") }) {
                    Icon(Icons.Rounded.AttachFile, stringResource(R.string.chat_attach))
                }
                TextField(
                    value = text,
                    onValueChange = {
                        text = it
                        vm.drafts[key] = it
                    },
                    placeholder = { Text(stringResource(R.string.chat_type_message)) },
                    modifier = Modifier.weight(1f).heightIn(min = 48.dp),
                    maxLines = 5,
                    shape = RoundedCornerShape(24.dp),
                    colors = TextFieldDefaults.colors(
                        focusedIndicatorColor = Color.Transparent,
                        unfocusedIndicatorColor = Color.Transparent,
                        disabledIndicatorColor = Color.Transparent,
                    ),
                )
            } else {
                RecordingBar(
                    Modifier.weight(1f),
                    elapsed = elapsed,
                    levels = levels,
                    locked = locked,
                    slide = slideX,
                    nearCancel = slideX < -cancelPx / 2,
                    onDiscard = { finish(cancel = true) },
                )
            }
            Spacer(Modifier.width(6.dp))
            val hasText = text.isNotBlank()
            val sendNow = rememberUpdatedState {
                repo.sendText(key, text, replies[key])
                text = ""
                vm.drafts.remove(key)
                vm.setReply(key, null)
            }
            Box(contentAlignment = Alignment.Center) {
                if (phase != RecPhase.IDLE && !locked) {
                    val lift by animateFloatAsState(if (-slideY > lockPx / 2) 1f else 0f, label = "lock")
                    Icon(
                        Icons.Rounded.Lock, null,
                        tint = if (lift > 0.5f) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.offset { IntOffset(0, -(with(density) { 64.dp.toPx() }).roundToInt()) }
                            .size(32.dp).clip(CircleShape).background(MaterialTheme.colorScheme.surfaceContainerHigh).padding(6.dp),
                    )
                }
                Box(
                    Modifier
                        .size(48.dp)
                        .offset { IntOffset(if (locked) 0 else slideX.roundToInt().coerceAtMost(0), if (locked) 0 else slideY.roundToInt().coerceAtMost(0)) }
                        .clip(CircleShape)
                        .background(if (phase != RecPhase.IDLE) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.primary)
                        .pointerInput(Unit) {
                            awaitEachGesture {
                                val down = awaitFirstDown()
                                if (text.isNotBlank()) {
                                    val up = waitForUpOrCancellation()
                                    if (up != null) sendNow.value()
                                    return@awaitEachGesture
                                }
                                if (locked && phase == RecPhase.RECORDING) {
                                    if (waitForUpOrCancellation() != null) finish(cancel = false)
                                    return@awaitEachGesture
                                }
                                released = false
                                var fired = false
                                holdJob = scope.launch {
                                    delay(HOLD_MS)
                                    fired = true
                                    start(repo.store.state.value.mediaMode == "video")
                                }
                                var cancelled = false
                                while (true) {
                                    val ev = awaitPointerEvent()
                                    val c = ev.changes.firstOrNull { it.id == down.id } ?: break
                                    if (!c.pressed) break
                                    if (phase != RecPhase.IDLE && !locked) {
                                        slideX = (c.position.x - down.position.x).coerceAtMost(0f)
                                        slideY = (c.position.y - down.position.y).coerceAtMost(0f)
                                        if (slideX <= -cancelPx) {
                                            cancelled = true
                                            finish(cancel = true)
                                            break
                                        }
                                        if (slideY <= -lockPx) {
                                            locked = true
                                            slideX = 0f
                                            slideY = 0f
                                        }
                                    }
                                    c.consume()
                                }
                                released = true
                                if (!fired) {
                                    holdJob?.cancel()
                                    // A tap switches what the button records.
                                    val next = if (repo.store.state.value.mediaMode == "video") "mic" else "video"
                                    repo.store.update { it.copy(mediaMode = next) }
                                    repo.note(if (next == "video") R.string.chat_rec_mode_video else R.string.chat_rec_mode_voice)
                                } else if (!cancelled && !locked && phase != RecPhase.IDLE) {
                                    finish(cancel = false)
                                }
                            }
                        },
                    contentAlignment = Alignment.Center,
                ) {
                    Icon(
                        when {
                            hasText || locked -> Icons.AutoMirrored.Rounded.Send
                            video -> Icons.Rounded.CameraAlt
                            else -> Icons.Rounded.Mic
                        },
                        stringResource(
                            when {
                                hasText -> R.string.chat_send
                                locked -> if (recVideo) R.string.chat_rec_send_video else R.string.chat_rec_send_voice
                                video -> R.string.chat_rec_mode_video_hint
                                else -> R.string.chat_rec_mode_voice_hint
                            },
                        ),
                        tint = Color.White,
                    )
                }
            }
        }
    }
}

@Composable
private fun RecordingBar(
    modifier: Modifier,
    elapsed: Long,
    levels: List<Float>,
    locked: Boolean,
    slide: Float,
    nearCancel: Boolean,
    onDiscard: () -> Unit,
) {
    val red = MaterialTheme.colorScheme.error
    val wave = if (nearCancel) red else MaterialTheme.colorScheme.primary
    Row(modifier.heightIn(min = 48.dp).padding(start = 4.dp), verticalAlignment = Alignment.CenterVertically) {
        if (locked) {
            IconButton(onClick = onDiscard) { Icon(Icons.Rounded.Delete, stringResource(R.string.chat_rec_delete), tint = red) }
        } else {
            Box(Modifier.padding(start = 12.dp).size(10.dp).clip(CircleShape).background(red))
        }
        Spacer(Modifier.width(8.dp))
        Text(clock(elapsed), style = MaterialTheme.typography.bodyMedium)
        Spacer(Modifier.width(8.dp))
        Canvas(Modifier.weight(1f).height(28.dp)) {
            val bar = 3.dp.toPx()
            val gap = 2.dp.toPx()
            val n = (size.width / (bar + gap)).toInt()
            val shown = levels.takeLast(n)
            shown.forEachIndexed { i, v ->
                val h = (kotlin.math.sqrt(v) * size.height).coerceAtLeast(2.dp.toPx())
                val x = size.width - (shown.size - i) * (bar + gap)
                drawRoundRect(wave, Offset(x, (size.height - h) / 2), Size(bar, h), CornerRadius(bar / 2, bar / 2))
            }
        }
        Text(
            if (locked) stringResource(R.string.chat_rec_tap_send) else "‹ " + stringResource(R.string.chat_rec_slide_cancel),
            style = MaterialTheme.typography.labelMedium,
            color = if (nearCancel) red else MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.offset { IntOffset((slide / 2).roundToInt(), 0) }.padding(end = 8.dp),
        )
    }
}

private const val HOLD_MS = 220L
private const val CANCEL_DX = 90
private const val LOCK_DY = 56
