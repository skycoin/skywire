package com.skycoin.skywire.wallet

import com.skycoin.skywire.core.DmsgWeb
import java.io.IOException
import java.net.InetSocketAddress
import java.net.Proxy
import java.net.ProxySelector
import java.net.SocketAddress
import java.net.URI

/**
 * Wallet requests to Skywire mesh names, as skycoin-web's --socks5-proxy does
 * on the desktop: a host ending in .dmsg goes through the visor's resolver on
 * loopback, which dials it over dmsg; every other host goes direct. The
 * resolver runs only while Skywire is connected.
 */
object WalletMesh {

    fun isMesh(host: String): Boolean = host.endsWith(".dmsg", ignoreCase = true)

    val proxySelector: ProxySelector = object : ProxySelector() {
        // The target host stays unresolved inside the SOCKS request, so the resolver is the one to read it.
        private val resolver = listOf(Proxy(Proxy.Type.SOCKS, InetSocketAddress("127.0.0.1", DmsgWeb.PORT)))

        override fun select(uri: URI?): List<Proxy> =
            if (uri?.host?.let(::isMesh) == true) resolver else listOf(Proxy.NO_PROXY)

        override fun connectFailed(uri: URI?, sa: SocketAddress?, ioe: IOException?) = Unit
    }

    private val hexHost = Regex("^(https?://)([0-9a-fA-F]{66})(\\.dmsg)(?=[:/?#]|$)", RegexOption.IGNORE_CASE)

    /**
     * A node address with its public key written as 66 hex characters, as
     * it is usually shared, rewritten to the 53-character base32 label
     * (pkg/cipher/dnslabel.go). A DNS label holds at most 63 characters
     * (RFC 1035 2.3.4), so no URL parser takes the hex form as a host.
     */
    fun normalize(url: String): String = hexHost.replace(url.trim()) { m ->
        m.groupValues[1] + dnsLabel(m.groupValues[2]) + m.groupValues[3].lowercase()
    }

    /** RFC 4648 base32 of the key's bytes, lowercase and unpadded, as cipher.PubKey.DNSLabel writes it. */
    internal fun dnsLabel(hex: String): String {
        val bytes = hex.chunked(2).map { it.toInt(16) }
        val out = StringBuilder()
        var buffer = 0
        var bits = 0
        for (b in bytes) {
            buffer = (buffer shl 8) or b
            bits += 8
            while (bits >= 5) {
                out.append(ALPHABET[(buffer shr (bits - 5)) and 31])
                bits -= 5
            }
        }
        if (bits > 0) out.append(ALPHABET[(buffer shl (5 - bits)) and 31])
        return out.toString()
    }

    private const val ALPHABET = "abcdefghijklmnopqrstuvwxyz234567"
}
