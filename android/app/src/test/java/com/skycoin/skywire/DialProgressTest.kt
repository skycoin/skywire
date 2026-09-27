package com.skycoin.skywire

import com.skycoin.skywire.api.DialState
import com.skycoin.skywire.api.OutgoingCall
import com.skycoin.skywire.core.VoiceCallState
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * How a call this phone places is reported: the visor's state strings, and
 * which of several listed calls the call screen shows.
 */
class DialProgressTest {

    @Test
    fun statesParseAndSplitIntoProgressAndOutcome() {
        val progress = mapOf(
            "connecting" to DialState.CONNECTING,
            "calling" to DialState.CALLING,
            "ringing" to DialState.RINGING,
        )
        val outcomes = mapOf(
            "offline" to DialState.OFFLINE,
            "declined" to DialState.DECLINED,
            "busy" to DialState.BUSY,
            "no_answer" to DialState.NO_ANSWER,
            "failed" to DialState.FAILED,
        )
        progress.forEach { (wire, state) ->
            assertEquals(state, DialState.parse(wire))
            assertFalse("$wire is progress, not an outcome", state.ended)
        }
        outcomes.forEach { (wire, state) ->
            assertEquals(state, DialState.parse(wire))
            assertTrue("$wire is an outcome", state.ended)
        }
    }

    /** A visor that predates dial progress sends no state: the call is just "calling". */
    @Test
    fun missingStateReadsAsCalling() {
        assertEquals(DialState.CALLING, DialState.parse(""))
        assertEquals(DialState.CALLING, DialState.parse("something-new"))
    }

    /**
     * A call being placed right now wins over the outcome of the last one,
     * which the visor lists for a few seconds after it ended.
     */
    @Test
    fun liveCallWinsOverAnOutcome() {
        val ended = OutgoingCall("old", "03aa", DialState.OFFLINE)
        val live = OutgoingCall("new", "03bb", DialState.RINGING)
        assertEquals(live, VoiceCallState(dialing = listOf(ended, live)).outgoing)
        assertEquals(ended, VoiceCallState(dialing = listOf(ended)).outgoing)
        assertTrue("an outcome still puts the call screen up", VoiceCallState(dialing = listOf(ended)).busy)
    }
}
