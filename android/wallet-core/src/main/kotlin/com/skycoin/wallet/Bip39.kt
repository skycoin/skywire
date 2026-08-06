package com.skycoin.wallet

import java.security.SecureRandom
import java.text.Normalizer

/**
 * BIP 39 mnemonics. Both chains use the same English wordlist and the same
 * checksummed phrase format; they differ only in how the phrase becomes key
 * material (Skycoin feeds the phrase bytes to its deterministic generator,
 * Bitcoin runs PBKDF2 per the BIP).
 */
object Bip39 {

    val words: List<String> by lazy {
        val stream = Bip39::class.java.getResourceAsStream("/bip39/english.txt")
            ?: error("bip39/english.txt missing from resources")
        stream.bufferedReader().readLines().map { it.trim() }.filter { it.isNotEmpty() }.also {
            check(it.size == 2048) { "wordlist must hold 2048 words, got ${it.size}" }
        }
    }

    private val wordIndex: Map<String, Int> by lazy {
        words.withIndex().associate { (i, w) -> w to i }
    }

    /** A fresh phrase; 128 bits of entropy → 12 words (24 words from 256). */
    fun newMnemonic(entropyBits: Int = 128): String {
        require(entropyBits == 128 || entropyBits == 256) { "entropy must be 128 or 256 bits" }
        val entropy = ByteArray(entropyBits / 8)
        SecureRandom().nextBytes(entropy)
        return entropyToMnemonic(entropy)
    }

    fun entropyToMnemonic(entropy: ByteArray): String {
        require(entropy.size % 4 == 0 && entropy.size in 16..32) { "invalid entropy length" }
        val entBits = entropy.size * 8
        val csBits = entBits / 32
        val hash = Hashes.sha256(entropy)

        val bits = BooleanArray(entBits + csBits)
        for (i in 0 until entBits) {
            bits[i] = (entropy[i / 8].toInt() shr (7 - i % 8)) and 1 == 1
        }
        for (i in 0 until csBits) {
            bits[entBits + i] = (hash[i / 8].toInt() shr (7 - i % 8)) and 1 == 1
        }

        return (bits.indices step 11).joinToString(" ") { start ->
            var index = 0
            for (i in 0 until 11) {
                index = (index shl 1) or if (bits[start + i]) 1 else 0
            }
            words[index]
        }
    }

    /** Normalized word array, or null if any token is off-list. */
    private fun tokens(mnemonic: String): List<String>? {
        val ts = Normalizer.normalize(mnemonic, Normalizer.Form.NFKD)
            .trim().lowercase().split(Regex("\\s+"))
        if (ts.isEmpty() || ts.any { it !in wordIndex }) return null
        return ts
    }

    /**
     * Full BIP 39 validation: word count, wordlist membership, checksum.
     * Word-order mistakes and swapped words fail here — the checksum covers them.
     */
    fun validate(mnemonic: String): Boolean {
        val ts = tokens(mnemonic) ?: return false
        if (ts.size !in intArrayOf(12, 15, 18, 21, 24)) return false

        val totalBits = ts.size * 11
        val csBits = totalBits / 33
        val entBits = totalBits - csBits
        val bits = BooleanArray(totalBits)
        ts.forEachIndexed { w, word ->
            val index = wordIndex.getValue(word)
            for (i in 0 until 11) {
                bits[w * 11 + i] = (index shr (10 - i)) and 1 == 1
            }
        }

        val entropy = ByteArray(entBits / 8)
        for (i in 0 until entBits) {
            if (bits[i]) entropy[i / 8] = (entropy[i / 8].toInt() or (1 shl (7 - i % 8))).toByte()
        }
        val hash = Hashes.sha256(entropy)
        for (i in 0 until csBits) {
            if (bits[entBits + i] != ((hash[i / 8].toInt() shr (7 - i % 8)) and 1 == 1)) return false
        }
        return true
    }

    /** The canonical single-spaced form of a valid phrase. */
    fun normalize(mnemonic: String): String =
        tokens(mnemonic)?.joinToString(" ") ?: mnemonic.trim()

    /** BIP 39 seed derivation — Bitcoin key material. */
    fun toSeed(mnemonic: String, passphrase: String = ""): ByteArray {
        val m = Normalizer.normalize(normalize(mnemonic), Normalizer.Form.NFKD)
        val salt = Normalizer.normalize("mnemonic$passphrase", Normalizer.Form.NFKD)
        return Hashes.pbkdf2HmacSha512(m.toCharArray(), salt.toByteArray(Charsets.UTF_8), 2048, 64)
    }

    fun isWord(word: String): Boolean = word.lowercase() in wordIndex

    fun suggestions(prefix: String, limit: Int = 4): List<String> {
        val p = prefix.trim().lowercase()
        if (p.isEmpty()) return emptyList()
        return words.filter { it.startsWith(p) }.take(limit)
    }
}
