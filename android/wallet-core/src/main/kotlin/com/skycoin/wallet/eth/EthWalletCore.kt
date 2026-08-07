package com.skycoin.wallet.eth

import com.skycoin.wallet.AddressBook
import com.skycoin.wallet.Bip39
import com.skycoin.wallet.FeePresets
import com.skycoin.wallet.SignedTx
import com.skycoin.wallet.TxPlan
import com.skycoin.wallet.TxRecord
import com.skycoin.wallet.WalletBalance
import com.skycoin.wallet.WalletCore
import com.skycoin.wallet.WalletException
import okhttp3.OkHttpClient
import java.math.BigInteger

/**
 * WalletCore for Ethereum: BIP 44 keys, EIP-1559 sends, a JSON-RPC node for
 * state and an etherscan-style indexer for history. One class covers the
 * native coin and any ERC-20 [token] — a token send is the same transaction
 * with the value moved into `transfer` call data and the gas still paid in
 * ETH.
 *
 * Base units are chosen to fit the seam's 64-bit amounts, which wei does
 * not (2⁶⁴ wei ≈ 18.4 ETH): the native coin is carried in **gwei** and a
 * token in units of 10^-[Erc20Token.baseExponent], losing nothing anyone
 * can spend deliberately. Wei only exists inside this class.
 */
class EthWalletCore(
    rpcUrl: String,
    indexerUrl: String?,
    client: OkHttpClient,
    private val token: Erc20Token? = null,
) : WalletCore {

    /** An ERC-20 the wallet holds: where it lives and how it counts. */
    class Erc20Token(val contract: String, val decimals: Int) {
        /** The seam's exponent — the token's own, capped so amounts fit 64 bits. */
        val baseExponent: Int = minOf(decimals, MAX_BASE_EXPONENT)

        /** Raw contract units per seam base unit. */
        val rawScale: BigInteger = BigInteger.TEN.pow(decimals - baseExponent)
    }

    private val api = EthRpcClient(rpcUrl, indexerUrl, client)

    /** Read once per core, first use — it never changes under a URL. */
    @Volatile private var chainId: BigInteger? = null

    private class Payload(
        val chainId: BigInteger,
        val nonce: BigInteger,
        val maxPriorityFeePerGas: BigInteger,
        val maxFeePerGas: BigInteger,
        val gasLimit: BigInteger,
        val to: ByteArray,
        val valueWei: BigInteger,
        val data: ByteArray,
        /** Which receive index funds and signs this send. */
        val fromIndex: Int,
    )

    override fun newSeed(): String = Bip39.newMnemonic(128)

    override fun validateSeed(mnemonic: String): Boolean = Bip39.validate(mnemonic)

    override fun deriveAddresses(seed: String, receiveCount: Int, changeCount: Int): AddressBook {
        val account = EthCrypto.accountKey(seed)
        return AddressBook(
            receive = (0 until receiveCount).map { EthCrypto.address(EthCrypto.key(account, it).pubKey()) },
            // An account chain has no change side: sends spend from one
            // address and the remainder simply stays on it.
            change = emptyList(),
        )
    }

    override fun validateAddress(address: String): Boolean = EthCrypto.isValidAddress(address)

    override suspend fun scanUsed(seed: String): Pair<Int, Int> {
        val account = EthCrypto.accountKey(seed)
        var lastUsed = -1
        var i = 0
        var gap = 0
        while (gap < SCAN_GAP && i < SCAN_CAP) {
            val addr = EthCrypto.address(EthCrypto.key(account, i).pubKey())
            val used = api.nonce(addr).signum() > 0 || api.balanceWei(addr).signum() > 0
            if (used) {
                lastUsed = i
                gap = 0
            } else {
                gap++
            }
            i++
        }
        return maxOf(lastUsed + 1, 1) to 0
    }

    override suspend fun balance(book: AddressBook): WalletBalance {
        var total = BigInteger.ZERO
        var funded = 0
        for (addr in book.receive) {
            val value = if (token == null) api.balanceWei(addr) else tokenBalanceRaw(addr)
            if (value.signum() > 0) funded++
            total += value
        }
        val base = toBase(total)
        return WalletBalance(
            confirmed = base,
            // No mempool view over plain RPC; the 30-second refresh is what
            // moves this number, exactly like a block would.
            predicted = base,
            hours = null,
            spendableOutputs = funded,
        )
    }

    override suspend fun history(book: AddressBook): List<TxRecord> {
        val ours = book.receive.map { it.lowercase() }.toHashSet()
        val seen = HashMap<String, TxRecord>()
        for (addr in book.receive) {
            val rows = if (token == null) {
                api.transactions(addr)
            } else {
                api.tokenTransfers(addr, token.contract)
            }
            for (row in rows) {
                if (row.hash in seen) continue
                // A failed send moved no value — only its gas burned, which
                // is not this list's story to tell.
                if (row.isError != "0") continue
                val from = row.from.lowercase()
                val to = row.to.lowercase()
                val incoming = from !in ours
                val confirmations = row.confirmations.toLongOrNull() ?: 0L
                val feeWei = (row.gasUsed.toBigIntegerOrNull() ?: BigInteger.ZERO) *
                    (row.gasPrice.toBigIntegerOrNull() ?: BigInteger.ZERO)
                seen[row.hash] = TxRecord(
                    txid = row.hash,
                    incoming = incoming,
                    amount = toBase(row.value.toBigIntegerOrNull() ?: BigInteger.ZERO),
                    party = when {
                        from in ours && to in ours -> null
                        incoming -> row.from
                        else -> row.to
                    },
                    timestamp = row.timestamp.toLongOrNull() ?: 0L,
                    confirmed = confirmations > 0,
                    confirmations = confirmations,
                    // Always the ETH gas, in gwei — a token has no fee of its
                    // own, and the UI labels this line ETH on both cores.
                    fee = feeWei.takeIf { it.signum() > 0 }?.let(::weiToGwei),
                )
            }
        }
        return seen.values.sortedWith(compareBy<TxRecord> { it.confirmed }.thenByDescending { it.timestamp })
    }

    /** EIP-1559 prices itself; there is no knob worth a card of presets. */
    override suspend fun feePresets(): FeePresets? = null

    /**
     * The Max prefill. Gas prices are not knowable here (this cannot
     * suspend), so the native coin holds back a generous fixed headroom;
     * sendMax in [buildTx] then computes the real remainder with live
     * prices. A token spends no ETH from its own balance, so Max is all of it.
     */
    override fun estimateMax(balance: WalletBalance, feeRate: Int?): ULong {
        if (token != null) return balance.confirmed
        return if (balance.confirmed > GAS_HEADROOM_GWEI) balance.confirmed - GAS_HEADROOM_GWEI else 0uL
    }

    override suspend fun buildTx(
        seed: String,
        book: AddressBook,
        toAddress: String,
        amount: ULong,
        feeRate: Int?,
        sendMax: Boolean,
    ): TxPlan {
        val dest = EthCrypto.parseAddress(toAddress.trim()) ?: throw WalletException.InvalidAddress()
        if (!sendMax && amount == 0uL) throw WalletException.InvalidAmount("enter an amount to send")

        // The funding account: the address holding the most of what is being
        // sent. An account chain does not sweep across addresses the way
        // UTXOs do — one address signs, and its balance is the ceiling.
        val balances = book.receive.mapIndexed { i, addr ->
            Triple(i, addr, if (token == null) api.balanceWei(addr) else tokenBalanceRaw(addr))
        }
        val (fromIndex, fromAddr, fromBalance) = balances.maxByOrNull { it.third }
            ?: throw WalletException.InsufficientBalance()

        val id = chainId ?: api.chainId().also { chainId = it }
        val priority = api.maxPriorityFeePerGas()
        // Twice the current base fee rides out the worst-case climb while
        // the send waits; anything unspent is never charged.
        val maxFee = api.baseFeePerGas() * BigInteger.valueOf(2) + priority
        val nonce = api.nonce(fromAddr)

        if (token == null) {
            var valueWei = gweiToWei(amount)
            var gas = padGas(api.estimateGas(fromAddr, toAddress, valueWei, null)) ?: GAS_TRANSFER
            var feeWei = gas * maxFee
            if (sendMax) {
                if (fromBalance <= feeWei) throw WalletException.InsufficientBalance()
                // Floor to a whole gwei so the plan's amount and the wire's
                // value are the same number.
                valueWei = gweiToWei(weiToGwei(fromBalance - feeWei))
                gas = padGas(api.estimateGas(fromAddr, toAddress, valueWei, null)) ?: gas
                feeWei = gas * maxFee
            }
            if (fromBalance < valueWei + feeWei) throw WalletException.InsufficientBalance()
            return TxPlan(
                toAddress = toAddress.trim(),
                amount = weiToGwei(valueWei),
                fee = weiToGweiCeil(feeWei),
                changeAmount = 0uL,
                hoursToRecipient = null,
                hoursChange = null,
                vsize = gas.toInt(),
                feeRate = weiToGweiCeil(maxFee).toLong().toInt(),
                payload = Payload(id, nonce, priority, maxFee, gas, dest, valueWei, ByteArray(0), fromIndex),
            )
        }

        val amountRaw = if (sendMax) fromBalance else amount.toBigInteger() * token.rawScale
        if (amountRaw.signum() <= 0) throw WalletException.InvalidAmount("enter an amount to send")
        if (fromBalance < amountRaw) throw WalletException.InsufficientBalance()
        val data = EthTxn.erc20Transfer(dest, amountRaw)
        val gas = padGas(api.estimateGas(fromAddr, token.contract, BigInteger.ZERO, data))
            ?: GAS_TOKEN_TRANSFER
        val feeWei = gas * maxFee
        val ethWei = api.balanceWei(fromAddr)
        if (ethWei < feeWei) {
            throw WalletException.InsufficientGas(
                "the sending address holds too little ETH for the network fee",
            )
        }
        val contractBytes = EthCrypto.parseAddress(token.contract)
            ?: error("token registered with an invalid contract address")
        return TxPlan(
            toAddress = toAddress.trim(),
            amount = (amountRaw / token.rawScale).toULongClamped(),
            fee = weiToGweiCeil(feeWei),
            changeAmount = 0uL,
            hoursToRecipient = null,
            hoursChange = null,
            vsize = gas.toInt(),
            feeRate = weiToGweiCeil(maxFee).toLong().toInt(),
            payload = Payload(id, nonce, priority, maxFee, gas, contractBytes, BigInteger.ZERO, data, fromIndex),
        )
    }

    override fun signTx(seed: String, plan: TxPlan): SignedTx {
        val payload = plan.payload as Payload
        val key = EthCrypto.key(EthCrypto.accountKey(seed), payload.fromIndex)
        val signed = EthTxn(
            chainId = payload.chainId,
            nonce = payload.nonce,
            maxPriorityFeePerGas = payload.maxPriorityFeePerGas,
            maxFeePerGas = payload.maxFeePerGas,
            gasLimit = payload.gasLimit,
            to = payload.to,
            value = payload.valueWei,
            data = payload.data,
        ).signed(key.key)
        return SignedTx(
            rawHex = signed.raw.joinToString("") { "%02x".format(it) },
            txid = "0x" + signed.hash.joinToString("") { "%02x".format(it) },
        )
    }

    override suspend fun broadcast(tx: SignedTx): String {
        val raw = ByteArray(tx.rawHex.length / 2) { i ->
            ((Character.digit(tx.rawHex[i * 2], 16) shl 4) or
                Character.digit(tx.rawHex[i * 2 + 1], 16)).toByte()
        }
        return api.sendRaw(raw)
    }

    // --- units ---

    private suspend fun tokenBalanceRaw(address: String): BigInteger {
        val owner = EthCrypto.parseAddress(address) ?: return BigInteger.ZERO
        val result = api.ethCall(token!!.contract, EthTxn.erc20BalanceOf(owner))
        return if (result.isEmpty()) BigInteger.ZERO else BigInteger(1, result)
    }

    /** Wei (native) or raw contract units (token) → the seam's base units. */
    private fun toBase(value: BigInteger): ULong =
        if (token == null) weiToGwei(value) else (value / token.rawScale).toULongClamped()

    private fun weiToGwei(wei: BigInteger): ULong = (wei / WEI_PER_GWEI).toULongClamped()

    private fun weiToGweiCeil(wei: BigInteger): ULong =
        ((wei + WEI_PER_GWEI - BigInteger.ONE) / WEI_PER_GWEI).toULongClamped()

    private fun gweiToWei(gwei: ULong): BigInteger = gwei.toBigInteger() * WEI_PER_GWEI

    /** A fifth over the node's estimate: first sends to fresh slots cost more. */
    private fun padGas(estimate: BigInteger?): BigInteger? =
        estimate?.let { it * BigInteger.valueOf(12) / BigInteger.TEN }

    private fun ULong.toBigInteger(): BigInteger = BigInteger(this.toString())

    private fun String.toBigIntegerOrNull(): BigInteger? =
        runCatching { BigInteger(this) }.getOrNull()

    private fun BigInteger.toULongClamped(): ULong = when {
        signum() <= 0 -> 0uL
        bitLength() > 64 -> ULong.MAX_VALUE
        else -> toString().toULong()
    }

    private companion object {
        val WEI_PER_GWEI: BigInteger = BigInteger.valueOf(1_000_000_000)

        /** A plain EOA transfer, exactly. */
        val GAS_TRANSFER: BigInteger = BigInteger.valueOf(21_000)

        /** Fallback when the node will not estimate a token transfer. */
        val GAS_TOKEN_TRANSFER: BigInteger = BigInteger.valueOf(100_000)

        /**
         * What the Max prefill holds back for gas on the native coin:
         * 21 000 gas at 100 gwei. The real remainder is computed with live
         * prices when the max send is actually planned.
         */
        const val GAS_HEADROOM_GWEI: ULong = 2_100_000uL

        /** Cap that keeps any token amount inside the seam's 64 bits. */
        const val MAX_BASE_EXPONENT = 9

        private const val SCAN_GAP = 3
        private const val SCAN_CAP = 10
    }
}
