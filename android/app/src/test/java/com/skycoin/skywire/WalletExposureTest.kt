package com.skycoin.skywire

import com.skycoin.skywire.wallet.CachedTx
import com.skycoin.skywire.wallet.WalletSnapshot
import com.skycoin.skywire.wallet.exposedAddresses
import com.skycoin.skywire.wallet.exposedWithCoins
import org.junit.Assert.assertEquals
import org.junit.Test

/** Which of a wallet's addresses have their public keys on the chain. */
class WalletExposureTest {

    private fun tx(incoming: Boolean, vararg own: String) =
        CachedTx(txid = own.joinToString(), incoming = incoming, amount = 1u, timestamp = 0, confirmed = false, confirmations = 0, own = own.toList())

    @Test
    fun onlyAddressesThatSentAreExposed() {
        val snap = WalletSnapshot(
            txs = listOf(tx(true, "a1"), tx(false, "a2", "a3"), tx(true, "a3")),
            perAddress = mapOf("a1" to 5uL, "a2" to 0uL, "a3" to 7uL),
        )
        assertEquals(setOf("a2", "a3"), snap.exposedAddresses)
        assertEquals(setOf("a3"), snap.exposedWithCoins)
    }
}
