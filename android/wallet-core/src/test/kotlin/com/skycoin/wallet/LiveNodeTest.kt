package com.skycoin.wallet

import com.skycoin.wallet.skycoin.SkyFiberWalletCore
import kotlinx.coroutines.runBlocking
import okhttp3.OkHttpClient
import org.junit.Assume.assumeTrue
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertTrue

/**
 * Live checks against the shipped node — parsing production JSON, not a
 * mock's idea of it. Off by default (CI must not depend on the internet):
 * run with SKYWIRE_NET_TESTS=1 ./gradlew :wallet-core:test
 */
class LiveNodeTest {

    // A Skycoin distribution address: public, funded, with on-chain history.
    private val richAddress = "R6aHqKWSQfvpdo2fGSrq4F1RYXkBWR9HHJ"

    private fun core() = SkyFiberWalletCore(
        "http://node.skycoin.com",
        OkHttpClient.Builder()
            .connectTimeout(10, TimeUnit.SECONDS)
            .readTimeout(30, TimeUnit.SECONDS)
            .build(),
    )

    private fun netTestsEnabled() = System.getenv("SKYWIRE_NET_TESTS") == "1"

    @Test
    fun balanceAndHistoryParseFromProduction() {
        assumeTrue(netTestsEnabled())
        runBlocking {
            val book = AddressBook(listOf(richAddress), emptyList())
            // The address may have been emptied since distribution — the
            // point is that production JSON parses, not what it holds.
            core().balance(book)

            val history = core().history(book)
            assertTrue(history.isNotEmpty(), "distribution address has transactions")
            val tx = history.last()
            assertTrue(tx.txid.length == 64)
            assertTrue(tx.confirmed)
            assertTrue(tx.confirmations > 0)
            assertTrue(tx.timestamp > 1_400_000_000L)
        }
    }
}
