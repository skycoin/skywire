import CoreClient
import SwiftUI

/// The call, over the whole window (Android: ui/call/CallScreen.kt): placing one, being rung
/// and being in one are the same screen; the buttons are what changes.
struct CallScreen: View {
    @EnvironmentObject private var model: CallModel

    var body: some View {
        let ui = model.ui
        ZStack {
            Color.skyBackground.ignoresSafeArea()
            if let peer = ui.peer {
                VStack(spacing: 0) {
                    VStack(spacing: 0) {
                        Color.clear.frame(height: 24)
                        MaterialIcon(MI.filledPerson, size: 56)
                            .foregroundStyle(Color.skyOnSurfaceVariant)
                            .frame(width: 112, height: 112)
                            .background(Color.skySurfaceVariant, in: Circle())
                        Text(verbatim: peer)
                            .font(.system(size: 22, weight: .bold, design: .monospaced))
                            .foregroundStyle(Color.skyOnSurface)
                            .multilineTextAlignment(.center)
                            .padding(.top, 20)
                            .accessibilityIdentifier("call-peer")
                        statusLine(ui).padding(.top, 8)
                    }
                    Spacer(minLength: 24)
                    controls(ui)
                    // The stated limit (playbook 6.5): nothing rings a suspended phone without push.
                    if ui.dialState == nil && !ui.connected && !ui.dialing {
                        Text("call_suspended_note")
                            .skyText(.bodySmall)
                            .foregroundStyle(Color.skyOnSurfaceVariant)
                            .multilineTextAlignment(.center)
                            .padding(.top, 16)
                    }
                }
                .padding(.horizontal, 32)
                .padding(.vertical, 48)
                .ignoresSafeArea()
            } else {
                MaterialSpinner()
            }
        }
        .foregroundStyle(Color.skyOnSurface)
    }

    /// Elapsed time, how the call being placed is going, why it ended, or "Incoming call".
    @ViewBuilder
    private func statusLine(_ ui: CallUiState) -> some View {
        if ui.connected {
            Text(verbatim: ui.elapsed)
                .skyText(.bodyLarge)
                .foregroundStyle(Color.skyOnSurfaceVariant)
                .accessibilityIdentifier("call-elapsed")
        } else if let dialState = ui.dialState {
            if dialState.ended {
                Text(Self.dialStatus(dialState)).skyText(.titleMedium).foregroundStyle(Color.callRed)
            } else {
                Text(Self.dialStatus(dialState)).skyText(.bodyLarge).foregroundStyle(Color.skyOnSurfaceVariant)
            }
        } else if ui.dialing {
            Text("call_dialing").skyText(.bodyLarge).foregroundStyle(Color.skyOnSurfaceVariant)
        } else {
            Text("call_incoming_title")
                .skyText(.bodyLarge)
                .foregroundStyle(Color.skyOnSurfaceVariant)
                .accessibilityIdentifier("call-incoming")
        }
    }

    @ViewBuilder
    private func controls(_ ui: CallUiState) -> some View {
        if ui.connected {
            HStack(spacing: 0) {
                Spacer(minLength: 0)
                CallButton(icon: ui.micMuted ? MI.filledMicOff : MI.filledMic,
                           label: L10n.key(ui.micMuted ? "call_unmute" : "call_mute"),
                           fill: ui.micMuted ? .skyPrimary : .skySurfaceVariant,
                           ink: ui.micMuted ? .skyOnPrimary : .skyOnSurface) { model.toggleMic() }
                Spacer(minLength: 0)
                CallButton(icon: MI.filledCallEnd, label: L10n.key("call_hang_up"), fill: .callRed) { model.hangUp() }
                    .accessibilityIdentifier("call-hang-up")
                Spacer(minLength: 0)
                CallButton(icon: ui.speakerphone ? MI.filledVolumeUp : MI.filledVolumeDown,
                           label: L10n.key("call_speaker"),
                           fill: ui.speakerphone ? .skyPrimary : .skySurfaceVariant,
                           ink: ui.speakerphone ? .skyOnPrimary : .skyOnSurface) { model.toggleSpeakerphone() }
                Spacer(minLength: 0)
            }
        } else if ui.dialState?.ended == true {
            // No Call again after a decline: calling straight back is not what the other side asked for.
            HStack(spacing: 0) {
                Spacer(minLength: 0)
                CallButton(icon: MI.filledClose, label: L10n.key("call_close"), fill: .skySurfaceVariant, ink: .skyOnSurface) { model.dismiss() }
                    .accessibilityIdentifier("call-close")
                if ui.dialState != .declined {
                    Spacer(minLength: 0)
                    CallButton(icon: MI.filledCall, label: L10n.key("call_again"), fill: .skySuccess) { model.callAgain() }
                        .accessibilityIdentifier("call-again")
                }
                Spacer(minLength: 0)
            }
        } else if ui.dialing {
            // Hang up while it rings cancels the invite.
            CallButton(icon: MI.filledCallEnd, label: L10n.key("call_hang_up"), fill: .callRed) { model.hangUp() }
                .accessibilityIdentifier("call-hang-up")
        } else {
            HStack(spacing: 0) {
                Spacer(minLength: 0)
                CallButton(icon: MI.filledCallEnd, label: L10n.key("call_decline"), fill: .callRed) { model.decline() }
                    .accessibilityIdentifier("call-decline")
                Spacer(minLength: 0)
                CallButton(icon: MI.filledCall, label: L10n.key("call_answer"), fill: .skySuccess) { model.answer() }
                    .accessibilityIdentifier("call-answer")
                Spacer(minLength: 0)
            }
        }
    }

    /// The line under the name while calling, in Android's words.
    private static func dialStatus(_ state: DialState) -> LocalizedStringKey {
        switch state {
        case .connecting: L10n.key("call_connecting")
        case .calling: L10n.key("call_dialing")
        case .ringing: L10n.key("call_ringing")
        case .offline: L10n.key("call_offline")
        case .declined: L10n.key("call_declined")
        case .busy: L10n.key("call_busy")
        case .noAnswer: L10n.key("call_no_answer")
        case .failed: L10n.key("call_failed")
        }
    }
}

/// CallButton: a 72 pt disc with a 32 pt glyph, and its caption under it.
private struct CallButton: View {
    let icon: String
    let label: LocalizedStringKey
    let fill: Color
    var ink: Color = .white
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(spacing: 8) {
                MaterialIcon(icon, size: 32)
                    .foregroundStyle(ink)
                    .frame(width: 72, height: 72)
                    .background(fill, in: Circle())
                Text(label).skyText(.labelMedium).foregroundStyle(Color.skyOnSurface)
            }
        }
        .buttonStyle(PressStyle())
        .accessibilityLabel(Text(label))
    }
}

private extension Color {
    /// Android's decline and hang-up red, the same in both themes.
    static let callRed = Color(hex: 0xD93B34)
}
