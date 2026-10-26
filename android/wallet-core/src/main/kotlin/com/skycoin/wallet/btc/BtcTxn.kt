package com.skycoin.wallet.btc

import com.skycoin.wallet.Hashes
import com.skycoin.wallet.Secp256k1
import com.skycoin.wallet.toHex
import java.io.ByteArrayOutputStream

/**
 * Transaction limited to what this wallet creates: P2WPKH inputs signed with
 * the BIP 143 sighash, or legacy P2PKH inputs signed with the original one,
 * arbitrary standard outputs, RBF signaled.
 */
class BtcTxn(
    val inputs: List<Input>,
    val outputs: List<Output>,
    private val version: Int = 2,
    private val locktime: Long = 0,
) {
    class Input(
        /** Display-order txid (as APIs show it); serialization reverses it. */
        val txid: String,
        val vout: Int,
        val valueSats: ULong,
        /** hash160 of the owning compressed pubkey — the P2WPKH program. */
        val pubKeyHash: ByteArray,
        /** Default opts into replacement (BIP 125) while still allowing locktime. */
        val sequence: Long = 0xFFFFFFFDL,
        /** A P2PKH input: signed in its scriptSig rather than a witness. */
        val legacy: Boolean = false,
    ) {
        var witness: List<ByteArray> = emptyList()
        var scriptSig: ByteArray = ByteArray(0)

        /** The P2PKH script over [pubKeyHash]: a legacy input's prevout script, a segwit one's scriptCode. */
        fun p2pkhScript(): ByteArray =
            byteArrayOf(0x76, 0xa9.toByte(), 0x14) + pubKeyHash + byteArrayOf(0x88.toByte(), 0xac.toByte())
    }

    class Output(val valueSats: ULong, val scriptPubKey: ByteArray)

    private fun outpoint(w: ByteArrayOutputStream, input: Input) {
        w.write(input.txid.chunked(2).reversed().joinToString("").hexBytes())
        w.writeU32(input.vout.toLong())
    }

    private fun serializeOutputs(w: ByteArrayOutputStream) {
        w.writeVarInt(outputs.size.toLong())
        for (o in outputs) {
            w.writeU64(o.valueSats)
            w.writeVarInt(o.scriptPubKey.size.toLong())
            w.write(o.scriptPubKey)
        }
    }

    private fun scriptSig(w: ByteArrayOutputStream, script: ByteArray) {
        w.writeVarInt(script.size.toLong())
        w.write(script)
    }

    /** Serialization without witness data — what the txid commits to. */
    fun serializeStripped(): ByteArray {
        val w = ByteArrayOutputStream()
        w.writeU32(version.toLong())
        w.writeVarInt(inputs.size.toLong())
        for (i in inputs) {
            outpoint(w, i)
            scriptSig(w, i.scriptSig) // empty on a segwit input: its witness carries the signature
            w.writeU32(i.sequence)
        }
        serializeOutputs(w)
        w.writeU32(locktime)
        return w.toByteArray()
    }

    fun serialize(): ByteArray {
        if (inputs.all { it.witness.isEmpty() }) return serializeStripped()
        val w = ByteArrayOutputStream()
        w.writeU32(version.toLong())
        w.write(0x00) // segwit marker
        w.write(0x01) // segwit flag
        w.writeVarInt(inputs.size.toLong())
        for (i in inputs) {
            outpoint(w, i)
            scriptSig(w, i.scriptSig)
            w.writeU32(i.sequence)
        }
        serializeOutputs(w)
        for (i in inputs) {
            w.writeVarInt(i.witness.size.toLong())
            for (item in i.witness) {
                w.writeVarInt(item.size.toLong())
                w.write(item)
            }
        }
        w.writeU32(locktime)
        return w.toByteArray()
    }

    fun txid(): String =
        Hashes.doubleSha256(serializeStripped()).reversedArray().toHex()

    /** vsize in vbytes = ceil(weight / 4). */
    fun vsize(): Int {
        val stripped = serializeStripped().size
        val full = serialize().size
        val weight = stripped * 3 + full
        return (weight + 3) / 4
    }

    /** BIP 143 signature hash for P2WPKH input [index], SIGHASH_ALL. */
    fun sighash(index: Int): ByteArray {
        val input = inputs[index]

        val prevouts = ByteArrayOutputStream()
        for (i in inputs) outpoint(prevouts, i)
        val hashPrevouts = Hashes.doubleSha256(prevouts.toByteArray())

        val sequences = ByteArrayOutputStream()
        for (i in inputs) sequences.writeU32(i.sequence)
        val hashSequence = Hashes.doubleSha256(sequences.toByteArray())

        val outs = ByteArrayOutputStream()
        for (o in outputs) {
            outs.writeU64(o.valueSats)
            outs.writeVarInt(o.scriptPubKey.size.toLong())
            outs.write(o.scriptPubKey)
        }
        val hashOutputs = Hashes.doubleSha256(outs.toByteArray())

        val w = ByteArrayOutputStream()
        w.writeU32(version.toLong())
        w.write(hashPrevouts)
        w.write(hashSequence)
        outpoint(w, input)
        // scriptCode of P2WPKH: the classic P2PKH script over the program.
        scriptSig(w, input.p2pkhScript())
        w.writeU64(input.valueSats)
        w.writeU32(input.sequence)
        w.write(hashOutputs)
        w.writeU32(locktime)
        w.writeU32(SIGHASH_ALL)
        return Hashes.doubleSha256(w.toByteArray())
    }

    /**
     * The original signature hash for legacy input [index], SIGHASH_ALL: the
     * transaction with that input's scriptSig replaced by its prevout script,
     * every other scriptSig emptied, and the hash type appended.
     */
    fun legacySighash(index: Int): ByteArray {
        val w = ByteArrayOutputStream()
        w.writeU32(version.toLong())
        w.writeVarInt(inputs.size.toLong())
        for ((n, i) in inputs.withIndex()) {
            outpoint(w, i)
            scriptSig(w, if (n == index) i.p2pkhScript() else ByteArray(0))
            w.writeU32(i.sequence)
        }
        serializeOutputs(w)
        w.writeU32(locktime)
        w.writeU32(SIGHASH_ALL)
        return Hashes.doubleSha256(w.toByteArray())
    }

    /** Sign every input with its key (keys[i] owns inputs[i]). */
    fun sign(keys: List<ByteArray>) {
        require(keys.size == inputs.size) { "one key per input" }
        for ((i, key) in keys.withIndex()) {
            val input = inputs[i]
            val pub = Secp256k1.pubKeyFromSecKey(key)
            check(Hashes.hash160(pub).contentEquals(input.pubKeyHash)) {
                "key does not own input $i"
            }
            if (input.legacy) {
                val sig = Secp256k1.signDer(legacySighash(i), key) + byteArrayOf(SIGHASH_ALL.toByte())
                input.scriptSig = byteArrayOf(sig.size.toByte()) + sig + byteArrayOf(pub.size.toByte()) + pub
            } else {
                val der = Secp256k1.signDer(sighash(i), key)
                input.witness = listOf(der + byteArrayOf(SIGHASH_ALL.toByte()), pub)
            }
        }
    }

    companion object {
        const val SIGHASH_ALL = 1L

        /** Pre-selection fee estimate: weight of a tx with n P2WPKH (or legacy P2PKH) inputs and the given output scripts. */
        fun estimateVsize(inputCount: Int, outputScripts: List<ByteArray>, legacy: Boolean = false): Int {
            // Non-witness: version(4) + in-count + out-count varints + locktime(4);
            // witness adds marker+flag (2 WU) once any input is segwit.
            var weight = (4 + varIntSize(inputCount.toLong()) + varIntSize(outputScripts.size.toLong()) + 4) * 4 +
                if (legacy) 0 else 2
            // Segwit input: outpoint 36 + empty-script varint 1 + sequence 4 (×4), witness ≈ 108 WU
            // (count 1 + 72-byte DER+sighash with its varint + 33-byte pubkey with its varint).
            // Legacy input: the same 107 bytes sit in the scriptSig, at full weight: 148 bytes.
            weight += inputCount * if (legacy) (36 + 1 + 107 + 4) * 4 else ((36 + 1 + 4) * 4 + 108)
            for (s in outputScripts) weight += (8 + varIntSize(s.size.toLong()) + s.size) * 4
            return (weight + 3) / 4
        }

        private fun varIntSize(v: Long): Int = when {
            v < 0xfd -> 1
            v <= 0xffff -> 3
            v <= 0xffffffffL -> 5
            else -> 9
        }
    }
}

private fun String.hexBytes(): ByteArray =
    ByteArray(length / 2) { ((Character.digit(this[2 * it], 16) shl 4) + Character.digit(this[2 * it + 1], 16)).toByte() }

private fun ByteArrayOutputStream.writeU32(v: Long) {
    write((v and 0xff).toInt())
    write(((v shr 8) and 0xff).toInt())
    write(((v shr 16) and 0xff).toInt())
    write(((v shr 24) and 0xff).toInt())
}

private fun ByteArrayOutputStream.writeU64(v: ULong) {
    var x = v
    repeat(8) {
        write((x and 0xffuL).toInt())
        x = x shr 8
    }
}

private fun ByteArrayOutputStream.writeVarInt(v: Long) {
    when {
        v < 0xfd -> write(v.toInt())
        v <= 0xffff -> {
            write(0xfd); write((v and 0xff).toInt()); write(((v shr 8) and 0xff).toInt())
        }
        v <= 0xffffffffL -> {
            write(0xfe); writeU32(v)
        }
        else -> {
            write(0xff)
            var x = v
            repeat(8) { write((x and 0xff).toInt()); x = x shr 8 }
        }
    }
}
