package com.skycoin.wallet

import java.math.BigDecimal

/**
 * The one seam the app sees. Two implementations: Skycoin/fiber nodes
 * (parameterized by node URL — every fiber chain speaks the same API) and
 * Bitcoin over an esplora-compatible server.
 *
 * Amounts are integers in the chain's base unit: droplets (10⁻⁶) for
 * Skycoin-family coins, satoshis (10⁻⁸) for Bitcoin. Fees mean different
 * things per chain — burned coin hours vs satoshis — and the plan carries
 * whichever applies.
 */
interface WalletCore {

    /** A fresh 12-word BIP 39 phrase. */
    fun newSeed(): String

    /** Wordlist + checksum validation of a phrase being restored. */
    fun validateSeed(mnemonic: String): Boolean

    /**
     * A phrase restored although its checksum fails, after the user confirmed
     * it. The older Skycoin wallets wrote 24 list words with no checksum.
     */
    fun acceptsLegacySeed(mnemonic: String): Boolean = false

    /**
     * Whether any text, taken exactly as written, can be a seed. The classic
     * fiber wallet hashes the seed's bytes, so skycoin-web lets a user restore
     * one from text that is not a BIP 39 phrase at all, after a warning.
     */
    fun takesAnySeed(): Boolean = false

    /**
     * Deterministic addresses for this seed. Skycoin-family wallets have a
     * single chain (change returns to the first address, change list stays
     * empty); Bitcoin derives a separate change chain the UI never shows.
     */
    fun deriveAddresses(seed: KeySource, receiveCount: Int, changeCount: Int): AddressBook

    fun validateAddress(address: String): Boolean

    /** On restore: probe the network for used addresses. (receive, change) counts, each ≥ minimum. */
    suspend fun scanUsed(seed: KeySource): Pair<Int, Int>

    suspend fun balance(book: AddressBook): WalletBalance

    /** The wallet's unspent outputs; empty where a chain has no coin control. */
    suspend fun utxos(book: AddressBook): List<UtxoRecord> = emptyList()

    suspend fun history(book: AddressBook): List<TxRecord>

    /** Bitcoin fee-rate presets in sat/vB; null on chains that burn hours instead. */
    suspend fun feePresets(): FeePresets?

    /** Largest sendable amount given the current balance (fee-adjusted on Bitcoin). */
    fun estimateMax(balance: WalletBalance, feeRate: Int?): ULong

    /**
     * Build and fully price an unsigned transaction. Throws
     * WalletException subtypes on validation problems, IOException when the
     * node cannot be reached.
     *
     * @param fromAddress spends only outputs held by this one address; null
     * spends from the whole wallet. Ignored where a wallet is one address.
     * @param inputs hashes of the unspent outputs to spend; null chooses
     * them automatically. Fiber chains only.
     * @param hoursTo hours the destination receives; null splits them
     * automatically. Fiber chains only.
     * @param more destinations after the first, in output order. Fiber
     * chains only; hours are given for every destination or for none.
     * @param changeAddress where change goes; null keeps the chain's own
     * rule. Fiber chains only.
     * @param shareFactor the share of post-burn hours automatic hours send
     * onward, 0 to 1; null is the default half. Fiber chains only.
     * @param feeLevel how much tip to offer; null is normal. Ethereum family only.
     */
    suspend fun buildTx(
        seed: KeySource,
        book: AddressBook,
        toAddress: String,
        amount: ULong,
        feeRate: Int?,
        sendMax: Boolean,
        fromAddress: String? = null,
        inputs: Set<String>? = null,
        hoursTo: ULong? = null,
        more: List<Recipient> = emptyList(),
        changeAddress: String? = null,
        shareFactor: BigDecimal? = null,
        feeLevel: FeeLevel? = null,
    ): TxPlan

    /** Local signing — the only step that touches keys, and it never suspends. */
    fun signTx(seed: KeySource, plan: TxPlan): SignedTx

    /** Hand the signed transaction to the node; returns the txid it reports. */
    suspend fun broadcast(tx: SignedTx): String

    /** The wallet's extended public keys, for a watch-only view; null where a wallet has none. */
    fun extendedPublicKeys(seed: KeySource): ExtendedPublicKeys? = null

    /** The plan's transaction with no signatures, to be signed elsewhere. Null where a chain has no such form here. */
    fun unsignedHex(plan: TxPlan): String? = null

    /**
     * Signs a raw transaction made elsewhere. Every input must be an unspent
     * output of this wallet. Fiber chains only.
     */
    suspend fun signRaw(seed: KeySource, book: AddressBook, rawHex: String): SignedTx =
        throw WalletException.BadTransaction("this chain does not sign raw transactions here")

    /** Broadcasts a raw signed transaction; returns the txid the node reports. Fiber chains only. */
    suspend fun broadcastRaw(rawHex: String): String =
        throw WalletException.BadTransaction("this chain does not broadcast raw transactions here")
}

/** A BIP 44 account's xpub and its receive and change chain xpubs. */
class ExtendedPublicKeys(val account: String, val receive: String, val change: String)

/** One destination of a send. [hours] is fiber only; null splits hours automatically. */
class Recipient(val address: String, val amount: ULong, val hours: ULong? = null)

/**
 * What a wallet's keys come from: its phrase, the BIP 39 passphrase that
 * goes with it, and the way keys are derived from the two.
 */
class KeySource(
    val mnemonic: String,
    val passphrase: String = "",
    val derivation: Derivation = Derivation.STANDARD,
)

/**
 * How keys come from a phrase. STANDARD is each chain's usual way:
 * Skycoin's deterministic chain over the phrase bytes for fiber coins,
 * BIP 84 for Bitcoin, BIP 44 for Ethereum. BIP44 is the alternative a
 * chain offers: BIP 44 coin 8000 for fiber coins, legacy BIP 44 for Bitcoin.
 */
enum class Derivation { STANDARD, BIP44 }

class AddressBook(val receive: List<String>, val change: List<String>) {
    fun all(): List<String> = receive + change
}

/** One unspent output, as a coin picker shows it. Fiber chains only. */
class UtxoRecord(
    val uxid: String,
    val address: String,
    val coins: ULong,
    /** Calculated hours at the chain head. */
    val hours: ULong,
)

class WalletBalance(
    /** Spendable now (confirmed, minus outputs already spent by pending txns). */
    val confirmed: ULong,
    /** Balance once pending transactions settle. */
    val predicted: ULong,
    /** Calculated coin hours held — null on Bitcoin. */
    val hours: ULong?,
    /** Confirmed spendable outputs backing the balance. */
    val spendableOutputs: Int,
    /** Confirmed coins held by each single address — the addresses sheet's
     *  numbers. Empty when a chain reports only the total. */
    val perAddress: Map<String, ULong> = emptyMap(),
)

class TxRecord(
    val txid: String,
    val incoming: Boolean,
    /** Net effect on this wallet, in base units, always ≥ 0. */
    val amount: ULong,
    /** Counterparty address when one exists (self-sends have none). */
    val party: String?,
    /** Unix seconds; block time when confirmed, first-seen time while pending. */
    val timestamp: Long,
    val confirmed: Boolean,
    val confirmations: Long,
    /** Burned hours (Skycoin family) or satoshis (Bitcoin); null when unknown. */
    val fee: ULong?,
    /** This wallet's addresses the coins left from (a send) or arrived at (a receive). */
    val own: List<String> = emptyList(),
)

class FeePresets(val economy: Int, val normal: Int, val priority: Int)

/** An Ethereum fee choice: the least tip, the node's suggested tip, or twice it. */
enum class FeeLevel { SLOW, NORMAL, FAST }

class TxPlan(
    /** The first destination. */
    val toAddress: String,
    /** Coins leaving for every destination together. */
    val amount: ULong,
    /** Burned hours on Skycoin-family chains, satoshis on Bitcoin. */
    val fee: ULong,
    val changeAmount: ULong,
    /** Hours arriving at the destination — Skycoin family only. */
    val hoursToRecipient: ULong?,
    /** Hours coming back with change — Skycoin family only. */
    val hoursChange: ULong?,
    /** Virtual size in vbytes — Bitcoin only. */
    val vsize: Int?,
    val feeRate: Int?,
    /** Implementation payload consumed by signTx. */
    internal val payload: Any,
    /** Every destination with the hours it receives, in output order. */
    val recipients: List<Recipient> = listOf(Recipient(toAddress, amount, hoursToRecipient)),
    /** Where the change goes, when the chain lets a send say. */
    val changeAddress: String? = null,
)

class SignedTx(val rawHex: String, val txid: String, internal val payload: Any? = null)

/** User-correctable problems building a transaction. */
sealed class WalletException(message: String) : Exception(message) {
    class InvalidAddress : WalletException("invalid destination address")
    class InvalidAmount(message: String) : WalletException(message)
    class InsufficientBalance : WalletException("balance is not sufficient")
    class InsufficientHours : WalletException("coin hours are not sufficient")
    class NoHoursToBurn : WalletException("outputs hold no coin hours yet — hours accrue over time")
    class DustChange : WalletException("amount leaves change too small to spend")
    class NodeRejected(message: String) : WalletException(message)

    /** Token sends: the token balance is there, the gas money is not. */
    class InsufficientGas(message: String) : WalletException(message)

    /** A raw transaction that does not decode, or is not in the state the step needs. */
    class BadTransaction(message: String) : WalletException(message)

    /** A raw transaction spends outputs this wallet holds no keys for. */
    class NotInWallet : WalletException("this wallet does not hold the keys of every input")
}
