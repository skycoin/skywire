package com.skycoin.skywire

import com.skycoin.skywire.ui.mail.attachmentWireSize
import org.junit.Assert.assertEquals
import org.junit.Test
import java.util.Base64

/**
 * The visor writes an attachment as MIME base64 and ends the last line with
 * CRLF too, which Java's MIME encoder leaves off.
 */
class MailSizeTest {

    private fun visorWrites(n: Int): Long =
        Base64.getMimeEncoder(76, "\r\n".toByteArray()).encode(ByteArray(n)).size + 2L

    @Test
    fun matchesTheMimeEncoding() {
        for (n in listOf(0, 1, 2, 3, 56, 57, 58, 114, 300 * 1024, 770_000, 1 shl 20)) {
            assertEquals("n=$n", visorWrites(n), attachmentWireSize(n.toLong()))
        }
    }
}
