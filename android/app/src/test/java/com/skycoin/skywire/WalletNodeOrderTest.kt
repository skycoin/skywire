package com.skycoin.skywire.wallet

import com.skycoin.skywire.wallet.CoinSpec
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** What a coin's ordered node list means: the user's order first, the
 *  shipped address behind it, and nothing before it until the user says. */
class WalletNodeOrderTest {

    @Test
    fun withoutOwnNodesTheShippedAddressStandsAlone() {
        val spec = CoinSpec.SKY.withNodeOverride(mapOf("SKY" to emptyList<String>()))
        assertEquals("an empty list changes nothing", CoinSpec.SKY, spec)
        assertEquals(listOf(CoinSpec.SKY.nodeUrl), spec.tryNodes)
    }

    @Test
    fun earlierMeansHigherPriority() {
        val spec = CoinSpec.SKY.withNodeOverride(
            mapOf("SKY" to listOf("https://mine.example", "http://lan:8080")),
        )
        assertEquals("the first node is the one in force", "https://mine.example", spec.nodeUrl)
        assertEquals(
            "the shipped address rides behind the list as the fallback",
            listOf("https://mine.example", "http://lan:8080", CoinSpec.SKY.nodeUrl),
            spec.tryNodes,
        )
    }

    @Test
    fun namingTheShippedAddressDoesNotDuplicateIt() {
        val spec = CoinSpec.SKY.withNodeOverride(
            mapOf("SKY" to listOf(CoinSpec.SKY.nodeUrl, "https://mine.example")),
        )
        assertEquals(
            "the fallback is never duplicated and always last",
            listOf("https://mine.example", CoinSpec.SKY.nodeUrl),
            spec.tryNodes,
        )
    }

    @Test
    fun blanksAreDropped() {
        val spec = CoinSpec.SKY.withNodeOverride(mapOf("SKY" to listOf("", "  ", "https://mine.example")))
        assertEquals(listOf("https://mine.example", CoinSpec.SKY.nodeUrl), spec.tryNodes)
        assertTrue(spec.tryNodes.all { it.isNotBlank() })
    }
}
