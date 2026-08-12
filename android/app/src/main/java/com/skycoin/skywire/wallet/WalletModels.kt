package com.skycoin.skywire.wallet

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
