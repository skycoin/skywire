package com.skycoin.skywire.chat

import android.annotation.SuppressLint
import android.content.Context
import android.media.MediaCodec
import android.media.MediaExtractor
import android.media.MediaFormat
import android.media.MediaMuxer
import android.media.MediaRecorder
import android.os.Build
import androidx.camera.core.CameraSelector
import androidx.camera.core.Preview
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.video.FallbackStrategy
import androidx.camera.video.FileOutputOptions
import androidx.camera.video.Quality
import androidx.camera.video.QualitySelector
import androidx.camera.video.Recorder
import androidx.camera.video.Recording
import androidx.camera.video.VideoCapture
import androidx.camera.video.ExperimentalPersistentRecording
import androidx.camera.video.VideoRecordEvent
import androidx.camera.view.PreviewView
import androidx.core.content.ContextCompat
import androidx.lifecycle.LifecycleOwner
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withTimeoutOrNull
import java.io.File
import java.nio.ByteBuffer
import kotlin.coroutines.resume

/**
 * Records a round video message with CameraX.
 *
 * Named `video-<stamp>.mp4`, the prefix every skychat UI reads as "recorded
 * in the composer" (index.html isVideoMessage), so the desktop shows it in
 * the round player rather than as an attached film.
 */
class VideoNoteRecorder(private val context: Context) {

    private var provider: ProcessCameraProvider? = null
    private var capture: VideoCapture<Recorder>? = null
    private var recording: Recording? = null
    private var finished: CompletableDeferred<Boolean>? = null
    private var startedAt = 0L
    private var audio: MediaRecorder? = null
    private var pictureFile: File? = null
    private var soundFile: File? = null

    var front = true
        private set

    var file: File? = null
        private set

    val isRecording: Boolean get() = recording != null

    val elapsedMs: Long get() = if (recording == null) 0 else System.currentTimeMillis() - startedAt

    suspend fun bind(owner: LifecycleOwner, view: PreviewView, front: Boolean = this.front) {
        this.front = front
        val p = provider ?: suspendCancellableCoroutine { cont ->
            val future = ProcessCameraProvider.getInstance(context)
            future.addListener({ cont.resume(future.get()) }, ContextCompat.getMainExecutor(context))
        }.also { provider = it }
        val preview = Preview.Builder().build().also { it.surfaceProvider = view.surfaceProvider }
        // The same capture across a camera flip keeps a persistent recording going.
        val video = capture ?: VideoCapture.withOutput(
            Recorder.Builder()
                // Profiles from the codecs rather than the camcorder table, which on some
                // devices pairs the video with AMR audio no browser plays.
                .setVideoCapabilitiesSource(Recorder.VIDEO_CAPABILITIES_SOURCE_CODEC_CAPABILITIES)
                .setQualitySelector(QualitySelector.from(Quality.SD, FallbackStrategy.higherQualityOrLowerThan(Quality.SD)))
                .setTargetVideoEncodingBitRate(BITRATE)
                .build(),
        )
        p.unbindAll()
        val selector = if (front) CameraSelector.DEFAULT_FRONT_CAMERA else CameraSelector.DEFAULT_BACK_CAMERA
        val chosen = if (p.hasCamera(selector)) selector else CameraSelector.DEFAULT_BACK_CAMERA
        p.bindToLifecycle(owner, chosen, preview, video)
        capture = video
    }

    @OptIn(ExperimentalPersistentRecording::class)
    @SuppressLint("MissingPermission")
    fun start(square: Boolean = false): File {
        val video = capture ?: error("camera not ready")
        val dir = File(context.cacheDir, "chat-rec").apply { mkdirs() }
        val name = (if (square) "video-sq-" else "video-") + clipStamp()
        val out = File(dir, "$name.mp4")
        val picture = File(dir, "$name.v.mp4")
        val sound = File(dir, "$name.a.m4a")
        val done = CompletableDeferred<Boolean>()
        // The camera records picture only; AAC comes from MediaRecorder and the two are
        // muxed at the end. CameraX takes the audio codec from the device's camcorder
        // profile, which on some devices is AMR — a clip no browser can play the sound of.
        recording = video.output
            .prepareRecording(context, FileOutputOptions.Builder(picture).build())
            .asPersistentRecording()
            .start(ContextCompat.getMainExecutor(context)) { event ->
                if (event is VideoRecordEvent.Finalize) done.complete(!event.hasError() || event.error == VideoRecordEvent.Finalize.ERROR_SOURCE_INACTIVE)
            }
        audio = runCatching { startAudio(sound) }.getOrNull()
        finished = done
        file = out
        pictureFile = picture
        soundFile = sound
        startedAt = System.currentTimeMillis()
        return out
    }

    private fun startAudio(target: File): MediaRecorder {
        val r = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) MediaRecorder(context) else @Suppress("DEPRECATION") MediaRecorder()
        r.setAudioSource(MediaRecorder.AudioSource.CAMCORDER)
        r.setOutputFormat(MediaRecorder.OutputFormat.MPEG_4)
        r.setAudioEncoder(MediaRecorder.AudioEncoder.AAC)
        r.setAudioEncodingBitRate(96_000)
        r.setAudioSamplingRate(44_100)
        r.setAudioChannels(1)
        r.setOutputFile(target.absolutePath)
        try {
            r.prepare()
            r.start()
        } catch (e: Exception) {
            r.release()
            throw e
        }
        return r
    }

    /** Stops; returns the clip unless [discard] or it did not record. */
    suspend fun stop(discard: Boolean = false): File? {
        val r = recording ?: return null
        recording = null
        val tooShort = System.currentTimeMillis() - startedAt < MIN_MS
        r.stop()
        val a = audio
        audio = null
        val audioOk = a?.let { runCatching { it.stop() }.isSuccess.also { _ -> it.release() } } ?: false
        val ok = withTimeoutOrNull(FINALIZE_MS) { finished?.await() } ?: false
        val out = file
        val picture = pictureFile
        val sound = soundFile
        file = null
        pictureFile = null
        soundFile = null
        try {
            if (discard || tooShort || !ok || out == null || picture == null || picture.length() == 0L) return null
            val muxed = audioOk && sound != null && sound.length() > 0 &&
                withContext(Dispatchers.IO) { runCatching { mux(picture, sound, out) }.isSuccess }
            if (!muxed) picture.renameTo(out)
            return out.takeIf { it.length() > 0 }
        } finally {
            picture?.delete()
            sound?.delete()
        }
    }

    /** Copy the picture track and the AAC track into one MP4, no re-encoding. */
    private fun mux(picture: File, sound: File, out: File) {
        val muxer = MediaMuxer(out.absolutePath, MediaMuxer.OutputFormat.MUXER_OUTPUT_MPEG_4)
        val sources = listOf(picture, sound).map { f ->
            MediaExtractor().apply { setDataSource(f.absolutePath) }
        }
        try {
            val tracks = sources.map { ex ->
                val i = (0 until ex.trackCount).first { ex.getTrackFormat(it).getString(MediaFormat.KEY_MIME)?.let { m -> m.startsWith("video/") || m.startsWith("audio/") } == true }
                ex.selectTrack(i)
                val format = ex.getTrackFormat(i)
                if (format.containsKey(MediaFormat.KEY_ROTATION)) muxer.setOrientationHint(format.getInteger(MediaFormat.KEY_ROTATION))
                muxer.addTrack(format)
            }
            muxer.start()
            val buf = ByteBuffer.allocate(1 shl 20)
            val info = MediaCodec.BufferInfo()
            sources.forEachIndexed { n, ex ->
                while (true) {
                    val size = ex.readSampleData(buf, 0)
                    if (size < 0) break
                    info.set(0, size, ex.sampleTime, if (ex.sampleFlags and MediaExtractor.SAMPLE_FLAG_SYNC != 0) MediaCodec.BUFFER_FLAG_KEY_FRAME else 0)
                    muxer.writeSampleData(tracks[n], buf, info)
                    ex.advance()
                }
            }
            muxer.stop()
        } finally {
            muxer.release()
            sources.forEach { it.release() }
        }
    }

    fun release() {
        recording?.stop()
        recording = null
        audio?.let { runCatching { it.stop() }; it.release() }
        audio = null
        file?.delete()
        file = null
        pictureFile?.delete()
        soundFile?.delete()
        provider?.unbindAll()
        capture = null
    }

    private companion object {
        const val BITRATE = 1_200_000
        const val MIN_MS = 500L
        const val FINALIZE_MS = 5_000L
    }
}
