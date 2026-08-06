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

    /**
     * Shared log viewer; {source} is core, process, `app-<name>`, or
     * `visor-<pk>` for a remote visor's feed (Fleet).
     */
    const val LOGS = "logs/{source}"

    fun logs(source: String) = "logs/$source"

    /** Routes pushed from the hub; the bar keeps the hub slot highlighted. */
    val hubPushed = setOf(SOCKS, VPN, DEX, FLEET)
}
