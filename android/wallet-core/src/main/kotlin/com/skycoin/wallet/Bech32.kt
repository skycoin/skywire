package com.skycoin.wallet

/**
 * BIP 173 (bech32) and BIP 350 (bech32m) — segwit address encoding.
 * Straight port of the reference implementation.
 */
object Bech32 {
    private const val CHARSET = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
    private const val BECH32_CONST = 1
    private const val BECH32M_CONST = 0x2bc830a3

    enum class Spec { BECH32, BECH32M }

    data class Segwit(val hrp: String, val version: Int, val program: ByteArray)

    private fun polymod(values: IntArray): Int {
        val gen = intArrayOf(0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3)
        var chk = 1
        for (v in values) {
            val top = chk ushr 25
            chk = ((chk and 0x1ffffff) shl 5) xor v
            for (i in 0..4) {
                if ((top ushr i) and 1 == 1) chk = chk xor gen[i]
            }
        }
        return chk
    }

    private fun hrpExpand(hrp: String): IntArray {
        val out = IntArray(hrp.length * 2 + 1)
        hrp.forEachIndexed { i, c -> out[i] = c.code ushr 5 }
        out[hrp.length] = 0
        hrp.forEachIndexed { i, c -> out[hrp.length + 1 + i] = c.code and 31 }
        return out
    }

    private fun createChecksum(hrp: String, data: IntArray, spec: Spec): IntArray {
        val values = hrpExpand(hrp) + data + IntArray(6)
        val const = if (spec == Spec.BECH32M) BECH32M_CONST else BECH32_CONST
        val mod = polymod(values) xor const
        return IntArray(6) { (mod ushr (5 * (5 - it))) and 31 }
    }

    private fun verifyChecksum(hrp: String, data: IntArray): Spec? =
        when (polymod(hrpExpand(hrp) + data)) {
            BECH32_CONST -> Spec.BECH32
            BECH32M_CONST -> Spec.BECH32M
            else -> null
        }

    fun encode(hrp: String, data: IntArray, spec: Spec): String {
        val combined = data + createChecksum(hrp, data, spec)
        return hrp + "1" + combined.joinToString("") { CHARSET[it].toString() }
    }

    /** Returns (hrp, data-without-checksum, spec) or null. */
    fun decode(bech: String): Triple<String, IntArray, Spec>? {
        if (bech.length > 90) return null
        if (bech.any { it.code < 33 || it.code > 126 }) return null
        val lower = bech.lowercase()
        if (bech != lower && bech != bech.uppercase()) return null
        val pos = lower.lastIndexOf('1')
        if (pos < 1 || pos + 7 > lower.length) return null
        val hrp = lower.substring(0, pos)
        val data = IntArray(lower.length - pos - 1)
        for (i in data.indices) {
            val d = CHARSET.indexOf(lower[pos + 1 + i])
            if (d < 0) return null
            data[i] = d
        }
        val spec = verifyChecksum(hrp, data) ?: return null
        return Triple(hrp, data.copyOfRange(0, data.size - 6), spec)
    }

    fun convertBits(data: IntArray, fromBits: Int, toBits: Int, pad: Boolean): IntArray? {
        var acc = 0
        var bits = 0
        val out = ArrayList<Int>()
        val maxv = (1 shl toBits) - 1
        for (value in data) {
            if (value < 0 || value ushr fromBits != 0) return null
            acc = (acc shl fromBits) or value
            bits += fromBits
            while (bits >= toBits) {
                bits -= toBits
                out.add((acc ushr bits) and maxv)
            }
        }
        if (pad) {
            if (bits > 0) out.add((acc shl (toBits - bits)) and maxv)
        } else if (bits >= fromBits || ((acc shl (toBits - bits)) and maxv) != 0) {
            return null
        }
        return out.toIntArray()
    }

    /** Decode a segwit address for the given hrp; enforces BIP 350 spec-per-version. */
    fun segwitDecode(hrp: String, addr: String): Segwit? {
        val (gotHrp, data, spec) = decode(addr) ?: return null
        if (gotHrp != hrp) return null
        if (data.isEmpty() || data[0] > 16) return null
        val program = convertBits(data.copyOfRange(1, data.size), 5, 8, false) ?: return null
        if (program.size < 2 || program.size > 40) return null
        if (data[0] == 0 && program.size != 20 && program.size != 32) return null
        if (data[0] == 0 && spec != Spec.BECH32) return null
        if (data[0] != 0 && spec != Spec.BECH32M) return null
        return Segwit(hrp, data[0], ByteArray(program.size) { program[it].toByte() })
    }

    fun segwitEncode(hrp: String, version: Int, program: ByteArray): String {
        val spec = if (version == 0) Spec.BECH32 else Spec.BECH32M
        val data = intArrayOf(version) +
            (convertBits(IntArray(program.size) { program[it].toInt() and 0xff }, 8, 5, true)
                ?: error("convertBits(8→5, pad) cannot fail"))
        return encode(hrp, data, spec)
    }
}
