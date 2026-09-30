import CoreBridge
import CoreClient
import SwiftUI
import UIKit

/// Connect, the core's state, and the visor card (Android: HomeScreen).
struct HomeView: View {
    @EnvironmentObject private var app: AppModel
    @StateObject private var model = HomeModel()

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(spacing: 28) {
                    StatusLine()
                    ConnectButton()
                    StatusCaption()
                    if app.connected, let summary = model.summary {
                        VisorCard(summary: summary, health: model.health)
                    } else if app.connected, let error = model.error {
                        Text(error).font(.footnote).foregroundStyle(.red).multilineTextAlignment(.center)
                    }
                }
                .padding(.horizontal, 16)
                .padding(.vertical, 24)
                .frame(maxWidth: .infinity)
            }
            .navigationTitle(Text("app_name"))
            .toolbar {
                ToolbarItem(placement: .primaryAction) {
                    Menu {
                        Button {
                            model.reconnectDmsg(app)
                        } label: {
                            Label("home_reconnect_dmsg", systemImage: "arrow.triangle.2.circlepath")
                        }
                        .disabled(!app.connected)
                        .accessibilityIdentifier("home-reconnect-dmsg")
                        Button {
                            app.restartCore()
                        } label: {
                            Label("home_restart_core", systemImage: "restart")
                        }
                        .disabled(app.coreState != .running || app.busy)
                        .accessibilityIdentifier("home-restart")
                        NavigationLink(value: LogSource.core) {
                            Label("view_logs", systemImage: "doc.text.magnifyingglass")
                        }
                    } label: {
                        Image(systemName: "ellipsis.circle")
                            .accessibilityLabel(Text("home_actions"))
                    }
                    .accessibilityIdentifier("home-actions")
                }
            }
            .navigationDestination(for: LogSource.self) { LogsView(source: $0) }
            .task(id: app.connected) { await model.poll(app) }
            .alert(
                Text(model.notice ?? ""),
                isPresented: Binding(get: { model.notice != nil }, set: { if !$0 { model.notice = nil } })
            ) {
                Button("ok", role: .cancel) {}
            }
        }
    }
}

/// The dot and the word for where the core is.
private struct StatusLine: View {
    @EnvironmentObject private var app: AppModel

    var body: some View {
        let (label, color): (LocalizedStringKey, Color) = switch app.coreState {
        case .running where app.apiUp: (L10n.key("state_connected"), .success)
        case .starting, .running: (L10n.key("state_starting"), .warning)
        case .stopping: (L10n.key("state_stopping"), .secondary)
        case .failed: (L10n.key("home_error_start"), .red)
        case .stopped: (L10n.key("state_disconnected"), .secondary)
        }
        HStack(spacing: 8) {
            StatusDot(color: color)
            Text(label).font(.headline)
        }
        .accessibilityElement(children: .combine)
        // For UI tests: the state in words no translation changes.
        .accessibilityIdentifier("core-state")
        .accessibilityValue(Text(verbatim: app.connected ? "connected" : app.coreState.description))
    }
}

/// The big round button. Idle it asks to be pressed (the brand gradient);
/// running it is a quiet disc, so stopping never looks like the loud action.
/// While the core starts it offers Disconnect: a start can take a minute on a
/// slow network, and stopping aborts it.
private struct ConnectButton: View {
    @EnvironmentObject private var app: AppModel

    var body: some View {
        let stopping = app.coreState == .stopping
        let showDisconnect = app.coreState == .running || app.coreState == .starting
        Button {
            showDisconnect ? app.disconnect() : app.connect()
        } label: {
            ZStack {
                Circle()
                    .fill(showDisconnect
                        ? AnyShapeStyle(Color(UIColor.secondarySystemFill))
                        : AnyShapeStyle(LinearGradient(colors: [.skywire, Color(red: 0, green: 0.55, blue: 1)], startPoint: .top, endPoint: .bottom)))
                    .shadow(color: showDisconnect ? .clear : .skywire.opacity(0.35), radius: 12, y: 6)
                if stopping {
                    ProgressView().controlSize(.large)
                } else if showDisconnect && !app.connected {
                    VStack(spacing: 10) {
                        ProgressView()
                        Text("disconnect").font(.headline)
                    }
                    .foregroundStyle(.primary)
                } else {
                    Text(showDisconnect ? L10n.key("disconnect") : L10n.key("connect"))
                        .font(.title2.weight(.semibold))
                        .foregroundStyle(showDisconnect ? Color.primary : Color.white)
                }
            }
            .frame(width: 180, height: 180)
            .overlay {
                if app.connected {
                    Circle().stroke(Color.skywire.opacity(0.35), lineWidth: 6).frame(width: 196, height: 196)
                }
            }
        }
        .buttonStyle(.plain)
        .disabled(stopping)
        .accessibilityIdentifier("connect-button")
    }
}

/// What to expect, or what went wrong, under the button.
private struct StatusCaption: View {
    @EnvironmentObject private var app: AppModel

    var body: some View {
        VStack(spacing: 12) {
            if let error = app.lastError {
                Text(error).font(.footnote).foregroundStyle(.red).multilineTextAlignment(.center)
                NavigationLink(value: LogSource.process) { Text("view_logs") }.buttonStyle(.bordered)
            } else {
                switch app.coreState {
                case .stopped:
                    Text("home_hint_disconnected").foregroundStyle(.secondary)
                case .failed:
                    NavigationLink(value: LogSource.process) { Text("view_logs") }.buttonStyle(.bordered)
                case .starting:
                    startingHint
                case .running where !app.apiUp:
                    startingHint
                default:
                    EmptyView()
                }
            }
        }
        .multilineTextAlignment(.center)
        .font(.subheadline)
    }

    /// A first start can take minutes (dmsg discovery retries): something to
    /// watch beats a bare spinner.
    @ViewBuilder private var startingHint: some View {
        Text("home_hint_starting").foregroundStyle(.secondary)
        NavigationLink(value: LogSource.process) { Text("view_logs") }.buttonStyle(.bordered)
    }
}

/// Who this visor is, on what build, for how long; the diagnostics (its
/// transports, dmsg servers, service health) one tap away.
private struct VisorCard: View {
    let summary: VisorSummary
    let health: [ServiceHealthEntry]
    @State private var expanded = false
    @State private var copied = false

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text("visor_info_title").font(.headline)
                Spacer()
                NavigationLink(value: LogSource.core) { Text("view_logs") }.buttonStyle(.bordered).controlSize(.small)
            }
            let pk = summary.overview.localPK
            Button {
                UIPasteboard.general.string = pk
                copied = true
            } label: {
                InfoRow(label: Text("visor_public_key"), value: Format.shortPK(pk), monospaced: true)
            }
            .buttonStyle(.plain)
            .accessibilityHint(Text("visor_copy_hint"))
            InfoRow(
                label: Text("visor_version"),
                value: [summary.overview.buildInfo?.version, summary.buildTag]
                    .compactMap { $0?.isEmpty == false ? $0 : nil }.joined(separator: " · ").nonEmpty ?? "—"
            )
            InfoRow(label: Text("visor_uptime"), value: Format.uptime(summary.uptime))
            if expanded {
                diagnostics
            }
            Button {
                withAnimation { expanded.toggle() }
            } label: {
                Label(expanded ? L10n.key("visor_show_less") : L10n.key("visor_show_more"), systemImage: expanded ? "chevron.up" : "chevron.down")
                    .frame(maxWidth: .infinity)
            }
            .buttonStyle(.borderless)
            .padding(.top, 4)
            .accessibilityIdentifier("visor-expand")
        }
        .padding(20)
        .background(RoundedRectangle(cornerRadius: 16).fill(Color(UIColor.secondarySystemBackground)))
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("visor-card")
        .overlay(alignment: .top) {
            if copied {
                Text("copied_to_clipboard")
                    .font(.footnote.weight(.semibold))
                    .padding(.horizontal, 12).padding(.vertical, 6)
                    .background(Capsule().fill(.thinMaterial))
                    .offset(y: -14)
                    .task {
                        try? await Task.sleep(for: .seconds(1.5))
                        copied = false
                    }
            }
        }
    }

    @ViewBuilder private var diagnostics: some View {
        Divider()
        Text("visor_transports").font(.subheadline).foregroundStyle(.secondary)
        let transports = summary.overview.transports
        if transports.isEmpty {
            Text("—")
        } else {
            // By carrier: on a phone the question is which carriers came up
            // (dmsg always, stcpr and sudph rarely behind carrier NAT).
            ForEach(Dictionary(grouping: transports) { $0.type.isEmpty ? "?" : $0.type }.sorted { $0.key < $1.key }, id: \.key) { type, entries in
                InfoRow(label: Text(verbatim: type), value: "\(entries.count)")
            }
            InfoRow(label: Text("visor_transports_total"), value: "\(transports.count)")
        }
        Divider()
        Text("visor_dmsg_servers").font(.subheadline).foregroundStyle(.secondary)
        if summary.dmsgServers.isEmpty {
            Text("—")
        } else {
            ForEach(Array(summary.dmsgServers.prefix(6).enumerated()), id: \.offset) { _, server in
                // The latency comes from an hourly self-ping, so a server that
                // joined since has none; a bare "0 ms" would be false.
                let parts = [
                    server.protocol.isEmpty ? server.carrier : server.protocol,
                    server.latencyNS > 0 ? "\(server.latencyNS / 1_000_000) ms" : "",
                ].filter { !$0.isEmpty }
                InfoRow(label: Text(verbatim: Format.shortPK(server.pk)), value: parts.joined(separator: " · ").nonEmpty ?? "—", monospaced: true)
            }
        }
        Divider()
        Text("visor_service_health").font(.subheadline).foregroundStyle(.secondary)
        if health.isEmpty {
            Text("—")
        } else {
            ForEach(Array(health.enumerated()), id: \.offset) { _, entry in
                // The status is the visor's word, translated where known; an
                // error is the service's own text and stays as it arrived.
                InfoRow(
                    label: Text(verbatim: entry.name),
                    value: !entry.status.isEmpty ? Format.health(entry.status) : (entry.error.isEmpty ? "?" : entry.error),
                    valueColor: entry.status.lowercased() == "healthy" ? .success : .secondary
                )
            }
        }
    }
}

extension String {
    /// Nil for an empty string, so `?? "—"` reads as "or a dash".
    var nonEmpty: String? { isEmpty ? nil : self }
}
