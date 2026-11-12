package com.skycoin.skywire

import com.skycoin.skywire.ui.mail.localMailDate
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.ZoneId
import java.util.Locale

class MailDateTest {

    private val shanghai = ZoneId.of("Asia/Shanghai")

    @Test
    fun showsTheDateInTheGivenZone() {
        val shown = localMailDate("Sun, 01 Nov 2026 14:40:28 +0000", shanghai, Locale.US)
        assertTrue(shown, shown.contains("10:40:28") && shown.contains("Nov 1, 2026"))
    }

    @Test
    fun dropsATrailingZoneComment() {
        val shown = localMailDate("Sun, 1 Nov 2026 14:40:28 +0000 (UTC)", shanghai, Locale.US)
        assertTrue(shown, shown.contains("10:40:28"))
    }

    @Test
    fun keepsAHeaderItCannotRead() {
        assertEquals("yesterday", localMailDate("yesterday", shanghai, Locale.US))
        assertEquals("", localMailDate("", shanghai, Locale.US))
    }
}
