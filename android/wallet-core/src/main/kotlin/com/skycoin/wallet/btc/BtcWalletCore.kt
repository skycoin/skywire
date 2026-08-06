package com.skycoin.wallet.btc

import com.skycoin.wallet.AddressBook
import com.skycoin.wallet.Bip39
import com.skycoin.wallet.FeePresets
import com.skycoin.wallet.Hashes
import com.skycoin.wallet.SignedTx
import com.skycoin.wallet.TxPlan
import com.skycoin.wallet.TxRecord
import com.skycoin.wallet.WalletBalance
import com.skycoin.wallet.WalletCore
import com.skycoin.wallet.WalletException
import okhttp3.OkHttpClient

/**
 * WalletCore for Bitcoin mainnet: BIP 84 keys, P2WPKH spends, an esplora
 * server for the chain view. Fee is sat/vB chosen by the user; transactions
 * signal RBF.
 */
class BtcWalletCore(
    esploraUrl: String,
    client: OkHttpClient,
) : WalletCore {

    private val api = BtcEsploraClient(esploraUrl, client)

    private class PlanInput(
        val txid: String,
        val vout: Int,
        val value: ULong,
        val change: Int,
        val index: Int,
        val pubKeyHash: ByteArray,
    )

    private class Payload(
        val inputs: List<PlanInput>,
        val outputs: List<BtcTxn.Output>,
        /** Fresh change chain index used by this plan, or -1 when no change. */
        val changeIndex: Int,
    )

    override fun newSeed(): String = Bip39.newMnemonic(128)

    override fun validateSeed(mnemonic: String): Boolean = Bip39.validate(mnemonic)

    override fun deriveAddresses(seed: String, receiveCount: Int, changeCount: Int): AddressBook {
        val account = Bip84.accountKey(seed)
        return AddressBook(
            receive = (0 until receiveCount).map { Bip84.address(Bip84.key(account, 0, it).pubKey()) },
            change = (0 until changeCount).map { Bip84.address(Bip84.key(account, 1, it).pubKey()) },
        )
    }

    override fun validateAddress(address: String): Boolean = BtcAddress.isValid(address)

    override suspend fun scanUsed(seed: String): Pair<Int, Int> {
        val account = Bip84.accountKey(seed)
        suspend fun scanChain(chain: Int, minimum: Int): Int {
            var lastUsed = -1
            var i = 0
            var gap = 0
            while (gap < SCAN_GAP && i < SCAN_CAP) {
                val addr = Bip84.address(Bip84.key(account, chain, i).pubKey())
                val info = api.addressInfo(addr)
                if (info.chain.txCount > 0 || info.mempool.txCount > 0) {
                    lastUsed = i
                    gap = 0
                } else {
                    gap++
                }
                i++
            }
            return maxOf(lastUsed + 1, minimum)
        }
        return scanChain(0, 1) to scanChain(1, 0)
    }

    override suspend fun balance(book: AddressBook): WalletBalance {
        var confirmed = 0uL
        var all = 0uL
        var outputs = 0
        for (addr in book.all()) {
            for (u in api.utxos(addr)) {
                all += u.value
                if (u.status.confirmed) {
                    confirmed += u.value
                    outputs++
                }
            }
        }
        return WalletBalance(
            confirmed = confirmed,
            predicted = all,
            hours = null,
            spendableOutputs = outputs,
        )
    }

    override suspend fun history(book: AddressBook): List<TxRecord> {
        val ours = book.all().toHashSet()
        val tip = runCatching { api.tipHeight() }.getOrDefault(0L)
        val seen = HashMap<String, TxRecord>()
        for (addr in book.all()) {
            for (tx in api.transactions(addr)) {
                if (tx.txid in seen) continue
                val inSum = tx.vin.sumOf { v -> if (v.prevout?.address in ours) v.prevout!!.value else 0uL }
                val outSum = tx.vout.sumOf { v -> if (v.address in ours) v.value else 0uL }
                val incoming = inSum == 0uL
                val amount = if (incoming) outSum else {
                    // What actually left the wallet, fee excluded from the headline number.
                    val spent = inSum - minOf(inSum, outSum)
                    if (spent >= tx.fee) spent - tx.fee else spent
                }
                val party = if (incoming) {
                    tx.vin.firstOrNull { it.prevout?.address != null && it.prevout.address !in ours }?.prevout?.address
                } else {
                    tx.vout.firstOrNull { it.address != null && it.address !in ours }?.address
                }
                val confirmations = if (tx.status.confirmed && tip >= tx.status.blockHeight) {
                    tip - tx.status.blockHeight + 1
                } else 0L
                seen[tx.txid] = TxRecord(
                    txid = tx.txid,
                    incoming = incoming,
                    amount = amount,
                    party = party,
                    timestamp = if (tx.status.confirmed) tx.status.blockTime else System.currentTimeMillis() / 1000,
                    confirmed = tx.status.confirmed,
                    confirmations = confirmations,
                    fee = tx.fee,
                )
            }
        }
        return seen.values.sortedWith(compareBy<TxRecord> { it.confirmed }.thenByDescending { it.timestamp })
    }

    override suspend fun feePresets(): FeePresets {
        val (economy, normal, priority) = api.feeRates()
        return FeePresets(economy, normal, priority)
    }

    override fun estimateMax(balance: WalletBalance, feeRate: Int?): ULong {
        val rate = (feeRate ?: 1).coerceAtLeast(1).toULong()
        if (balance.spendableOutputs == 0) return 0uL
        val vsize = BtcTxn.estimateVsize(balance.spendableOutputs, listOf(ByteArray(22))).toULong()
        val fee = rate * vsize
        return if (balance.confirmed > fee) balance.confirmed - fee else 0uL
    }

    override suspend fun buildTx(
        seed: String,
        book: AddressBook,
        toAddress: String,
        amount: ULong,
        feeRate: Int?,
        sendMax: Boolean,
    ): TxPlan {
        val destScript = BtcAddress.scriptPubKey(toAddress) ?: throw WalletException.InvalidAddress()
        val rate = (feeRate ?: 1).coerceAtLeast(1).toULong()

        val account = Bip84.accountKey(seed)
        val programOf = HashMap<String, Triple<Int, Int, ByteArray>>() // address → (chain, index, hash160)
        book.receive.forEachIndexed { i, a -> programOf[a] = Triple(0, i, Hashes.hash160(Bip84.key(account, 0, i).pubKey())) }
        book.change.forEachIndexed { i, a -> programOf[a] = Triple(1, i, Hashes.hash160(Bip84.key(account, 1, i).pubKey())) }

        // Confirmed outputs only — the design promise is that pending funds
        // cannot be respent from this wallet.
        val utxos = ArrayList<Pair<String, BtcEsploraClient.Utxo>>()
        for (addr in book.all()) {
            for (u in api.utxos(addr)) if (u.status.confirmed) utxos.add(addr to u)
        }
        if (utxos.isEmpty()) throw WalletException.InsufficientBalance()
        utxos.sortByDescending { it.second.value }

        val changeScriptLen = 22 // P2WPKH change

        fun planInput(p: Pair<String, BtcEsploraClient.Utxo>): PlanInput {
            val (chain, index, hash) = programOf.getValue(p.first)
            return PlanInput(p.second.txid, p.second.vout, p.second.value, chain, index, hash)
        }

        if (sendMax) {
            val total = utxos.sumOf { it.second.value }
            val fee = rate * BtcTxn.estimateVsize(utxos.size, listOf(destScript)).toULong()
            if (total <= fee + DUST_SATS) throw WalletException.InsufficientBalance()
            val sendAmount = total - fee
            val outputs = listOf(BtcTxn.Output(sendAmount, destScript))
            return TxPlan(
                toAddress = toAddress,
                amount = sendAmount,
                fee = fee,
                changeAmount = 0uL,
                hoursToRecipient = null,
                hoursChange = null,
                vsize = BtcTxn.estimateVsize(utxos.size, listOf(destScript)),
                feeRate = rate.toLong().toInt(),
                payload = Payload(utxos.map(::planInput), outputs, changeIndex = -1),
            )
        }

        if (amount == 0uL) throw WalletException.InvalidAmount("enter an amount to send")
        if (amount < DUST_SATS) throw WalletException.InvalidAmount("amount is below the dust limit")

        val selected = ArrayList<Pair<String, BtcEsploraClient.Utxo>>()
        var inSum = 0uL
        var feeWithChange = 0uL
        for (u in utxos) {
            selected.add(u)
            inSum += u.second.value
            feeWithChange = rate * BtcTxn.estimateVsize(selected.size, listOf(destScript, ByteArray(changeScriptLen))).toULong()
            if (inSum >= amount + feeWithChange) break
        }
        if (inSum < amount) throw WalletException.InsufficientBalance()

        var change = if (inSum >= amount + feeWithChange) inSum - amount - feeWithChange else 0uL
        var fee = feeWithChange
        var changeIndex = -1
        val outputs = ArrayList<BtcTxn.Output>()
        outputs.add(BtcTxn.Output(amount, destScript))

        if (change < DUST_SATS) {
            // No change output: whatever is left over joins the fee.
            fee = inSum - amount
            val feeNoChange = rate * BtcTxn.estimateVsize(selected.size, listOf(destScript)).toULong()
            if (fee < feeNoChange) throw WalletException.InsufficientBalance()
            change = 0uL
        } else {
            changeIndex = book.change.size
            val changeAddr = Bip84.address(Bip84.key(account, 1, changeIndex).pubKey())
            val changeScript = BtcAddress.scriptPubKey(changeAddr) ?: error("derived change address failed to decode")
            outputs.add(BtcTxn.Output(change, changeScript))
        }

        val vsize = BtcTxn.estimateVsize(selected.size, outputs.map { it.scriptPubKey })
        return TxPlan(
            toAddress = toAddress,
            amount = amount,
            fee = fee,
            changeAmount = change,
            hoursToRecipient = null,
            hoursChange = null,
            vsize = vsize,
            feeRate = rate.toLong().toInt(),
            payload = Payload(selected.map(::planInput), outputs, changeIndex),
        )
    }

    override fun signTx(seed: String, plan: TxPlan): SignedTx {
        val payload = plan.payload as Payload
        val account = Bip84.accountKey(seed)
        val txn = BtcTxn(
            inputs = payload.inputs.map { BtcTxn.Input(it.txid, it.vout, it.value, it.pubKeyHash) },
            outputs = payload.outputs,
        )
        val keys = payload.inputs.map { Bip84.key(account, it.change, it.index).key }
        txn.sign(keys)
        return SignedTx(rawHex = txn.serialize().let { b -> b.joinToString("") { "%02x".format(it) } }, txid = txn.txid(), payload = payload.changeIndex)
    }

    override suspend fun broadcast(tx: SignedTx): String = api.broadcast(tx.rawHex)

    /** The change chain index a broadcast plan consumed, or -1. */
    fun consumedChangeIndex(tx: SignedTx): Int = (tx.payload as? Int) ?: -1

    companion object {
        /** Core's dust threshold for P2WPKH outputs. */
        const val DUST_SATS = 294uL
        private const val SCAN_GAP = 5
        private const val SCAN_CAP = 40
    }
}
