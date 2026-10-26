package com.skycoin.skywire.ui.components

import android.content.Context
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.net.Uri
import com.google.zxing.BinaryBitmap
import com.google.zxing.DecodeHintType
import com.google.zxing.MultiFormatReader
import com.google.zxing.RGBLuminanceSource
import com.google.zxing.common.HybridBinarizer

/**
 * The text of the QR code in [bitmap], or null when there is none.
 *
 * TRY_HARDER because these are photographs of screens and camera frames,
 * not clean renders: the extra passes find a code that is slightly rotated
 * or out of focus.
 */
fun decodeQr(bitmap: Bitmap): String? {
    val width = bitmap.width
    val height = bitmap.height
    val pixels = IntArray(width * height)
    bitmap.getPixels(pixels, 0, width, 0, 0, width, height)
    val source = RGBLuminanceSource(width, height, pixels)
    val hints = mapOf(DecodeHintType.TRY_HARDER to true)
    return runCatching {
        MultiFormatReader().decode(BinaryBitmap(HybridBinarizer(source)), hints).text
    }.getOrNull()?.takeIf { it.isNotEmpty() }
}

/**
 * Decodes a QR code from a picked image. The image is read scaled down to
 * at most [maxEdge] pixels on its longer side, which keeps a 50 MP photo
 * from taking the heap and still leaves a code its readable size.
 */
fun decodeQrImage(context: Context, uri: Uri, maxEdge: Int = 1600): String? {
    val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
    context.contentResolver.openInputStream(uri)?.use { BitmapFactory.decodeStream(it, null, bounds) }
    var sample = 1
    while (maxOf(bounds.outWidth, bounds.outHeight) / sample > maxEdge) sample *= 2
    val bitmap = context.contentResolver.openInputStream(uri)?.use {
        BitmapFactory.decodeStream(it, null, BitmapFactory.Options().apply { inSampleSize = sample })
    } ?: return null
    return try {
        decodeQr(bitmap)
    } finally {
        bitmap.recycle()
    }
}
