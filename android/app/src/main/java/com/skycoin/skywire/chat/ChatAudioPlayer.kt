package com.skycoin.skywire.chat

import android.content.Context
import android.media.AudioAttributes
import android.media.MediaPlayer
import android.media.PlaybackParams
import com.skycoin.skywire.core.ChatMedia
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import java.io.File

/** What the one chat audio player is doing. */
data class AudioPlayback(
    val key: String? = null,
    val playing: Boolean = false,
    val positionMs: Long = 0,
    val durationMs: Long = 0,
    val speed: Float = 1f,
)

/**
 * The single player behind every voice message and audio attachment: starting
 * one stops whatever else was playing, the way a chat app behaves. While a clip
 * plays it also holds the shade's media controls ([ChatMedia]).
 */
object ChatAudioPlayer {

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main)
    private val mutable = MutableStateFlow(AudioPlayback())
    val state: StateFlow<AudioPlayback> = mutable.asStateFlow()

    private var player: MediaPlayer? = null
    private var ticker: Job? = null
    private var app: Context? = null
    private var title = ""
    private var artist = ""

    val speeds = floatArrayOf(0.75f, 1f, 1.25f, 1.5f, 2f)

    /** Who and what is playing, for the media notification. */
    fun describe(context: Context, title: String, artist: String) {
        app = context.applicationContext
        this.title = title
        this.artist = artist
    }

    fun toggle(key: String, file: File, startAt: Float? = null) {
        val p = player
        if (mutable.value.key == key && p != null) {
            if (p.isPlaying) {
                pause()
            } else {
                startAt?.let { p.seekTo((it * p.duration).toInt()) }
                resume()
            }
            return
        }
        play(key, file, startAt)
    }

    fun play(key: String, file: File, startAt: Float? = null) {
        stop()
        val p = MediaPlayer()
        p.setAudioAttributes(
            AudioAttributes.Builder()
                .setUsage(AudioAttributes.USAGE_MEDIA)
                .setContentType(AudioAttributes.CONTENT_TYPE_SPEECH)
                .build(),
        )
        runCatching {
            p.setDataSource(file.absolutePath)
            p.prepare()
        }.onFailure {
            p.release()
            mutable.value = AudioPlayback(speed = mutable.value.speed)
            return
        }
        startAt?.let { p.seekTo((it * p.duration).toInt()) }
        p.setOnCompletionListener { stop() }
        player = p
        p.start()
        applySpeed(p, mutable.value.speed)
        mutable.value = AudioPlayback(key, true, p.currentPosition.toLong(), p.duration.toLong(), mutable.value.speed)
        report()
        tick()
    }

    fun pause() {
        val p = player ?: return
        if (p.isPlaying) p.pause()
        mutable.value = mutable.value.copy(playing = false, positionMs = p.currentPosition.toLong())
        report()
    }

    fun resume() {
        val p = player ?: return
        p.start()
        applySpeed(p, mutable.value.speed)
        mutable.value = mutable.value.copy(playing = true)
        report()
        tick()
    }

    fun seek(key: String, fraction: Float) {
        val p = player ?: return
        if (mutable.value.key != key) return
        seekTo((fraction.coerceIn(0f, 1f) * p.duration).toLong())
    }

    fun seekTo(ms: Long) {
        val p = player ?: return
        p.seekTo(ms.coerceIn(0, p.duration.toLong()).toInt())
        mutable.value = mutable.value.copy(positionMs = p.currentPosition.toLong())
        report()
    }

    fun seekBy(deltaMs: Long) {
        val p = player ?: return
        seekTo(p.currentPosition + deltaMs)
    }

    fun setSpeed(speed: Float) {
        mutable.value = mutable.value.copy(speed = if (speeds.any { it == speed }) speed else 1f)
        player?.let { if (it.isPlaying) applySpeed(it, mutable.value.speed) }
        if (player != null) report()
    }

    fun stop() {
        ticker?.cancel()
        val had = player != null
        player?.let {
            runCatching { it.stop() }
            it.release()
        }
        player = null
        mutable.value = AudioPlayback(speed = mutable.value.speed)
        if (had) app?.let { ChatMedia.clear(it) }
    }

    private fun report() {
        val context = app ?: return
        ChatMedia.show(context, mutable.value, title, artist)
    }

    private fun applySpeed(p: MediaPlayer, speed: Float) {
        runCatching { p.playbackParams = PlaybackParams().setSpeed(speed) }
    }

    private fun tick() {
        ticker?.cancel()
        ticker = scope.launch {
            while (isActive) {
                val p = player ?: break
                mutable.value = mutable.value.copy(positionMs = p.currentPosition.toLong(), playing = p.isPlaying)
                delay(TICK_MS)
            }
        }
    }

    private const val TICK_MS = 100L
}
