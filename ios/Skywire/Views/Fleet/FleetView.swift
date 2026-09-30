import CoreClient
import SwiftUI
import UIKit

/// Fleet (Android: FleetScreen): the visors you run elsewhere, as they report
/// in over DMSG, once this phone lets them (the opt-in, `dmsg_ingest`).
struct FleetView: View {
    @EnvironmentObject private var app: AppModel
    @ObservedObject private var settings: AppSettings
    @StateObject private var model = FleetModel()
    @StateObject private var settingsModel = SettingsModel()
    @State private var confirming: Bool?
    @State private var restartTarget: VisorSummary?
    @State private var renameTarget: VisorSummary?
    @State private var nameText = ""
    @State private var copied = false

    init(settings: AppSettings) {
        self.settings = settings
    }

    var body: some View {
        List {
            Section {
                Toggle(isOn: Binding(get: { settings.fleetEnabled }, set: { confirming = $0 })) {
                    Text("fleet_enable")
                }
                .disabled(app.busy)
                .accessibilityIdentifier("fleet-screen-toggle")
                if app.busy, app.coreState != .stopped {
                    Text("fleet_core_restarting").font(.footnote).foregroundStyle(.secondary)
                }
            } footer: {
                Text("fleet_enable_hint")
            }
            if !settings.fleetEnabled {
                Section {
                    Text("fleet_off_body")
                    Text("fleet_off_reachable").font(.footnote).foregroundStyle(.secondary)
                } header: {
                    Text("fleet_off_title")
                }
            }
            addSection
            if settings.fleetEnabled {
                visorsSection
            }
        }
        .navigationTitle(Text("app_fleet"))
        .refreshable { await model.load(app) }
        .task(id: RunKey(connected: app.connected, enabled: settings.fleetEnabled)) { await model.run(app) }
        .overlay(alignment: .top) {
            if copied {
                Text("copied_to_clipboard")
                    .font(.footnote.weight(.semibold))
                    .padding(.horizontal, 12).padding(.vertical, 6)
                    .background(Capsule().fill(.thinMaterial))
                    .task {
                        try? await Task.sleep(for: .seconds(1.5))
                        copied = false
                    }
            }
        }
        // The switch restarts a running core: asked first, as on Android.
        .confirmationDialog(
            Text(confirming == true ? L10n.key("fleet_confirm_on_title") : L10n.key("fleet_confirm_off_title")),
            isPresented: Binding(get: { confirming != nil }, set: { if !$0 { confirming = nil } }),
            titleVisibility: .visible
        ) {
            Button("fleet_confirm_restart_action") {
                if let enabled = confirming { settingsModel.setFleet(enabled, app) }
                confirming = nil
            }
            Button("cancel", role: .cancel) { confirming = nil }
        } message: {
            Text("fleet_confirm_restart_body")
        }
        .alert(
            Text(restartTarget.map { L10n.format("fleet_restart_confirm", model.label($0.overview.localPK)) } ?? ""),
            isPresented: Binding(get: { restartTarget != nil }, set: { if !$0 { restartTarget = nil } })
        ) {
            Button("fleet_restart", role: .destructive) {
                if let visor = restartTarget {
                    model.restart(visor.overview.localPK, label: model.label(visor.overview.localPK), app)
                }
                restartTarget = nil
            }
            Button("cancel", role: .cancel) { restartTarget = nil }
        }
        .alert(Text("fleet_rename"), isPresented: Binding(get: { renameTarget != nil }, set: { if !$0 { renameTarget = nil } })) {
            TextField(L10n.text("fleet_rename_label"), text: $nameText)
            Button("cancel", role: .cancel) { renameTarget = nil }
            Button("save") {
                if let visor = renameTarget { model.rename(visor.overview.localPK, nameText) }
                renameTarget = nil
            }
        } message: {
            Text(renameTarget.map { L10n.format("fleet_rename_hint", Format.shortPK($0.overview.localPK)) } ?? "")
        }
        .alert(
            Text(model.message ?? ""),
            isPresented: Binding(get: { model.message != nil }, set: { if !$0 { model.message = nil } })
        ) {
            Button("ok", role: .cancel) {}
        }
    }

    private struct RunKey: Equatable {
        let connected: Bool
        let enabled: Bool
    }

    /// What the other machine needs: this phone's key in its config.
    private var addSection: some View {
        Section {
            Text("fleet_add_body")
            if let pk = app.publicKey {
                let command = L10n.format("fleet_add_command", pk)
                Button {
                    UIPasteboard.general.string = command
                    copied = true
                } label: {
                    VStack(alignment: .leading, spacing: 4) {
                        Text(verbatim: Format.breakable(command)).font(.footnote.monospaced()).foregroundStyle(.primary)
                        Text("fleet_add_tap_to_copy").font(.caption).foregroundStyle(.secondary)
                    }
                }
                .buttonStyle(.plain)
                .accessibilityIdentifier("fleet-add-command")
            } else {
                Text("fleet_add_pk_pending").font(.footnote).foregroundStyle(.secondary)
            }
            Text("fleet_add_then_restart").font(.footnote).foregroundStyle(.secondary)
        } header: {
            Text("fleet_add_title")
        }
    }

    @ViewBuilder
    private var visorsSection: some View {
        Section {
            if !app.connected {
                Text(app.coreState == .stopped ? L10n.key("fleet_core_offline") : L10n.key("fleet_core_starting"))
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            } else if model.loading {
                HStack {
                    Spacer()
                    ProgressView()
                    Spacer()
                }
            } else if model.visors.isEmpty {
                Text("fleet_visors_none").font(.footnote).foregroundStyle(.secondary)
            }
            ForEach(model.visors, id: \.overview.localPK) { visor in
                FleetRow(visor: visor, name: model.names[visor.overview.localPK], restarting: model.restarting == visor.overview.localPK)
                    .contextMenu {
                        Button {
                            restartTarget = visor
                        } label: {
                            Label("fleet_restart", systemImage: "restart")
                        }
                        .disabled(!visor.online)
                        Button {
                            nameText = model.names[visor.overview.localPK] ?? ""
                            renameTarget = visor
                        } label: {
                            Label("fleet_rename", systemImage: "pencil")
                        }
                    }
                    .swipeActions {
                        Button("fleet_restart") { restartTarget = visor }
                            .tint(.orange)
                            .disabled(!visor.online)
                    }
            }
            if let error = model.error {
                Text(error).font(.footnote).foregroundStyle(.red)
            }
        } header: {
            HStack {
                Text(L10n.format("fleet_visors", model.visors.count))
                Spacer()
                Button {
                    Task { await model.load(app) }
                } label: {
                    Image(systemName: "arrow.clockwise")
                        .accessibilityLabel(Text("fleet_refresh"))
                }
                .disabled(!app.connected)
            }
        }
    }
}

/// One remote visor: its name or key, up or down, version, uptime and health,
/// or when it last answered.
private struct FleetRow: View {
    let visor: VisorSummary
    let name: String?
    let restarting: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 8) {
                StatusDot(color: visor.online ? .success : .secondary)
                Text(name ?? Format.shortPK(visor.overview.localPK))
                    .font(name == nil ? .body.monospaced() : .body.weight(.semibold))
                Spacer()
                if restarting {
                    ProgressView().controlSize(.small)
                } else if !visor.online {
                    Text("fleet_state_offline").font(.footnote).foregroundStyle(.secondary)
                }
            }
            if name != nil {
                Text(Format.shortPK(visor.overview.localPK)).font(.caption.monospaced()).foregroundStyle(.secondary)
            }
            if let version = visor.overview.buildInfo?.version, !version.isEmpty {
                InfoRow(label: Text("visor_version"), value: version)
            }
            InfoRow(label: Text("visor_uptime"), value: Format.uptime(visor.uptime))
            InfoRow(
                label: Text("fleet_health"),
                value: visor.health.map { Format.health($0.servicesHealth) } ?? L10n.text("fleet_health_unknown")
            )
            if !visor.online, let seen = visor.lastSeenAt, let date = ISO8601DateFormatter().date(from: seen) {
                let ago = Format.uptime(max(0, Date().timeIntervalSince(date)))
                Text(L10n.format("fleet_last_seen", ago)).font(.caption).foregroundStyle(.secondary)
            }
        }
        .padding(.vertical, 4)
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("fleet-visor")
    }
}
