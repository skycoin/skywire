package com.skycoin.wallet.skycoin

import com.skycoin.wallet.Base58
import com.skycoin.wallet.Hashes
import com.skycoin.wallet.Secp256k1

/**
 * Skycoin deterministic key generation and the address codec.
 *
 * The derivation chain is Skycoin's own (src/cipher, secp256k1-go): the wallet
 * seed is the raw bytes of the mnemonic string, and each address advances the
 * chain seed by one iterator step. Same seed, same addresses as the desktop
 * wallet — that is the whole point.
 */
object SkycoinCrypto {

    class KeyPair(val public: ByteArray, val secret: ByteArray)

    /** One sha256-until-valid step; returns (pub, sec) for the stepped seed. */
    private fun iteratorStep(seed32: ByteArray): KeyPair {
        var s = seed32
        while (true) {
            s = Hashes.sha256(s)
            if (Secp256k1.isValidSecKey(s)) {
                return KeyPair(Secp256k1.pubKeyFromSecKey(s), s)
            }
        }
    }

    /** secp256k1Hash: sha256 salted with an ECDH operation on the curve. */
    fun secp256k1Hash(seed: ByteArray): ByteArray {
        val hash = Hashes.sha256(seed)
        val secKey = iteratorStep(hash).secret
        val pubKey = iteratorStep(Hashes.sha256(hash)).public
        val ecdh = Secp256k1.multiply(pubKey, secKey)
        return Hashes.sha256(hash, ecdh)
    }

    /** Returns (nextSeed, keyPair) — feed nextSeed back in for the next address. */
    fun deterministicKeyPairIterator(seedIn: ByteArray): Pair<ByteArray, KeyPair> {
        require(seedIn.isNotEmpty()) { "empty seed" }
        val seed1 = secp256k1Hash(seedIn)
        val seed2 = Hashes.sha256(seedIn, seed1)
        return seed1 to iteratorStep(seed2)
    }

    /** The first n keypairs of the wallet chain, in address order. */
    fun generateKeyPairs(seed: ByteArray, n: Int): List<KeyPair> {
        var s = seed
        val out = ArrayList<KeyPair>(n)
        repeat(n) {
            val (next, kp) = deterministicKeyPairIterator(s)
            s = next
            out.add(kp)
        }
        return out
    }

    // Address = base58( key20 ‖ version ‖ sha256(key20 ‖ version)[0..3] )
    // where key20 = ripemd160(sha256(sha256(pubkey))) and version is 0.

    fun addressFromPubKey(pub: ByteArray): String {
        val key = Hashes.skyAddressHash(pub)
        val body = key + byteArrayOf(0)
        val checksum = Hashes.sha256(body).copyOfRange(0, 4)
        return Base58.encode(body + checksum)
    }

    /** Checksum-validated decode; returns the 20-byte key hash or null. */
    fun decodeAddress(address: String): ByteArray? {
        val b = Base58.decode(address) ?: return null
        if (b.size != 25) return null
        val body = b.copyOfRange(0, 21)
        val checksum = b.copyOfRange(21, 25)
        if (!Hashes.sha256(body).copyOfRange(0, 4).contentEquals(checksum)) return null
        if (body[20].toInt() != 0) return null // version must be 0
        return b.copyOfRange(0, 20)
    }

    fun isValidAddress(address: String): Boolean = decodeAddress(address) != null
}
