import Combine
import CoreClient
import Foundation
import Observation

/// What the phone knows about calls right now (Android: core/VoiceCalls.kt's
/// `VoiceCallState`).
struct VoiceCallState: Equatable {
    var ringing: [VoiceInvite] = []
    var dialing: [OutgoingCall] = []
    var activeIds: [String] = []

    var inCall: Bool { !activeIds.isEmpty }

    /// The call to offer an answer for — one at a time is all a phone shows.
    var invite: VoiceInvite? { ringing.first }

    /// The call being placed, if any — or, for a few seconds after one ended
    /// unanswered, that call and why (`OutgoingCall.state`). A live one wins.
    var outgoing: OutgoingCall? {
        dialing.first { !$0.state.ended } ?? dialing.first
    }

    /// True whenever there is a call to put on screen, in either direction.
    var busy: Bool { inCall || invite != nil || outgoing != nil }

    /// This state minus one call, whichever of the three lists it is in.
    func without(_ callId: String) -> VoiceCallState {
        var copy = self
        copy.ringing.removeAll { $0.callId == callId }
        copy.dialing.removeAll { $0.callId == callId }
        copy.activeIds.removeAll { $0 == callId }
        return copy
    }
}

/// The process-wide view of the visor's calls (Android: `object VoiceCalls`).
/// Read by the call screen, written by the watcher (`CallCenter`), so a call
/// rings and connects whether or not any screen is looking — which is the
/// whole point of a phone.
@MainActor
final class VoiceCalls: ObservableObject {
    static let shared = VoiceCalls()

    @Published private(set) var state = VoiceCallState()

    /// Calls this phone has hung up or declined but the visor still reports.
    ///
    /// The watcher polls, so without this the call screen stayed up for the
    /// rest of the tick after the red button — several seconds of a phone
    /// that looks like it did not hear you. Hanging up removes the call here
    /// at once and suppresses it until the visor agrees it is gone, which is
    /// the order every phone does this in. Nothing is lost if the visor
    /// disagrees: a hang-up that fails un-suppresses the id (`endFailed`)
    /// and the next poll puts the call back.
    private var ended: Set<String> = []

    /// The answer request a tap left behind, waiting for the screen that
    /// will act on it (Android's replayed `answers` flow). One request at a
    /// time: answering has to happen where the call can be heard, so the
    /// notification's Answer opens the app rather than answering in place —
    /// and this is how the id survives that trip.
    private var pendingAnswer: String?

    private var answerWaiters: [CheckedContinuation<String?, Never>] = []

    /// The operator's names for public keys, from skychat's address book.
    /// Held here rather than fetched per screen because a call screen has to
    /// name its peer the instant it appears. Refreshed by the watcher on the
    /// same tick as the calls.
    private var names: [String: String] = [:]

    /// Poll now rather than at the next tick — a call was just placed or
    /// ended. Read and cleared by the watcher's delay, in slices, so the
    /// state behind a closed screen catches up while the user is still
    /// looking at the phone.
    private var refreshWanted = false

    /// The user ended `callId` — drop it now, confirm with the visor after.
    func endLocally(_ callId: String) {
        ended.insert(callId)
        state = state.without(callId)
        refreshWanted = true
    }

    /// The end never reached the visor: let the call come back on the next
    /// poll.
    func endFailed(_ callId: String) {
        ended.remove(callId)
        refreshWanted = true
    }

    func refresh() {
        refreshWanted = true
    }

    /// Waits out the watcher's current delay, or returns early the moment a
    /// nudge lands.
    func delay(_ interval: Duration) async {
        var waited: Duration = .zero
        while waited < interval, !refreshWanted {
            let slice: Duration = .milliseconds(50)
            try? await Task.sleep(for: slice)
            waited += slice
        }
        refreshWanted = false
    }

    /// The notification's or CallKit's Answer was tapped for `callId`.
    func requestAnswer(_ callId: String) {
        pendingAnswer = callId
        resumeWaiters(with: callId)
    }

    /// The next answer request, or the one already waiting (replayed once):
    /// the tap that carries it can be the thing that creates the screen it
    /// is meant for.
    func nextAnswer() async -> String? {
        if let pendingAnswer { return pendingAnswer }
        return await withCheckedContinuation { continuation in
            answerWaiters.append(continuation)
        }
    }

    /// The request for `callId` has been acted on; a later screen must not
    /// answer it again. One that arrived meanwhile stays, as Android's
    /// buffered flow keeps it.
    func answerHandled(_ callId: String) {
        if pendingAnswer == callId {
            pendingAnswer = nil
        }
    }

    /// Wait until `callId` is one of the ringing calls, or give up. The tap
    /// that carries an answer request can be the thing that starts the app,
    /// so the call it names is routinely not in `state` yet — the first poll
    /// that knows about it may still be in flight. Hence a wait rather than
    /// a look.
    ///
    /// Bounded, because the id may name a call that will never arrive: a
    /// notification outlives the process that posted it, so the tap can land
    /// long after the call ended, and an unbounded wait parks the reader of
    /// these requests with every later one queued behind it forever.
    func awaitRinging(_ callId: String, timeout: Duration = VoiceCalls.answerWait) async -> Bool {
        let deadline = ContinuousClock.now + timeout
        while ContinuousClock.now < deadline {
            if state.ringing.contains(where: { $0.callId == callId }) { return true }
            try? await Task.sleep(for: .milliseconds(50))
        }
        return state.ringing.contains { $0.callId == callId }
    }

    /// How long `awaitRinging` gives a call to appear. Generous against the
    /// two-second poll, and short enough that a dead request clears while
    /// the user is looking at the screen it failed to open.
    static let answerWait: Duration = .seconds(15)

    /// What to call `pk`: the operator's name for it, else the shortened
    /// key. The same order skychat uses for a notification title, so the two
    /// never disagree about who called. `03ab…c9d1` — a 66-character key is
    /// not a caller ID.
    func displayName(_ pk: String) -> String {
        if let name = names[pk.lowercased()], !name.isEmpty { return name }
        return pk.count <= 12 ? pk : "\(pk.prefix(6))…\(pk.suffix(4))"
    }

    func setNames(_ book: [String: String]) {
        names = book.mapKeys { $0.lowercased() }
    }

    /// The watcher's tick: the visor's three lists, minus the calls this
    /// phone ended on its own authority. Ids the visor has stopped reporting
    /// have served their purpose and leave the suppression set with it.
    func set(ringing: [VoiceInvite], dialing: [OutgoingCall], activeIds: [String]) {
        let reported = Set(ringing.map(\.callId) + dialing.map(\.callId) + activeIds)
        ended.formIntersection(reported)
        state = VoiceCallState(
            ringing: ringing.filter { !ended.contains($0.callId) },
            dialing: dialing.filter { !ended.contains($0.callId) },
            activeIds: activeIds.filter { !ended.contains($0) }
        )
    }

    /// The core is going away, so the calls are too.
    func clear() {
        ended = []
        state = VoiceCallState()
        pendingAnswer = nil
        resumeWaiters(with: nil)
    }

    private func resumeWaiters(with value: String?) {
        for waiter in answerWaiters {
            waiter.resume(returning: value)
        }
        answerWaiters = []
    }
}

private extension Dictionary where Key == String, Value == String {
    func mapKeys(_ transform: (String) -> String) -> [String: String] {
        Dictionary(map { (transform($0.key), $0.value) }, uniquingKeysWith: { first, _ in first })
    }
}
