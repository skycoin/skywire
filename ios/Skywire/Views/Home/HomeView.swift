import CoreBridge
import CoreClient
import SwiftUI
import UIKit

/// Connect, the core's state, and the visor card (Android: HomeScreen). No top bar.
struct HomeView: View {
    @EnvironmentObject private var app: AppModel
    @StateObject private var model = HomeModel()

    var body: some View {
        ScrollView {
            VStack(spacing: 0) {
                Color.clear.frame(height: 24)
                StatusLine()
                ConnectButton().padding(.top, 20)
                StatusCaption(pollError: model.error).padding(.top, 16)
                if app.connected, let summary = model.summary {
                    VisorCard(summary: summary, health: model.health).padding(.top, 24)
                }
                Color.clear.frame(height: 24)
            }
            .padding(24)
            .frame(maxWidth: .infinity)
        }
        .background(Color.skyBackground)
        .task(id: app.connected) { await model.poll(app) }
    }
}

/// The dot and the word for where the core is.
private struct StatusLine: View {
    @EnvironmentObject private var app: AppModel

    var body: some View {
        let (label, color): (LocalizedStringKey, Color) = switch app.coreState {
        case .running where app.apiUp: (L10n.key("state_connected"), .skySuccess)
        case .starting, .running: (L10n.key("state_starting"), .skyWarning)
        case .stopping: (L10n.key("state_stopping"), .skyOnSurfaceVariant)
        case .failed: (L10n.key("home_error_start"), .skyError)
        case .stopped: (L10n.key("state_disconnected"), .skyOnSurfaceVariant)
        }
        HStack(spacing: 8) {
            StatusDot(color: color)
            Text(label).skyText(.titleMedium).foregroundStyle(Color.skyOnBackground)
        }
        .accessibilityElement(children: .combine)
        // For UI tests: the state in words no translation changes.
        .accessibilityIdentifier("core-state")
        .accessibilityValue(Text(verbatim: app.connected ? "connected" : app.coreState.description))
    }
}

/// The 180 pt disc: the gradient asks to connect; a quiet tonal disc offers Disconnect.
private struct ConnectButton: View {
    @EnvironmentObject private var app: AppModel
    @EnvironmentObject private var notifications: NotificationBridge

    var body: some View {
        let state = app.coreState
        // A start can take a minute on a slow network: Disconnect aborts it (Android's Running-not-up).
        let tonal = state == .starting || state == .running
        let busy = state == .stopping
        ZStack {
            if app.connected {
                PulseRing(size: 180)
            }
            Button {
                if tonal {
                    app.disconnect()
                } else {
                    app.connect()
                    // Asked here, as Android asks at Connect; once per install.
                    notifications.requestAuthorization()
                }
            } label: {
                ZStack {
                    if tonal {
                        Circle().fill(Color.skySecondaryContainer)
                    } else {
                        Circle().fill(SkyGradient.button).skyElevation(10)
                    }
                    if busy {
                        MaterialSpinner(size: 44, stroke: 4, color: .white)
                    } else if tonal && !app.connected {
                        VStack(spacing: 10) {
                            MaterialSpinner(size: 28, stroke: 3)
                            Text("disconnect").skyText(.titleMedium).foregroundStyle(Color.skyOnSecondaryContainer)
                        }
                    } else {
                        Text(tonal ? L10n.key("disconnect") : L10n.key("connect"))
                            .skyText(.titleLarge)
                            .foregroundStyle(tonal ? Color.skyOnSecondaryContainer : .white)
                    }
                }
                .frame(width: 180, height: 180)
                .contentShape(Circle())
            }
            .buttonStyle(PressStyle(layer: tonal ? .skyOnSecondaryContainer : .white, shape: AnyShape(Circle())))
            .disabled(busy)
            .accessibilityIdentifier("connect-button")
        }
    }
}

/// What to expect, or what went wrong, under the button.
private struct StatusCaption: View {
    let pollError: String?
    @EnvironmentObject private var app: AppModel
    @EnvironmentObject private var navigator: Navigator

    var body: some View {
        VStack(spacing: 0) {
            if let error = app.lastError ?? (app.coreState == .failed ? "" : nil) {
                if !error.isEmpty {
                    Text(verbatim: error).skyText(.bodySmall).foregroundStyle(Color.skyError)
                }
                viewLogs
            } else {
                switch app.coreState {
                case .stopped:
                    Text("home_hint_disconnected").skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant)
                case .starting:
                    startingHint
                case .running where !app.apiUp:
                    startingHint
                case .running:
                    if let pollError {
                        Text(verbatim: pollError).skyText(.bodySmall).foregroundStyle(Color.skyError)
                    }
                default:
                    EmptyView()
                }
            }
        }
        .multilineTextAlignment(.center)
    }

    /// A first start can take minutes (dmsg discovery retries): something to watch.
    @ViewBuilder private var startingHint: some View {
        Text("home_hint_starting").skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant)
        viewLogs
    }

    private var viewLogs: some View {
        Button { navigator.push(.logs(.process)) } label: { Text("view_logs") }
            .buttonStyle(.tonal)
    }
}

/// Who this visor is, on what build, for how long; its diagnostics one tap away.
private struct VisorCard: View {
    let summary: VisorSummary
    let health: [ServiceHealthEntry]
    @State private var expanded = false
    @EnvironmentObject private var navigator: Navigator
    @EnvironmentObject private var dialogs: SkyDialogs

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text("visor_info_title").skyText(.titleMedium)
                Spacer()
                Button { navigator.push(.logs(.core)) } label: { Text("view_logs") }
                    .buttonStyle(.tonal)
            }
            Color.clear.frame(height: 4)
            let pk = summary.overview.localPK
            Button {
                UIPasteboard.general.string = pk
                dialogs.toast(Text("copied_to_clipboard"))
            } label: {
                SkyInfoRow(label: Text("visor_public_key"), value: Format.shortPK(pk), mono: true)
                    .contentShape(Rectangle())
            }
            .buttonStyle(PressStyle(layer: .skyOnSurface))
            .accessibilityHint(Text("visor_copy_hint"))
            SkyInfoRow(
                label: Text("visor_version"),
                value: [summary.overview.buildInfo?.version, summary.buildTag]
                    .compactMap { $0?.isEmpty == false ? $0 : nil }.joined(separator: " · ").nonEmpty ?? "—"
            )
            SkyInfoRow(label: Text("visor_uptime"), value: Format.uptime(summary.uptime))
            if expanded {
                diagnostics
            }
            Button {
                expanded.toggle()
            } label: {
                HStack(spacing: 0) {
                    Text(expanded ? L10n.key("visor_show_less") : L10n.key("visor_show_more"))
                    MaterialIcon(expanded ? MI.filledExpandLess : MI.filledExpandMore)
                }
            }
            .buttonStyle(.skyText)
            .frame(maxWidth: .infinity)
            .padding(.top, 4)
            .accessibilityIdentifier("visor-expand")
        }
        .padding(20)
        .foregroundStyle(Color.skyOnSurface)
        .background(Color.skySurfaceVariant, in: .sky(SkyRadius.medium))
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("visor-card")
    }

    private var divider: some View {
        SkyDivider(color: .skyContainerHighest).padding(.vertical, 10)
    }

    private func section(_ title: LocalizedStringKey) -> some View {
        Text(title).skyText(.labelMedium).foregroundStyle(Color.skyOnSurfaceVariant).padding(.bottom, 4)
    }

    private var dash: some View {
        Text(verbatim: "—").skyText(.bodyMedium)
    }

    @ViewBuilder private var diagnostics: some View {
        divider
        section(L10n.key("visor_transports"))
        let transports = summary.overview.transports
        if transports.isEmpty {
            dash
        } else {
            ForEach(Dictionary(grouping: transports) { $0.type.isEmpty ? "?" : $0.type }.sorted { $0.key < $1.key }, id: \.key) { type, entries in
                SkyInfoRow(label: Text(verbatim: type), value: "\(entries.count)")
            }
            SkyInfoRow(label: Text("visor_transports_total"), value: "\(transports.count)")
        }
        divider
        section(L10n.key("visor_dmsg_servers"))
        if summary.dmsgServers.isEmpty {
            dash
        } else {
            ForEach(Array(summary.dmsgServers.prefix(4).enumerated()), id: \.offset) { _, server in
                // The latency comes from an hourly self-ping: a server that joined since has none.
                let parts = [
                    server.protocol.isEmpty ? server.carrier : server.protocol,
                    server.latencyNS > 0 ? "\(server.latencyNS / 1_000_000) ms" : "",
                ].filter { !$0.isEmpty }
                SkyInfoRow(label: Text(verbatim: Format.shortPK(server.pk)), value: parts.joined(separator: " · ").nonEmpty ?? "—", mono: true)
            }
        }
        divider
        section(L10n.key("visor_service_health"))
        if health.isEmpty {
            dash
        } else {
            ForEach(Array(health.enumerated()), id: \.offset) { _, entry in
                SkyInfoRow(
                    label: Text(verbatim: entry.name),
                    value: !entry.status.isEmpty ? Format.health(entry.status) : (entry.error.isEmpty ? "?" : entry.error),
                    valueColor: entry.status.lowercased() == "healthy" ? .skySuccess : .skyOnSurfaceVariant
                )
            }
        }
    }
}

extension String {
    /// Nil for an empty string, so `?? "—"` reads as "or a dash".
    var nonEmpty: String? { isEmpty ? nil : self }
}
