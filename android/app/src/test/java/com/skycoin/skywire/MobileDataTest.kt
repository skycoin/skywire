package com.skycoin.skywire

import com.skycoin.skywire.core.CoreState
import com.skycoin.skywire.core.MobileData
import com.skycoin.skywire.ui.home.HomeUiState
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** What counts as metered, and what Home says about it. */
class MobileDataTest {

    private val running = HomeUiState(coreState = CoreState.Running(0, 1), apiUp = true, metered = true)

    @Test
    fun aVpnDoesNotAnswerForTheNetworkUnderIt() {
        assertNull(MobileData.meteredOf(vpn = true, notMetered = false))
        assertNull(MobileData.meteredOf(vpn = true, notMetered = true))
        assertEquals(true, MobileData.meteredOf(vpn = false, notMetered = false))
        assertEquals(false, MobileData.meteredOf(vpn = false, notMetered = true))
    }

    @Test
    fun aCoreOnMobileDataSaysSo() {
        assertTrue(running.onMobileData)
        assertFalse(running.waitingForWifi)
        assertFalse(running.copy(metered = false).onMobileData)
    }

    @Test
    fun withMobileDataOffTheCoreWaitsForWifi() {
        val off = running.copy(useMobileData = false)
        assertTrue(off.waitingForWifi)
        assertFalse(off.onMobileData)
        assertTrue(off.copy(coreState = CoreState.Starting(1), apiUp = false).waitingForWifi)
        assertFalse(off.copy(metered = false).waitingForWifi)
        assertFalse(off.copy(coreState = CoreState.Stopped).waitingForWifi)
    }

    @Test
    fun noInternetIsNotAWaitForWifi() {
        val offline = running.copy(useMobileData = false, online = false)
        assertTrue(offline.waitingForInternet)
        assertFalse(offline.waitingForWifi)
    }
}
