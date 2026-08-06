package com.skycoin.skywire.wallet

import kotlinx.serialization.Serializable

/** Which family a coin belongs to — the two protocols this wallet speaks. */
enum class CoinKind { SKY_FIBER, BTC }

/**
 * A coin the wallet can hold. SKY and BTC ship built in; fiber coins are
 * added by the user with a name, a ticker and their node's address — every
 * fiber chain runs the same daemon and differs only in where it lives.
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
) {
    /** Base-unit exponent: droplets are 10⁻⁶, satoshis 10⁻⁸. */
    val exponent: Int get() = if (kind == CoinKind.BTC) 8 else 6

    /** Decimals shown in balances and amount fields. */
    val displayDecimals: Int get() = if (kind == CoinKind.BTC) 8 else 3

    companion object {
        val SKY = CoinSpec(
            id = "SKY",
            name = "Skycoin",
            ticker = "SKY",
            kind = CoinKind.SKY_FIBER,
            nodeUrl = "http://node.skycoin.com",
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
)
