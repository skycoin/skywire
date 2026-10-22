package com.skycoin.wallet.skycoin

import com.skycoin.wallet.AddressBook
import com.skycoin.wallet.Amounts
import com.skycoin.wallet.Bip32
import com.skycoin.wallet.Bip39
import com.skycoin.wallet.Derivation
import com.skycoin.wallet.ExtendedPublicKeys
import com.skycoin.wallet.FeeLevel
import com.skycoin.wallet.FeePresets
import com.skycoin.wallet.KeySource
import com.skycoin.wallet.NodeInfo
import com.skycoin.wallet.NodeProblem
import com.skycoin.wallet.Recipient
import com.skycoin.wallet.SignedTx
import com.skycoin.wallet.TxPlan
import com.skycoin.wallet.TxRecord
import com.skycoin.wallet.UtxoRecord
import com.skycoin.wallet.WalletBalance
import com.skycoin.wallet.WalletCore
import com.skycoin.wallet.WalletException
import com.skycoin.wallet.hexToBytes
import com.skycoin.wallet.toHex
import okhttp3.OkHttpClient
import java.math.BigDecimal

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
        val book: AddressBook,
    )

    override fun newSeed(): String = Bip39.newMnemonic(128)

    override fun validateSeed(mnemonic: String): Boolean = Bip39.validate(mnemonic)

    // The seed is the phrase's bytes (deriveAddresses), so no checksum is needed.
    override fun acceptsLegacySeed(mnemonic: String): Boolean = Bip39.legacyWords(mnemonic)

    override fun takesAnySeed(): Boolean = true

    override fun deriveAddresses(seed: KeySource, receiveCount: Int, changeCount: Int): AddressBook {
        if (seed.derivation == Derivation.BIP44) {
            val account = SkycoinBip44.accountKey(seed.mnemonic, seed.passphrase)
            fun chain(c: Int, n: Int) = List(n) { SkycoinCrypto.addressFromPubKey(SkycoinBip44.key(account, c, it).pubKey()) }
            return AddressBook(receive = chain(0, receiveCount), change = chain(1, changeCount))
        }
        // The classic wallet has one chain; change returns to an address of it.
        val keys = SkycoinCrypto.generateKeyPairs(seed.mnemonic.toByteArray(Charsets.UTF_8), receiveCount)
        return AddressBook(
            receive = keys.map { SkycoinCrypto.addressFromPubKey(it.public) },
            change = emptyList(),
        )
    }

    /** The account and chain xpubs of a BIP 44 wallet, as skycoin's GetXPubKey gives them; null otherwise. */
    override fun extendedPublicKeys(seed: KeySource): ExtendedPublicKeys? {
        if (seed.derivation != Derivation.BIP44) return null
        val account = SkycoinBip44.accountKey(seed.mnemonic, seed.passphrase)
        return ExtendedPublicKeys(
            account = account.xpub(),
            receive = Bip32.ckdPriv(account, 0).xpub(),
            change = Bip32.ckdPriv(account, 1).xpub(),
        )
    }

    override fun validateAddress(address: String): Boolean = SkycoinCrypto.isValidAddress(address)

    override suspend fun scanUsed(seed: KeySource): Pair<Int, Int> {
        val receive = scanChain(1) { n -> deriveAddresses(seed, n, 0).receive }
        // A BIP 44 wallet's change chain starts empty; its first address may never have been used.
        val change = if (seed.derivation == Derivation.BIP44) scanChain(0) { n -> deriveAddresses(seed, 0, n).change } else 0
        return receive to change
    }

    /** How many addresses of one chain are in use, looking [SCAN_AHEAD] past the last used one. */
    private suspend fun scanChain(start: Int, derive: (Int) -> List<String>): Int {
        var count = start
        while (count < SCAN_CAP) {
            val window = derive(count + SCAN_AHEAD).subList(count, count + SCAN_AHEAD)
            val used = usedAddresses(window)
            val lastUsed = window.indexOfLast { it in used }
            if (lastUsed < 0) break
            count += lastUsed + 1
        }
        return count.coerceAtMost(SCAN_CAP)
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
        // The per-address split: head outputs are the confirmed unspent
        // ones, which is exactly what the node's confirmed balance sums.
        val per = HashMap<String, ULong>()
        for (o in node.outputs(book.all()).headOutputs) {
            val coins = Amounts.parse(o.coins, DROPLET_EXPONENT) ?: 0uL
            per[o.address] = (per[o.address] ?: 0uL) + coins
        }
        return WalletBalance(
            confirmed = b.confirmed.coins,
            predicted = b.predicted.coins,
            hours = b.confirmed.hours,
            spendableOutputs = 0,
            perAddress = per,
        )
    }

    /** The confirmed unspent outputs a coin picker can offer. */
    override suspend fun utxos(book: AddressBook): List<UtxoRecord> {
        val outputs = node.outputs(book.all())
        val outgoing = outputs.outgoingOutputs.map { it.hash }.toHashSet()
        return outputs.headOutputs
            .filter { it.hash !in outgoing }
            .map {
                UtxoRecord(
                    uxid = it.hash,
                    address = it.address,
                    coins = Amounts.parse(it.coins, DROPLET_EXPONENT) ?: 0uL,
                    hours = it.calculatedHours,
                )
            }
            .sortedByDescending { it.coins }
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
            val own = if (incoming) {
                txn.outputs.map { it.dst }.filter { it in ours }
            } else {
                txn.inputs.map { it.owner }.filter { it in ours }
            }.distinct()
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
                own = own,
            )
        }.sortedWith(compareBy<TxRecord> { it.confirmed }.thenByDescending { it.timestamp })
    }

    private inline fun <T> List<T>.sumOfDroplets(selector: (T) -> String): ULong {
        var sum = 0uL
        for (e in this) sum += Amounts.parse(selector(e), DROPLET_EXPONENT) ?: 0uL
        return sum
    }

    override suspend fun nodeInfo(): NodeInfo {
        val h = node.health()
        val name = h.fiber.displayName.ifEmpty { h.fiber.name.ifEmpty { h.coin } }
        return NodeInfo(
            coinName = name.takeIf { it.isNotEmpty() }?.replaceFirstChar { it.uppercase() },
            ticker = h.fiber.ticker.ifEmpty { h.fiber.name.uppercase() }.takeIf { it.isNotEmpty() },
            version = h.version.version.takeIf { it.isNotEmpty() },
            height = h.blockchain.head.seq.toLong(),
            burnFactor = h.userVerifyTxn.burnFactor.toInt(),
            pubkey = h.blockchainPubkey.takeIf { it.isNotEmpty() },
            explorerUrl = h.fiber.explorerUrl.trimEnd('/').takeIf { it.isNotEmpty() },
            // skycoin-web refuses nodes older than 0.24.0; the API this wallet calls is from there on.
            problem = if (versionBelow(h.version.version, listOf(0, 24, 0))) NodeProblem.TOO_OLD else null,
        )
    }

    override suspend fun feePresets(): FeePresets? = null

    override fun estimateMax(balance: WalletBalance, feeRate: Int?): ULong = balance.confirmed

    override suspend fun buildTx(
        seed: KeySource,
        book: AddressBook,
        toAddress: String,
        amount: ULong,
        feeRate: Int?,
        sendMax: Boolean,
        fromAddress: String?,
        inputs: Set<String>?,
        hoursTo: ULong?,
        more: List<Recipient>,
        changeAddress: String?,
        shareFactor: BigDecimal?,
        @Suppress("UNUSED_PARAMETER") feeLevel: FeeLevel?,
    ): TxPlan {
        val allTo = listOf(toAddress) + more.map { it.address } + listOfNotNull(changeAddress)
        if (allTo.any { !validateAddress(it) }) {
            throw WalletException.InvalidAddress()
        }
        if (sendMax && more.isNotEmpty()) throw WalletException.InvalidAmount("Max sends to one destination")
        if (more.any { (it.hours == null) != (hoursTo == null) }) {
            throw WalletException.InvalidAmount("set the hours for every destination, or for none")
        }

        val health = node.health()
        val params = health.userVerifyTxn

        val outputs = node.outputs(book.all())
        val outgoing = outputs.outgoingOutputs.map { it.hash }.toHashSet()
        val spendable = outputs.headOutputs
            // A chosen from-address spends only what it holds, and picked
            // outputs crowd out the automatic choice; the rest never enter
            // the selection.
            .filter {
                it.hash !in outgoing &&
                    (fromAddress == null || it.address == fromAddress) &&
                    (inputs == null || it.hash in inputs)
            }
            .map {
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
        val destinations = listOf(SkycoinCreate.Destination(toAddress, sendAmount, hoursTo)) +
            more.map { SkycoinCreate.Destination(it.address, it.amount, it.hours) }
        if (destinations.any { it.coins == 0uL }) throw WalletException.InvalidAmount("enter an amount to send")

        // The chain caps decimal precision; sub-precision amounts are rejected
        // by every node, so fail here with the limit spelled out.
        var divisor = 1uL
        repeat(DROPLET_EXPONENT - params.maxDecimals) { divisor *= 10uL }
        if (destinations.any { it.coins % divisor != 0uL }) {
            throw WalletException.InvalidAmount("amounts on this chain carry at most ${params.maxDecimals} decimals")
        }

        // A BIP 44 wallet's change goes to a fresh address of its change chain, as
        // skycoin's own BIP 44 wallet does; the classic wallet's goes back where it came from.
        val change = changeAddress ?: when (seed.derivation) {
            Derivation.BIP44 -> deriveAddresses(seed, 0, book.change.size + 1).change.last()
            Derivation.STANDARD -> fromAddress ?: book.receive.first()
        }
        val created = try {
            SkycoinCreate.create(
                unspents = spendable,
                to = destinations,
                changeAddress = change,
                burnFactor = params.burnFactor,
                shareFactor = shareFactor ?: SkycoinCreate.DEFAULT_SHARE,
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

        var total = 0uL
        for (d in destinations) total += d.coins
        return TxPlan(
            toAddress = toAddress,
            amount = total,
            fee = created.feeHours,
            changeAmount = created.changeCoins,
            hoursToRecipient = created.hoursToDestinations,
            hoursChange = created.changeHours,
            vsize = null,
            feeRate = null,
            payload = Payload(created.txn, inputAddresses, book),
            // The destinations are the transaction's first outputs, in order.
            recipients = destinations.mapIndexed { i, d ->
                Recipient(d.address, d.coins, created.txn.outputs[i].hours)
            },
            changeAddress = change,
        )
    }

    override fun signTx(seed: KeySource, plan: TxPlan): SignedTx {
        val payload = plan.payload as Payload
        val byAddress = keysByAddress(seed, payload.book)
        val inputKeys = payload.inputAddresses.map { addr ->
            byAddress[addr] ?: error("input address $addr is not part of this wallet")
        }
        payload.txn.signInputs(inputKeys)
        return SignedTx(rawHex = payload.txn.serializeHex(), txid = payload.txn.txidHex())
    }

    private fun keysByAddress(seed: KeySource, book: AddressBook): Map<String, ByteArray> {
        if (seed.derivation == Derivation.BIP44) {
            val account = SkycoinBip44.accountKey(seed.mnemonic, seed.passphrase)
            fun chain(c: Int, n: Int) = List(n) { SkycoinBip44.key(account, c, it) }
            return (chain(0, book.receive.size) + chain(1, book.change.size))
                .associate { SkycoinCrypto.addressFromPubKey(it.pubKey()) to it.key }
        }
        return SkycoinCrypto.generateKeyPairs(seed.mnemonic.toByteArray(Charsets.UTF_8), book.receive.size)
            .associate { SkycoinCrypto.addressFromPubKey(it.public) to it.secret }
    }

    override suspend fun broadcast(tx: SignedTx): String = node.inject(tx.rawHex)

    override fun unsignedHex(plan: TxPlan): String = (plan.payload as Payload).txn.unsignedHex()

    override suspend fun signRaw(seed: KeySource, book: AddressBook, rawHex: String): SignedTx {
        val txn = decodeRaw(rawHex)
        // The owner of each input is the address its unspent output sits at.
        val owners = node.outputs(book.all()).headOutputs.associate { it.hash to it.address }
        val byAddress = keysByAddress(seed, book)
        val keys = txn.inputs.map { uxid ->
            owners[uxid.toHex()]?.let(byAddress::get) ?: throw WalletException.NotInWallet()
        }
        txn.signInputs(keys)
        return SignedTx(rawHex = txn.serializeHex(), txid = txn.txidHex())
    }

    override suspend fun broadcastRaw(rawHex: String): String {
        if (!decodeRaw(rawHex).isSigned()) throw WalletException.BadTransaction("the transaction is not signed")
        return node.inject(rawHex.trim())
    }

    private fun decodeRaw(rawHex: String): SkycoinTxn = try {
        SkycoinTxn.decode(rawHex)
    } catch (e: IllegalArgumentException) {
        throw WalletException.BadTransaction(e.message ?: "not a transaction")
    }

    companion object {
        /** True when [version] parses and is below [floor]; an unknown version is not held against a node. */
        internal fun versionBelow(version: String, floor: List<Int>): Boolean {
            val parts = version.trim().removePrefix("v").substringBefore('-').split('.').map { it.toIntOrNull() ?: return false }
            for (i in floor.indices) {
                val v = parts.getOrElse(i) { 0 }
                if (v != floor[i]) return v < floor[i]
            }
            return false
        }

        const val DROPLET_EXPONENT = 6
        private const val SCAN_AHEAD = 10
        private const val SCAN_CAP = 100
    }
}
