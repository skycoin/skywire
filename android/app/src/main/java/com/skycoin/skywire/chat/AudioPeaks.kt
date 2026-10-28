package com.skycoin.skywire.chat

import android.media.MediaCodec
import android.media.MediaExtractor
import android.media.MediaFormat
import java.io.File
import java.nio.ByteOrder
import kotlin.math.abs

/** Waveform bars for a voice message: decode to PCM and keep each bucket's peak. */
object AudioPeaks {

    /** [bars] values in 0..1 plus the clip's duration in ms, or null when it does not decode. */
    fun compute(file: File, bars: Int): Pair<FloatArray, Long>? = runCatching {
        val extractor = MediaExtractor()
        extractor.setDataSource(file.absolutePath)
        val track = (0 until extractor.trackCount).firstOrNull {
            extractor.getTrackFormat(it).getString(MediaFormat.KEY_MIME)?.startsWith("audio/") == true
        } ?: return null
        extractor.selectTrack(track)
        val format = extractor.getTrackFormat(track)
        val durationUs = if (format.containsKey(MediaFormat.KEY_DURATION)) format.getLong(MediaFormat.KEY_DURATION) else 0L
        val codec = MediaCodec.createDecoderByType(format.getString(MediaFormat.KEY_MIME)!!)
        codec.configure(format, null, null, 0)
        codec.start()
        val samples = ArrayList<Float>(1 shl 14)
        val info = MediaCodec.BufferInfo()
        var inputDone = false
        var outputDone = false
        var lastUs = 0L
        // Keep one value per ~10 ms so a long clip does not hold every sample.
        var acc = 0f
        var n = 0
        val stride = 480
        while (!outputDone) {
            if (!inputDone) {
                val inIdx = codec.dequeueInputBuffer(10_000)
                if (inIdx >= 0) {
                    val buf = codec.getInputBuffer(inIdx)!!
                    val size = extractor.readSampleData(buf, 0)
                    if (size < 0) {
                        codec.queueInputBuffer(inIdx, 0, 0, 0, MediaCodec.BUFFER_FLAG_END_OF_STREAM)
                        inputDone = true
                    } else {
                        lastUs = extractor.sampleTime
                        codec.queueInputBuffer(inIdx, 0, size, extractor.sampleTime, 0)
                        extractor.advance()
                    }
                }
            }
            val outIdx = codec.dequeueOutputBuffer(info, 10_000)
            if (outIdx >= 0) {
                val out = codec.getOutputBuffer(outIdx)!!.order(ByteOrder.LITTLE_ENDIAN).asShortBuffer()
                while (out.hasRemaining()) {
                    val v = abs(out.get().toInt()) / 32768f
                    if (v > acc) acc = v
                    if (++n >= stride) {
                        samples += acc
                        acc = 0f
                        n = 0
                    }
                }
                codec.releaseOutputBuffer(outIdx, false)
                if (info.flags and MediaCodec.BUFFER_FLAG_END_OF_STREAM != 0) outputDone = true
            }
        }
        codec.stop()
        codec.release()
        extractor.release()
        if (samples.isEmpty()) return null
        Pair(reduce(samples, bars), (if (durationUs > 0) durationUs else lastUs) / 1000)
    }.getOrNull()

    fun reduce(samples: List<Float>, bars: Int): FloatArray {
        val out = FloatArray(bars)
        if (samples.isEmpty()) return out
        for (i in 0 until bars) {
            val from = i * samples.size / bars
            val to = maxOf(from + 1, (i + 1) * samples.size / bars)
            var peak = 0f
            for (j in from until minOf(to, samples.size)) if (samples[j] > peak) peak = samples[j]
            out[i] = peak
        }
        val max = out.maxOrNull()?.takeIf { it > 0f } ?: return out
        for (i in out.indices) out[i] = (out[i] / max).coerceIn(0.06f, 1f)
        return out
    }
}
