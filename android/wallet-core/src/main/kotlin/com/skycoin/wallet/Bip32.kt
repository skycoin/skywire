package com.skycoin.wallet

import java.math.BigInteger

/** BIP 32 hierarchical deterministic private keys — only what BIP 84 needs. */
object Bip32 {
    const val HARDENED = -0x80000000 // 0x80000000

    class ExtKey(val key: ByteArray, val chainCode: ByteArray) {
        fun pubKey(): ByteArray = Secp256k1.pubKeyFromSecKey(key)
    }

    fun master(seed: ByteArray): ExtKey {
        val i = Hashes.hmacSha512("Bitcoin seed".toByteArray(Charsets.US_ASCII), seed)
        val il = i.copyOfRange(0, 32)
        require(Secp256k1.isValidSecKey(il)) { "invalid master key from this seed" }
        return ExtKey(il, i.copyOfRange(32, 64))
    }

    fun ckdPriv(parent: ExtKey, index: Int): ExtKey {
        val data = ByteArray(37)
        if (index and HARDENED != 0) {
            // 0x00 ‖ ser256(k)
            parent.key.copyInto(data, 1)
        } else {
            parent.pubKey().copyInto(data, 0)
        }
        data[33] = (index ushr 24).toByte()
        data[34] = (index ushr 16).toByte()
        data[35] = (index ushr 8).toByte()
        data[36] = index.toByte()

        val i = Hashes.hmacSha512(parent.chainCode, data)
        val il = BigInteger(1, i.copyOfRange(0, 32))
        require(il < Secp256k1.n) { "derived key out of range" }
        val child = il.add(BigInteger(1, parent.key)).mod(Secp256k1.n)
        require(child.signum() != 0) { "derived key is zero" }
        return ExtKey(child.to32Bytes(), i.copyOfRange(32, 64))
    }

    /** Derive along a path like listOf(84 or HARDENED, 0 or HARDENED, ...). */
    fun derive(master: ExtKey, path: IntArray): ExtKey {
        var k = master
        for (index in path) k = ckdPriv(k, index)
        return k
    }
}
