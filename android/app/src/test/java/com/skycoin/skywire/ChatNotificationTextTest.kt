package com.skycoin.skywire

import com.skycoin.skywire.core.localizeFileBody
import org.junit.Assert.assertEquals
import org.junit.Test

/** What a SkyChat notification says about a file: skychat writes it in English, the phone in its own language. */
class ChatNotificationTextTest {

    private fun shown(body: String) = localizeFileBody(body, "语音消息", "视频消息")

    @Test
    fun aRecordedClipIsNamedInThePhonesLanguage() {
        // group.go deliverGroupMessages: "<sender>: 📎 <name>".
        assertEquals("Acer: \uD83C\uDFA4 语音消息", shown("Acer: \uD83D\uDCCE voice-20261116-134400.m4a"))
        assertEquals("Acer: \uD83D\uDCF9 视频消息", shown("Acer: \uD83D\uDCCE video-sq-20261116-134400.mp4"))
        // filexfer.go onXferDone and holdInbound: "📎 <Kind>: <name>".
        assertEquals("\uD83C\uDFA4 语音消息", shown("\uD83D\uDCCE Audio: voice-20261120-171651.oga"))
        assertEquals("\uD83D\uDCF9 视频消息", shown("\uD83D\uDCCE Video: video-20261120-171651.mp4"))
    }

    @Test
    fun anAttachmentKeepsItsNameWithoutTheEnglishKind() {
        assertEquals("\uD83D\uDCCE photo.jpg", shown("\uD83D\uDCCE Image: photo.jpg"))
        assertEquals("Acer: \uD83D\uDCCE report.pdf", shown("Acer: \uD83D\uDCCE report.pdf"))
        // A name that only starts like a clip, and plain text, are left alone.
        assertEquals("\uD83D\uDCCE voice-notes.txt", shown("\uD83D\uDCCE File: voice-notes.txt"))
        assertEquals("Acer: Audio: later", shown("Acer: Audio: later"))
    }
}
