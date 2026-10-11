package com.skycoin.skywire.core

import java.util.Locale

/**
 * Skymail addresses: `<anything>@<label>.dmsg` or `.skynet`, where the label is
 * the visor's public key in lowercase unpadded base32 (pkg/cipher/dnslabel.go),
 * 53 characters for 33 bytes. Any local part reaches the same mailbox.
 */
object SkymailAddress {

    const val DMSG = ".dmsg"
    const val SKYNET = ".skynet"
    private const val LABEL_LENGTH = 53
    private const val PK_HEX_LENGTH = 66
    private const val ALPHABET = "abcdefghijklmnopqrstuvwxyz234567"

    /** The DNS label of a hex public key, or null when it is not one. */
    fun label(pkHex: String): String? {
        val hex = pkHex.trim().lowercase(Locale.ROOT)
        if (hex.length != PK_HEX_LENGTH || !hex.all { it in '0'..'9' || it in 'a'..'f' }) return null
        val bytes = ByteArray(hex.length / 2) { hex.substring(it * 2, it * 2 + 2).toInt(16).toByte() }
        val out = StringBuilder()
        var buffer = 0
        var bits = 0
        for (b in bytes) {
            buffer = (buffer shl 8) or (b.toInt() and 0xff)
            bits += 8
            while (bits >= 5) {
                out.append(ALPHABET[(buffer shr (bits - 5)) and 0x1f])
                bits -= 5
            }
        }
        if (bits > 0) out.append(ALPHABET[(buffer shl (5 - bits)) and 0x1f])
        return out.toString()
    }

    /** The hex public key a label names, or null when it is not one. */
    fun pkOf(label: String): String? {
        val text = label.trim().lowercase(Locale.ROOT)
        if (text.length != LABEL_LENGTH || !text.all { it in ALPHABET }) return null
        val out = ArrayList<Byte>()
        var buffer = 0
        var bits = 0
        for (c in text) {
            buffer = (buffer shl 5) or ALPHABET.indexOf(c)
            bits += 5
            if (bits >= 8) {
                out.add(((buffer shr (bits - 8)) and 0xff).toByte())
                bits -= 8
            }
        }
        if (out.size != PK_HEX_LENGTH / 2) return null
        return out.joinToString("") { "%02x".format(it) }
    }

    /** The mailbox address of a hex public key, over dmsg by default. */
    fun of(pkHex: String, local: String = "mail", suffix: String = DMSG): String? =
        label(pkHex)?.let { "$local@$it$suffix" }

    /** The public key an address delivers to, or null when it is not a Skymail address. */
    fun pkOfAddress(address: String): String? {
        val domain = address.trim().substringAfterLast('@', "").lowercase(Locale.ROOT)
        val host = when {
            domain.endsWith(DMSG) -> domain.removeSuffix(DMSG)
            domain.endsWith(SKYNET) -> domain.removeSuffix(SKYNET)
            else -> return null
        }
        return pkOf(host.substringAfterLast('.'))
    }

    /**
     * What a typed recipient means: an address as given, or a bare public key
     * (hex or skychat://) turned into its dmsg address. Null when it is neither.
     */
    fun recipient(input: String): String? {
        val text = input.trim().removePrefix("skychat://").trimEnd('/')
        if ('@' in text) return text.takeIf { pkOfAddress(it) != null }
        return of(text)
    }
}
