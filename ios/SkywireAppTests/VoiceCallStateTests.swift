import CoreClient
@testable import Skywire
import os
import XCTest

/// The call state machine: which of several calls the screen shows, and the
/// answer-request handoff behind the notification's Answer. The ports of
/// Android's DialProgressTest (state half) and VoiceAnswerTest.
@MainActor
final class VoiceCallStateTests: XCTestCase {

    override func setUp() async throws {
        VoiceCalls.shared.clear()
    }

    override func tearDown() async throws {
        VoiceCalls.shared.clear()
    }

    private func invite(_ id: String) -> VoiceInvite {
        VoiceInvite(callId: id, fromPk: "03" + String(repeating: "a", count: 64))
    }

    // --- DialProgressTest, state half ---

    /// A call being placed right now wins over the outcome of the last one,
    /// which the visor lists for a few seconds after it ended.
    func testLiveCallWinsOverAnOutcome() {
        let ended = OutgoingCall(callId: "old", peerPk: "03aa", state: .offline)
        let live = OutgoingCall(callId: "new", peerPk: "03bb", state: .ringing)
        VoiceCalls.shared.set(ringing: [], dialing: [ended, live], activeIds: [])
        XCTAssertEqual(VoiceCalls.shared.state.outgoing, live)
        XCTAssertTrue(VoiceCalls.shared.state.busy, "an outcome still puts the call screen up")
        VoiceCalls.shared.set(ringing: [], dialing: [ended], activeIds: [])
        XCTAssertEqual(VoiceCalls.shared.state.outgoing, ended)
    }

    /// A call this phone ended leaves the screen at once, suppressed until
    /// the visor agrees; a failed end puts it back.
    func testEndLocallySuppressesUntilTheVisorAgrees() {
        VoiceCalls.shared.set(ringing: [invite("c1")], dialing: [], activeIds: [])
        VoiceCalls.shared.endLocally("c1")
        XCTAssertFalse(VoiceCalls.shared.state.busy)
        // The visor still reports it: suppressed.
        VoiceCalls.shared.set(ringing: [invite("c1")], dialing: [], activeIds: [])
        XCTAssertNil(VoiceCalls.shared.state.invite)
        // The end never arrived: unsuppressed.
        VoiceCalls.shared.endFailed("c1")
        VoiceCalls.shared.set(ringing: [invite("c1")], dialing: [], activeIds: [])
        XCTAssertEqual(VoiceCalls.shared.state.invite?.callId, "c1")
        // Gone from the visor: the suppression goes with it, or it would
        // grow for the life of the process.
        VoiceCalls.shared.set(ringing: [], dialing: [], activeIds: [])
        VoiceCalls.shared.set(ringing: [invite("c1")], dialing: [], activeIds: [])
        XCTAssertEqual(VoiceCalls.shared.state.invite?.callId, "c1")
    }

    // --- VoiceAnswerTest ---

    func testACallAlreadyRingingIsAnsweredWithoutWaiting() async {
        VoiceCalls.shared.set(ringing: [invite("abc")], dialing: [], activeIds: [])
        let answered = await VoiceCalls.shared.awaitRinging("abc", timeout: .milliseconds(300))
        XCTAssertTrue(answered)
    }

    /// The case the wait exists for: the tap started the app, and the poll
    /// that knows about the call has not come back yet.
    func testACallThatArrivesOnALaterPollIsStillAnswered() async {
        let waiting = Task {
            await VoiceCalls.shared.awaitRinging("abc", timeout: .seconds(5))
        }
        try? await Task.sleep(for: .milliseconds(200))
        VoiceCalls.shared.set(ringing: [invite("abc")], dialing: [], activeIds: [])
        let answered = await waiting.value
        XCTAssertTrue(answered)
    }

    /// The defect, as an assertion: without a bound this call never returns
    /// and the test times out rather than failing.
    func testACallThatNeverArrivesGivesUpInsteadOfHanging() async {
        let answered = await VoiceCalls.shared.awaitRinging("never-rings", timeout: .milliseconds(300))
        XCTAssertFalse(answered)
    }

    /// Why the bound matters beyond one lost answer: requests are read
    /// sequentially, so a wait that never returns takes the feature down
    /// rather than one tap.
    func testGivingUpDoesNotBlockTheNextRequest() async {
        let first = await VoiceCalls.shared.awaitRinging("never-rings", timeout: .milliseconds(300))
        XCTAssertFalse(first)
        VoiceCalls.shared.set(ringing: [invite("second")], dialing: [], activeIds: [])
        let second = await VoiceCalls.shared.awaitRinging("second", timeout: .milliseconds(300))
        XCTAssertTrue(second)
    }

    /// The reason the request is held at all: the tap creates the screen
    /// that is supposed to read it, so the emission precedes its only
    /// collector.
    func testARequestIsReplayedForAScreenThatDoesNotExistYet() async {
        VoiceCalls.shared.requestAnswer("abc")
        let seen = await withTimeout(.milliseconds(300)) { await VoiceCalls.shared.nextAnswer() }
        XCTAssertEqual(seen, "abc")
    }

    /// A retired request must not be replayed to the next screen.
    func testAHandledRequestIsNotAnsweredTwice() async {
        VoiceCalls.shared.requestAnswer("abc")
        let first = await withTimeout(.milliseconds(300)) { await VoiceCalls.shared.nextAnswer() }
        XCTAssertEqual(first, "abc")
        VoiceCalls.shared.answerHandled()
        // Nothing pending: the wait must not produce the dead id again…
        let seen = OSAllocatedUnfairLock<String?>(initialState: nil)
        let waiter = Task {
            let value = await VoiceCalls.shared.nextAnswer()
            seen.withLock { $0 = value }
        }
        try? await Task.sleep(for: .milliseconds(300))
        XCTAssertNil(seen.withLock { $0 }, "a retired request must not be replayed")
        // …while a later one still arrives.
        VoiceCalls.shared.requestAnswer("next")
        _ = waiter
        try? await Task.sleep(for: .milliseconds(200))
        XCTAssertEqual(seen.withLock { $0 }, "next")
    }
}

/// Runs `body` with a wall-clock budget; nil when it runs out, which for
/// these tests means "nothing was delivered".
@MainActor
private func withTimeout<T: Sendable>(_ budget: Duration, _ body: @escaping @MainActor () async -> T) async -> T? {
    let box = OSAllocatedUnfairLock(initialState: Optional<T>.none)
    let runner = Task {
        let value = await body()
        box.withLock { $0 = value }
    }
    try? await Task.sleep(for: budget)
    _ = runner
    return box.withLock { $0 }
}
