package com.skycoin.skywire.core

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import android.media.AudioAttributes
import android.media.AudioFormat
import android.media.AudioManager
import android.media.AudioRecord
import android.media.AudioTrack
import android.media.MediaRecorder
import android.media.audiofx.AcousticEchoCanceler
import android.media.audiofx.AutomaticGainControl
import android.media.audiofx.NoiseSuppressor
import android.util.Log
import androidx.core.content.ContextCompat
import com.skycoin.skywire.api.VisorApi
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody
import okio.BufferedSink
import java.io.IOException
import java.nio.ByteBuffer
import java.nio.ByteOrder
import kotlin.coroutines.coroutineContext

/**
 * The phone's microphone and speaker, lent to the visor for the duration of a
 * call.
 *
 * The visor has no audio device of its own on Android — it is a headless
 * process, and the Go audio backends target PulseAudio, miniaudio and WebAudio,
 * none of which exist here. So it exposes the two ends of its call mixer over
 * the local API and this class plays the device: capture goes up the
 * microphone stream, playback comes down the speaker stream.
 *
 * **Everything here is raw little-endian int16 PCM at [SAMPLE_RATE], mono.**
 * That is the format the call package fixes for every backend, so there is
 * nothing to negotiate — and nothing to notice if it were wrong, since a
 * mismatched rate is not an error, just the wrong pitch.
 *
 * Both directions run for as long as the engine does and reconnect on their
 * own: a stream that drops mid-call (the visor restarting under us, the
 * process being frozen and thawed) should cost a moment of audio, not the call.
 */
class VoiceAudioEngine(context: Context) {

    private val app = context.applicationContext
    private val api = VisorApi.get(app)
    private val audioManager = app.getSystemService(Context.AUDIO_SERVICE) as AudioManager

    private var capture: Job? = null
    private var playback: Job? = null
    private var priorMode: Int? = null

    /** True while the mic loop is actually recording — false if the permission is missing. */
    @Volatile
    var capturing: Boolean = false
        private set

    /**
     * Start both directions.
     *
     * [onMicrophoneReady] is invoked each time capture is about to open, once
     * the permission is in hand — the host uses it to claim the microphone
     * foreground-service type, which the platform refuses while the permission
     * is missing and which must be in place before the first frame is recorded.
     *
     * Idempotent: a second call while running is a no-op, since the call
     * watcher may re-observe the same active call.
     */
    fun start(scope: CoroutineScope, onMicrophoneReady: () -> Unit = {}) {
        if (playback?.isActive == true) return
        // Voice-call routing: earpiece rather than the media speaker, and the
        // input tuned for speech. Also what makes the platform's echo
        // canceller apply — the Go side has none, so without this the peer
        // hears themselves back through our speaker.
        priorMode = audioManager.mode
        runCatching { audioManager.mode = AudioManager.MODE_IN_COMMUNICATION }
        playback = scope.launch(Dispatchers.IO) { playbackLoop() }
        capture = scope.launch(Dispatchers.IO) { captureLoop(onMicrophoneReady) }
    }

    /** Stop both directions and hand the device back to the system. */
    fun stop() {
        capture?.cancel()
        playback?.cancel()
        capture = null
        playback = null
        capturing = false
        priorMode?.let { mode -> runCatching { audioManager.mode = mode } }
        priorMode = null
    }

    // --- microphone → visor ---

    /**
     * Holds one long-lived POST open, writing captured frames into it.
     *
     * The permission is re-checked rather than required up front: a call can
     * arrive before the user has ever granted the microphone (they answer from
     * the notification), and the honest behaviour then is a call they can HEAR
     * while the grant is still outstanding — so this waits and picks the grant
     * up whenever it lands, instead of failing the call.
     */
    private suspend fun captureLoop(onMicrophoneReady: () -> Unit) {
        while (coroutineContext.isActive) {
            if (!hasMicPermission()) {
                capturing = false
                delay(PERMISSION_RETRY_MS)
                continue
            }
            onMicrophoneReady()
            try {
                api.voiceMicStream { micBody() }.use { resp ->
                    if (!resp.isSuccessful) {
                        Log.w(TAG, "mic stream rejected: ${resp.code}")
                        delay(RETRY_MS)
                    }
                }
            } catch (e: IOException) {
                // The body writer ends by throwing when the recorder stops or
                // the socket goes; both are "reopen", not "give up".
                Log.d(TAG, "mic stream ended: ${e.message}")
            } finally {
                capturing = false
            }
            coroutineContext.ensureActive()
            delay(RETRY_MS)
        }
    }

    /**
     * The request body IS the microphone: OkHttp calls [RequestBody.writeTo]
     * once and we stay inside it, recording and writing, until the call ends.
     *
     * Recording is opened here rather than in the loop above so the device is
     * held for exactly as long as the stream that carries it — an open
     * AudioRecord with nowhere to send its audio is a microphone indicator in
     * the status bar and nothing else.
     */
    private fun micBody(): RequestBody = object : RequestBody() {
        override fun contentType() = "application/octet-stream".toMediaType()

        // Unknown length: this is a stream, and declaring a length would make
        // OkHttp buffer the whole call in memory before sending a byte.
        override fun contentLength(): Long = -1

        override fun isOneShot(): Boolean = true

        override fun writeTo(sink: BufferedSink) {
            val record = openRecorder() ?: throw IOException("microphone unavailable")
            val effects = attachEffects(record.audioSessionId)
            try {
                record.startRecording()
                capturing = true
                val samples = ShortArray(FRAME_SAMPLES * CHUNK_FRAMES)
                val bytes = ByteBuffer.allocate(samples.size * 2).order(ByteOrder.LITTLE_ENDIAN)
                while (true) {
                    val n = record.read(samples, 0, samples.size)
                    if (n <= 0) {
                        // A negative result is a dead recorder (device stolen
                        // by another app, session invalidated); ending the body
                        // is what reopens it.
                        throw IOException("capture ended ($n)")
                    }
                    bytes.clear()
                    for (i in 0 until n) bytes.putShort(samples[i])
                    sink.write(bytes.array(), 0, n * 2)
                    sink.flush()
                }
            } finally {
                capturing = false
                runCatching { record.stop() }
                record.release()
                effects.forEach { runCatching { it.release() } }
            }
        }
    }

    private fun openRecorder(): AudioRecord? {
        val minBuffer = AudioRecord.getMinBufferSize(SAMPLE_RATE, CHANNEL_IN, ENCODING)
        if (minBuffer <= 0) {
            Log.w(TAG, "no $SAMPLE_RATE Hz mono capture on this device ($minBuffer)")
            return null
        }
        val record = try {
            @Suppress("MissingPermission") // hasMicPermission() gates every path here.
            AudioRecord(
                // VOICE_COMMUNICATION is what asks the platform for the
                // echo-cancelled, speech-tuned capture path. MIC would give us
                // a rawer signal and no AEC at all.
                MediaRecorder.AudioSource.VOICE_COMMUNICATION,
                SAMPLE_RATE,
                CHANNEL_IN,
                ENCODING,
                maxOf(minBuffer, FRAME_SAMPLES * CHUNK_FRAMES * 2) * BUFFER_FACTOR,
            )
        } catch (e: IllegalArgumentException) {
            Log.w(TAG, "could not open capture", e)
            return null
        }
        if (record.state != AudioRecord.STATE_INITIALIZED) {
            record.release()
            Log.w(TAG, "capture did not initialize")
            return null
        }
        return record
    }

    /**
     * Platform echo cancellation, noise suppression and gain control, where
     * the device offers them. Best-effort by design: they are hardware
     * features on most phones and simply absent on some, and a call without
     * them is worse, not broken. Nothing else in the pipeline provides them —
     * the visor's Opus path has no APM.
     */
    private fun attachEffects(sessionId: Int): List<android.media.audiofx.AudioEffect> {
        val effects = mutableListOf<android.media.audiofx.AudioEffect>()
        if (AcousticEchoCanceler.isAvailable()) {
            AcousticEchoCanceler.create(sessionId)?.let {
                it.enabled = true
                effects += it
            }
        }
        if (NoiseSuppressor.isAvailable()) {
            NoiseSuppressor.create(sessionId)?.let {
                it.enabled = true
                effects += it
            }
        }
        if (AutomaticGainControl.isAvailable()) {
            AutomaticGainControl.create(sessionId)?.let {
                it.enabled = true
                effects += it
            }
        }
        return effects
    }

    // --- visor → speaker ---

    private suspend fun playbackLoop() {
        while (coroutineContext.isActive) {
            try {
                api.voiceSpeakerStream().use { resp ->
                    if (!resp.isSuccessful) {
                        Log.w(TAG, "speaker stream rejected: ${resp.code}")
                        return@use
                    }
                    playInto(resp.body.byteStream())
                }
            } catch (e: IOException) {
                Log.d(TAG, "speaker stream ended: ${e.message}")
            }
            coroutineContext.ensureActive()
            delay(RETRY_MS)
        }
    }

    private fun playInto(stream: java.io.InputStream) {
        val track = openTrack() ?: return
        try {
            track.play()
            val bytes = ByteArray(FRAME_SAMPLES * CHUNK_FRAMES * 2)
            val samples = ShortArray(bytes.size / 2)
            // A frame can arrive split across reads; a half sample left in the
            // buffer has to lead the next read or every sample after it is
            // built from the wrong pair of bytes.
            var pending = 0
            while (true) {
                val n = stream.read(bytes, pending, bytes.size - pending)
                if (n < 0) return
                val total = pending + n
                val whole = total / 2
                val buffer = ByteBuffer.wrap(bytes, 0, whole * 2).order(ByteOrder.LITTLE_ENDIAN)
                for (i in 0 until whole) samples[i] = buffer.short
                if (whole > 0) track.write(samples, 0, whole)
                pending = total % 2
                if (pending == 1) bytes[0] = bytes[total - 1]
            }
        } finally {
            runCatching { track.pause() }
            runCatching { track.flush() }
            track.release()
        }
    }

    private fun openTrack(): AudioTrack? {
        val minBuffer = AudioTrack.getMinBufferSize(SAMPLE_RATE, CHANNEL_OUT, ENCODING)
        if (minBuffer <= 0) {
            Log.w(TAG, "no $SAMPLE_RATE Hz mono playback on this device ($minBuffer)")
            return null
        }
        val track = AudioTrack.Builder()
            .setAudioAttributes(
                AudioAttributes.Builder()
                    // VOICE_COMMUNICATION routes to the earpiece and rides the
                    // in-call volume stream, the way a phone call does.
                    .setUsage(AudioAttributes.USAGE_VOICE_COMMUNICATION)
                    .setContentType(AudioAttributes.CONTENT_TYPE_SPEECH)
                    .build(),
            )
            .setAudioFormat(
                AudioFormat.Builder()
                    .setEncoding(ENCODING)
                    .setSampleRate(SAMPLE_RATE)
                    .setChannelMask(CHANNEL_OUT)
                    .build(),
            )
            .setBufferSizeInBytes(maxOf(minBuffer, FRAME_SAMPLES * CHUNK_FRAMES * 2) * BUFFER_FACTOR)
            .setTransferMode(AudioTrack.MODE_STREAM)
            .build()
        if (track.state != AudioTrack.STATE_INITIALIZED) {
            track.release()
            Log.w(TAG, "playback did not initialize")
            return null
        }
        return track
    }

    private fun hasMicPermission(): Boolean =
        ContextCompat.checkSelfPermission(app, Manifest.permission.RECORD_AUDIO) ==
            PackageManager.PERMISSION_GRANTED

    companion object {
        private const val TAG = "SkywireVoice"

        /**
         * Fixed by the visor's call package (`call.SampleRate` /
         * `call.FrameSamples`). Changing either here alone corrupts audio
         * silently — both ends must move together.
         */
        const val SAMPLE_RATE = 48_000
        private const val FRAME_SAMPLES = 960 // 20 ms
        private const val CHUNK_FRAMES = 2

        private const val CHANNEL_IN = AudioFormat.CHANNEL_IN_MONO
        private const val CHANNEL_OUT = AudioFormat.CHANNEL_OUT_MONO
        private const val ENCODING = AudioFormat.ENCODING_PCM_16BIT

        /** Room for a scheduling hiccup without dropping frames. */
        private const val BUFFER_FACTOR = 4

        private const val RETRY_MS = 500L
        private const val PERMISSION_RETRY_MS = 1_500L
    }
}
