package com.skycoin.wallet

import com.skycoin.wallet.btc.Bip84
import com.skycoin.wallet.btc.BtcAddress
import com.skycoin.wallet.btc.BtcKeys
import com.skycoin.wallet.btc.BtcTxn
import com.skycoin.wallet.btc.BtcWalletCore
import okhttp3.OkHttpClient
import java.math.BigInteger
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * Legacy BIP 44 Bitcoin wallets: P2PKH addresses and the original sighash.
 * The addresses are skycoin's bip44wallet's for the same phrase, and without a
 * passphrase the published BIP 44 vector. The sighashes are skycoin's
 * src/btc computeSigHash over the same transaction.
 */
class LegacyBtcTest {

    private val phrase = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
    private val keys = KeySource(phrase, derivation = Derivation.BIP44)

    @Test
    fun addresses() {
        val core = BtcWalletCore("http://127.0.0.1:1", OkHttpClient())
        val book = core.deriveAddresses(keys, 2, 2)
        assertEquals(listOf("1LqBGSKuX5yYUonjxT5qGfpUsXKYYWeabA", "1Ak8PffB2meyfYnbXZR9EGfLfFZVpzJvQP"), book.receive)
        assertEquals(listOf("1J3J6EvPrv8q6AC3VCjWV45Uf3nssNMRtH", "13vKxXzHXXd8HquAYdpkJoi9ULVXUgfpS5"), book.change)
        val withPass = core.deriveAddresses(KeySource(phrase, "TREZOR", Derivation.BIP44), 1, 1)
        assertEquals(listOf("1PEha8dk5Me5J1rZWpgqSt5F4BroTBLS5y"), withPass.receive)
        assertEquals(listOf("1MVkh45Zn9A7ZBga697r34DBiRFLXD4UKG"), withPass.change)
    }

    @Test
    fun legacySighashAndSignatures() {
        val account = BtcKeys.accountKey(keys)
        val key0 = Bip84.key(account, 0, 0)
        val key1 = Bip84.key(account, 0, 1)
        val txn = BtcTxn(
            inputs = listOf(
                BtcTxn.Input("aa00000000000000000000000000000000000000000000000000000000000001", 0, 100_000u, Hashes.hash160(key0.pubKey()), legacy = true),
                BtcTxn.Input("bb00000000000000000000000000000000000000000000000000000000000002", 3, 100_000u, Hashes.hash160(key1.pubKey()), legacy = true),
            ),
            outputs = listOf(
                BtcTxn.Output(150_000u, BtcAddress.scriptPubKey("1J3J6EvPrv8q6AC3VCjWV45Uf3nssNMRtH")!!),
                BtcTxn.Output(40_000u, BtcAddress.scriptPubKey("13vKxXzHXXd8HquAYdpkJoi9ULVXUgfpS5")!!),
            ),
        )
        assertEquals("689f4e47442756327f5dda6896e0b427197b6832db83d86770d77abefd2fe130", txn.legacySighash(0).toHex())
        assertEquals("335b6bb3e2009c7a2984f0e7d47c180270288092a81f0338465acfa4d73654ba", txn.legacySighash(1).toHex())

        // Signing does not change what was signed, so each signature checks against its sighash.
        txn.sign(listOf(key0.key, key1.key))
        listOf(key0, key1).forEachIndexed { i, k ->
            val script = txn.inputs[i].scriptSig
            val sig = script.copyOfRange(1, 1 + script[0])
            val pub = script.copyOfRange(2 + script[0], script.size)
            assertEquals(1, sig.last().toInt(), "SIGHASH_ALL")
            assertTrue(pub.contentEquals(k.pubKey()))
            assertTrue(derVerifies(txn.legacySighash(i), sig.copyOf(sig.size - 1), pub), "input $i")
        }
        // No witness anywhere, so the full and stripped forms are one.
        assertTrue(txn.serialize().contentEquals(txn.serializeStripped()))
    }

    /** ECDSA check of a DER signature: some recovery id must give back the key. */
    private fun derVerifies(hash: ByteArray, der: ByteArray, pub: ByteArray): Boolean {
        val rLen = der[3].toInt()
        val r = BigInteger(1, der.copyOfRange(4, 4 + rLen))
        val sLen = der[5 + rLen].toInt()
        val s = BigInteger(1, der.copyOfRange(6 + rLen, 6 + rLen + sLen))
        return (0..3).any { recid ->
            val compact = r.to32Bytes() + s.to32Bytes() + byteArrayOf(recid.toByte())
            Secp256k1.recoverCompact(hash, compact)?.contentEquals(pub) == true
        }
    }
}
