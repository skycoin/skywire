package com.skycoin.wallet

import com.skycoin.wallet.btc.BtcWalletCore
import com.skycoin.wallet.skycoin.SkyFiberWalletCore
import okhttp3.OkHttpClient
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/**
 * An older wallet's 24-word seed restores the same addresses it had. The
 * expected addresses come from cipher.GenerateDeterministicKeyPairsSeed in
 * the Skycoin Go module this repo vendors.
 */
class LegacySeedRestoreTest {

    private val legacy = "abandon ability able about above absent absorb abstract absurd abuse access accident " +
        "account accuse achieve acid acoustic acquire across act action actor actress actual"

    private val fiber = SkyFiberWalletCore("http://127.0.0.1:1", OkHttpClient())

    @Test
    fun aFiberWalletTakesTheOldPhrase() {
        assertFalse(fiber.validateSeed(legacy), "the old phrase has no checksum")
        assertTrue(fiber.acceptsLegacySeed(legacy))
        assertEquals(
            listOf("2Z5cJuiiUynTHDR9GL3Fm4nq9dS9bWaUDt9", "pQDsuJoS5SmJSZX3ABz5Qz7HtSJGVp9KU"),
            fiber.deriveAddresses(legacy, 2, 0).receive,
        )
    }

    @Test
    fun onlyTwentyFourListWordsAreLegacy() {
        assertFalse(fiber.acceptsLegacySeed(legacy.substringBeforeLast(" ")), "23 words")
        assertFalse(fiber.acceptsLegacySeed(legacy.replace("actual", "notaword")), "an off-list word")
    }

    @Test
    fun bitcoinKeepsTheChecksum() {
        // BIP 39 seeds a Bitcoin wallet from the checksummed phrase.
        assertFalse(BtcWalletCore("http://127.0.0.1:1", OkHttpClient()).acceptsLegacySeed(legacy))
    }
}
