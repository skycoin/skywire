package com.skycoin.wallet.eth

import com.skycoin.wallet.Secp256k1
import java.math.BigInteger

/**
 * One EIP-1559 (type 2) transaction. This wallet only ever sends value or
 * calls `transfer` on a token, so `to` is always a real address and the
 * access list is always empty.
 */
class EthTxn(
    val chainId: BigInteger,
    val nonce: BigInteger,
    val maxPriorityFeePerGas: BigInteger,
    val maxFeePerGas: BigInteger,
    val gasLimit: BigInteger,
    /** 20 bytes — contract creation has no place in a phone wallet. */
    val to: ByteArray,
    val value: BigInteger,
    val data: ByteArray,
) {

    init {
        require(to.size == 20) { "destination must be 20 bytes" }
    }

    private fun unsignedFields(): MutableList<Rlp.Item> = mutableListOf(
        Rlp.of(chainId),
        Rlp.of(nonce),
        Rlp.of(maxPriorityFeePerGas),
        Rlp.of(maxFeePerGas),
        Rlp.of(gasLimit),
        Rlp.of(to),
        Rlp.of(value),
        Rlp.of(data),
        Rlp.Lst(emptyList()), // access list
    )

    /** What gets signed: keccak(0x02 ‖ rlp(unsigned fields)). */
    fun signingHash(): ByteArray =
        EthCrypto.keccak256(byteArrayOf(TYPE) + Rlp.encode(Rlp.Lst(unsignedFields())))

    /**
     * Sign and serialize. The compact signature's recovery id doubles as the
     * yParity field; with low-S enforced it is 0 or 1 except for one
     * astronomically improbable r ≥ n case, which is refused rather than
     * broadcast malformed.
     */
    fun signed(sec: ByteArray): Signed {
        val sig = Secp256k1.signCompact(signingHash(), sec)
        val yParity = sig[64].toInt()
        require(yParity < 2) { "signature recovery id not representable in yParity" }
        val fields = unsignedFields()
        fields += Rlp.of(BigInteger.valueOf(yParity.toLong()))
        fields += Rlp.of(BigInteger(1, sig.copyOfRange(0, 32)))
        fields += Rlp.of(BigInteger(1, sig.copyOfRange(32, 64)))
        val raw = byteArrayOf(TYPE) + Rlp.encode(Rlp.Lst(fields))
        return Signed(raw = raw, hash = EthCrypto.keccak256(raw), signature = sig)
    }

    /** The wire bytes, their keccak (the txid), and the compact signature. */
    class Signed(val raw: ByteArray, val hash: ByteArray, val signature: ByteArray)

    companion object {
        private const val TYPE: Byte = 0x02

        /** `transfer(address,uint256)` call data — the whole ERC-20 surface this wallet uses. */
        fun erc20Transfer(to: ByteArray, amount: BigInteger): ByteArray {
            require(to.size == 20) { "recipient must be 20 bytes" }
            val selector = byteArrayOf(0xa9.toByte(), 0x05, 0x9c.toByte(), 0xbb.toByte())
            return selector + pad32(to) + pad32(Rlp.of(amount).bytes)
        }

        /** `balanceOf(address)` call data. */
        fun erc20BalanceOf(owner: ByteArray): ByteArray {
            require(owner.size == 20) { "owner must be 20 bytes" }
            val selector = byteArrayOf(0x70, 0xa0.toByte(), 0x82.toByte(), 0x31)
            return selector + pad32(owner)
        }

        private fun pad32(b: ByteArray): ByteArray {
            require(b.size <= 32) { "ABI word overflow" }
            return ByteArray(32 - b.size) + b
        }
    }
}
