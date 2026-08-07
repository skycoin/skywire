package com.skycoin.wallet.eth

import com.skycoin.wallet.Bip32
import com.skycoin.wallet.Bip39
import org.bouncycastle.crypto.digests.KeccakDigest
import org.bouncycastle.crypto.ec.CustomNamedCurves

/**
 * Ethereum key and address arithmetic: Keccak-256, the BIP 44 Ethereum path,
 * and EIP-55 checksummed addresses. Signing itself stays in
 * [com.skycoin.wallet.Secp256k1] — an Ethereum signature is the same compact
 * recoverable form Skycoin uses, taken over a different hash.
 */
object EthCrypto {

    private val curve = CustomNamedCurves.getByName("secp256k1")

    fun keccak256(data: ByteArray): ByteArray {
        val digest = KeccakDigest(256)
        digest.update(data, 0, data.size)
        val out = ByteArray(32)
        digest.doFinal(out, 0)
        return out
    }

    /** m/44'/60'/0'/0 — the node every index-i external address hangs off. */
    fun accountKey(mnemonic: String): Bip32.ExtKey =
        Bip32.derive(
            Bip32.master(Bip39.toSeed(mnemonic)),
            intArrayOf(44 or Bip32.HARDENED, 60 or Bip32.HARDENED, Bip32.HARDENED, 0),
        )

    fun key(account: Bip32.ExtKey, index: Int): Bip32.ExtKey = Bip32.ckdPriv(account, index)

    /**
     * The 20 address bytes behind a public key: Keccak-256 of the raw 64-byte
     * point (the 0x04 prefix of the uncompressed encoding excluded), low 20.
     */
    fun addressBytes(pubCompressed: ByteArray): ByteArray {
        val uncompressed = curve.curve.decodePoint(pubCompressed).getEncoded(false)
        val hash = keccak256(uncompressed.copyOfRange(1, uncompressed.size))
        return hash.copyOfRange(12, 32)
    }

    /** EIP-55 mixed-case form — the only form this wallet ever prints. */
    fun checksumAddress(bytes: ByteArray): String {
        require(bytes.size == 20) { "an address is 20 bytes" }
        val hex = bytes.joinToString("") { "%02x".format(it) }
        val hash = keccak256(hex.toByteArray(Charsets.US_ASCII))
        val sb = StringBuilder("0x")
        for (i in hex.indices) {
            val c = hex[i]
            // Uppercase where the hash nibble at the same position is ≥ 8.
            val nibble = (hash[i / 2].toInt() ushr (if (i % 2 == 0) 4 else 0)) and 0xf
            sb.append(if (c in 'a'..'f' && nibble >= 8) c.uppercaseChar() else c)
        }
        return sb.toString()
    }

    fun address(pubCompressed: ByteArray): String = checksumAddress(addressBytes(pubCompressed))

    /**
     * The 20 bytes behind any accepted spelling of an address, or null.
     * All-lowercase and all-uppercase hex pass unchecked (they carry no
     * checksum); mixed case must be the exact EIP-55 form — a mixed-case
     * address that fails its own checksum is a typo, not a preference.
     */
    fun parseAddress(address: String): ByteArray? {
        if (!address.startsWith("0x")) return null
        val body = address.substring(2).takeIf { it.length == 40 } ?: return null
        if (!body.all { it.isDigit() || it in 'a'..'f' || it in 'A'..'F' }) return null
        val bytes = ByteArray(20) { i ->
            ((Character.digit(body[i * 2], 16) shl 4) or Character.digit(body[i * 2 + 1], 16)).toByte()
        }
        val hasLower = body.any { it in 'a'..'f' }
        val hasUpper = body.any { it in 'A'..'F' }
        if (hasLower && hasUpper && checksumAddress(bytes) != "0x$body") return null
        return bytes
    }

    fun isValidAddress(address: String): Boolean = parseAddress(address) != null
}
