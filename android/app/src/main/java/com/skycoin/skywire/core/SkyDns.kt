package com.skycoin.skywire.core

/**
 * SkyDNS opens .dmsg and .skynet names in every app (pkg/skydns). It rides SkyVPN's
 * tunnel when SkyVPN is on, and asks [SkyVpnService] for a tunnel of its own when not.
 */
object SkyDns {

    /** The in-process app that runs SkyDNS without SkyVPN. */
    const val APP = "skydns"

    /** pkg/skyenv's SkyDNSPort. */
    const val APP_PORT = 42

    /** The Apps hub switch: SkyDNS on its own, while SkyVPN is off. */
    const val PREF_STANDALONE = "skydns_standalone"
    const val DEFAULT_STANDALONE = false

    /** The SkyVPN screen switch: SkyDNS inside SkyVPN's tunnel. */
    const val PREF_IN_VPN = "vpn_skydns"
    const val DEFAULT_IN_VPN = true

    /** vpn-client's flag for SkyDNS inside its tunnel. */
    const val VPN_FLAG = "--mesh-gateway"

    /** vpn-client's argv with SkyDNS turned [on] or off, everything else kept. */
    fun vpnArgs(args: List<String>, on: Boolean): List<String> {
        val rest = args.filter { it != VPN_FLAG && !it.startsWith("$VPN_FLAG=") }
        return if (on) rest + VPN_FLAG else rest
    }

    /** Whether vpn-client's argv turns SkyDNS on. */
    fun inVpnArgs(args: List<String>): Boolean =
        args.any { it == VPN_FLAG || it == "$VPN_FLAG=true" }
}
