package com.skycoin.wallet

import java.math.BigInteger

/** Bitcoin-alphabet base58, as used by Skycoin addresses and legacy BTC addresses. */
object Base58 {
    private const val ALPHABET = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
    private val INDEXES = IntArray(128) { -1 }.also { idx ->
        ALPHABET.forEachIndexed { i, c -> idx[c.code] = i }
    }
    private val FIFTY_EIGHT = BigInteger.valueOf(58)

    fun encode(input: ByteArray): String {
        if (input.isEmpty()) return ""
        var zeros = 0
        while (zeros < input.size && input[zeros].toInt() == 0) zeros++

        val sb = StringBuilder()
        var num = BigInteger(1, input)
        while (num.signum() > 0) {
            val (q, r) = num.divideAndRemainder(FIFTY_EIGHT)
            sb.append(ALPHABET[r.toInt()])
            num = q
        }
        repeat(zeros) { sb.append('1') }
        return sb.reverse().toString()
    }

    /** Returns null on any character outside the alphabet or an empty string. */
    fun decode(input: String): ByteArray? {
        if (input.isEmpty()) return null
        var zeros = 0
        while (zeros < input.length && input[zeros] == '1') zeros++

        var num = BigInteger.ZERO
        for (c in input) {
            if (c.code >= 128) return null
            val digit = INDEXES[c.code]
            if (digit < 0) return null
            num = num.multiply(FIFTY_EIGHT).add(BigInteger.valueOf(digit.toLong()))
        }
        val body = if (num.signum() == 0) ByteArray(0) else num.toByteArray().let {
            // BigInteger prepends a sign byte when the high bit is set.
            if (it[0].toInt() == 0) it.copyOfRange(1, it.size) else it
        }
        return ByteArray(zeros) + body
    }
}
