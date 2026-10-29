package com.skycoin.skywire.chat

import android.content.Context
import android.media.MediaRecorder
import android.os.Build
import java.io.File

/**
 * Records a voice message into the cache.
 *
 * The file is named `voice-<stamp>.<ext>`: that prefix is how every skychat
 * UI tells a recorded clip from an attached audio file (index.html
 * isVoiceMessage), so the desktop shows it with the voice player too.
 * Ogg/Opus where the platform has it (API 29+), AAC in MP4 before that —
 * both play in every browser the desktop UI runs in.
 */
class VoiceRecorder(private val context: Context) {

    private var recorder: MediaRecorder? = null
    private var startedAt = 0L

    var file: File? = null
        private set

    /** Peak amplitudes sampled while recording, 0..1, for the live waveform. */
    val peaks = mutableListOf<Float>()

    fun start(): File {
        stop(discard = true)
        val dir = File(context.cacheDir, "chat-rec").apply { mkdirs() }
        val opus = Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q
        val out = File(dir, "voice-${clipStamp()}." + if (opus) "oga" else "m4a")
        val r = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) MediaRecorder(context) else @Suppress("DEPRECATION") MediaRecorder()
        r.setAudioSource(MediaRecorder.AudioSource.VOICE_COMMUNICATION)
        if (opus) {
            r.setOutputFormat(MediaRecorder.OutputFormat.OGG)
            r.setAudioEncoder(MediaRecorder.AudioEncoder.OPUS)
            r.setAudioEncodingBitRate(32_000)
            r.setAudioSamplingRate(48_000)
        } else {
            r.setOutputFormat(MediaRecorder.OutputFormat.MPEG_4)
            r.setAudioEncoder(MediaRecorder.AudioEncoder.AAC)
            r.setAudioEncodingBitRate(64_000)
            r.setAudioSamplingRate(44_100)
        }
        r.setAudioChannels(1)
        r.setOutputFile(out.absolutePath)
        try {
            r.prepare()
            r.start()
        } catch (e: Exception) {
            r.release()
            out.delete()
            throw e
        }
        recorder = r
        file = out
        peaks.clear()
        startedAt = System.currentTimeMillis()
        return out
    }

    val elapsedMs: Long get() = if (recorder == null) 0 else System.currentTimeMillis() - startedAt

    /** Current input level 0..1; also appended to [peaks]. */
    fun sample(): Float {
        val r = recorder ?: return 0f
        val level = (runCatching { r.maxAmplitude }.getOrDefault(0) / 32767f).coerceIn(0f, 1f)
        peaks += level
        return level
    }

    /** Stops recording; returns the finished file unless [discard] or it is too short to keep. */
    fun stop(discard: Boolean = false): File? {
        val r = recorder ?: return null
        recorder = null
        val tooShort = System.currentTimeMillis() - startedAt < MIN_MS
        val ok = runCatching { r.stop() }.isSuccess
        r.release()
        val out = file
        file = null
        if (discard || tooShort || !ok || out == null || out.length() == 0L) {
            out?.delete()
            return null
        }
        return out
    }

    val recording: Boolean get() = recorder != null

    private companion object {
        const val MIN_MS = 500L
    }
}

/** `YYYYMMDD-HHMMSS` in local time, the stamp the web recorder names clips with. */
fun clipStamp(): String =
    java.text.SimpleDateFormat("yyyyMMdd-HHmmss", java.util.Locale.US).format(java.util.Date())
