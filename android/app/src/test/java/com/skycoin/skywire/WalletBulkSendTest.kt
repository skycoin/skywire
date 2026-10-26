package com.skycoin.skywire

import com.skycoin.skywire.ui.wallet.BulkParse
import com.skycoin.skywire.ui.wallet.DestDraft
import com.skycoin.skywire.ui.wallet.parseBulk
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** Bulk send text: one `address,amount[,hours]` per line, as skycoin-web takes it. */
class WalletBulkSendTest {

    @Test
    fun linesBecomeDestinationsAndBlankLinesAreSkipped() {
        val r = parseBulk(" a1, 1.5 \n\n a2,2\r\n")
        assertTrue(r is BulkParse.Ok)
        assertEquals(listOf(DestDraft("a1", "1.5"), DestDraft("a2", "2")), (r as BulkParse.Ok).destinations)
    }

    @Test
    fun hoursOnEveryLine() {
        val r = parseBulk("a1,1,10\na2,2,20") as BulkParse.Ok
        assertEquals(listOf("10", "20"), r.destinations.map { it.hoursText })
    }

    @Test
    fun errorsNameTheLine() {
        assertEquals(BulkParse.Empty, parseBulk(" \n "))
        assertEquals(2, (parseBulk("a1,1\na2") as BulkParse.BadLine).line)
        assertEquals(1, (parseBulk("a1,,1") as BulkParse.BadLine).line)
        // The blank line still counts, so the number matches what the user sees.
        assertEquals(3, (parseBulk("a1,1,5\n\na2,2") as BulkParse.MixedHours).line)
    }
}
