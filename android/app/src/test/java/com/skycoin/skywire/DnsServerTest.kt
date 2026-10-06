package com.skycoin.skywire

import com.skycoin.skywire.core.DnsServer
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** The `--dns` SkyVPN and SkyDNS are given, set and cleared without touching the rest of their argv. */
class DnsServerTest {

    private val base = listOf("--srv", "02aa", "--killswitch", "--mesh-gateway")

    @Test
    fun settingAddsTheFlagOnce() {
        val set = DnsServer.args(base, "9.9.9.9")
        assertEquals(base + listOf("--dns", "9.9.9.9"), set)
        assertEquals(set, DnsServer.args(set, "9.9.9.9"))
        assertEquals("9.9.9.9", DnsServer.current(set))
    }

    @Test
    fun changingReplacesEitherSpelling() {
        assertEquals(base + listOf("--dns", "8.8.8.8"), DnsServer.args(listOf("--dns", "1.1.1.1") + base, "8.8.8.8"))
        assertEquals(base + listOf("--dns", "8.8.8.8"), DnsServer.args(base + "--dns=1.1.1.1", "8.8.8.8"))
    }

    @Test
    fun blankRemovesItSoTheAppUsesItsDefault() {
        assertEquals(base, DnsServer.args(base + listOf("--dns", "9.9.9.9"), ""))
        assertNull(DnsServer.current(base))
    }

    @Test
    fun onlyIpv4AddressesAreAccepted() {
        assertTrue(DnsServer.isValid("9.9.9.9"))
        assertTrue(DnsServer.isValid(" 192.168.1.1 "))
        assertFalse(DnsServer.isValid("256.1.1.1"))
        assertFalse(DnsServer.isValid("1.1.1"))
        assertFalse(DnsServer.isValid("dns.google"))
        assertFalse(DnsServer.isValid("2606:4700:4700::1111"))
        assertFalse(DnsServer.isValid(""))
    }

    @Test
    fun anythingInvalidIsStoredAsBlank() {
        assertEquals("", DnsServer.sanitize(null))
        assertEquals("", DnsServer.sanitize("not an address"))
        assertEquals("9.9.9.9", DnsServer.sanitize(" 9.9.9.9 "))
        // Blank means the phone's own DNS to SkyDNS, so 1.1.1.1 is a real choice.
        assertEquals("1.1.1.1", DnsServer.sanitize("1.1.1.1"))
    }
}
