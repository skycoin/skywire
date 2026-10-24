package com.skycoin.skywire

import com.skycoin.skywire.wallet.CoinSpec
import com.skycoin.skywire.wallet.WalletPrices
import com.skycoin.skywire.wallet.coinTextFromUsd
import com.skycoin.skywire.wallet.usdPlain
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/** USD prices: coinpaprika's answer read, and amounts turned between coins and dollars. */
class WalletPricesTest {

    @Test
    fun readsCoinpaprikasTicker() {
        val body = """{"id":"sky-skycoin","symbol":"SKY","quotes":{"USD":{"price":0.07485954376531992,"volume_24h":1}}}"""
        assertEquals(0.07485954376531992, WalletPrices.parse(body), 0.0)
    }

    @Test
    fun dollarsToCoinsRoundDown() {
        // 10 USD at 0.075 is 133.333… SKY; SKY shows three decimals, so 133.333.
        assertEquals("133.333", CoinSpec.SKY.coinTextFromUsd("10", 0.075))
        assertEquals("0.00012202", CoinSpec.BTC.coinTextFromUsd("10", 81948.49))
        assertNull(CoinSpec.SKY.coinTextFromUsd("ten", 0.075))
        assertNull(CoinSpec.SKY.coinTextFromUsd("10", 0.0))
    }

    @Test
    fun coinsToPlainDollars() {
        assertEquals("10.00", CoinSpec.SKY.usdPlain(133_334_000uL, 0.075))
        assertEquals("1.50", CoinSpec.ETH.usdPlain(1_000_000uL, 1500.0)) // 0.001 ETH in gwei units
    }
}
