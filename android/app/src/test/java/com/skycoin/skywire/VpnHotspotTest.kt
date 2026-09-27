package com.skycoin.skywire

import com.skycoin.skywire.core.NetIface
import com.skycoin.skywire.core.hotspotAddresses
import com.skycoin.skywire.core.isPrivateIpv4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Which addresses the VPN hotspot's proxy answers on. The rule that matters:
 * a network the phone is merely joined to — the café's Wi-Fi — never gets the
 * tunnel, only the phone's own hotspot does.
 */
class VpnHotspotTest {

    private fun iface(name: String, vararg ipv4: String, up: Boolean = true, loopback: Boolean = false) =
        NetIface(name, up, loopback, ipv4.toList())

    @Test
    fun theHotspotIsServedAndTheWifiThePhoneJoinedIsNot() {
        val interfaces = listOf(
            iface("lo", "127.0.0.1", loopback = true),
            iface("wlan0", "192.168.1.23"),
            iface("wlan1", "192.168.43.1"),
            iface("rmnet_data0", "10.64.12.9"),
            iface("tun0", "192.168.255.6"),
        )
        val uplinks = setOf("wlan0", "rmnet_data0", "tun0")
        assertEquals(listOf("192.168.43.1"), hotspotAddresses(interfaces, uplinks))
    }

    /**
     * A phone without Wi-Fi concurrency runs the hotspot on wlan0 itself; it
     * is not joined to any Wi-Fi then, so wlan0 is no uplink.
     */
    @Test
    fun aHotspotOnWlan0IsServedWhenWlan0IsNotAnUplink() {
        val interfaces = listOf(iface("wlan0", "10.98.52.1"), iface("rmnet_data1", "100.71.3.4"))
        assertEquals(listOf("10.98.52.1"), hotspotAddresses(interfaces, setOf("rmnet_data1")))
    }

    @Test
    fun usbAndBluetoothTetheringCount() {
        val interfaces = listOf(iface("rndis0", "192.168.42.129"), iface("bt-pan", "192.168.44.1"))
        assertEquals(listOf("192.168.42.129", "192.168.44.1"), hotspotAddresses(interfaces, emptySet()))
    }

    /** Mobile data's siblings are not all networks an app can see: named out. */
    @Test
    fun mobileDataAndTheVpnAreNeverServedEvenWhenUnlisted() {
        val interfaces = listOf(
            iface("rmnet_data2", "10.1.1.1"),
            iface("ccmni1", "10.2.2.2"),
            iface("v4-rmnet_data0", "192.0.0.4"),
            iface("tun0", "10.8.0.4"),
            iface("dummy0", "10.3.3.3"),
        )
        assertEquals(emptyList<String>(), hotspotAddresses(interfaces, emptySet()))
    }

    @Test
    fun downInterfacesAndPublicAddressesAreSkipped() {
        val interfaces = listOf(iface("ap0", "192.168.43.1", up = false), iface("swlan0", "203.0.113.5"))
        assertEquals(emptyList<String>(), hotspotAddresses(interfaces, emptySet()))
    }

    @Test
    fun privateRanges() {
        listOf("10.0.0.1", "172.16.0.1", "172.31.255.254", "192.168.43.1").forEach {
            assertTrue(it, isPrivateIpv4(it))
        }
        listOf("172.15.0.1", "172.32.0.1", "192.169.0.1", "8.8.8.8", "100.64.0.1", "not.an.ip.x", "10.0.0").forEach {
            assertFalse(it, isPrivateIpv4(it))
        }
    }
}
