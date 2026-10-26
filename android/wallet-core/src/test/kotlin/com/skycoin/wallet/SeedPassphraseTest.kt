package com.skycoin.wallet

import com.skycoin.wallet.btc.BtcWalletCore
import com.skycoin.wallet.eth.EthWalletCore
import com.skycoin.wallet.skycoin.SkyFiberWalletCore
import okhttp3.OkHttpClient
import kotlin.test.Test
import kotlin.test.assertEquals

/**
 * A BIP 39 passphrase changes every key. The expected addresses are what
 * skycoin's bip44wallet derives for the same phrase and passphrase at the
 * version go.mod pins; without a passphrase they are the published BIP 44
 * and BIP 84 vectors.
 */
class SeedPassphraseTest {

    private val phrase = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
    private val client = OkHttpClient()

    @Test
    fun bitcoinSegwit() {
        val core = BtcWalletCore("http://127.0.0.1:1", client)
        val book = core.deriveAddresses(KeySource(phrase, "TREZOR"), 2, 2)
        assertEquals(listOf("bc1qv5rmq0kt9yz3pm36wvzct7p3x6mtgehjul0feu", "bc1qq4w9xyzsylpcy6hd76xr8pecxsvzqe743t2uj0"), book.receive)
        assertEquals(listOf("bc1qest2x6zfncjezklycdghhjxzlptky4xx6rgewa", "bc1qdrnsf9gw4wuk42n9a3xuzjzzfpv079fd4a6jdq"), book.change)
        assertEquals("bc1qcr8te4kr609gcawutmrza0j4xv80jy8z306fyu", core.deriveAddresses(KeySource(phrase), 1, 0).receive[0])
    }

    @Test
    fun ethereum() {
        val core = EthWalletCore("http://127.0.0.1:1", null, client)
        assertEquals(
            listOf("0x9c32F71D4DB8Fb9e1A58B0a80dF79935e7256FA6", "0x7AF7283bd1462C3b957e8FAc28Dc19cBbF2FAdfe"),
            core.deriveAddresses(KeySource(phrase, "TREZOR"), 2, 0).receive,
        )
        assertEquals("0x9858EfFD232B4033E47d90003D41EC34EcaEda94", core.deriveAddresses(KeySource(phrase), 1, 0).receive[0])
    }

    @Test
    fun skycoinBip44() {
        val book = SkyFiberWalletCore("http://127.0.0.1:1", client)
            .deriveAddresses(KeySource(phrase, "TREZOR", Derivation.BIP44), 2, 2)
        assertEquals(listOf("zaGHV3FFnn98Bf6QXs3V1gPGuCNKdQPew3", "2DA27wgBuD9wvpWrfhV3g9uB4qh2sLLhgmv"), book.receive)
        assertEquals(listOf("2bAwYXgbWEF9cs7mVfHbd18oSWDYdE3QiHX", "2YijEC2kuUg8ew4mWnmFRiFbctGcLxt8Y98"), book.change)
    }
}
