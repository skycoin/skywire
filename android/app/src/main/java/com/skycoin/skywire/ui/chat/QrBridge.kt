package com.skycoin.skywire.ui.chat

import android.graphics.BitmapFactory
import android.util.Base64
import android.util.Log
import android.webkit.JavascriptInterface
import com.skycoin.skywire.ui.components.decodeQr

/**
 * QR decoding for the chat page, done by the host.
 *
 * The page decodes with `BarcodeDetector`, which the Android WebView does not
 * have — it is a Chrome-on-Android API, and WebView never wired it up. Both of
 * the page's entry points (the camera and picking a saved image) go through
 * that one call, so on the phone both failed, with a toast telling the user to
 * "use the Android app" while they were standing in it. Scanning the same code
 * with the phone's own camera app worked, because that decodes it elsewhere and
 * arrives as a `skychat://` link.
 *
 * So the page keeps the camera, the picker and the dialog; only the decode
 * crosses over. That keeps one set of scanning UI for every platform and adds
 * no dependency — zxing is already here for the wallet's address scanner.
 *
 * Exposed as a narrow surface on purpose: one method, images in, text out, no
 * access to anything else. [addJavascriptInterface] hands this object to every
 * script in the WebView, and the WebView loads exactly one origin (the local
 * skychat app), but the rule still holds — nothing here reads a file, touches
 * a preference or names a peer.
 */
class QrBridge {

    /**
     * Decode a QR code out of a data URL. Returns the decoded text, or an
     * empty string when there is no code in the image — the page treats those
     * the same way it treats a frame with nothing in it yet.
     *
     * A data URL rather than a bitmap handle because both callers already have
     * one: the camera path paints a video frame onto a canvas, and the file
     * path reads the picked image. Neither needs a second Android surface.
     */
    @JavascriptInterface
    fun decode(dataUrl: String?): String {
        val payload = dataUrl?.substringAfter("base64,", "").orEmpty()
        if (payload.isEmpty()) {
            Log.w(TAG, "decode called with no base64 payload (len=${dataUrl?.length ?: 0})")
            return ""
        }
        return try {
            val bytes = Base64.decode(payload, Base64.DEFAULT)
            val bitmap = BitmapFactory.decodeByteArray(bytes, 0, bytes.size)
            if (bitmap == null) {
                Log.w(TAG, "image bytes did not decode (b64=${payload.length}, bytes=${bytes.size})")
                return ""
            }
            val width = bitmap.width
            val height = bitmap.height
            // The TRY_HARDER cost lands on one frame every fifth of a second.
            val text = decodeQr(bitmap).also { bitmap.recycle() }
            if (text == null) {
                // "No code in this frame" arrives many times a second while
                // scanning, so nothing here is logged above debug.
                Log.d(TAG, "no QR in frame")
                return ""
            }
            Log.i(TAG, "decoded ${width}x$height -> ${text.length} chars")
            text
        } catch (e: Exception) {
            Log.d(TAG, "frame did not decode: ${e.javaClass.simpleName}")
            ""
        }
    }

    private companion object {
        const val TAG = "SkywireQr"

        /** The name the page looks for. Changing it breaks _qrDetector's fallback. */
        const val JS_NAME = "SkywireQr"
    }
}
