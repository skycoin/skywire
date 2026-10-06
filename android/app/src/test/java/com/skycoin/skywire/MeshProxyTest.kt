package com.skycoin.skywire

import com.skycoin.skywire.core.MeshProxy
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** Which hosts SkyVPN's HTTP proxy takes, given only its exclusion list. */
class MeshProxyTest {

    // A host matching any pattern bypasses the proxy. '*' is a glob, as in
    // Chromium's bypass rules and Java's http.nonProxyHosts.
    private val bypass = Regex(
        MeshProxy.exclusions.joinToString("|") { p ->
            p.split('*').joinToString(".*") { Regex.escape(it) }
        },
        RegexOption.IGNORE_CASE,
    )

    private fun proxied(host: String) = !bypass.matches(host)

    /** ProxyInfo drops a list this rejects. EXCL_REGEX in com.android.net.module.util.ProxyUtils. */
    @Test
    fun androidAcceptsTheList() {
        val excl = "[a-zA-Z0-9*]+(\\-[a-zA-Z0-9*]+)*(\\.[a-zA-Z0-9*]+(\\-[a-zA-Z0-9*]+)*)*"
        val list = Regex("^$|^$excl(,$excl)*$")
        assertTrue(list.matches(MeshProxy.exclusions.joinToString(",")))
    }

    @Test
    fun meshNamesGoToTheProxy() {
        val pk = "022e607e0914d6e7ccda7587f95790c09e126bbd506cc476a1eda852325aadd1aa"
        for (host in listOf("$pk.dmsg", "skywire.dmsg", "status.skynet", "a.b.skynet", "X.DMSG")) {
            assertTrue(host, proxied(host))
        }
    }

    @Test
    fun everythingElseStaysOnTheTunnel() {
        val hosts = listOf(
            "example.com", "api.ipify.org", "104.26.13.205", "localhost", "127.0.0.1",
            "mynet.net", "sg.dmsg.com", "foodmsg", "fooskynet", "x.ynet", "dmsg", "skynet",
        )
        for (host in hosts) {
            assertTrue(host, !proxied(host))
        }
    }

    /** Hosts that share a few characters with a suffix are where a gap would hide. */
    @Test
    fun sweepNearTheSuffixes() {
        val alphabet = "dmsgkynet.-1x"
        val stems = mutableListOf("")
        var level = listOf("")
        repeat(3) {
            level = level.flatMap { s -> alphabet.map { s + it } }
            stems += level
        }
        val tails = listOf("", "g", "sg", "msg", "dmsg", ".dmsg", "t", "net", "ynet", "skynet", ".skynet")
        for (stem in stems) {
            for (tail in tails) {
                val host = "x$stem$tail"
                // Known gap: Android rejects a pattern ending in '.' or '-', so a
                // host such as "example.com." still goes to the proxy.
                if (host.last() == '.' || host.last() == '-') continue
                val want = MeshProxy.SUFFIXES.any { host.endsWith(it) }
                assertEquals(host, want, proxied(host))
            }
        }
    }
}
