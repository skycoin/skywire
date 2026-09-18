package com.skycoin.skywire

import com.skycoin.skywire.wallet.WalletMeta
import com.skycoin.skywire.wallet.addressScanPending
import com.skycoin.skywire.wallet.settledAddresses
import com.skycoin.wallet.AddressBook
import kotlinx.serialization.json.Json
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * A restored wallet has to end up holding every address its phrase has used,
 * and it has to be honest in the meantime.
 *
 * Restoring asks the node which of the seed's addresses have been used. When
 * that question cannot be put — the node is slow, the link is bad — the
 * wallet is still created, holding only the first address. Nothing used to
 * record that the answer was missing, so the wallet stayed wrong forever and
 * said nothing about it: the coins on the other addresses were invisible and
 * unspendable, and the balance on screen looked like the whole of it.
 */
class WalletAddressScanTest {

    private val json = Json { ignoreUnknownKeys = true }

    private fun meta(vararg receive: String, scannedAt: Long = 0) = WalletMeta(
        id = "w1",
        coinId = "SKY",
        name = "SKY",
        createdAtMs = 1,
        receiveAddresses = receive.toList(),
        addressScanAtMs = scannedAt,
    )

    @Test
    fun anUnansweredScanIsPendingAndAnAnsweredOneIsNot() {
        assertTrue(meta("a").addressScanPending)
        assertFalse(meta("a", scannedAt = 1_000).addressScanPending)
    }

    @Test
    fun aWalletStoredBeforeTheFieldExistedIsReAsked() {
        // Every wallet already on a phone is in this shape, including any
        // that was silently truncated by a restore on a bad connection.
        // Defaulting to pending is what gets those repaired.
        val legacy = """{"id":"w1","coinId":"SKY","name":"SKY","createdAtMs":1,
            |"receiveAddresses":["a"]}""".trimMargin()
        val restored = json.decodeFromString(WalletMeta.serializer(), legacy)
        assertEquals(listOf("a"), restored.receiveAddresses)
        assertTrue(restored.addressScanPending)
    }

    @Test
    fun aScanThatFoundMoreAddressesGrowsTheWallet() {
        val settled = settledAddresses(meta("a"), AddressBook(listOf("a", "b", "c"), emptyList()), 9)
        assertEquals(listOf("a", "b", "c"), settled.receiveAddresses)
        assertEquals(9, settled.addressScanAtMs)
        assertFalse(settled.addressScanPending)
    }

    @Test
    fun aScanNeverTakesAwayAnAddressTheWalletAlreadyHandedOut() {
        // The user asked for three receive addresses locally and may have
        // given the third to someone. The chain has only seen the first, so
        // the scan reports one — dropping the other two would point coins at
        // an address the wallet no longer watches.
        val held = meta("a", "b", "c")
        val settled = settledAddresses(held, AddressBook(listOf("a"), emptyList()), 9)
        assertEquals(listOf("a", "b", "c"), settled.receiveAddresses)
        assertFalse("but the question is still answered", settled.addressScanPending)
    }

    @Test
    fun achangeAddressesAreHeldToTheSameRule() {
        // SkyFiberWalletCore derives no change addresses at all, so a scan
        // there hands back an empty change list. It must not erase one.
        val held = meta("a").copy(changeAddresses = listOf("c1"))
        val settled = settledAddresses(held, AddressBook(listOf("a"), emptyList()), 9)
        assertEquals(listOf("c1"), settled.changeAddresses)
    }

    @Test
    fun settlingIsWhatClosesTheQuestion() {
        val settled = settledAddresses(meta("a"), AddressBook(listOf("a"), emptyList()), 9)
        assertEquals(9, settled.addressScanAtMs)
        // And a wallet that was already settled keeps a real timestamp.
        val again = settledAddresses(settled, AddressBook(listOf("a", "b"), emptyList()), 20)
        assertEquals(20, again.addressScanAtMs)
        assertEquals(listOf("a", "b"), again.receiveAddresses)
    }
}
