import CoreClient
import CoreImage.CIFilterBuiltins
import SwiftUI
import UIKit

/// Identity, routing, the network switches, remote management, the app lock,
/// language, diagnostics (Android: SettingsScreen, with the routing controls
/// the SkySOCKS and SkyVPN screens also carry).
struct SettingsView: View {
    @EnvironmentObject private var app: AppModel
    @EnvironmentObject private var lock: AppLock
    @StateObject private var model = SettingsModel()
    @State private var granting = false

    var body: some View {
        NavigationStack {
            Form {
                IdentitySection()
                RoutingSection(model: model)
                NetworkSection(model: model)
                RemoteSection(model: model, granting: $granting)
                AppSection()
                Section {
                    NavigationLink(value: SettingsRoute.diagnostics) {
                        Label("settings_diagnostics", systemImage: "stethoscope")
                    }
                    .accessibilityIdentifier("diagnostics-link")
                } footer: {
                    Text("settings_diagnostics_hint")
                }
                AboutSection(model: model)
            }
            .navigationTitle(Text("tab_settings"))
            // Every push in this stack is by value, so a link on a pushed
            // screen (Diagnostics' log sources) finds its destination too.
            .navigationDestination(for: SettingsRoute.self) { route in
                switch route {
                case .diagnostics: DiagnosticsView(model: model)
                case .transport: TransportChoiceView(model: model)
                }
            }
            .navigationDestination(for: LogSource.self) { LogsView(source: $0) }
            .task(id: app.connected) { await model.load(app) }
            .sheet(isPresented: $granting) {
                GrantSheet(model: model)
            }
            .alert(
                Text(model.notice ?? ""),
                isPresented: Binding(get: { model.notice != nil }, set: { if !$0 { model.notice = nil } })
            ) {
                Button("ok", role: .cancel) {}
            }
        }
    }
}

/// This phone's key, as text to copy and as a QR code to scan.
private struct IdentitySection: View {
    @EnvironmentObject private var app: AppModel

    var body: some View {
        Section {
            if let pk = app.publicKey {
                HStack {
                    Text(verbatim: Format.shortPK(pk)).font(.body.monospaced())
                    Spacer()
                    Button {
                        UIPasteboard.general.string = pk
                    } label: {
                        Image(systemName: "doc.on.doc")
                    }
                    .accessibilityLabel(Text("settings_copy_pk"))
                }
                if let qr = QRCode.image(for: pk) {
                    Image(uiImage: qr)
                        .interpolation(.none)
                        .resizable()
                        .scaledToFit()
                        .frame(width: 200, height: 200)
                        .frame(maxWidth: .infinity)
                        .accessibilityLabel(Text("settings_pk_qr"))
                }
            } else {
                Text("settings_identity_none").foregroundStyle(.secondary)
            }
        } header: {
            Text("settings_identity")
        }
    }
}

/// Settings' pushed screens.
enum SettingsRoute: Hashable {
    case diagnostics
    case transport
}

/// The transport tried first and the route length.
private struct RoutingSection: View {
    @EnvironmentObject private var app: AppModel
    @ObservedObject var model: SettingsModel

    var body: some View {
        Section {
            NavigationLink(value: SettingsRoute.transport) {
                HStack {
                    Text("transport_sheet_title")
                    Spacer()
                    Text(TransportChoiceView.name(app.settings.transportPrimary)).foregroundStyle(.secondary)
                }
            }
            .accessibilityIdentifier("transport-link")
        } header: {
            Text("transport_title")
        } footer: {
            Text("transport_hint")
        }

        Section {
            ForEach(SettingsModel.hopChoices, id: \.self) { hops in
                let needsTransports = hops > 1 && !app.settings.publicAutoconnect
                Button {
                    model.setMinHops(hops, app)
                } label: {
                    HStack {
                        Text(Self.label(hops))
                        Text(verbatim: "· \(hops)").foregroundStyle(.secondary)
                        Spacer()
                        if model.minHops == hops {
                            Image(systemName: "checkmark").foregroundStyle(Color.skywire)
                        }
                    }
                }
                .disabled(model.minHops == nil || needsTransports)
            }
        } header: {
            Text("hops_title")
        } footer: {
            Text(hopsHint)
        }
    }

    private var hopsHint: LocalizedStringKey {
        guard let hops = model.minHops else { return L10n.key("hops_hint_unknown") }
        if !app.settings.publicAutoconnect { return L10n.key("hops_hint_needs_autoconnect") }
        return hops > 1 ? L10n.key("hops_hint_multi") : L10n.key("hops_hint_direct")
    }

    private static func label(_ hops: Int) -> LocalizedStringKey {
        switch hops {
        case 1: L10n.key("hops_label_fastest")
        case 2: L10n.key("hops_label_balanced")
        default: L10n.key("hops_label_private")
        }
    }
}

/// The primary transport, one of the three the visor can create on demand,
/// each with what it needs from the network (Android: the transport sheet).
struct TransportChoiceView: View {
    @EnvironmentObject private var app: AppModel
    @ObservedObject var model: SettingsModel

    var body: some View {
        Form {
            Section {
                ForEach(TransportPreference.choices, id: \.self) { type in
                    Button {
                        model.setTransportPrimary(type, app)
                    } label: {
                        HStack(alignment: .firstTextBaseline) {
                            VStack(alignment: .leading, spacing: 4) {
                                Text(Self.name(type)).foregroundStyle(Color.primary)
                                Text(Self.hint(type)).font(.footnote).foregroundStyle(Color.secondary)
                                if type == TransportPreference.defaultPrimary {
                                    Text("transport_recommended").font(.footnote).foregroundStyle(Color.skywire)
                                }
                            }
                            Spacer()
                            if app.settings.transportPrimary == type {
                                Image(systemName: "checkmark").foregroundStyle(Color.skywire)
                            }
                        }
                    }
                }
            } footer: {
                Text("transport_sheet_hint")
            }
        }
        .navigationTitle(Text("transport_sheet_title"))
        .navigationBarTitleDisplayMode(.inline)
    }

    static func name(_ type: String) -> LocalizedStringKey {
        switch type {
        case TransportPreference.stcpr: L10n.key("transport_stcpr")
        case TransportPreference.sudph: L10n.key("transport_sudph")
        default: L10n.key("transport_dmsg")
        }
    }

    private static func hint(_ type: String) -> LocalizedStringKey {
        switch type {
        case TransportPreference.stcpr: L10n.key("transport_stcpr_hint")
        case TransportPreference.sudph: L10n.key("transport_sudph_hint")
        default: L10n.key("transport_dmsg_hint")
        }
    }
}

/// The two switches that change what the core opens to the network.
private struct NetworkSection: View {
    @EnvironmentObject private var app: AppModel
    @ObservedObject var model: SettingsModel

    var body: some View {
        Section {
            Toggle(isOn: Binding(get: { app.settings.publicAutoconnect }, set: { app.settings.publicAutoconnect = $0 })) {
                Text("settings_autoconnect")
            }
        } footer: {
            Text("settings_autoconnect_hint") + Text(verbatim: " ") + Text("settings_autoconnect_restart")
        }
        Section {
            Toggle(isOn: Binding(get: { app.settings.fleetEnabled }, set: { model.setFleet($0, app) })) {
                Text("fleet_enable")
            }
            .accessibilityIdentifier("fleet-toggle")
        } footer: {
            Text("fleet_enable_hint") + Text(verbatim: " ") + Text("settings_fleet_restart")
        }
    }
}

/// The one key that may drive this visor from elsewhere.
private struct RemoteSection: View {
    @EnvironmentObject private var app: AppModel
    @ObservedObject var model: SettingsModel
    @Binding var granting: Bool

    var body: some View {
        Section {
            if let pk = app.settings.remoteManagementPK {
                HStack {
                    Text(verbatim: Format.shortPK(pk)).font(.body.monospaced())
                    Spacer()
                    Button(role: .destructive) {
                        model.revokeRemoteManagement(app)
                    } label: {
                        Text("settings_remote_revoke")
                    }
                }
            } else {
                Button {
                    granting = true
                } label: {
                    Text("settings_remote_grant")
                }
            }
        } header: {
            Text("settings_remote")
        } footer: {
            Text("settings_remote_hint") + Text(verbatim: " ") + Text("settings_remote_restart")
        }
    }
}

private struct GrantSheet: View {
    @EnvironmentObject private var app: AppModel
    @ObservedObject var model: SettingsModel
    @Environment(\.dismiss) private var dismiss
    @State private var key = ""
    @State private var invalid = false

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField(text: $key, axis: .vertical) {
                        Text("settings_remote_label")
                    }
                    .font(.body.monospaced())
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .onChange(of: key) { _ in invalid = false }
                    if invalid {
                        Text("settings_remote_invalid").foregroundStyle(.red).font(.footnote)
                    }
                } footer: {
                    Text("settings_remote_dialog_hint")
                }
            }
            .navigationTitle(Text("settings_remote_dialog_title"))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("cancel") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("settings_remote_grant_confirm") {
                        if model.grantRemoteManagement(key, app) {
                            dismiss()
                        } else {
                            invalid = true
                        }
                    }
                    .disabled(key.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }
            }
        }
        .presentationDetents([.medium])
    }
}

/// The app lock and the language.
private struct AppSection: View {
    @EnvironmentObject private var app: AppModel
    @EnvironmentObject private var lock: AppLock

    var body: some View {
        Section {
            Toggle(isOn: Binding(get: { app.settings.appLockEnabled }, set: { setLock($0) })) {
                Text("settings_app_lock")
            }
            .disabled(!AppLock.available)
            .accessibilityIdentifier("app-lock-toggle")
        } footer: {
            Text(AppLock.available ? L10n.key("settings_app_lock_hint_ios") : L10n.key("settings_app_lock_unavailable"))
        }
        Section {
            Button {
                if let url = URL(string: UIApplication.openSettingsURLString) {
                    UIApplication.shared.open(url)
                }
            } label: {
                HStack {
                    Text("settings_language").foregroundStyle(Color.primary)
                    Spacer()
                    Text(verbatim: Self.currentLanguage).foregroundStyle(.secondary)
                    Image(systemName: "arrow.up.forward.app").foregroundStyle(.secondary)
                }
            }
        } footer: {
            Text("settings_language_hint_ios")
        }
    }

    /// Turning the lock on or off asks for the check first, so it can be
    /// neither set up nor removed by someone who cannot pass it.
    private func setLock(_ enabled: Bool) {
        Task {
            let reason = L10n.text(enabled ? "settings_lock_enable_prompt" : "settings_lock_disable_prompt")
            if await lock.confirm(reason: reason) {
                app.settings.appLockEnabled = enabled
            }
        }
    }

    /// The language the app is drawn in, in that language.
    private static var currentLanguage: String {
        let code = Bundle.main.preferredLocalizations.first ?? "en"
        let locale = Locale(identifier: code)
        return locale.localizedString(forIdentifier: code)?.localizedCapitalized ?? code
    }
}

private struct AboutSection: View {
    @ObservedObject var model: SettingsModel

    var body: some View {
        Section {
            InfoRow(label: Text("settings_app_version"), value: Self.appVersion)
            InfoRow(label: Text("settings_core_version"), value: model.coreVersion ?? "—")
        } header: {
            Text("settings_about")
        }
    }

    private static var appVersion: String {
        let info = Bundle.main.infoDictionary
        let version = info?["CFBundleShortVersionString"] as? String ?? "?"
        let build = info?["CFBundleVersion"] as? String ?? "?"
        return "\(version) (\(build))"
    }
}

/// A QR code for a string, drawn at one pixel per module (scaled up with
/// nearest-neighbour by the view).
enum QRCode {
    static func image(for text: String) -> UIImage? {
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data(text.utf8)
        filter.correctionLevel = "M"
        guard let output = filter.outputImage,
              let cgImage = CIContext().createCGImage(output, from: output.extent)
        else { return nil }
        return UIImage(cgImage: cgImage)
    }
}
