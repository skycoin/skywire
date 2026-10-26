package com.skycoin.wallet

import java.math.BigInteger

/** BIP 32 hierarchical deterministic private keys, and the xpub of one. */
object Bip32 {
    const val HARDENED = -0x80000000 // 0x80000000

    class ExtKey(
        val key: ByteArray,
        val chainCode: ByteArray,
        val depth: Int = 0,
        val parentFingerprint: ByteArray = ByteArray(4),
        val childNumber: Int = 0,
    ) {
        fun pubKey(): ByteArray = Secp256k1.pubKeyFromSecKey(key)

        /** The serialized extended public key, BIP 32 "Serialization format", mainnet. */
        fun xpub(): String {
            val body = byteArrayOf(0x04, 0x88.toByte(), 0xb2.toByte(), 0x1e, depth.toByte()) +
                parentFingerprint + beBytes(childNumber) + chainCode + pubKey()
            return Base58.encode(body + Hashes.doubleSha256(body).copyOfRange(0, 4))
        }
    }

    private fun beBytes(v: Int) = byteArrayOf((v ushr 24).toByte(), (v ushr 16).toByte(), (v ushr 8).toByte(), v.toByte())

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
        beBytes(index).copyInto(data, 33)

        val i = Hashes.hmacSha512(parent.chainCode, data)
        val il = BigInteger(1, i.copyOfRange(0, 32))
        require(il < Secp256k1.n) { "derived key out of range" }
        val child = il.add(BigInteger(1, parent.key)).mod(Secp256k1.n)
        require(child.signum() != 0) { "derived key is zero" }
        return ExtKey(
            child.to32Bytes(),
            i.copyOfRange(32, 64),
            depth = parent.depth + 1,
            parentFingerprint = Hashes.hash160(parent.pubKey()).copyOfRange(0, 4),
            childNumber = index,
        )
    }

    /** Derive along a path like listOf(84 or HARDENED, 0 or HARDENED, ...). */
    fun derive(master: ExtKey, path: IntArray): ExtKey {
        var k = master
        for (index in path) k = ckdPriv(k, index)
        return k
    }
}
