package com.skycoin.skywire

import com.skycoin.skywire.wallet.CachedTx
import com.skycoin.skywire.wallet.WalletSnapshot
import com.skycoin.skywire.wallet.historyBehind
import com.skycoin.skywire.wallet.mergeSnapshot
import com.skycoin.wallet.TxRecord
import com.skycoin.wallet.WalletBalance
import kotlinx.serialization.json.Json
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * What a cached wallet view is allowed to claim about itself.
 *
 * A refresh fetches the balance and the transaction list separately, because
 * they cost wildly different amounts — 268 bytes against 19.5 MB for the same
 * address, measured against node.skycoin.com — and the big half is allowed to
 * miss so that a slow connection cannot cost someone their balance and their
 * ability to send. The price of that is a snapshot whose two halves can be of
 * different ages, and one screen reads the difference: an empty tx list is
 * either "nothing has ever moved" or "the list did not arrive", and telling
 * someone looking for their coins the wrong one is the worst answer available.
 */
class WalletSnapshotTest {

    private val json = Json { ignoreUnknownKeys = true }

    @Test
    fun aSnapshotWhoseHalvesAgreeIsComplete() {
        val snap = WalletSnapshot(confirmed = 1u, fetchedAtMs = 1_000, historyFetchedAtMs = 1_000)
        assertFalse(snap.historyBehind)
    }

    @Test
    fun aBalanceFresherThanItsHistoryIsBehind() {
        // The history request failed, so the balance moved on without it.
        val snap = WalletSnapshot(confirmed = 1u, fetchedAtMs = 2_000, historyFetchedAtMs = 1_000)
        assertTrue(snap.historyBehind)
    }

    @Test
    fun historyThatNeverArrivedIsBehind() {
        val snap = WalletSnapshot(confirmed = 1u, fetchedAtMs = 2_000)
        assertTrue(snap.historyBehind)
    }

    @Test
    fun aCacheWrittenBeforeTheFieldExistedDoesNotClaimAHistory() {
        // Every wallet already on a phone has a file in exactly this shape.
        // Defaulting the missing field to 0 is what makes it read as "not
        // vouched for" rather than as a complete and empty history.
        val legacy = """{"confirmed":42,"predicted":42,"txs":[],"fetchedAtMs":1700000000000}"""
        val snap = json.decodeFromString(WalletSnapshot.serializer(), legacy)
        assertEquals(42uL, snap.confirmed)
        assertEquals(1_700_000_000_000, snap.fetchedAtMs)
        assertTrue("a legacy cache must not pass as a fetched history", snap.historyBehind)
    }

    @Test
    fun theFieldSurvivesARoundTrip() {
        val snap = WalletSnapshot(confirmed = 7u, fetchedAtMs = 5, historyFetchedAtMs = 5)
        val back = json.decodeFromString(
            WalletSnapshot.serializer(),
            json.encodeToString(WalletSnapshot.serializer(), snap),
        )
        assertEquals(snap, back)
        assertFalse(back.historyBehind)
    }

    // --- what a refresh folds into the cache ---------------------------------

    private fun balance(coins: ULong) = WalletBalance(
        confirmed = coins, predicted = coins, hours = 0u, spendableOutputs = 1,
    )

    private fun record(txid: String) = TxRecord(
        txid = txid, incoming = true, amount = 1u, party = null,
        timestamp = 1, confirmed = true, confirmations = 1, fee = 0u,
    )

    @Test
    fun afetchedHistoryReplacesTheCachedOneAndIsVouchedFor() {
        val previous = WalletSnapshot(
            txs = listOf(CachedTx(txid = "old", incoming = true, amount = 1u, timestamp = 1, confirmed = true, confirmations = 1)),
            fetchedAtMs = 1_000,
            historyFetchedAtMs = 1_000,
        )
        val merged = mergeSnapshot(balance(50u), listOf(record("new")), previous, 2_000)
        assertEquals(50uL, merged.confirmed)
        assertEquals(listOf("new"), merged.txs.map { it.txid })
        assertEquals(2_000, merged.historyFetchedAtMs)
        assertFalse(merged.historyBehind)
    }

    @Test
    fun aMissedHistoryKeepsTheLastOneAndItsAge() {
        // The whole point of splitting the two halves: the balance is current
        // and spendable even though the 19 MB list did not arrive, and the
        // list that is shown is honestly dated to when it did.
        val previous = WalletSnapshot(
            txs = listOf(CachedTx(txid = "old", incoming = true, amount = 1u, timestamp = 1, confirmed = true, confirmations = 1)),
            fetchedAtMs = 1_000,
            historyFetchedAtMs = 1_000,
        )
        val merged = mergeSnapshot(balance(50u), null, previous, 2_000)
        assertEquals("the balance still lands", 50uL, merged.confirmed)
        assertEquals("the last list is kept", listOf("old"), merged.txs.map { it.txid })
        assertEquals("and keeps its own age", 1_000, merged.historyFetchedAtMs)
        assertEquals(2_000, merged.fetchedAtMs)
        assertTrue(merged.historyBehind)
    }

    @Test
    fun aMissedHistoryWithNothingCachedIsEmptyButNotVouchedFor() {
        // A first refresh after a restore, on the connection that could not
        // fetch the list. An empty list here must never read as "this wallet
        // has no transactions".
        val merged = mergeSnapshot(balance(50u), null, null, 2_000)
        assertEquals(50uL, merged.confirmed)
        assertTrue(merged.txs.isEmpty())
        assertEquals(0, merged.historyFetchedAtMs)
        assertTrue(merged.historyBehind)
    }

    @Test
    fun anEmptyHistoryThatDidArriveIsVouchedFor() {
        // A genuinely unused wallet: the list is empty AND fetched, which is
        // the one case the screen may call "nothing here yet".
        val merged = mergeSnapshot(balance(0u), emptyList(), null, 2_000)
        assertTrue(merged.txs.isEmpty())
        assertFalse(merged.historyBehind)
    }
}
