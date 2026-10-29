package com.skycoin.skywire

import com.skycoin.skywire.api.DexOrder
import com.skycoin.skywire.dex.DexLegacyImport
import com.skycoin.skywire.dex.mergeHistory
import com.skycoin.skywire.ui.dex.DexArgs
import com.skycoin.skywire.ui.dex.dexNumber
import com.skycoin.skywire.ui.dex.parseDecimal
import com.skycoin.skywire.ui.dex.paymentUri
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * What the native SkyDEX screens must do the way the web trading page did:
 * print amounts, build the links wallets scan, and keep finished trades.
 */
class DexNativeTest {

    /** Market amounts are JSON floats; the page never showed exponent form. */
    @Test
    fun amountsPrintAsPlainDecimals() {
        assertEquals("0.1", dexNumber(0.1))
        assertEquals("5", dexNumber(5.0))
        assertEquals("0.0000001", dexNumber(1e-7))
        assertEquals("12.345", dexNumber(12.345))
    }

    /** QrButton.jsx buildUri: known schemes only, 8 places, trailing zeros trimmed. */
    @Test
    fun paymentLinksMatchThePage() {
        assertEquals("bitcoin:1abc?amount=0.0001", paymentUri("1abc", 0.0001, "BTC"))
        assertEquals("skycoin:2xyz?amount=12.5", paymentUri("2xyz", 12.5, "sky"))
        assertEquals("litecoin:Labc?amount=0.0000001", paymentUri("Labc", 1e-7, "LTC"))
        assertNull(paymentUri("addr", 1.0, "XMR"))
        assertNull(paymentUri("addr", 0.0, "BTC"))
        assertNull(paymentUri("", 1.0, "BTC"))
    }

    /** Decimal keyboards in es and fr offer a comma. */
    @Test
    fun decimalsAcceptEitherSeparator() {
        assertEquals(1.5, parseDecimal("1,5")!!, 0.0)
        assertEquals(0.25, parseDecimal(" 0.25 ")!!, 0.0)
        assertNull(parseDecimal(""))
        assertNull(parseDecimal("abc"))
    }

    /** api.js mergeTradeHistory: only finished orders, tagged once, purged ones kept. */
    @Test
    fun historyKeepsFinishedOrdersTheMarketPurged() {
        val purged = DexOrder(id = "old", status = "completed", marketPk = "02aa", marketName = "Old", savedAt = 5)
        val market = listOf(
            DexOrder(id = "a", status = "completed"),
            DexOrder(id = "b", status = "pending_payment"),
            DexOrder(id = "c", status = "canceled"),
        )
        val merged = mergeHistory(listOf(purged), market, "03bb", "Bazaar", now = 100)
        assertEquals(listOf("old", "a", "c"), merged.map { it.id })
        assertEquals("Bazaar", merged[1].marketName)
        assertEquals(100L, merged[1].savedAt)
        assertEquals("Old", merged[0].marketName)

        // Seen again later: refreshed, but the first saved time stays.
        val again = mergeHistory(merged, listOf(DexOrder(id = "a", status = "completed", price = 2.0)), "03bb", "", now = 900)
        val a = again.first { it.id == "a" }
        assertEquals(2.0, a.price, 0.0)
        assertEquals(100L, a.savedAt)
        assertEquals("Bazaar", a.marketName)
    }

    /** A self-trade is one order id seen as a buy and as a sell; both sides stay. */
    @Test
    fun historyKeepsBothSidesOfASelfTrade() {
        val both = listOf(DexOrder(id = "s", type = "buy", status = "completed"), DexOrder(id = "s", type = "sell", status = "completed"))
        assertEquals(listOf("buy", "sell"), mergeHistory(emptyList(), both, "03bb", "", now = 1).map { it.type })
    }

    /** The keys api.js wrote to localStorage. */
    @Test
    fun legacyStorageParses() {
        val storage = mapOf(
            "exchange:wallets" to """{"SKY":"2abc ","BTC":"1btc","LTC":""}""",
            "exchange:markets" to """[{"pk":"03aa","name":"Bazaar","ts":42},{"name":"no key"}]""",
            "exchange:history" to """[{"id":"x1","type":"buy","sell_coin":"SKY","amount":1.5,"status":"completed","market_pk":"03aa","saved_at":7},{"type":"sell"}]""",
            "unrelated" to "1",
        )
        val legacy = DexLegacyImport.parse(storage)
        assertEquals(mapOf("SKY" to "2abc", "BTC" to "1btc"), legacy.wallets)
        assertEquals(1, legacy.markets.size)
        assertEquals("Bazaar", legacy.markets[0].name)
        assertEquals(42L, legacy.markets[0].ts)
        assertEquals(listOf("x1"), legacy.history.map { it.id })
        assertEquals(1.5, legacy.history[0].amount, 0.0)
        assertEquals(7L, legacy.history[0].savedAt)
    }

    @Test
    fun brokenLegacyStorageIsIgnored() {
        val legacy = DexLegacyImport.parse(mapOf("exchange:wallets" to "{not json", "exchange:history" to "null"))
        assertEquals(emptyMap<String, String>(), legacy.wallets)
        assertEquals(0, legacy.history.size)
    }

    @Test
    fun marketKeyIsWrittenIntoTheArgv() {
        assertEquals("--addr :8051 --market-pk 03ff", DexArgs.withMarketPk(listOf("--addr", ":8051"), "03ff"))
        assertEquals("--market-pk=03ff", DexArgs.withMarketPk(listOf("--market-pk=02aa"), "03ff"))
        assertEquals("03ff", DexArgs.marketPk(listOf("-market-pk", "03ff")))
    }
}
