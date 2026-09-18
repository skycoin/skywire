package com.skycoin.skywire

import com.skycoin.skywire.api.VoiceInvite
import com.skycoin.skywire.core.VoiceCalls
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.async
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runTest
import org.junit.After
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

/**
 * Answering a call from its notification, and the wait that stands behind it.
 *
 * The notification's Answer cannot answer in place — a call has to have
 * somewhere to happen — so it opens the app and leaves the call id behind in a
 * replayed flow. The screen that picks it up then has to wait, because the tap
 * is often what started the app and the first poll that knows about the call
 * is still in flight.
 *
 * **That wait is the whole risk.** An independent review found it unbounded:
 * `state.first { … }` on an id that never arrives suspends forever, and since
 * the collector reading these requests is sequential, one dead request parks
 * every later one behind it — permanently, because the replay cache is only
 * cleared *after* a successful answer, so the next view model replays the same
 * dead id and hangs again. The Answer button stops working until the process
 * restarts, silently, and the in-app button still works so nothing looks
 * broken.
 *
 * The id can be dead for ordinary reasons: a notification outlives the process
 * that posted it, so killing the app mid-ring leaves the ring on screen with
 * nobody to cancel it, and the tap can land long after the call ended.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class VoiceAnswerTest {

    @Before
    fun clean() = VoiceCalls.clear()

    @After
    fun tidy() = VoiceCalls.clear()

    @Test
    fun aCallAlreadyRingingIsAnsweredWithoutWaiting() = runTest {
        VoiceCalls.set(ringing = listOf(invite("abc")), dialing = emptyList(), activeIds = emptyList())
        assertTrue(VoiceCalls.awaitRinging("abc"))
    }

    @Test
    fun aCallThatArrivesOnALaterPollIsStillAnswered() = runTest {
        // The case the wait exists for: the tap started the app, and the poll
        // that knows about the call has not come back yet.
        val waiting = async { VoiceCalls.awaitRinging("abc") }
        advanceTimeBy(2_000)
        VoiceCalls.set(ringing = listOf(invite("abc")), dialing = emptyList(), activeIds = emptyList())
        assertTrue(waiting.await())
    }

    @Test
    fun aCallThatNeverArrivesGivesUpInsteadOfHanging() {
        // The defect, as an assertion: without a bound this call never
        // returns and the test times out rather than failing.
        runTest {
            assertFalse(VoiceCalls.awaitRinging("never-rings"))
        }
    }

    @Test
    fun givingUpDoesNotBlockTheNextRequest() {
        // Why the bound matters beyond one lost answer: requests are read
        // sequentially, so a wait that never returns is a wait that takes the
        // feature down rather than one tap.
        runTest {
            assertFalse(VoiceCalls.awaitRinging("never-rings"))
            VoiceCalls.set(ringing = listOf(invite("second")), dialing = emptyList(), activeIds = emptyList())
            assertTrue(VoiceCalls.awaitRinging("second"))
        }
    }

    @Test
    fun aRequestIsReplayedForAScreenThatDoesNotExistYet() {
        // The reason the flow replays at all: the tap creates the screen that
        // is supposed to read it, so the emission precedes its only collector.
        runTest {
            VoiceCalls.requestAnswer("abc")
            VoiceCalls.set(ringing = listOf(invite("abc")), dialing = emptyList(), activeIds = emptyList())
            val seen = VoiceCalls.answers.replayCache
            assertTrue("the request must survive until a screen exists", seen.contains("abc"))
        }
    }

    @Test
    fun aHandledRequestIsNotAnsweredTwice() {
        runTest {
            VoiceCalls.requestAnswer("abc")
            VoiceCalls.answerHandled()
            assertTrue(
                "a retired request must not be replayed to the next screen",
                VoiceCalls.answers.replayCache.isEmpty(),
            )
        }
    }

    private fun invite(id: String) = VoiceInvite(callId = id, fromPk = "03" + "a".repeat(64))
}
