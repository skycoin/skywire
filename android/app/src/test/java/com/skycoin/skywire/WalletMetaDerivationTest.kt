package com.skycoin.skywire

import com.skycoin.skywire.wallet.WalletMeta
import com.skycoin.wallet.Derivation
import kotlinx.serialization.json.Json
import org.junit.Assert.assertEquals
import org.junit.Test

/** The wallet type is stored with each wallet, and wallets stored before it existed stay classic. */
class WalletMetaDerivationTest {

    private val json = Json { ignoreUnknownKeys = true }

    @Test
    fun walletsStoredBeforeTheChoiceAreStandard() {
        val old = """{"id":"w-1","coinId":"SKY","name":"SKY","createdAtMs":1,"receiveAddresses":["a"]}"""
        assertEquals(Derivation.STANDARD, json.decodeFromString(WalletMeta.serializer(), old).derivation)
    }

    @Test
    fun bip44RoundTrips() {
        val meta = WalletMeta("w-2", "SKY", "SKY 2", 1, listOf("a"), derivation = Derivation.BIP44)
        val back = json.decodeFromString(WalletMeta.serializer(), json.encodeToString(WalletMeta.serializer(), meta))
        assertEquals(Derivation.BIP44, back.derivation)
    }
}
