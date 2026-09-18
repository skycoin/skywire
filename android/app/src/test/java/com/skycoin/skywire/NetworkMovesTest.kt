package com.skycoin.skywire

import com.skycoin.skywire.core.Attachment
import com.skycoin.skywire.core.NetworkMoves
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.net.InetAddress

/**
 * Which network events cost the core a re-dial, and which do not.
 *
 * Both halves matter. Missing a real move leaves the visor holding sockets on
 * an address the phone no longer has, invisible to its hypervisor and its
 * dmsg peers until a keepalive times out ~75 s later. Calling a move on
 * something that is not one tears down working sessions — and on a phone the
 * framework re-reports the same network freely, so "not one" is the common
 * case.
 */
class NetworkMovesTest {

    private fun attachment(id: String, vararg addresses: String) =
        Attachment.of(id, addresses.map { InetAddress.getByName(it) })

    @Test
    fun theNetworkTheCoreStartedOnIsNotAMove() {
        val moves = NetworkMoves()
        // The framework delivers the current default as soon as we register.
        assertFalse(moves.observe(attachment("100", "192.168.1.14")))
    }

    @Test
    fun theSameAttachmentReportedAgainIsNotAMove() {
        val moves = NetworkMoves()
        moves.observe(attachment("100", "192.168.1.14"))
        // onAvailable then onLinkPropertiesChanged for the same network, and
        // whatever else the framework feels like repeating.
        assertFalse(moves.observe(attachment("100", "192.168.1.14")))
        assertFalse(moves.observe(attachment("100", "192.168.1.14")))
    }

    @Test
    fun switchingNetworkIsAMove() {
        val moves = NetworkMoves()
        moves.observe(attachment("100", "192.168.1.14"))
        // Wi-Fi out of range: the default becomes cellular.
        assertTrue(moves.observe(attachment("101", "10.84.22.7")))
    }

    @Test
    fun aNewAddressOnTheSameNetworkIsAMove() {
        val moves = NetworkMoves()
        moves.observe(attachment("101", "10.84.22.7"))
        // Same cellular network, carrier hands over a new address. Every
        // socket bound to the old one is dead.
        assertTrue(moves.observe(attachment("101", "10.84.30.91")))
    }

    @Test
    fun comingBackFromAnOutageIsAMove() {
        val moves = NetworkMoves()
        moves.observe(attachment("100", "192.168.1.14"))
        moves.lost("100")
        assertNull("a lost default leaves nothing current", moves.current())
        // Even when the same network returns with the same address: the gap
        // is long enough to have dropped the sockets either way.
        assertTrue(moves.observe(attachment("100", "192.168.1.14")))
    }

    @Test
    fun losingANetworkThatIsNotTheDefaultChangesNothing() {
        val moves = NetworkMoves()
        val wifi = attachment("100", "192.168.1.14")
        moves.observe(wifi)
        // Make-before-break: cellular is torn down after Wi-Fi took over.
        moves.lost("101")
        assertEquals(wifi, moves.current())
        assertFalse(moves.observe(wifi))
    }

    @Test
    fun addressOrderIsNotAChangeButTheAddressSetIs() {
        val moves = NetworkMoves()
        moves.observe(attachment("100", "192.168.1.14", "2001:db8::5"))
        // The framework's ordering is not a fact about the network.
        assertFalse(moves.observe(attachment("100", "2001:db8::5", "192.168.1.14")))
        // Losing the v6 address is.
        assertTrue(moves.observe(attachment("100", "192.168.1.14")))
    }

    @Test
    fun linkLocalAndLoopbackAreIgnored() {
        // fe80:: is per-interface and constant across the moves that matter;
        // no dmsg session is bound to one. Neither should register as a
        // change, or every interface event would cost a re-dial.
        val moves = NetworkMoves()
        moves.observe(attachment("100", "192.168.1.14"))
        assertFalse(moves.observe(attachment("100", "192.168.1.14", "fe80::1")))
        assertFalse(moves.observe(attachment("100", "192.168.1.14", "127.0.0.1")))
    }
}
