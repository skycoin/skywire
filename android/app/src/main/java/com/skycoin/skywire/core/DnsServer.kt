package com.skycoin.skywire.core

/**
 * The resolver SkyVPN and SkyDNS ask for every name that is not .dmsg or
 * .skynet. Both apps take it as `--dns`. With none, SkyVPN uses [DEFAULT],
 * since a VPN server has no resolver to share, and SkyDNS on its own uses the
 * phone network's resolvers, which [SkyVpnService] reports to it.
 *
 * A phone preference, pinned into both apps' argv on every launch by
 * [ConfigManager], and rewritten in a running core by Settings.
 */
object DnsServer {

    /** The stored address; blank or absent means each app's own default. */
    const val PREF_KEY = "dns_server"

    /** SkyVPN's with no `--dns`: Cloudflare's, which answers DNS-over-TLS. pkg/vpn's shareDefaultDNS. */
    const val DEFAULT = "1.1.1.1"

    private const val FLAG = "--dns"
    private val FLAGS = listOf(FLAG, "-dns")

    /** IPv4 only: the tunnel and the hotspot proxy carry IPv4 (pkg/vpn/share.go). */
    fun isValid(address: String): Boolean {
        val parts = address.trim().split('.')
        return parts.size == 4 && parts.all { part ->
            part.length in 1..3 && part.all(Char::isDigit) && part.toInt() <= 255
        }
    }

    /** What a stored value means for the apps: a valid address, or blank for the defaults. */
    fun sanitize(stored: String?): String = stored?.trim()?.takeIf(::isValid).orEmpty()

    /** [args] with `--dns` set to [server], or without it when [server] is blank. */
    fun args(args: List<String>, server: String): List<String> {
        val rest = mutableListOf<String>()
        var i = 0
        while (i < args.size) {
            val token = args[i]
            when {
                FLAGS.any { token.startsWith("$it=") } -> Unit
                token in FLAGS -> i++ // and its value
                else -> rest += token
            }
            i++
        }
        return if (server.isBlank()) rest else rest + listOf(FLAG, server.trim())
    }

    /** The `--dns` value in [args], or null when there is none. */
    fun current(args: List<String>): String? = argValue(args, FLAGS)
}
