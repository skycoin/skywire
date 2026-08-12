package com.skycoin.skywire.ui.chat

import android.graphics.Bitmap
import android.graphics.Color
import android.util.Base64
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.google.zxing.BarcodeFormat
import com.google.zxing.qrcode.QRCodeWriter
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import java.io.ByteArrayOutputStream

/**
 * The host's QR decode, which the chat page leans on because the Android
 * WebView has no BarcodeDetector.
 *
 * Round-trips through the same shapes the page produces: a data URL carrying
 * a JPEG or PNG of a rendered code. On device rather than the JVM because
 * every part of it is Android — BitmapFactory, Base64, Bitmap.compress — and
 * a stub for those would be testing the stub.
 */
@RunWith(AndroidJUnit4::class)
class QrBridgeTest {

    private val bridge = QrBridge()

    /** A QR of [text], rendered like the page renders one: black on white. */
    private fun qrDataUrl(
        text: String,
        size: Int = 600,
        format: Bitmap.CompressFormat = Bitmap.CompressFormat.PNG,
        quality: Int = 100,
    ): String {
        val matrix = QRCodeWriter().encode(text, BarcodeFormat.QR_CODE, size, size)
        val bitmap = Bitmap.createBitmap(size, size, Bitmap.Config.ARGB_8888)
        for (x in 0 until size) {
            for (y in 0 until size) {
                bitmap.setPixel(x, y, if (matrix[x, y]) Color.BLACK else Color.WHITE)
            }
        }
        val out = ByteArrayOutputStream()
        bitmap.compress(format, quality, out)
        val mime = if (format == Bitmap.CompressFormat.PNG) "image/png" else "image/jpeg"
        return "data:$mime;base64," + Base64.encodeToString(out.toByteArray(), Base64.NO_WRAP)
    }

    @Test
    fun decodesASkychatAddress() {
        val address = "skychat://02e52cafdca05b22420ba8d26133f48c0dedfc599df7243ec3620727a816815c30"
        assertEquals(address, bridge.decode(qrDataUrl(address)))
    }

    /** The camera path sends JPEG, and lossy compression must not break it. */
    @Test
    fun decodesALossyJpegFrame() {
        val address = "skychat://02e52cafdca05b22420ba8d26133f48c0dedfc599df7243ec3620727a816815c30"
        val decoded = bridge.decode(
            qrDataUrl(address, format = Bitmap.CompressFormat.JPEG, quality = 80),
        )
        assertEquals(address, decoded)
    }

    /** The camera's downscaled frames are small; a code still has to survive. */
    @Test
    fun decodesADownscaledFrame() {
        val address = "skychat://02e52cafdca05b22420ba8d26133f48c0dedfc599df7243ec3620727a816815c30"
        val decoded = bridge.decode(
            qrDataUrl(address, size = 320, format = Bitmap.CompressFormat.JPEG, quality = 80),
        )
        assertEquals(address, decoded)
    }

    /** Nothing to find is an empty answer, not a crash — the camera loop relies on it. */
    @Test
    fun answersEmptyForAnImageWithNoCode() {
        val blank = Bitmap.createBitmap(200, 200, Bitmap.Config.ARGB_8888).also {
            it.eraseColor(Color.WHITE)
        }
        val out = ByteArrayOutputStream()
        blank.compress(Bitmap.CompressFormat.PNG, 100, out)
        val url = "data:image/png;base64," + Base64.encodeToString(out.toByteArray(), Base64.NO_WRAP)
        assertEquals("", bridge.decode(url))
    }

    @Test
    fun answersEmptyForRubbishInput() {
        for (input in listOf(null, "", "not-a-data-url", "data:image/png;base64,zzzz")) {
            assertEquals("input $input should decode to nothing", "", bridge.decode(input))
        }
    }

    /** The page hands over a data URL; the prefix must not reach the decoder. */
    @Test
    fun toleratesTheDataUrlPrefix() {
        val address = "skychat://02e52cafdca05b22420ba8d26133f48c0dedfc599df7243ec3620727a816815c30"
        val url = qrDataUrl(address)
        assertTrue("test fixture is not a data URL", url.startsWith("data:image/png;base64,"))
        assertEquals(address, bridge.decode(url))
    }
}
