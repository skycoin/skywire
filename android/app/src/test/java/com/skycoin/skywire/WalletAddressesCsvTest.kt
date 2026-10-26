package com.skycoin.skywire

import com.skycoin.skywire.ui.wallet.addressesCsv
import com.skycoin.skywire.wallet.WalletMeta
import org.junit.Assert.assertEquals
import org.junit.Test

/** Saved addresses: one per line, receive then change, as skycoin-web writes them. */
class WalletAddressesCsvTest {

    @Test
    fun everyAddressOnItsOwnLine() {
        val w = WalletMeta("w", "BTC", "BTC", 0, receiveAddresses = listOf("r1", "r2"), changeAddresses = listOf("c1"))
        assertEquals("r1\nr2\nc1", addressesCsv(w))
    }
}
