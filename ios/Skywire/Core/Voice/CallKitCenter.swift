import AVFoundation
import CallKit
import CoreClient
import Foundation
import os

/// The system's half of a call (playbook 6.3): `CXProvider`'s incoming-call
/// UI on the lock screen and over other apps, its Answer and Decline, and
/// the mute its in-call controls carry.
///
/// Reported only when the app is not the thing on screen: in the foreground
/// the app's own call screen is the call (and the Simulator's CallKit UI is
/// partial, so the in-app screen is the tested surface). Without VoIP push
/// there is nothing to ring a suspended phone with — the stated limit of
/// playbook 6.5 — so a report here means the app was alive in the
/// background's short grace when the call arrived.
@MainActor
final class CallKitCenter: NSObject, CXProviderDelegate {
    private let provider: CXProvider
    private var log = Logger(subsystem: Bundle.main.bundleIdentifier ?? "skywire", category: "voice-callkit")

    /// What the provider's actions do; CallCenter wires these to the API.
    struct Handlers {
        var answer: (String) -> Void
        var decline: (String) -> Void
        var end: (String) -> Void
        var setMuted: (String, Bool) -> Void
        /// The system activated the shared audio session for a call it
        /// answered; the engine must not manage the session itself.
        var sessionActivated: () -> Void
    }

    private let handlers: Handlers
    /// The call each reported uuid names. One ringing call at a time, like
    /// the screens.
    private var reported: [UUID: String] = [:]

    init(handlers: Handlers) {
        self.handlers = handlers
        let configuration = CXProviderConfiguration()
        configuration.supportsVideo = false
        configuration.maximumCallGroups = 1
        configuration.supportedHandleTypes = [.generic]
        // A Skywire call is not a phone call in the phone's history.
        configuration.includesCallsInRecents = false
        provider = CXProvider(configuration: configuration)
        super.init()
        provider.setDelegate(self, queue: nil)
    }

    /// Puts a ringing call in front of the user. Once per call id: the
    /// watcher polls, and re-reporting an unchanged call re-raises it, the
    /// bug Android's `showing` guard exists for.
    func reportIncoming(_ invite: VoiceInvite, caller: String) {
        let uuid = UUID()
        reported[uuid] = invite.callId
        let update = CXCallUpdate()
        update.remoteHandle = CXHandle(type: .generic, value: caller)
        update.supportsHolding = false
        update.supportsGrouping = false
        update.supportsUngrouping = false
        update.hasVideo = false
        update.localizedCallerName = caller
        provider.reportNewIncomingCall(with: uuid, update: update) { [log] error in
            if let error {
                log.notice("CallKit would not take the call: \(error.localizedDescription, privacy: .public)")
            }
        }
    }

    /// A ringing call the system was told about is gone. Unanswered reads as
    /// ended at the far end, which the system shows as missed.
    func endReportedCalls(reason: CXCallEndedReason) {
        for uuid in reported.keys {
            provider.reportCall(with: uuid, endedAt: nil, reason: reason)
        }
        reported.removeAll()
    }

    // MARK: CXProviderDelegate

    nonisolated func providerDidReset(_ provider: CXProvider) {
        Task { @MainActor [self] in
            reported.removeAll()
        }
    }

    /// Answer goes through the same bounded wait a notification's Answer
    /// does, not straight to the API: the tap can be what wakes the app, and
    /// the poll that knows about the call may still be in flight. The action
    /// is fulfilled at once (a CXAction is not Sendable, and the answering
    /// is asynchronous anyway, exactly as a notification's Answer is); a
    /// call that never arrives ends the report on the next poll.
    nonisolated func provider(_ provider: CXProvider, perform action: CXAnswerCallAction) {
        let uuid = action.callUUID
        action.fulfill()
        Task { @MainActor [self] in
            if let callId = reported[uuid] {
                handlers.sessionActivated()
                handlers.answer(callId)
            }
        }
    }

    /// Decline without opening the app: the whole point of the button.
    nonisolated func provider(_ provider: CXProvider, perform action: CXEndCallAction) {
        let uuid = action.callUUID
        action.fulfill()
        Task { @MainActor [self] in
            if let callId = reported[uuid] {
                reported[uuid] = nil
                handlers.end(callId)
            }
        }
    }

    nonisolated func provider(_ provider: CXProvider, perform action: CXSetMutedCallAction) {
        let uuid = action.callUUID
        let muted = action.isMuted
        action.fulfill()
        Task { @MainActor [self] in
            if let callId = reported[uuid] {
                handlers.setMuted(callId, muted)
            }
        }
    }

    nonisolated func provider(_ provider: CXProvider, didActivate audioSession: AVAudioSession) {
        Task { @MainActor [self] in
            handlers.sessionActivated()
        }
    }

    nonisolated func provider(_ provider: CXProvider, didDeactivate audioSession: AVAudioSession) {}
}
