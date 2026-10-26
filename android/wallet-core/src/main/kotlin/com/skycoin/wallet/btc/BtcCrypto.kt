package com.skycoin.wallet.btc

import com.skycoin.wallet.Base58
import com.skycoin.wallet.Bech32
import com.skycoin.wallet.Bip32
import com.skycoin.wallet.Bip39
import com.skycoin.wallet.Derivation
import com.skycoin.wallet.Hashes
import com.skycoin.wallet.KeySource

/**
 * BIP 84 wallet keys: m/84'/0'/0'/change/index, native segwit (bc1q…).
 * The phrase runs through BIP 39 PBKDF2 here — unlike the Skycoin chain,
 * where the phrase bytes are the seed directly.
 */
object Bip84 {
    private val PURPOSE = intArrayOf(84 or Bip32.HARDENED, 0 or Bip32.HARDENED, 0 or Bip32.HARDENED)

    fun accountKey(mnemonic: String, passphrase: String = ""): Bip32.ExtKey =
        Bip32.derive(Bip32.master(Bip39.toSeed(mnemonic, passphrase)), PURPOSE)

    fun key(account: Bip32.ExtKey, change: Int, index: Int): Bip32.ExtKey =
        Bip32.ckdPriv(Bip32.ckdPriv(account, change), index)

    fun address(pubKey: ByteArray): String =
        Bech32.segwitEncode("bc", 0, Hashes.hash160(pubKey))
}

/**
 * A Bitcoin wallet's keys and addresses by derivation: BIP 84 native segwit
 * (bc1q…) by default, or legacy BIP 44 P2PKH (1…), m/44'/0'/0'/change/index.
 */
object BtcKeys {
    private val BIP44 = intArrayOf(44 or Bip32.HARDENED, 0 or Bip32.HARDENED, 0 or Bip32.HARDENED)

    fun accountKey(seed: KeySource): Bip32.ExtKey = when (seed.derivation) {
        Derivation.BIP44 -> Bip32.derive(Bip32.master(Bip39.toSeed(seed.mnemonic, seed.passphrase)), BIP44)
        Derivation.STANDARD -> Bip84.accountKey(seed.mnemonic, seed.passphrase)
    }

    fun address(pubKey: ByteArray, derivation: Derivation): String = when (derivation) {
        Derivation.BIP44 -> p2pkhAddress(Hashes.hash160(pubKey))
        Derivation.STANDARD -> Bip84.address(pubKey)
    }

    private fun p2pkhAddress(hash160: ByteArray): String {
        val body = byteArrayOf(0x00) + hash160
        return Base58.encode(body + Hashes.doubleSha256(body).copyOfRange(0, 4))
    }
}

/** Destination-address decoding: every mainnet form a send can target. */
object BtcAddress {

    /** scriptPubKey for the address, or null when the address is invalid. */
    fun scriptPubKey(address: String): ByteArray? {
        val a = address.trim()
        if (a.isEmpty()) return null

        // bech32 / bech32m
        if (a.lowercase().startsWith("bc1")) {
            val sw = Bech32.segwitDecode("bc", a) ?: return null
            return when {
                sw.version == 0 && (sw.program.size == 20 || sw.program.size == 32) ->
                    byteArrayOf(0x00, sw.program.size.toByte()) + sw.program
                sw.version in 1..16 && sw.program.size in 2..40 ->
                    byteArrayOf((0x50 + sw.version).toByte(), sw.program.size.toByte()) + sw.program
                else -> null
            }
        }

        // base58check
        val raw = Base58.decode(a) ?: return null
        if (raw.size != 25) return null
        val body = raw.copyOfRange(0, 21)
        val checksum = raw.copyOfRange(21, 25)
        if (!Hashes.doubleSha256(body).copyOfRange(0, 4).contentEquals(checksum)) return null
        val hash = body.copyOfRange(1, 21)
        return when (body[0].toInt() and 0xff) {
            0x00 -> byteArrayOf(0x76, 0xa9.toByte(), 0x14) + hash + byteArrayOf(0x88.toByte(), 0xac.toByte()) // P2PKH
            0x05 -> byteArrayOf(0xa9.toByte(), 0x14) + hash + byteArrayOf(0x87.toByte()) // P2SH
            else -> null
        }
    }

    fun isValid(address: String): Boolean = scriptPubKey(address) != null

    /** Standard-output size in weight units (8-byte value + script with its varint). */
    fun outputWeight(scriptPubKey: ByteArray): Int = (8 + 1 + scriptPubKey.size) * 4
}
