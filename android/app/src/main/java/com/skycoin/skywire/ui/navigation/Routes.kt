package com.skycoin.skywire.ui.navigation

/**
 * Navigation routes. Five top-level bottom-bar destinations; SkySOCKS /
 * SkyVPN / SkyDEX / Fleet are full-screen routes pushed from the hub —
 * back returns to the hub.
 */
object Routes {
    const val HOME = "home"
    const val CHAT = "chat"
    const val HUB = "hub"
    const val WALLET = "wallet"
    const val SETTINGS = "settings"

    const val SOCKS = "socks"
    const val VPN = "vpn"
    const val DEX = "dex"
    const val FLEET = "fleet"

    /** Logs & diagnostics — pushed from Settings. */
    const val DIAGNOSTICS = "diagnostics"

    // Wallet flow — pushed from the Wallet tab root; the bar keeps the
    // Wallet slot highlighted throughout.
    const val WALLET_CREATE = "wallet/create"
    const val WALLET_VERIFY = "wallet/verify"
    const val WALLET_RESTORE = "wallet/restore"
    const val WALLET_RECEIVE = "wallet/receive"
    const val WALLET_SEND = "wallet/send"
    const val WALLET_RESULT = "wallet/result"
    const val WALLET_HISTORY = "wallet/history"
    const val WALLET_TX = "wallet/tx/{txid}"
    const val WALLET_WALLETS = "wallet/wallets"
    const val WALLET_REVEAL = "wallet/reveal/{walletId}"
    const val WALLET_ADD_COIN = "wallet/addcoin"

    fun walletTx(txid: String) = "wallet/tx/$txid"
    fun walletReveal(walletId: String) = "wallet/reveal/$walletId"

    /**
     * Shared log viewer; {source} is core, process, `app-<name>`, or
     * `visor-<pk>` for a remote visor's feed (Fleet).
     */
    const val LOGS = "logs/{source}"

    fun logs(source: String) = "logs/$source"

    /** Routes pushed from the hub; the bar keeps the hub slot highlighted. */
    val hubPushed = setOf(SOCKS, VPN, DEX, FLEET)

    /** Same, for the Settings tab. */
    val settingsPushed = setOf(DIAGNOSTICS)

    /** Same, for the Wallet tab. */
    val walletPushed = setOf(
        WALLET_CREATE, WALLET_VERIFY, WALLET_RESTORE, WALLET_RECEIVE,
        WALLET_SEND, WALLET_RESULT, WALLET_HISTORY, WALLET_TX,
        WALLET_WALLETS, WALLET_REVEAL, WALLET_ADD_COIN,
    )
}
