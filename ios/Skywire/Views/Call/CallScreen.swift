import CoreClient
import SwiftUI

/// The call, full screen — in either direction, over every tab (Android:
/// ui/call/CallScreen.kt).
///
/// Placing one, being rung, and being in one are the same screen because
/// they are the same event seen at three moments; the controls are what
/// changes. It covers the Chat tab too: the embedded page has its own banner
/// and panel, but a call is not something to notice inside a conversation
/// list, it is what the phone is doing.
struct CallScreen: View {
    @EnvironmentObject private var model: CallModel

    var body: some View {
        let ui = model.ui
        Group {
            if let peer = ui.peer {
                VStack(spacing: 0) {
                    VStack(spacing: 8) {
                        Spacer(minLength: 32)
                        Image(systemName: "person.crop.circle.fill")
                            .font(.system(size: 96))
                            .foregroundStyle(.tertiary)
                            .accessibilityHidden(true)
                        Text(peer)
                            .font(.title2.weight(.semibold))
                            .monospaced()
                            .accessibilityIdentifier("call-peer")
                        statusLine(ui)
                    }
                    Spacer()
                    controls(ui)
                        .padding(.bottom, 24)
                    // The stated limit (playbook 6.5): nothing rings a
                    // suspended phone without push, and this screen is not
                    // the place to hide that.
                    if ui.dialState == nil && !ui.connected && !ui.dialing {
                        Text("call_suspended_note")
                            .font(.footnote)
                            .foregroundStyle(.secondary)
                            .padding(.bottom, 16)
                    }
                }
                .padding(.horizontal, 32)
            } else {
                ProgressView()
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Color(.systemBackground).ignoresSafeArea())
    }

    /// The line under the name: how the call is going, or why it ended.
    @ViewBuilder
    private func statusLine(_ ui: CallUiState) -> some View {
        let dialState = ui.dialState
        let outcome = dialState?.ended == true
        Group {
            if ui.connected {
                Text(ui.elapsed)
                    .font(.title3.monospacedDigit())
                    .accessibilityIdentifier("call-elapsed")
            } else if let dialState {
                Text(Self.dialStatus(dialState))
                    .font(outcome ? .title3.weight(.semibold) : .title3)
                    .foregroundStyle(outcome ? Color.decline : Color.secondary)
            } else if ui.dialing {
                Text("call_dialing")
                    .font(.title3)
                    .foregroundStyle(.secondary)
            } else {
                Text("call_incoming_title")
                    .font(.title3)
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("call-incoming")
            }
        }
    }

    @ViewBuilder
    private func controls(_ ui: CallUiState) -> some View {
        if ui.connected {
            HStack(spacing: 44) {
                CallButton(
                    systemImage: ui.micMuted ? "mic.slash.fill" : "mic.fill",
                    label: L10n.key(ui.micMuted ? "call_unmute" : "call_mute"),
                    tint: ui.micMuted ? .skywire : Color(.secondarySystemFill),
                    ink: ui.micMuted ? .white : .primary
                ) { model.toggleMic() }
                CallButton(
                    systemImage: "phone.down.fill",
                    label: L10n.key("call_hang_up"),
                    tint: .decline,
                    ink: .white
                ) { model.hangUp() }
                .accessibilityIdentifier("call-hang-up")
                CallButton(
                    systemImage: ui.speakerphone ? "speaker.wave.3.fill" : "speaker.wave.1.fill",
                    label: L10n.key("call_speaker"),
                    tint: ui.speakerphone ? .skywire : Color(.secondarySystemFill),
                    ink: ui.speakerphone ? .white : .primary
                ) { model.toggleSpeakerphone() }
            }
        } else if ui.dialState?.ended == true {
            // It ended unanswered: say so, and offer to try again — except
            // after a decline, where calling straight back is not what the
            // other side asked for.
            HStack(spacing: 44) {
                CallButton(
                    systemImage: "xmark",
                    label: L10n.key("call_close"),
                    tint: Color(.secondarySystemFill),
                    ink: .primary
                ) { model.dismiss() }
                .accessibilityIdentifier("call-close")
                if ui.dialState != .declined {
                    CallButton(
                        systemImage: "phone.fill",
                        label: L10n.key("call_again"),
                        tint: .answer,
                        ink: .white
                    ) { model.callAgain() }
                }
            }
        } else if ui.dialing {
            // Placing a call: the only thing to offer is giving up on it,
            // which cancels the invite rather than closing a session.
            CallButton(
                systemImage: "phone.down.fill",
                label: L10n.key("call_hang_up"),
                tint: .decline,
                ink: .white
            ) { model.hangUp() }
        } else {
            HStack(spacing: 44) {
                CallButton(
                    systemImage: "phone.down.fill",
                    label: L10n.key("call_decline"),
                    tint: .decline,
                    ink: .white
                ) { model.decline() }
                .accessibilityIdentifier("call-decline")
                CallButton(
                    systemImage: "phone.fill",
                    label: L10n.key("call_answer"),
                    tint: .answer,
                    ink: .white
                ) { model.answer() }
                .accessibilityIdentifier("call-answer")
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

/// One round control (Android: CallButton). `ink` is the glyph's colour on
/// it: only the filled buttons — Answer green, Hang up red — are dark
/// enough for white; the neutral ones sit on a fill near the background.
private struct CallButton: View {
    let systemImage: String
    let label: LocalizedStringKey
    let tint: Color
    let ink: Color
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(spacing: 8) {
                Image(systemName: systemImage)
                    .font(.title2)
                    .frame(width: 68, height: 68)
                    .background(Circle().fill(tint))
                    .foregroundStyle(ink)
                Text(label).font(.footnote)
            }
        }
        .buttonStyle(.plain)
        // The whole column is the button's label, matched by its shape.
        .accessibilityLabel(Text(label))
    }
}

// Answer/decline keep their conventional colours in both themes: on a call
// screen these two are read by colour before they are read at all, and the
// palette's error role shifts between themes.
private extension Color {
    static let answer = Color(uiColor: .systemGreen)
    static let decline = Color(red: 0.851, green: 0.231, blue: 0.204)
}
