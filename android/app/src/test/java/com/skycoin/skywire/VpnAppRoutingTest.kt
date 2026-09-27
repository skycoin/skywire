package com.skycoin.skywire

import com.skycoin.skywire.core.VpnAppMode
import com.skycoin.skywire.core.VpnAppRouting
import com.skycoin.skywire.core.tunAppRules
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * What the VPN builder is told for each of SkyVPN's three app modes. The one
 * rule none of them may break: this app — and the visor running as its child —
 * stays out of the tunnel its traffic is carrying.
 */
class VpnAppRoutingTest {

    private val self = "com.skycoin.skywire"
    private val browser = "org.mozilla.firefox"
    private val bank = "com.example.bank"

    @Test
    fun allAppsIsTheOldPhoneWideTunnel() {
        val rules = tunAppRules(VpnAppRouting(VpnAppMode.ALL, only = setOf(browser), except = setOf(bank)), self)
        assertEquals(emptyList<String>(), rules.allowed)
        assertEquals(listOf(self), rules.disallowed)
    }

    /** An allow-list, and never a deny-list with it: Android takes one or the other. */
    @Test
    fun onlySelectedAllowsJustThose() {
        val rules = tunAppRules(VpnAppRouting(VpnAppMode.ONLY, only = setOf(browser, bank)), self)
        assertEquals(listOf(bank, browser), rules.allowed)
        assertTrue(rules.disallowed.isEmpty())
    }

    /** Choosing this app itself for the allow-list must not pull the visor into the tunnel. */
    @Test
    fun selfIsNeverAllowed() {
        val rules = tunAppRules(VpnAppRouting(VpnAppMode.ONLY, only = setOf(self, browser)), self)
        assertEquals(listOf(browser), rules.allowed)
    }

    @Test
    fun allExceptSelectedDeniesThoseAndSelf() {
        val rules = tunAppRules(VpnAppRouting(VpnAppMode.EXCEPT, except = setOf(bank, self)), self)
        assertTrue(rules.allowed.isEmpty())
        assertEquals(listOf(self, bank), rules.disallowed)
    }

    /**
     * "Only these apps" with none chosen cannot be built: to Android an empty
     * allow-list means every app, the visor included.
     */
    @Test
    fun onlyWithNothingChosenIsNotUsable() {
        assertFalse(VpnAppRouting(VpnAppMode.ONLY).usable)
        assertTrue(VpnAppRouting(VpnAppMode.ONLY, only = setOf(browser)).usable)
        assertTrue(VpnAppRouting(VpnAppMode.EXCEPT).usable)
        assertTrue(VpnAppRouting(VpnAppMode.ALL).usable)
    }

    /** Each mode keeps its own list; switching between them loses neither. */
    @Test
    fun selectedFollowsTheMode() {
        val routing = VpnAppRouting(VpnAppMode.ONLY, only = setOf(browser), except = setOf(bank))
        assertEquals(setOf(browser), routing.selected)
        assertEquals(setOf(bank), routing.copy(mode = VpnAppMode.EXCEPT).selected)
        assertTrue(routing.copy(mode = VpnAppMode.ALL).selected.isEmpty())
    }

    @Test
    fun modeKeysRoundTripAndDefaultToAll() {
        VpnAppMode.entries.forEach { assertEquals(it, VpnAppMode.of(it.key)) }
        assertEquals(VpnAppMode.ALL, VpnAppMode.of(null))
        assertEquals(VpnAppMode.ALL, VpnAppMode.of("something-else"))
    }
}
