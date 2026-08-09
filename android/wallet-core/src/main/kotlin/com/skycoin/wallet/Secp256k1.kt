package com.skycoin.wallet

import org.bouncycastle.crypto.digests.SHA256Digest
import org.bouncycastle.crypto.ec.CustomNamedCurves
import org.bouncycastle.crypto.params.ECDomainParameters
import org.bouncycastle.crypto.params.ECPrivateKeyParameters
import org.bouncycastle.crypto.signers.ECDSASigner
import org.bouncycastle.crypto.signers.HMacDSAKCalculator
import org.bouncycastle.math.ec.ECAlgorithms
import org.bouncycastle.math.ec.ECPoint
import java.math.BigInteger

/**
 * secp256k1 over BouncyCastle's lightweight API.
 *
 * Signatures are deterministic (RFC 6979) and low-S normalized. Skycoin's own
 * signer draws a random nonce, but the chain only verifies that a signature
 * recovers to the right key and is not malleable — both hold here, and a
 * deterministic nonce removes the one failure mode (a bad RNG) that has
 * actually lost coins on phones.
 */
object Secp256k1 {
    private val x9 = CustomNamedCurves.getByName("secp256k1")
    private val domain = ECDomainParameters(x9.curve, x9.g, x9.n, x9.h)
    val n: BigInteger = x9.n
    private val halfN: BigInteger = n.shiftRight(1)
    private val prime: BigInteger = x9.curve.field.characteristic

    fun isValidSecKey(sec: ByteArray): Boolean {
        if (sec.size != 32) return false
        val d = BigInteger(1, sec)
        return d.signum() > 0 && d < n
    }

    fun pubKeyFromSecKey(sec: ByteArray): ByteArray {
        require(isValidSecKey(sec)) { "invalid secret key" }
        return x9.g.multiply(BigInteger(1, sec)).getEncoded(true)
    }

    fun isValidPubKey(pub: ByteArray): Boolean {
        if (pub.size != 33) return false
        return try {
            val p = x9.curve.decodePoint(pub)
            !p.isInfinity && p.isValid
        } catch (e: RuntimeException) {
            false
        }
    }

    /**
     * Compact recoverable signature: R(32) ‖ S(32) ‖ recovery id(1),
     * the Skycoin wire format. Low-S enforced.
     */
    fun signCompact(hash: ByteArray, sec: ByteArray): ByteArray {
        val (r, s) = signRs(hash, sec)
        val pub = pubKeyFromSecKey(sec)
        val e = BigInteger(1, hash)
        for (recid in 0..3) {
            val q = recoverPoint(e, r, s, recid) ?: continue
            if (q.getEncoded(true).contentEquals(pub)) {
                return r.to32Bytes() + s.to32Bytes() + byteArrayOf(recid.toByte())
            }
        }
        error("could not determine recovery id — this cannot happen for a valid signature")
    }

    /** Deterministic (r, s), s already normalized to the low half. */
    private fun signRs(hash: ByteArray, sec: ByteArray): Pair<BigInteger, BigInteger> {
        require(hash.size == 32) { "hash must be 32 bytes" }
        require(isValidSecKey(sec)) { "invalid secret key" }
        val signer = ECDSASigner(HMacDSAKCalculator(SHA256Digest()))
        signer.init(true, ECPrivateKeyParameters(BigInteger(1, sec), domain))
        val sig = signer.generateSignature(hash)
        var s = sig[1]
        if (s > halfN) s = n.subtract(s)
        return sig[0] to s
    }

    /** DER-encoded low-S signature — Bitcoin script format (without sighash byte). */
    fun signDer(hash: ByteArray, sec: ByteArray): ByteArray {
        val (r, s) = signRs(hash, sec)
        fun derInt(v: BigInteger): ByteArray {
            val b = v.toByteArray() // minimal two's complement, keeps a leading 0x00 when needed
            return byteArrayOf(0x02, b.size.toByte()) + b
        }
        val body = derInt(r) + derInt(s)
        return byteArrayOf(0x30, body.size.toByte()) + body
    }

    /** Recover the compressed public key from a 65-byte compact signature, or null. */
    fun recoverCompact(hash: ByteArray, sig: ByteArray): ByteArray? {
        if (sig.size != 65 || hash.size != 32) return null
        val recid = sig[64].toInt() and 0xff
        if (recid >= 4) return null
        val r = BigInteger(1, sig.copyOfRange(0, 32))
        val s = BigInteger(1, sig.copyOfRange(32, 64))
        if (r.signum() == 0 || s.signum() == 0 || r >= n || s >= n) return null
        val q = recoverPoint(BigInteger(1, hash), r, s, recid) ?: return null
        return q.getEncoded(true)
    }

    /**
     * Skycoin's verification semantics for received signatures: recover must
     * succeed, match the given key, and S's top bit must be clear (their
     * malleability rule — implied by our low-S but checked for foreign sigs).
     */
    fun verifyCompact(hash: ByteArray, sig: ByteArray, pub: ByteArray): Boolean {
        if (sig.size != 65) return false
        if ((sig[32].toInt() and 0x80) != 0) return false
        val recovered = recoverCompact(hash, sig) ?: return false
        return recovered.contentEquals(pub)
    }

    /** ECDH as Skycoin uses it: the compressed product point, un-hashed. */
    fun multiply(pub: ByteArray, sec: ByteArray): ByteArray {
        require(isValidPubKey(pub)) { "invalid public key" }
        require(isValidSecKey(sec)) { "invalid secret key" }
        return x9.curve.decodePoint(pub).multiply(BigInteger(1, sec)).getEncoded(true)
    }

    private fun recoverPoint(e: BigInteger, r: BigInteger, s: BigInteger, recid: Int): ECPoint? {
        val x = r.add(BigInteger.valueOf((recid shr 1).toLong()).multiply(n))
        if (x >= prime) return null
        val xBytes = x.to32Bytes()
        val prefix: Byte = if (recid and 1 == 1) 0x03 else 0x02
        val bigR = try {
            x9.curve.decodePoint(byteArrayOf(prefix) + xBytes)
        } catch (ex: RuntimeException) {
            return null
        }
        if (!bigR.multiply(n).isInfinity) return null
        val rInv = r.modInverse(n)
        val srInv = s.multiply(rInv).mod(n)
        val eNegRInv = e.mod(n).negate().mod(n).multiply(rInv).mod(n)
        val q = ECAlgorithms.sumOfTwoMultiplies(x9.g, eNegRInv, bigR, srInv).normalize()
        if (q.isInfinity) return null
        return q
    }
}

fun BigInteger.to32Bytes(): ByteArray {
    val b = toByteArray()
    return when {
        b.size == 32 -> b
        b.size > 32 -> b.copyOfRange(b.size - 32, b.size)
        else -> ByteArray(32 - b.size) + b
    }
}
