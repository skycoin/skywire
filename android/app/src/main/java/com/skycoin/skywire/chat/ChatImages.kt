package com.skycoin.skywire.chat

import android.content.Context
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Canvas
import android.graphics.Matrix
import android.graphics.Rect
import android.net.Uri
import android.util.LruCache
import android.media.ExifInterface
import java.io.ByteArrayOutputStream

/** Decoding, a small memory cache, and the avatar encoder the profile and group meta share. */
object ChatImages {

    private val cache = object : LruCache<String, Bitmap>(CACHE_BYTES) {
        override fun sizeOf(key: String, value: Bitmap): Int = value.byteCount
    }

    fun cached(key: String): Bitmap? = cache.get(key)

    fun remember(key: String, bitmap: Bitmap) {
        cache.put(key, bitmap)
    }

    fun forget(key: String) {
        cache.remove(key)
    }

    /** Decode [bytes] no larger than [maxEdge] on its longer side. */
    fun decode(bytes: ByteArray, maxEdge: Int): Bitmap? {
        val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
        BitmapFactory.decodeByteArray(bytes, 0, bytes.size, bounds)
        if (bounds.outWidth <= 0 || bounds.outHeight <= 0) return null
        val opts = BitmapFactory.Options().apply {
            inSampleSize = sampleSize(bounds.outWidth, bounds.outHeight, maxEdge)
        }
        val raw = BitmapFactory.decodeByteArray(bytes, 0, bytes.size, opts) ?: return null
        return rotated(raw, orientation(bytes))
    }

    fun decodeUri(context: Context, uri: Uri, maxEdge: Int): Bitmap? = runCatching {
        val bytes = context.contentResolver.openInputStream(uri)?.use { it.readBytes() } ?: return null
        decode(bytes, maxEdge)
    }.getOrNull()

    /**
     * A square avatar under [maxBytes]: centre-cropped, scaled to at most
     * [maxEdge], PNG when that fits and JPEG at falling quality otherwise —
     * the same steps the web page took, so the cap is never the user's problem.
     */
    fun encodeAvatar(source: Bitmap, maxEdge: Int, maxBytes: Int): ByteArray? {
        var edge = minOf(maxEdge, minOf(source.width, source.height))
        while (edge >= MIN_EDGE) {
            val square = squareCrop(source, edge)
            val png = ByteArrayOutputStream().also { square.compress(Bitmap.CompressFormat.PNG, 100, it) }.toByteArray()
            if (png.size <= maxBytes) return png
            for (quality in JPEG_QUALITIES) {
                val jpeg = ByteArrayOutputStream().also { square.compress(Bitmap.CompressFormat.JPEG, quality, it) }.toByteArray()
                if (jpeg.size <= maxBytes) return jpeg
            }
            edge = edge * 3 / 4
        }
        return null
    }

    fun squareCrop(source: Bitmap, edge: Int): Bitmap {
        val side = minOf(source.width, source.height)
        val left = (source.width - side) / 2
        val top = (source.height - side) / 2
        val out = Bitmap.createBitmap(edge, edge, Bitmap.Config.ARGB_8888)
        Canvas(out).drawBitmap(source, Rect(left, top, left + side, top + side), Rect(0, 0, edge, edge), null)
        return out
    }

    private fun sampleSize(width: Int, height: Int, maxEdge: Int): Int {
        var sample = 1
        while (maxOf(width, height) / (sample * 2) >= maxEdge) sample *= 2
        return sample
    }

    private fun orientation(bytes: ByteArray): Int = runCatching {
        ExifInterface(bytes.inputStream()).getAttributeInt(ExifInterface.TAG_ORIENTATION, ExifInterface.ORIENTATION_NORMAL)
    }.getOrDefault(ExifInterface.ORIENTATION_NORMAL)

    private fun rotated(bitmap: Bitmap, orientation: Int): Bitmap {
        val degrees = when (orientation) {
            ExifInterface.ORIENTATION_ROTATE_90 -> 90f
            ExifInterface.ORIENTATION_ROTATE_180 -> 180f
            ExifInterface.ORIENTATION_ROTATE_270 -> 270f
            else -> return bitmap
        }
        val m = Matrix().apply { postRotate(degrees) }
        return Bitmap.createBitmap(bitmap, 0, 0, bitmap.width, bitmap.height, m, true)
    }

    private const val CACHE_BYTES = 24 * 1024 * 1024
    private const val MIN_EDGE = 48
    private val JPEG_QUALITIES = intArrayOf(92, 85, 75, 65, 55, 45)
}
