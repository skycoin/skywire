package com.skycoin.wallet.eth

import java.math.BigInteger

/**
 * Recursive-length-prefix encoding — exactly the subset a transaction needs:
 * byte strings, unsigned integers (minimal big-endian, zero = empty), and
 * lists. No decoder: this wallet only ever authors RLP, never parses it.
 */
object Rlp {

    sealed class Item
    class Str(val bytes: ByteArray) : Item()
    class Lst(val items: List<Item>) : Item()

    fun of(bytes: ByteArray): Str = Str(bytes)

    fun of(value: BigInteger): Str {
        require(value.signum() >= 0) { "RLP integers are unsigned" }
        if (value.signum() == 0) return Str(ByteArray(0))
        val b = value.toByteArray()
        // BigInteger prepends a sign byte when the top bit is set.
        return Str(if (b[0] == 0.toByte()) b.copyOfRange(1, b.size) else b)
    }

    fun encode(item: Item): ByteArray = when (item) {
        is Str -> {
            val b = item.bytes
            if (b.size == 1 && (b[0].toInt() and 0xff) < 0x80) b
            else lengthPrefix(b.size, 0x80) + b
        }
        is Lst -> {
            val body = item.items.fold(ByteArray(0)) { acc, i -> acc + encode(i) }
            lengthPrefix(body.size, 0xc0) + body
        }
    }

    private fun lengthPrefix(length: Int, offset: Int): ByteArray {
        if (length < 56) return byteArrayOf((offset + length).toByte())
        val lenBytes = BigInteger.valueOf(length.toLong()).toByteArray()
            .let { if (it[0] == 0.toByte()) it.copyOfRange(1, it.size) else it }
        return byteArrayOf((offset + 55 + lenBytes.size).toByte()) + lenBytes
    }
}
