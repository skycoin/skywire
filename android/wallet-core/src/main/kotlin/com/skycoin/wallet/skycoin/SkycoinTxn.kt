package com.skycoin.wallet.skycoin

import com.skycoin.wallet.Hashes
import com.skycoin.wallet.Secp256k1
import com.skycoin.wallet.hexToBytes
import com.skycoin.wallet.toHex
import java.io.ByteArrayOutputStream
import java.nio.ByteBuffer
import java.nio.ByteOrder

/**
 * The Skycoin transaction and its exact wire encoding (skyencoder output,
 * little-endian, u32 length prefixes on slices):
 *
 *   u32 Length | u8 Type | 32B InnerHash
 *   | u32 nSigs | 65B × sig | u32 nIn | 32B × uxid
 *   | u32 nOut | (u8 version ‖ 20B keyHash ‖ u64 coins ‖ u64 hours) × out
 *
 * InnerHash = sha256 over the In and Out sections alone; the hash signed for
 * input i is sha256(InnerHash ‖ In[i]); the txid is sha256 of the whole
 * serialization.
 */
class SkycoinTxn {
    var length: Long = 0
    var type: Int = 0
    var innerHash: ByteArray = ByteArray(32)
    val sigs = ArrayList<ByteArray>()          // 65 bytes each; zero-filled when unsigned
    val inputs = ArrayList<ByteArray>()        // 32-byte uxids
    val outputs = ArrayList<Output>()

    class Output(val addressKey: ByteArray, val addressVersion: Int, val coins: ULong, val hours: ULong)

    fun pushInput(uxid: ByteArray) {
        require(uxid.size == 32) { "uxid must be 32 bytes" }
        inputs.add(uxid)
    }

    fun pushOutput(address: String, coins: ULong, hours: ULong) {
        val key = SkycoinCrypto.decodeAddress(address) ?: throw IllegalArgumentException("invalid address $address")
        outputs.add(Output(key, 0, coins, hours))
    }

    private fun writeInOut(w: LeWriter) {
        w.u32(inputs.size)
        for (i in inputs) w.bytes(i)
        w.u32(outputs.size)
        for (o in outputs) {
            w.u8(o.addressVersion)
            w.bytes(o.addressKey)
            w.u64(o.coins)
            w.u64(o.hours)
        }
    }

    fun hashInner(): ByteArray {
        val w = LeWriter()
        writeInOut(w)
        return Hashes.sha256(w.toByteArray())
    }

    fun serialize(): ByteArray {
        val w = LeWriter()
        w.u32(length)
        w.u8(type)
        w.bytes(innerHash)
        w.u32(sigs.size)
        for (s in sigs) w.bytes(s)
        writeInOut(w)
        return w.toByteArray()
    }

    /** Sets Length, Type and InnerHash — the Go UpdateHeader. */
    fun updateHeader() {
        type = 0
        innerHash = hashInner()
        length = serialize().size.toLong()
        // Length itself is part of the serialization but has fixed width,
        // so one pass settles it.
    }

    /** Sign input i with the key owning its output. InnerHash must be set. */
    fun signInputs(keys: List<ByteArray>) {
        require(keys.size == inputs.size) { "one key per input" }
        innerHash = hashInner()
        sigs.clear()
        for ((i, key) in keys.withIndex()) {
            val h = Hashes.sha256(innerHash, inputs[i])
            sigs.add(Secp256k1.signCompact(h, key))
        }
        updateHeader()
    }

    fun txidHex(): String = Hashes.sha256(serialize()).toHex()

    fun serializeHex(): String = serialize().toHex()

    /** The same transaction with every signature zeroed, as an unsigned transaction is written. */
    fun unsignedHex(): String {
        val copy = SkycoinTxn()
        copy.inputs.addAll(inputs)
        copy.outputs.addAll(outputs)
        repeat(inputs.size) { copy.sigs.add(ByteArray(65)) }
        copy.updateHeader()
        return copy.serializeHex()
    }

    /** True when every input carries a signature. */
    fun isSigned(): Boolean = sigs.size == inputs.size && sigs.none { s -> s.all { it == 0.toByte() } }

    companion object {
        /**
         * Reads the wire encoding back. Refuses trailing bytes, a length
         * field that disagrees, and an inner hash that does not match the
         * inputs and outputs, so a mangled paste fails here and not at a node.
         */
        fun decode(hex: String): SkycoinTxn {
            val bytes = runCatching { hex.trim().hexToBytes() }.getOrNull()
                ?: throw IllegalArgumentException("not a hex transaction")
            val r = ByteBuffer.wrap(bytes).order(ByteOrder.LITTLE_ENDIAN)
            fun need(n: Long) = require(n <= r.remaining()) { "transaction ends early" }
            fun take(n: Int) = ByteArray(n).also { need(n.toLong()); r.get(it) }
            fun count(size: Int): Int {
                need(4)
                return r.int.also { require(it >= 0) { "transaction ends early" }; need(it.toLong() * size) }
            }
            val txn = SkycoinTxn()
            need(4 + 1)
            txn.length = r.int.toLong() and 0xffffffffL
            txn.type = r.get().toInt() and 0xff
            txn.innerHash = take(32)
            repeat(count(65)) { txn.sigs.add(take(65)) }
            repeat(count(32)) { txn.inputs.add(take(32)) }
            repeat(count(1 + 20 + 8 + 8)) {
                val version = r.get().toInt() and 0xff
                val key = take(20)
                txn.outputs.add(Output(key, version, r.long.toULong(), r.long.toULong()))
            }
            require(!r.hasRemaining()) { "bytes after the transaction" }
            require(txn.length == bytes.size.toLong()) { "length field does not match" }
            require(txn.innerHash.contentEquals(txn.hashInner())) { "inner hash does not match" }
            return txn
        }
    }

    private class LeWriter {
        private val out = ByteArrayOutputStream()
        fun u8(v: Int) = out.write(v and 0xff)
        fun u32(v: Int) = u32(v.toLong())
        fun u32(v: Long) {
            out.write((v and 0xff).toInt())
            out.write(((v shr 8) and 0xff).toInt())
            out.write(((v shr 16) and 0xff).toInt())
            out.write(((v shr 24) and 0xff).toInt())
        }
        fun u64(v: ULong) {
            var x = v
            repeat(8) {
                out.write((x and 0xffuL).toInt())
                x = x shr 8
            }
        }
        fun bytes(b: ByteArray) = out.write(b)
        fun toByteArray(): ByteArray = out.toByteArray()
    }
}
