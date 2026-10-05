import CoreClient
import SwiftUI
import UIKit

/// Fleet (Android: FleetScreen): the visors you run elsewhere, as they report in over DMSG,
/// once this phone lets them (`dmsg_ingest`).
struct FleetView: View {
    @EnvironmentObject private var app: AppModel
    @EnvironmentObject private var navigator: Navigator
    @EnvironmentObject private var dialogs: SkyDialogs
    @ObservedObject private var settings: AppSettings
    @StateObject private var model = FleetModel()
    @StateObject private var settingsModel = SettingsModel()

    init(settings: AppSettings) {
        self.settings = settings
    }

    var body: some View {
        VStack(spacing: 0) {
            // Fleet's ? is a plain icon that opens the add-a-visor sheet (Android).
            SkyTopBar(title: Text("app_fleet"), onBack: { navigator.back() }) {
                Button {
                    dialogs.presentSheet { AddVisorSheet(publicKey: app.publicKey) }
                } label: {
                    MaterialIcon(MI.outlinedHelpOutline).foregroundStyle(Color.skyOnBackground).frame(width: 48, height: 48)
                }
                .buttonStyle(PressStyle())
                .accessibilityLabel(Text("help_open"))
            }
            ScrollView {
                LazyVStack(spacing: 16) {
                    enableCard
                    if !settings.fleetEnabled {
                        SectionCard {
                            Text("fleet_off_title").skyText(.titleMedium)
                            Text("fleet_off_body").skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 8)
                            Text("fleet_off_reachable").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 12)
                        }
                    } else if !app.connected {
                        SectionCard {
                            Text(app.coreState == .stopped || app.coreState == .failed ? L10n.key("fleet_core_offline") : L10n.key("fleet_core_starting"))
                                .skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant)
                        }
                    } else {
                        visors
                    }
                }
                .padding(.horizontal, 20)
                .padding(.vertical, 16)
            }
        }
        .background(Color.skyBackground)
        .task(id: RunKey(connected: app.connected, enabled: settings.fleetEnabled)) { await model.run(app) }
        .onChange(of: model.message) { message in
            guard let message else { return }
            dialogs.snackbar(Text(verbatim: message))
            model.message = nil
        }
    }

    private struct RunKey: Equatable {
        let connected: Bool
        let enabled: Bool
    }

    private var enableCard: some View {
        SectionCard {
            HStack(spacing: 12) {
                VStack(alignment: .leading, spacing: 4) {
                    Text("fleet_enable").skyText(.titleMedium)
                    Text("fleet_enable_hint").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                Toggle(isOn: Binding(get: { settings.fleetEnabled }, set: { toggle($0) })) { Text("fleet_enable") }
                    .toggleStyle(.skySwitch)
                    .labelsHidden()
                    .disabled(app.busy)
                    .accessibilityIdentifier("fleet-screen-toggle")
            }
            if app.busy, app.coreState != .stopped {
                HStack(spacing: 10) {
                    MaterialSpinner(size: 14, stroke: 2)
                    Text("fleet_core_restarting").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                }
                .padding(.top, 10)
            }
        }
    }

    /// A running core restarts to read the setting: asked first. A stopped one just takes it.
    private func toggle(_ enabled: Bool) {
        guard app.coreState == .running || app.coreState == .starting else {
            settingsModel.setFleet(enabled, app)
            return
        }
        dialogs.show(SkyDialog(
            title: Text(enabled ? L10n.key("fleet_confirm_on_title") : L10n.key("fleet_confirm_off_title")),
            message: Text("fleet_confirm_restart_body"),
            actions: [
                SkyDialog.Action(label: Text("cancel")),
                SkyDialog.Action(label: Text("fleet_confirm_restart_action")) { settingsModel.setFleet(enabled, app) },
            ]
        ))
    }

    @ViewBuilder
    private var visors: some View {
        HStack {
            Text(verbatim: L10n.format("fleet_visors", model.visors.count)).skyText(.titleMedium)
            Spacer()
            if model.loading {
                MaterialSpinner(size: 20, stroke: 2).frame(width: 48, height: 48)
            } else {
                Button { Task { await model.load(app) } } label: {
                    MaterialIcon(MI.filledRefresh).foregroundStyle(Color.skyOnBackground).frame(width: 48, height: 48)
                }
                .buttonStyle(PressStyle())
                .accessibilityLabel(Text("fleet_refresh"))
            }
        }
        ForEach(model.visors, id: \.overview.localPK) { visor in
            let pk = visor.overview.localPK
            VisorCard(visor: visor, name: model.names[pk], restarting: model.restarting == pk,
                      logs: { navigator.push(.logs(.visor(pk))) },
                      restart: { confirmRestart(visor) },
                      rename: { rename(visor) })
        }
        if let error = model.error {
            Text(verbatim: error).skyText(.bodySmall).foregroundStyle(Color.skyError).frame(maxWidth: .infinity, alignment: .leading)
        }
        if model.visors.isEmpty, !model.loading {
            Text("fleet_visors_none").skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant)
                .multilineTextAlignment(.center).frame(maxWidth: .infinity)
        }
    }

    private func confirmRestart(_ visor: VisorSummary) {
        let pk = visor.overview.localPK
        let label = model.label(pk)
        dialogs.show(SkyDialog(
            title: Text("fleet_restart"),
            message: Text(verbatim: L10n.format("fleet_restart_confirm", label)),
            actions: [
                SkyDialog.Action(label: Text("cancel")),
                SkyDialog.Action(label: Text("fleet_restart")) { model.restart(pk, label: label, app) },
            ]
        ))
    }

    private func rename(_ visor: VisorSummary) {
        let pk = visor.overview.localPK
        dialogs.showCustom { RenameDialog(pk: pk, current: model.names[pk] ?? "") { model.rename(pk, $0) } }
    }
}

/// One remote visor: name or key, up or down, version, uptime, transports, health, Logs, Restart.
private struct VisorCard: View {
    let visor: VisorSummary
    let name: String?
    let restarting: Bool
    let logs: () -> Void
    let restart: () -> Void
    let rename: () -> Void
    @EnvironmentObject private var dialogs: SkyDialogs

    var body: some View {
        let pk = visor.overview.localPK
        SectionCard {
            HStack(spacing: 0) {
                StatusDot(color: visor.online ? .skySuccess : .skyOnSurfaceVariant)
                VStack(alignment: .leading, spacing: 0) {
                    if let name {
                        Text(verbatim: name).skyText(.titleMedium)
                    } else {
                        Text(verbatim: Format.shortPK(pk)).skyText(.titleMedium, mono: true)
                            .onTapGesture { copy(pk) }
                    }
                    (visor.online ? Text("state_connected") : Text("fleet_state_offline"))
                        .skyText(.bodySmall)
                        .foregroundStyle(visor.online ? Color.skySuccess : Color.skyOnSurfaceVariant)
                }
                .padding(.leading, 8)
                .frame(maxWidth: .infinity, alignment: .leading)
                if restarting {
                    MaterialSpinner(size: 16, stroke: 2).padding(.trailing, 8)
                }
                Button(action: rename) {
                    MaterialIcon(MI.outlinedEdit, size: 18).foregroundStyle(Color.skyOnSurfaceVariant).frame(width: 32, height: 32)
                }
                .buttonStyle(PressStyle())
                .accessibilityLabel(Text("fleet_rename"))
            }
            if name != nil {
                Text(verbatim: Format.shortPK(pk)).skyText(.bodySmall, mono: true).foregroundStyle(Color.skyOnSurfaceVariant)
                    .padding(.top, 4)
                    .onTapGesture { copy(pk) }
            }
            Color.clear.frame(height: 8)
            SkyInfoRow(label: Text("visor_version"),
                       value: [visor.overview.buildInfo?.version, visor.buildTag]
                           .compactMap { $0?.isEmpty == false ? $0 : nil }.joined(separator: " · ").nonEmpty ?? "—")
            SkyInfoRow(label: Text("visor_uptime"), value: Format.uptime(visor.uptime))
            transports
            health
            if stale, let ago = lastSeenAgo {
                Text(verbatim: L10n.format("fleet_last_seen", Format.duration(ago)))
                    .skyText(.labelSmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 4)
            }
            HStack(spacing: 0) {
                Spacer()
                Button(action: logs) { Text("logs_title") }.buttonStyle(.tonal).disabled(!visor.online)
                Button(action: restart) { Text("fleet_restart") }.buttonStyle(.tonal).disabled(!visor.online || restarting)
            }
            .padding(.top, 8)
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("fleet-visor")
    }

    @ViewBuilder private var transports: some View {
        let all = visor.overview.transports
        if all.isEmpty {
            SkyInfoRow(label: Text("visor_transports"), value: "—")
        } else {
            Text("visor_transports").skyText(.labelMedium).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 4)
            ForEach(Dictionary(grouping: all) { $0.type.isEmpty ? "?" : $0.type }.sorted { $0.key < $1.key }, id: \.key) { type, entries in
                SkyInfoRow(label: Text(verbatim: "   " + type), value: "\(entries.count)")
            }
            SkyInfoRow(label: Text(verbatim: "   " + L10n.text("visor_transports_total")), value: "\(all.count)")
            Color.clear.frame(height: 4)
        }
    }

    private var health: some View {
        let status = visor.health?.servicesHealth ?? ""
        let value: String = !visor.online ? "—" : (status.isEmpty ? L10n.text("fleet_health_unknown") : Format.health(status))
        return SkyInfoRow(label: Text("fleet_health"), value: value,
                          valueColor: visor.online && status.lowercased() == "healthy" ? .skySuccess : .skyOnSurfaceVariant)
    }

    private var lastSeenAgo: Double? {
        guard let seen = visor.lastSeenAt, let date = ISO8601DateFormatter().date(from: seen) else { return nil }
        return max(0, Date().timeIntervalSince(date))
    }

    /// Offline, or last seen 45 s or more ago: everything shown is from then.
    private var stale: Bool { !visor.online || (lastSeenAgo ?? 0) >= 45 }

    private func copy(_ text: String) {
        UIPasteboard.general.string = text
        dialogs.toast(Text("copied_to_clipboard"))
    }
}

/// Name this visor: kept on this phone only; an empty name removes it.
private struct RenameDialog: View {
    let pk: String
    let current: String
    let save: (String) -> Void
    @State private var text = ""

    var body: some View {
        DialogFrame(title: Text("fleet_rename"), confirm: L10n.key("save"), onConfirm: { save(text) }) {
            VStack(alignment: .leading, spacing: 12) {
                Text(verbatim: L10n.format("fleet_rename_hint", Format.shortPK(pk))).skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                SkyOutlinedTextField(label: L10n.key("fleet_rename_label"), text: $text, notch: .skyContainerHigh)
                    .onChange(of: text) { value in if value.count > 40 { text = String(value.prefix(40)) } }
            }
        }
        .onAppear { text = current }
    }
}

/// Fleet's ?: what Fleet is, then how to add a visor (this phone's key and the command).
private struct AddVisorSheet: View {
    let publicKey: String?
    @EnvironmentObject private var dialogs: SkyDialogs

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("help_fleet_title").skyText(.titleMedium)
            Text("help_fleet_body").skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 8)
            Text("fleet_add_title").skyText(.titleMedium).padding(.top, 20)
            Text("fleet_add_body").skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 8)
            if let pk = publicKey {
                let command = L10n.format("fleet_add_command", pk)
                Button { copy(pk) } label: {
                    SkyInfoRow(label: Text("fleet_this_phone"), value: Format.shortPK(pk), mono: true).contentShape(Rectangle())
                }
                .buttonStyle(PressStyle())
                .padding(.top, 16)
                Button { copy(command) } label: {
                    Text(verbatim: Format.breakable(command)).skyText(.bodySmall, mono: true)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(.horizontal, 12)
                        .padding(.vertical, 10)
                        .background(Color.skySurfaceVariant, in: .sky(10))
                }
                .buttonStyle(PressStyle())
                .padding(.top, 10)
                .accessibilityIdentifier("fleet-add-command")
                Text("fleet_add_tap_to_copy").skyText(.labelSmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 6)
                Text("fleet_add_then_restart").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 12)
            } else {
                Text("fleet_add_pk_pending").skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 16)
            }
        }
        .padding(.horizontal, 24)
        .padding(.bottom, 24)
    }

    private func copy(_ text: String) {
        UIPasteboard.general.string = text
        dialogs.toast(Text("copied_to_clipboard"))
    }
}
