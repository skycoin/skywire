package com.skycoin.skywire

import com.skycoin.skywire.ui.mail.DraftAttachment
import com.skycoin.skywire.ui.mail.draftFilesSize
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class MailSizeTest {

    /** A tester's 4.9 MB and 2.58 MB files were refused against a 10 MB limit. */
    @Test
    fun theLimitCountsTheFilesNotTheirBase64() {
        val files = listOf(5_138_022, 2_580_000).map { DraftAttachment("f.mp3", "audio/mpeg", ByteArray(it)) }
        assertEquals(7_718_022L, draftFilesSize(files))
        assertTrue(draftFilesSize(files) <= 10L shl 20)
    }
}
