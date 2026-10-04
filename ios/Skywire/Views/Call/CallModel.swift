import Combine
import CoreClient
import Foundation
import Observation
import UIKit

/// What the call screen draws (Android: CallViewModel's `CallUiState`).
struct CallUiState: Equatable {
    /// Short form of the other side's key; nil when there is no call.
    var peer: String?
    var callId: String?
    var connected = false
    /// Placing a call, waiting for the other side to pick up.
    var dialing = false
    /// How the call being placed is going — or, once it ended unanswered,
    /// why (offline, declined, no answer…). Nil when not calling.
    var dialState: DialState?
    /// The other side's key, for Call again.
    var peerPk: String?
    var micMuted = false
    var speakerphone = false
    /// `m:ss`, counted from when this device saw the call connect.
    var elapsed = "0:00"
}

/// Drives the full-screen call UI off `VoiceCalls` (Android: CallViewModel):
/// the same list of calls the visor answers with, so this screen and the
/// system's CallKit report can never disagree about whether there is a call.
///
/// The elapsed time is counted here rather than read from the visor: the
/// visor does not publish a start time, and a timer that begins when THIS
/// phone saw the call connect is the honest thing to show anyway.
@MainActor
final class CallModel: ObservableObject {
    @Published private(set) var ui = CallUiState()

    private let calls: VoiceCalls
    private let client: CoreClient
    private let tones = CallTones()
    private var connectedAt: ContinuousClock.Instant?
    private var ringbackFor: String?
    private var endedSignalFor: String?
    private var stateTask: Task<Void, Never>?
    private var timer: Task<Void, Never>?

    init(calls: VoiceCalls = .shared, client: CoreClient) {
        self.calls = calls
        self.client = client
        stateTask = Task { [weak self] in
            await self?.observe()
        }
        timer = Task { [weak self] in
            while !Task.isCancelled {
                self?.tick()
                try? await Task.sleep(for: .milliseconds(500))
            }
        }
    }

    deinit {
        stateTask?.cancel()
        timer?.cancel()
    }

    private func observe() async {
        for await state in calls.$state.values {
            sync(state)
        }
    }

    private func sync(_ snapshot: VoiceCallState) {
        let invite = snapshot.invite
        let outgoing = snapshot.outgoing
        let active = snapshot.activeIds.first
        let connected = active != nil
        if connected, connectedAt == nil {
            connectedAt = .now
        }
        if !connected {
            connectedAt = nil
        }

        var next = ui
        // Once connected the peer is kept from whichever side named it: the
        // active list carries ids only, so re-deriving it every poll would
        // blank the name the moment the call was answered.
        next.peer = if connected {
            ui.peer ?? invite.map { self.calls.displayName($0.fromPk) } ?? outgoing.map { self.calls.displayName($0.peerPk) }
        } else if let invite {
            self.calls.displayName(invite.fromPk)
        } else if let outgoing {
            self.calls.displayName(outgoing.peerPk)
        } else {
            nil
        }
        next.peerPk = if connected {
            ui.peerPk ?? invite?.fromPk ?? outgoing?.peerPk
        } else if let invite {
            invite.fromPk
        } else {
            outgoing?.peerPk
        }
        next.callId = active ?? invite?.callId ?? outgoing?.callId
        next.connected = connected
        next.dialing = !connected && outgoing != nil
        next.dialState = connected ? nil : outgoing?.state
        // A call that ended takes its mute state with it.
        next.micMuted = snapshot.busy ? ui.micMuted : false
        ui = next

        // Ring only for a call coming IN, and only here: the point of the
        // full-screen UI is that it, not a sound under it, is the call. A
        // call we are placing rings at the other end.
        if invite != nil, !connected {
            tones.playRing()
        } else {
            tones.stop()
        }
        syncRingback(!connected && invite == nil ? outgoing : nil)
    }

    private func tick() {
        guard let connectedAt else { return }
        let seconds = (ContinuousClock.now - connectedAt).components.seconds
        ui.elapsed = "\(seconds / 60):\(String(format: "%02d", seconds % 60))"
    }

    // MARK: The buttons

    func answer() {
        act { id in try await self.client.voiceAnswer(callId: id) }
    }

    func decline() {
        end { id in try await self.client.voiceDecline(callId: id) }
    }

    func hangUp() {
        end { id in try await self.client.voiceHangup(callId: id) }
    }

    /// Close the screen on a call that ended unanswered. The hang-up only
    /// tells the visor it can stop listing the outcome — which it does on
    /// its own a few seconds later — so a failure has nothing to put back.
    func dismiss() {
        guard let id = ui.callId else { return }
        tones.stop()
        calls.endLocally(id)
        Task { try? await client.voiceHangup(callId: id) }
    }

    /// Try the same person again, from an unanswered call's outcome.
    func callAgain() {
        guard let peer = ui.peerPk else { return }
        dismiss()
        Task { [client, calls] in
            do {
                _ = try await client.voiceCall(peer: peer)
            } catch {
                // The outcome screen will say so on the next poll.
            }
            calls.refresh()
        }
    }

    func toggleMic() {
        let muted = !ui.micMuted
        ui.micMuted = muted
        act { id in try await self.client.voiceMute(callId: id, mic: muted, speaker: false) }
    }

    /// Earpiece ⇄ speakerphone. Routing is the phone's business, not the
    /// visor's — the audio has already arrived by the time this matters.
    /// The engine belongs to the watcher (a call is connected before this
    /// screen exists), so the toggle routes to whatever is running.
    func toggleSpeakerphone() {
        let on = !ui.speakerphone
        CallCenter.shared.engine?.setSpeakerphone(on)
        ui.speakerphone = on
    }

    private func act(_ block: @escaping (String) async throws -> Void) {
        guard let id = ui.callId else { return }
        Task {
            do {
                try await block(id)
            } catch {
                // The next poll puts the truth on screen.
            }
        }
    }

    /// Hang up / decline: the two actions whose whole point is that the call
    /// stops NOW. The call leaves the shared state before the request goes
    /// out, so the screen closes on the tap rather than on the watcher's
    /// next poll — which is a tick away and read as a phone ignoring the
    /// button. A request that fails hands the call back, and the poll behind
    /// it puts the screen up again.
    private func end(_ block: @escaping (String) async throws -> Void) {
        guard let id = ui.callId else { return }
        tones.stop()
        calls.endLocally(id)
        Task { [calls] in
            do {
                try await block(id)
            } catch {
                calls.endFailed(id)
            }
        }
    }

    // MARK: Ringback

    /// Keep what the caller hears in step with how the call is going:
    /// silence while it is still connecting (there is no ringing yet to
    /// report), a ring while it rings, the busy signal once when it ends
    /// unanswered.
    private func syncRingback(_ call: OutgoingCall?) {
        switch call?.state {
        case nil, .connecting:
            if ringbackFor != nil {
                ringbackFor = nil
                tones.stop()
            }
        case .some(let state) where state.ended:
            if ringbackFor != call?.callId {
                ringbackFor = call?.callId
                tones.stop()
                if endedSignalFor != call?.callId {
                    endedSignalFor = call?.callId
                    tones.playEndedSignal()
                }
            }
        default:
            guard let call else { return }
            if call.ringback, ringbackFor != call.callId {
                ringbackFor = call.callId
                Task { [client, tones] in
                    guard let bytes = try? await client.voiceRingback(callId: call.callId) else { return }
                    _ = await tones.playCustomRingback(bytes)
                }
            } else if !call.ringback, ringbackFor != call.callId {
                ringbackFor = call.callId
                tones.playRing()
            }
        }
    }
}
