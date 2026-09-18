package com.skycoin.skywire

import com.skycoin.skywire.wallet.CoinKind
import com.skycoin.skywire.wallet.CoinSpec
import com.skycoin.skywire.wallet.nodeUrlEditable
import com.skycoin.skywire.wallet.withNodeOverride
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Which node a coin is actually reached on.
 *
 * The address the app ships with is not reachable from everywhere — it can be
 * blocked, or throttled past the point where a wallet with history can finish
 * reading it — so a user may name another. That answer has to reach the code
 * that opens the socket and the screen that reports where it went, and it has
 * to be the same answer in both, or the wallet is talking to one node while
 * saying it is on another.
 */
class WalletNodeUrlTest {

    private val fiber = CoinSpec(
        id = "fiber-1",
        name = "Testcoin",
        ticker = "TST",
        kind = CoinKind.SKY_FIBER,
        nodeUrl = "https://node.example",
    )

    @Test
    fun theShippedAddressIsUsedWhenNothingIsSet() {
        assertSame(fiber, fiber.withNodeOverride(emptyMap()))
        assertEquals("https://node.example", fiber.withNodeOverride(emptyMap()).nodeUrl)
    }

    @Test
    fun anAddressTheUserSetWins() {
        val moved = fiber.withNodeOverride(mapOf("fiber-1" to "http://10.0.0.5:6420"))
        assertEquals("http://10.0.0.5:6420", moved.nodeUrl)
        // Nothing else about the coin moves with it.
        assertEquals(fiber.id, moved.id)
        assertEquals(fiber.ticker, moved.ticker)
        assertEquals(fiber.kind, moved.kind)
    }

    @Test
    fun anotherCoinsOverrideIsNotThisOnes() {
        assertSame(fiber, fiber.withNodeOverride(mapOf("SKY" to "http://elsewhere")))
    }

    @Test
    fun blankAndWhitespaceMeanTheShippedAddress() {
        // Clearing the field is how "go back to the default" is expressed, and
        // an entry that survived as whitespace must not point the wallet at
        // nothing.
        assertSame(fiber, fiber.withNodeOverride(mapOf("fiber-1" to "")))
        assertSame(fiber, fiber.withNodeOverride(mapOf("fiber-1" to "   ")))
    }

    @Test
    fun anOverrideEqualToTheShippedAddressChangesNothing() {
        assertSame(fiber, fiber.withNodeOverride(mapOf("fiber-1" to "https://node.example")))
    }

    @Test
    fun onlyCoinsWhoseNodeIsTheWholeStoryOfferTheSetting() {
        // A fiber daemon answers balances, history and broadcast alike, so one
        // address moves all of it. The Ethereum family reads history from a
        // separate indexer, so one field there would move half and silently
        // leave the rest — worse than not offering it.
        assertTrue(CoinSpec.SKY.nodeUrlEditable)
        assertTrue(fiber.nodeUrlEditable)
        assertFalse(CoinSpec.ETH.nodeUrlEditable)
        assertFalse(CoinSpec.USDT.nodeUrlEditable)
        assertFalse(CoinSpec.BTC.nodeUrlEditable)
    }

    @Test
    fun theShippedSkycoinNodeIsEncrypted() {
        // Every other endpoint in the app is https, including Skycoin's own
        // explorer. Over plain http the query string of a balance call carries
        // the whole address book, which is the one thing a wallet has to keep
        // to itself.
        assertTrue(CoinSpec.SKY.nodeUrl.startsWith("https://"))
        assertTrue(CoinSpec.BTC.nodeUrl.startsWith("https://"))
        assertTrue(CoinSpec.ETH.nodeUrl.startsWith("https://"))
    }
}
