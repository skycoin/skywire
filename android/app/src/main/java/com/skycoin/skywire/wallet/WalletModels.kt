package com.skycoin.skywire.wallet

import com.skycoin.wallet.AddressBook
import com.skycoin.wallet.TxRecord
import com.skycoin.wallet.WalletBalance
import kotlinx.serialization.Serializable

/** Which family a coin belongs to — the protocols this wallet speaks. */
enum class CoinKind { SKY_FIBER, BTC, ETH, ERC20 }

/**
 * A coin the wallet can hold. SKY, BTC, ETH and USDT ship built in; fiber
 * coins and ERC-20 tokens are added by the user — every fiber chain runs the
 * same daemon and differs only in where it lives, and every ERC-20 speaks
 * the same contract surface and differs only in address and decimals.
 */
@Serializable
data class CoinSpec(
    val id: String,
    val name: String,
    val ticker: String,
    val kind: CoinKind,
    val nodeUrl: String,
    /** %s is the txid; null hides the explorer button. */
    val explorerTxUrl: String? = null,
    val builtIn: Boolean = false,
    /** ERC-20 only: the token's contract address. */
    val contract: String? = null,
    /** ERC-20 only: the token's on-chain decimals. */
    val tokenDecimals: Int? = null,
    /** ETH family: etherscan-style history API base (Blockscout, keyless). */
    val indexerUrl: String? = null,
    /** User-added coins: the badge symbol picked at creation — a key into
     *  the wallet UI's symbol set. Built-ins carry bundled logos instead;
     *  null falls back to ticker letters. */
    val icon: String? = null,
) {
    /**
     * Base-unit exponent: droplets 10⁻⁶, satoshis 10⁻⁸ — and for the ETH
     * family, whatever fits the app's 64-bit amounts: gwei (10⁻⁹) for the
     * native coin because wei overflows 64 bits at ~18 ETH, and a token's
     * own decimals capped at nine for the same reason.
     */
    val exponent: Int get() = when (kind) {
        CoinKind.BTC -> 8
        CoinKind.ETH -> 9
        CoinKind.ERC20 -> minOf(tokenDecimals ?: DEFAULT_TOKEN_DECIMALS, 9)
        CoinKind.SKY_FIBER -> 6
    }

    /**
     * True for the native coin and every ERC-20 — one account on one chain,
     * one address, differing only in which asset is being looked at. A wallet
     * for any of them is a wallet for all of them, which is why creating one
     * mirrors across the rest (WalletRepository.mirrorIntoEthFamily).
     *
     * Fiber coins are deliberately not a family: they derive alike but each
     * is a separate chain with its own ledger, so sharing a wallet between
     * two of them would claim a balance that is not there.
     */
    val isEthFamily: Boolean get() = kind == CoinKind.ETH || kind == CoinKind.ERC20

    /** Decimals shown in balances and amount fields. */
    val displayDecimals: Int get() = when (kind) {
        CoinKind.BTC -> 8
        CoinKind.ETH -> 6
        CoinKind.ERC20 -> minOf(exponent, 6)
        CoinKind.SKY_FIBER -> 3
    }

    companion object {
        val SKY = CoinSpec(
            id = "SKY",
            name = "Skycoin",
            ticker = "SKY",
            kind = CoinKind.SKY_FIBER,
            // https, like every other endpoint here including Skycoin's own
            // explorer on the next line. Over plain http the query string of
            // every balance and history call carries the whole address book
            // in the clear, which hands anyone on the path the one thing a
            // wallet most wants kept apart: which addresses belong together.
            // Funds are not at risk either way — signing is local and a
            // tampered transaction fails verification — but the linkage is,
            // and it cannot be taken back once seen. The host serves TLS.
            nodeUrl = "https://node.skycoin.com",
            explorerTxUrl = "https://explorer.skycoin.com/app/transaction/%s",
            builtIn = true,
        )
        val BTC = CoinSpec(
            id = "BTC",
            name = "Bitcoin",
            ticker = "BTC",
            kind = CoinKind.BTC,
            nodeUrl = "https://mempool.space",
            explorerTxUrl = "https://mempool.space/tx/%s",
            builtIn = true,
        )
        val ETH = CoinSpec(
            id = "ETH",
            name = "Ethereum",
            ticker = "ETH",
            kind = CoinKind.ETH,
            nodeUrl = ETH_NODE,
            explorerTxUrl = "$ETH_INDEXER/tx/%s",
            builtIn = true,
            indexerUrl = ETH_INDEXER,
        )
        val USDT = CoinSpec(
            id = "USDT",
            name = "Tether USD",
            ticker = "USDT",
            kind = CoinKind.ERC20,
            nodeUrl = ETH_NODE,
            explorerTxUrl = "$ETH_INDEXER/tx/%s",
            builtIn = true,
            contract = "0xdAC17F958D2ee523a2206206994597C13D831ec7",
            tokenDecimals = 6,
            indexerUrl = ETH_INDEXER,
        )

        /** Keyless public endpoints; both are user-replaceable per token. */
        const val ETH_NODE = "https://ethereum-rpc.publicnode.com"
        const val ETH_INDEXER = "https://eth.blockscout.com"

        const val DEFAULT_TOKEN_DECIMALS = 18
    }
}

/** A wallet: one seed, one coin, its derived addresses. Addresses are public
 *  and cached here so opening the app never needs the sealed seed. */
@Serializable
data class WalletMeta(
    val id: String,
    val coinId: String,
    val name: String,
    val createdAtMs: Long,
    val receiveAddresses: List<String>,
    val changeAddresses: List<String> = emptyList(),
    /**
     * When this wallet's addresses were last discovered from the chain, or 0
     * while that has never succeeded.
     *
     * A restore asks the node which of the seed's addresses have been used;
     * a fresh phrase has nothing to ask about and is scanned by definition.
     * When the question cannot be put — the node is slow, the link is bad —
     * the wallet is still created, holding only the first address, and the
     * coins on the rest are invisible and unspendable. That used to be the
     * end of it: nothing recorded that the answer was missing and nothing
     * ever asked again, so a restore on a bad connection quietly produced a
     * wallet that was wrong forever.
     *
     * 0 means the question is still open. Refresh asks it again until it is
     * answered, and the screen says so meanwhile. Wallets written before
     * this field existed default to 0 and are re-asked once, which is what
     * repairs any that were truncated.
     */
    val addressScanAtMs: Long = 0,
)

/** True while this wallet's address list has never been confirmed against the chain. */
val WalletMeta.addressScanPending: Boolean get() = addressScanAtMs <= 0

/**
 * Fold a completed address scan into a wallet, and mark the question closed.
 *
 * The lists only ever grow. A scan reports how many addresses the CHAIN has
 * seen; that is not how many the wallet HOLDS, because a user can ask for
 * further ones locally and may already have handed one out. Taking an
 * address away because nobody has paid it yet would be the same class of
 * mistake as never finding it: coins arriving somewhere the wallet no longer
 * watches.
 */
fun settledAddresses(meta: WalletMeta, scanned: AddressBook, nowMs: Long): WalletMeta = meta.copy(
    receiveAddresses = scanned.receive.takeIf { it.size >= meta.receiveAddresses.size }
        ?: meta.receiveAddresses,
    changeAddresses = scanned.change.takeIf { it.size >= meta.changeAddresses.size }
        ?: meta.changeAddresses,
    addressScanAtMs = nowMs,
)

/** One remembered transaction — TxRecord flattened for the cache file. */
@Serializable
data class CachedTx(
    val txid: String,
    val incoming: Boolean,
    val amount: ULong,
    val party: String? = null,
    val timestamp: Long,
    val confirmed: Boolean,
    val confirmations: Long,
    val fee: ULong? = null,
)

/**
 * The last successful view of a wallet, kept on disk so the tab renders
 * instantly and honestly when the node is unreachable — the UI marks it
 * stale rather than blank.
 */
@Serializable
data class WalletSnapshot(
    val confirmed: ULong = 0u,
    val predicted: ULong = 0u,
    val hours: ULong? = null,
    val spendableOutputs: Int = 0,
    val txs: List<CachedTx> = emptyList(),
    val fetchedAtMs: Long = 0,
    /**
     * When [txs] was last actually fetched, which can lag [fetchedAtMs]: the
     * balance and the history are fetched separately and the history is the
     * one that can be too big to arrive (see WalletRepository.refresh). 0 on
     * a snapshot written before this field existed, and on one whose history
     * has never landed — both mean "do not claim this list is complete".
     */
    val historyFetchedAtMs: Long = 0,
)

/** True when the tx list is older than the balance beside it, or never arrived. */
val WalletSnapshot.historyBehind: Boolean get() = historyFetchedAtMs < fetchedAtMs

/**
 * Fold one refresh's results into the snapshot that gets cached.
 *
 * [history] is null when that fetch failed, which is a normal outcome rather
 * than an error: the balance is a few hundred bytes and the transaction list
 * is unbounded, so on a slow link the second can miss while the first lands.
 * When it misses, the last list we did get is carried forward unchanged and
 * its timestamp with it — so the snapshot goes on saying, truthfully, how old
 * that list is, and never passes an empty one off as a fetched one.
 *
 * Pure, and separate from the fetching, because this rule is the whole point
 * of splitting the two halves and is worth being able to state on its own.
 */
fun mergeSnapshot(
    balance: WalletBalance,
    history: List<TxRecord>?,
    previous: WalletSnapshot?,
    nowMs: Long,
): WalletSnapshot = WalletSnapshot(
    confirmed = balance.confirmed,
    predicted = balance.predicted,
    hours = balance.hours,
    spendableOutputs = balance.spendableOutputs,
    txs = history?.map {
        CachedTx(
            txid = it.txid,
            incoming = it.incoming,
            amount = it.amount,
            party = it.party,
            timestamp = it.timestamp,
            confirmed = it.confirmed,
            confirmations = it.confirmations,
            fee = it.fee,
        )
    } ?: previous?.txs.orEmpty(),
    fetchedAtMs = nowMs,
    historyFetchedAtMs = if (history != null) nowMs else previous?.historyFetchedAtMs ?: 0,
)
