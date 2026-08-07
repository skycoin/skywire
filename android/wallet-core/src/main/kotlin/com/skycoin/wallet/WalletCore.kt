package com.skycoin.wallet

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
     * Deterministic addresses for this seed. Skycoin-family wallets have a
     * single chain (change returns to the first address, change list stays
     * empty); Bitcoin derives a separate change chain the UI never shows.
     */
    fun deriveAddresses(seed: String, receiveCount: Int, changeCount: Int): AddressBook

    fun validateAddress(address: String): Boolean

    /** On restore: probe the network for used addresses. (receive, change) counts, each ≥ minimum. */
    suspend fun scanUsed(seed: String): Pair<Int, Int>

    suspend fun balance(book: AddressBook): WalletBalance

    suspend fun history(book: AddressBook): List<TxRecord>

    /** Bitcoin fee-rate presets in sat/vB; null on chains that burn hours instead. */
    suspend fun feePresets(): FeePresets?

    /** Largest sendable amount given the current balance (fee-adjusted on Bitcoin). */
    fun estimateMax(balance: WalletBalance, feeRate: Int?): ULong

    /**
     * Build and fully price an unsigned transaction. Throws
     * WalletException subtypes on validation problems, IOException when the
     * node cannot be reached.
     */
    suspend fun buildTx(
        seed: String,
        book: AddressBook,
        toAddress: String,
        amount: ULong,
        feeRate: Int?,
        sendMax: Boolean,
    ): TxPlan

    /** Local signing — the only step that touches keys, and it never suspends. */
    fun signTx(seed: String, plan: TxPlan): SignedTx

    /** Hand the signed transaction to the node; returns the txid it reports. */
    suspend fun broadcast(tx: SignedTx): String
}

class AddressBook(val receive: List<String>, val change: List<String>) {
    fun all(): List<String> = receive + change
}

class WalletBalance(
    /** Spendable now (confirmed, minus outputs already spent by pending txns). */
    val confirmed: ULong,
    /** Balance once pending transactions settle. */
    val predicted: ULong,
    /** Calculated coin hours held — null on Bitcoin. */
    val hours: ULong?,
    /** Confirmed spendable outputs backing the balance. */
    val spendableOutputs: Int,
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
)

class FeePresets(val economy: Int, val normal: Int, val priority: Int)

class TxPlan(
    val toAddress: String,
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
}
