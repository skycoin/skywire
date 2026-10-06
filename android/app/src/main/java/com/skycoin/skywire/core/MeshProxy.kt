package com.skycoin.skywire.core

/**
 * The HTTP proxy SkyVPN gives proxy-aware apps, used for .dmsg and .skynet
 * names only. Every other host stays on tun0 and leaves through the VPN exit.
 */
object MeshProxy {

    const val HOST = "127.0.0.1"

    /** dmsgweb, which also answers HTTP proxy requests and chains .skynet to 4446. */
    const val PORT = 4445

    /** The config sections that turn the two resolving proxies on. */
    val CONFIG_KEYS = listOf("dmsg_web", "skynet_web")

    val SUFFIXES = listOf(".dmsg", ".skynet")

    private const val HOST_CHARS = "abcdefghijklmnopqrstuvwxyz0123456789-."

    /**
     * Every host not ending in [SUFFIXES]. Android has no include list and no PAC
     * over a VPN (VpnService.Builder.setHttpProxy docs), so this is the complement.
     */
    val exclusions: List<String> = complementOf(SUFFIXES)

    internal fun complementOf(suffixes: List<String>): List<String> {
        val out = sortedSetOf<String>()
        fun walk(tail: String) {
            val next = suffixes
                .filter { it.length > tail.length && it.endsWith(tail) }
                .map { it[it.length - tail.length - 1] }
                .toSet()
            for (c in HOST_CHARS) {
                // No host ends in '-' or '.', and Android rejects such a pattern.
                if (c in next || (tail.isEmpty() && (c == '-' || c == '.'))) continue
                out += "*$c$tail"
            }
            for (c in next) {
                if (c != '.') walk(c + tail)
            }
            if (tail.isNotEmpty()) out += tail
        }
        walk("")
        return out.toList()
    }
}
