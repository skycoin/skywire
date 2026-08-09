package com.skycoin.wallet

import org.bouncycastle.crypto.digests.RIPEMD160Digest
import java.security.MessageDigest
import javax.crypto.Mac
import javax.crypto.SecretKeyFactory
import javax.crypto.spec.PBEKeySpec
import javax.crypto.spec.SecretKeySpec

/** The hash set the two chains are built from. */
object Hashes {

    fun sha256(data: ByteArray): ByteArray =
        MessageDigest.getInstance("SHA-256").digest(data)

    fun sha256(a: ByteArray, b: ByteArray): ByteArray {
        val md = MessageDigest.getInstance("SHA-256")
        md.update(a)
        md.update(b)
        return md.digest()
    }

    fun doubleSha256(data: ByteArray): ByteArray = sha256(sha256(data))

    fun ripemd160(data: ByteArray): ByteArray {
        val d = RIPEMD160Digest()
        d.update(data, 0, data.size)
        val out = ByteArray(20)
        d.doFinal(out, 0)
        return out
    }

    /** Bitcoin hash160: ripemd160(sha256(x)). */
    fun hash160(data: ByteArray): ByteArray = ripemd160(sha256(data))

    /** Skycoin address key hash: ripemd160(sha256(sha256(pubkey))). */
    fun skyAddressHash(pubKey: ByteArray): ByteArray = ripemd160(doubleSha256(pubKey))

    fun hmacSha512(key: ByteArray, data: ByteArray): ByteArray {
        val mac = Mac.getInstance("HmacSHA512")
        mac.init(SecretKeySpec(key, "HmacSHA512"))
        return mac.doFinal(data)
    }

    fun pbkdf2HmacSha512(password: CharArray, salt: ByteArray, iterations: Int, keyLengthBytes: Int): ByteArray {
        val factory = SecretKeyFactory.getInstance("PBKDF2WithHmacSHA512")
        val spec = PBEKeySpec(password, salt, iterations, keyLengthBytes * 8)
        return try {
            factory.generateSecret(spec).encoded
        } finally {
            spec.clearPassword()
        }
    }
}

fun ByteArray.toHex(): String = joinToString("") { "%02x".format(it) }

fun String.hexToBytes(): ByteArray {
    require(length % 2 == 0) { "odd-length hex string" }
    return ByteArray(length / 2) { i ->
        val hi = Character.digit(this[2 * i], 16)
        val lo = Character.digit(this[2 * i + 1], 16)
        require(hi >= 0 && lo >= 0) { "invalid hex character" }
        ((hi shl 4) + lo).toByte()
    }
}
