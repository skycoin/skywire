package com.skycoin.wallet.skycoin

import com.skycoin.wallet.AddressBook
import com.skycoin.wallet.Amounts
import com.skycoin.wallet.Bip39
import com.skycoin.wallet.FeePresets
import com.skycoin.wallet.SignedTx
import com.skycoin.wallet.TxPlan
import com.skycoin.wallet.TxRecord
import com.skycoin.wallet.WalletBalance
import com.skycoin.wallet.WalletCore
import com.skycoin.wallet.WalletException
import com.skycoin.wallet.hexToBytes
import okhttp3.OkHttpClient

/**
 * WalletCore for Skycoin and every fiber chain — same daemon, same rules,
 * different node URL. Transaction verification parameters (burn factor,
 * decimal precision, size cap) are read from the node's health endpoint, so
 * a fiber chain with its own settings is honored automatically.
 */
class SkyFiberWalletCore(
    nodeUrl: String,
    client: OkHttpClient,
) : WalletCore {

    private val node = SkycoinNodeClient(nodeUrl, client)

    private class Payload(
        val txn: SkycoinTxn,
        /** Owning address of each input, in input order. */
        val inputAddresses: List<String>,
        val receiveAddresses: List<String>,
    )

    override fun newSeed(): String = Bip39.newMnemonic(128)

    override fun validateSeed(mnemonic: String): Boolean = Bip39.validate(mnemonic)

    override fun deriveAddresses(seed: String, receiveCount: Int, changeCount: Int): AddressBook {
        val keys = SkycoinCrypto.generateKeyPairs(seed.toByteArray(Charsets.UTF_8), receiveCount)
        return AddressBook(
            receive = keys.map { SkycoinCrypto.addressFromPubKey(it.public) },
            change = emptyList(),
        )
    }

    override fun validateAddress(address: String): Boolean = SkycoinCrypto.isValidAddress(address)

    override suspend fun scanUsed(seed: String): Pair<Int, Int> {
        var count = 1
        while (count < SCAN_CAP) {
            val window = deriveAddresses(seed, count + SCAN_AHEAD, 0)
                .receive.subList(count, count + SCAN_AHEAD)
            val used = usedAddresses(window)
            val lastUsed = window.indexOfLast { it in used }
            if (lastUsed < 0) break
            count += lastUsed + 1
        }
        return count.coerceAtMost(SCAN_CAP) to 0
    }

    private suspend fun usedAddresses(addrs: List<String>): Set<String> {
        val txns = node.transactions(addrs)
        val used = HashSet<String>()
        val probe = addrs.toHashSet()
        for (entry in txns) {
            entry.txn.inputs.forEach { if (it.owner in probe) used.add(it.owner) }
            entry.txn.outputs.forEach { if (it.dst in probe) used.add(it.dst) }
        }
        return used
    }

    override suspend fun balance(book: AddressBook): WalletBalance {
        val b = node.balance(book.all())
        return WalletBalance(
            confirmed = b.confirmed.coins,
            predicted = b.predicted.coins,
            hours = b.confirmed.hours,
            spendableOutputs = 0,
        )
    }

    override suspend fun history(book: AddressBook): List<TxRecord> {
        val ours = book.all().toHashSet()
        return node.transactions(book.all()).mapNotNull { entry ->
            val txn = entry.txn
            val inSum = txn.inputs.filter { it.owner in ours }
                .sumOfDroplets { it.coins }
            val outSum = txn.outputs.filter { it.dst in ours }
                .sumOfDroplets { it.coins }
            val incoming = inSum == 0uL
            val amount = if (incoming) outSum else (inSum - minOf(inSum, outSum))
            val party = if (incoming) {
                txn.inputs.firstOrNull { it.owner !in ours }?.owner ?: txn.inputs.firstOrNull()?.owner
            } else {
                txn.outputs.firstOrNull { it.dst !in ours }?.dst
            }
            val ts = if (txn.timestamp > 0uL) txn.timestamp.toLong() else entry.time.toLong()
            TxRecord(
                txid = txn.txid,
                incoming = incoming,
                amount = amount,
                party = party,
                timestamp = ts,
                confirmed = entry.status.confirmed,
                confirmations = entry.status.height.toLong(),
                fee = txn.fee,
            )
        }.sortedWith(compareBy<TxRecord> { it.confirmed }.thenByDescending { it.timestamp })
    }

    private inline fun <T> List<T>.sumOfDroplets(selector: (T) -> String): ULong {
        var sum = 0uL
        for (e in this) sum += Amounts.parse(selector(e), DROPLET_EXPONENT) ?: 0uL
        return sum
    }

    override suspend fun feePresets(): FeePresets? = null

    override fun estimateMax(balance: WalletBalance, feeRate: Int?): ULong = balance.confirmed

    override suspend fun buildTx(
        seed: String,
        book: AddressBook,
        toAddress: String,
        amount: ULong,
        feeRate: Int?,
        sendMax: Boolean,
    ): TxPlan {
        if (!validateAddress(toAddress)) throw WalletException.InvalidAddress()

        val health = node.health()
        val params = health.userVerifyTxn

        val outputs = node.outputs(book.all())
        val outgoing = outputs.outgoingOutputs.map { it.hash }.toHashSet()
        val spendable = outputs.headOutputs.filter { it.hash !in outgoing }.map {
            SkycoinCreate.UxBalance(
                hash = it.hash.hexToBytes(),
                hashHex = it.hash,
                bkSeq = it.blockSeq,
                address = it.address,
                coins = Amounts.parse(it.coins, DROPLET_EXPONENT)
                    ?: throw WalletException.NodeRejected("node reported an unparseable output amount"),
                initialHours = it.hours,
                hours = it.calculatedHours,
            )
        }

        val sendAmount = if (sendMax) {
            var total = 0uL
            for (u in spendable) total += u.coins
            if (total == 0uL) throw WalletException.InsufficientBalance()
            total
        } else {
            amount
        }
        if (sendAmount == 0uL) throw WalletException.InvalidAmount("enter an amount to send")

        // The chain caps decimal precision; sub-precision amounts are rejected
        // by every node, so fail here with the limit spelled out.
        var divisor = 1uL
        repeat(DROPLET_EXPONENT - params.maxDecimals) { divisor *= 10uL }
        if (sendAmount % divisor != 0uL) {
            throw WalletException.InvalidAmount("amounts on this chain carry at most ${params.maxDecimals} decimals")
        }

        val created = try {
            SkycoinCreate.create(
                unspents = spendable,
                to = listOf(SkycoinCreate.Destination(toAddress, sendAmount)),
                changeAddress = book.receive.first(),
                burnFactor = params.burnFactor,
            )
        } catch (e: SkycoinCreate.InsufficientBalance) {
            throw WalletException.InsufficientBalance()
        } catch (e: SkycoinCreate.InsufficientHours) {
            throw WalletException.InsufficientHours()
        } catch (e: SkycoinCreate.NoFee) {
            throw WalletException.NoHoursToBurn()
        } catch (e: SkycoinCreate.ZeroSpend) {
            throw WalletException.InvalidAmount("enter an amount to send")
        }

        val size = created.txn.serialize().size
        if (size.toUInt() > params.maxTransactionSize) {
            throw WalletException.NodeRejected(
                "transaction of $size bytes exceeds the chain limit of ${params.maxTransactionSize}"
            )
        }

        val inputAddresses = created.txn.inputs.map { uxid ->
            created.spends.first { it.hash.contentEquals(uxid) }.address
        }

        return TxPlan(
            toAddress = toAddress,
            amount = sendAmount,
            fee = created.feeHours,
            changeAmount = created.changeCoins,
            hoursToRecipient = created.hoursToDestinations,
            hoursChange = created.changeHours,
            vsize = null,
            feeRate = null,
            payload = Payload(created.txn, inputAddresses, book.receive),
        )
    }

    override fun signTx(seed: String, plan: TxPlan): SignedTx {
        val payload = plan.payload as Payload
        val keys = SkycoinCrypto.generateKeyPairs(
            seed.toByteArray(Charsets.UTF_8),
            payload.receiveAddresses.size,
        )
        val byAddress = payload.receiveAddresses.indices.associate { i ->
            SkycoinCrypto.addressFromPubKey(keys[i].public) to keys[i].secret
        }
        val inputKeys = payload.inputAddresses.map { addr ->
            byAddress[addr] ?: error("input address $addr is not part of this wallet")
        }
        payload.txn.signInputs(inputKeys)
        return SignedTx(rawHex = payload.txn.serializeHex(), txid = payload.txn.txidHex())
    }

    override suspend fun broadcast(tx: SignedTx): String = node.inject(tx.rawHex)

    companion object {
        const val DROPLET_EXPONENT = 6
        private const val SCAN_AHEAD = 10
        private const val SCAN_CAP = 100
    }
}
