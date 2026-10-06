package com.skycoin.skywire

import com.skycoin.skywire.core.SkyDns
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** vpn-client's SkyDNS flag, set and cleared without touching the rest of its argv. */
class SkyDnsTest {

    private val base = listOf("--dns", "1.1.1.1", "--srv", "02aa", "--killswitch")

    @Test
    fun turningOnAddsTheFlagOnce() {
        val on = SkyDns.vpnArgs(base, true)
        assertEquals(base + "--mesh-gateway", on)
        assertEquals(on, SkyDns.vpnArgs(on, true))
        assertTrue(SkyDns.inVpnArgs(on))
    }

    @Test
    fun turningOffKeepsEverythingElse() {
        val off = SkyDns.vpnArgs(listOf("--mesh-gateway") + base + "--mesh-gateway=true", false)
        assertEquals(base, off)
        assertFalse(SkyDns.inVpnArgs(off))
    }

    @Test
    fun anExplicitFalseIsOff() {
        assertFalse(SkyDns.inVpnArgs(base + "--mesh-gateway=false"))
        assertEquals(base + "--mesh-gateway", SkyDns.vpnArgs(base + "--mesh-gateway=false", true))
    }
}
