import AVFoundation
import CoreClient
import os
import UIKit

/// Polls the visor for ringing and connected calls, and turns the answer
/// into the things the phone owes a call (Android: core/VoiceCallWatcher.kt
/// plus VoiceCallService's start/stop): the call screen is drawn by
/// `VoiceCalls`'s state from wherever, the system's incoming-call UI when
/// the app is not on screen, and — while connected — the engine that lends
/// the visor the microphone and speaker.
///
/// Polling rather than a subscription because that is the surface the visor
/// offers; at a two-second tick against a loopback API the cost is noise.
@MainActor
final class CallCenter {
    static let shared = CallCenter()

    private let log = Logger(subsystem: Bundle.main.bundleIdentifier ?? "skywire", category: "voice")
    static let pollInterval: Duration = .seconds(2)
    static let namesRefreshInterval: Duration = .seconds(30)

    /// The engine lending the visor this phone's microphone and speaker
    /// while a call is connected. The call screen's speaker toggle routes
    /// here.
    private(set) var engine: VoiceAudioEngine?
    private var wasInCall = false
    /// Whether the call now connected was answered from the system's UI, in
    /// which case CallKit owns the audio session, not the engine.
    private var answeredByCallKit = false
    private var callKit: CallKitCenter?
    /// The ringing call the system was told about, if any.
    private var reportedRinging: String?
    private var namesAt = Date.distantPast
    private var namesRefreshFailed = false
    private var permissionPrompted = false

    /// Runs for as long as the core is connected (SwiftUI's
    /// `.task(id: app.connected)` ends it).
    func run(_ app: AppModel) async {
        guard app.connected else {
            windDown()
            return
        }
        let client = app.client
        let callKit = CallKitCenter(handlers: CallKitCenter.Handlers(
            answer: { [weak self] callId in self?.answerRequest(callId, viaCallKit: true) },
            decline: { [weak self] callId in self?.decline(callId, client: client) },
            end: { [weak self] callId in self?.end(callId, client: client) },
            setMuted: { callId, muted in
                Task { try? await client.voiceMute(callId: callId, mic: muted, speaker: false) }
            },
            sessionActivated: { [weak self] in self?.answeredByCallKit = true }
        ))
        self.callKit = callKit
        let collector = Task { [weak self] in
            await self?.answerCollector(app)
        }
        defer {
            collector.cancel()
            windDown()
        }
        while !Task.isCancelled {
            // Before the calls, so a name is already in hand when one
            // arrives — a call screen that shows hex and corrects itself a
            // moment later is barely better than one that never knew.
            await refreshNames(app)
            do {
                let ringing = try await app.client.voiceIncoming()
                let active = try await app.client.voiceActive()
                // A dialing list a visor predating the feature cannot serve
                // costs nothing: no calls being placed.
                let dialing = (try? await app.client.voiceDialing()) ?? []
                // A failed poll is left alone rather than reported as "no
                // calls": the visor restarting mid-call would otherwise drop
                // a live call's screen and stop the audio.
                VoiceCalls.shared.set(ringing: ringing, dialing: dialing, activeIds: active)
                present(ringing.first)
                let inCall = !active.isEmpty
                if inCall != wasInCall {
                    if inCall {
                        startEngine(app)
                    } else {
                        stopEngine()
                        // The system's in-call UI goes with the call.
                        callKit.endReportedCalls(reason: .remoteEnded)
                        reportedRinging = nil
                    }
                    wasInCall = inCall
                }
            } catch {
                if !app.handle(error) {
                    log.notice("call poll failed: \(error.localizedDescription, privacy: .public)")
                }
            }
            await VoiceCalls.shared.delay(Self.pollInterval)
        }
    }

    /// A ringing call the system should show: only when the app is not the
    /// thing on screen (in the foreground the call screen is the call), and
    /// once per call id — the watcher polls, and re-reporting an unchanged
    /// call re-raises the system's UI, roughly twenty times for one ring.
    private func present(_ invite: VoiceInvite?) {
        guard let callKit else { return }
        guard let invite else {
            if let id = reportedRinging {
                reportedRinging = nil
                // Answered rather than gone: the report becomes the
                // connected call instead of a missed one.
                if !VoiceCalls.shared.state.activeIds.contains(id) {
                    callKit.endReportedCalls(reason: .remoteEnded)
                }
            }
            return
        }
        guard UIApplication.shared.applicationState != .active else { return }
        guard invite.callId != reportedRinging else { return }
        reportedRinging = invite.callId
        callKit.reportIncoming(invite, caller: VoiceCalls.shared.displayName(invite.fromPk))
    }

    // MARK: The actions, from every surface

    /// The Answer tapped on the system's UI or a notification: the bounded
    /// wait plus the API call, and the call is heard where the screen is.
    private func answerRequest(_ callId: String, viaCallKit: Bool) {
        answeredByCallKit = viaCallKit
        VoiceCalls.shared.requestAnswer(callId)
    }

    /// The one collector of answer requests (Android's VoiceCalls.answers
    /// collector). Sequential on purpose: a request that never resolves must
    /// not park the ones behind it, which is why the wait is bounded and
    /// the request retired either way.
    private func answerCollector(_ app: AppModel) async {
        while !Task.isCancelled {
            guard let callId = await VoiceCalls.shared.nextAnswer() else { continue }
            if await VoiceCalls.shared.awaitRinging(callId) {
                requestMicPermissionOnce()
                do {
                    try await app.client.voiceAnswer(callId: callId)
                } catch {
                    log.warning("answer did not reach the visor: \(error.localizedDescription, privacy: .public)")
                }
            } else {
                log.notice("answer request for a call that never arrived")
            }
            VoiceCalls.shared.answerHandled(callId)
        }
    }

    /// Declining without opening the app: drop it locally first so the ring
    /// stops on the tap, and a request that fails hands the call back.
    private func decline(_ callId: String, client: CoreClient) {
        VoiceCalls.shared.endLocally(callId)
        Task {
            do {
                try await client.voiceDecline(callId: callId)
            } catch {
                log.warning("decline did not reach the visor: \(error.localizedDescription, privacy: .public)")
                VoiceCalls.shared.endFailed(callId)
            }
        }
    }

    /// The system's End for a ringing or connected call: decline or hang up
    /// by where the call is.
    private func end(_ callId: String, client: CoreClient) {
        if VoiceCalls.shared.state.activeIds.contains(callId) {
            VoiceCalls.shared.endLocally(callId)
            Task {
                do {
                    try await client.voiceHangup(callId: callId)
                } catch {
                    VoiceCalls.shared.endFailed(callId)
                }
            }
        } else {
            decline(callId, client: client)
        }
    }

    // MARK: The engine

    private func startEngine(_ app: AppModel) {
        requestMicPermissionOnce()
        stopEngine()
        let engine = VoiceAudioEngine(sessionOwner: answeredByCallKit ? .callKit : .engine)
        self.engine = engine
        answeredByCallKit = false
        engine.start(client: app.client)
    }

    private func stopEngine() {
        engine?.stop()
        engine = nil
    }

    /// The microphone's prompt, once per process, at the first call: a call
    /// can arrive before the user has ever been asked, and the honest
    /// behaviour is a call they can hear while the grant is outstanding.
    private func requestMicPermissionOnce() {
        guard !permissionPrompted else { return }
        permissionPrompted = true
        if #available(iOS 17.0, *) {
            Task { _ = await AVAudioApplication.requestRecordPermission() }
        } else {
            AVAudioSession.sharedInstance().requestRecordPermission { _ in }
        }
    }

    // MARK: Names and teardown

    /// The operator's names for keys, from skychat's address book. Rarely:
    /// names change when a human edits one, and polling at the call rate
    /// would ask every two seconds for a file that changes twice a month.
    private func refreshNames(_ app: AppModel) async {
        guard Date().timeIntervalSince(namesAt) > TimeInterval(Self.namesRefreshInterval.components.seconds) else { return }
        namesAt = Date()
        guard let state = try? await app.client.app(SkychatProfile.app) else { return }
        let origin = SkychatProfile.origin(port: SkychatProfile.listenPort(state.args))
        let skychat = SkychatClient(transport: LoopbackTransport(origin: origin)) {
            try SecretStore.app().password(.skychatPassword)
        }
        let book = await skychat.contacts()
        if !book.isEmpty || !namesRefreshFailed {
            VoiceCalls.shared.setNames(book)
        }
        namesRefreshFailed = book.isEmpty
    }

    /// The core is going away, so the calls are too: leave nothing ringing
    /// and no engine holding the microphone.
    private func windDown() {
        stopEngine()
        wasInCall = false
        answeredByCallKit = false
        reportedRinging = nil
        callKit?.endReportedCalls(reason: .remoteEnded)
        callKit = nil
        VoiceCalls.shared.clear()
    }
}
