import CoreClient
import SwiftUI
import UIKit
import UniformTypeIdentifiers

/// Settings (Android: ui/settings/SettingsScreen.kt): who this visor is, how to get its config
/// off the phone, what guards the app, and where the logs are. Every card action is a tonal
/// button; text buttons are the dialogs'. The identity changes end this phone's identity, so
/// each is asked twice, the consequence spelled out.
struct SettingsView: View {
    @EnvironmentObject private var app: AppModel
    @EnvironmentObject private var navigator: Navigator
    @EnvironmentObject private var dialogs: SkyDialogs
    @StateObject private var model = SettingsModel()
    @State private var export: ConfigFile?

    var body: some View {
        VStack(spacing: 0) {
            SkyTopBar(title: Text("tab_settings"), onBack: { navigator.back() }, help: .settings)
            ScrollView {
                LazyVStack(spacing: 16) {
                    IdentityCard(model: model)
                    ConfigCard(model: model) { askExport() }
                    AutoconnectCard()
                    RemoteCard(model: model)
                    AppLockCard()
                    ThemeCard()
                    LanguageCard()
                    DiagnosticsRow { navigator.push(.diagnostics) }
                    UpdatesCard()
                    AboutCard(model: model)
                }
                .padding(.horizontal, 20)
                .padding(.vertical, 16)
            }
        }
        .background(Color.skyBackground)
        .task(id: app.connected) { await model.load(app) }
        .onChange(of: model.notice) { notice in
            guard let notice else { return }
            dialogs.snackbar(Text(verbatim: notice))
            model.notice = nil
        }
        .fileExporter(isPresented: Binding(get: { export != nil }, set: { if !$0 { export = nil } }),
                      document: export, contentType: .json, defaultFilename: "skywire-config.json") { result in
            switch result {
            case .success: model.notice = L10n.text("settings_export_done")
            case .failure(let error): model.notice = error.localizedDescription
            }
        }
    }

    /// The warning, then the device-owner check, then the system's save panel.
    private func askExport() {
        dialogs.showCustom {
            DialogFrame(title: Text("settings_export_title"), confirm: L10n.key("settings_continue"), onConfirm: {
                Task {
                    guard await WalletAuth.confirm(reason: L10n.text("settings_export_prompt")) else { return }
                    do {
                        export = ConfigFile(text: try app.vault.readText())
                    } catch {
                        model.notice = error.localizedDescription
                    }
                }
            }) {
                Text("settings_export_warning").skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant)
            }
        }
    }
}

/// The config as the file the save panel writes.
struct ConfigFile: FileDocument {
    static var readableContentTypes: [UTType] { [.json] }
    let text: String

    init(text: String) {
        self.text = text
    }

    init(configuration: ReadConfiguration) throws {
        text = String(decoding: configuration.file.regularFileContents ?? Data(), as: UTF8.self)
    }

    func fileWrapper(configuration: WriteConfiguration) throws -> FileWrapper {
        FileWrapper(regularFileWithContents: Data(text.utf8))
    }
}

/// A card's title and hint, Android's common pattern: titleMedium, 4 pt, bodySmall.
private struct CardHeader: View {
    let title: LocalizedStringKey
    var hint: LocalizedStringKey? = nil
    var titleStyle: SkyTextStyle = .titleMedium

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(title).skyText(titleStyle)
            if let hint {
                Text(hint).skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// A title and hint with a switch beside them.
private struct SwitchRow: View {
    let title: LocalizedStringKey
    let hint: LocalizedStringKey
    var titleStyle: SkyTextStyle = .titleMedium
    let isOn: Binding<Bool>
    var identifier: String? = nil

    var body: some View {
        HStack(spacing: 12) {
            CardHeader(title: title, hint: hint, titleStyle: titleStyle)
            Toggle(isOn: isOn) { Text(title) }
                .toggleStyle(.skySwitch)
                .accessibilityIdentifier(identifier ?? "")
        }
    }
}

private struct Note: View {
    let text: LocalizedStringKey

    var body: some View {
        Text(text).skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
    }
}

// MARK: Identity

private struct IdentityCard: View {
    @ObservedObject var model: SettingsModel
    @EnvironmentObject private var app: AppModel
    @EnvironmentObject private var dialogs: SkyDialogs

    var body: some View {
        SectionCard {
            CardHeader(title: L10n.key("settings_identity"), hint: L10n.key("settings_identity_hint"))
            Group {
                if let pk = app.publicKey {
                    Button {
                        UIPasteboard.general.string = pk
                        dialogs.toast(Text("copied_to_clipboard"))
                    } label: {
                        SkyInfoRow(label: Text("visor_public_key"), value: Format.shortPK(pk), mono: true).contentShape(Rectangle())
                    }
                    .buttonStyle(PressStyle(layer: .skyOnSurface))
                    .accessibilityIdentifier("settings-pk")
                } else {
                    Text("settings_identity_none").skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant)
                }
            }
            .padding(.top, 12)
            Group {
                if model.identityBusy {
                    HStack(spacing: 10) {
                        MaterialSpinner(size: 14, stroke: 2)
                        Text("settings_identity_working").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                    }
                } else {
                    ProportionalRow(spacing: 12) {
                        Button { enterKey() } label: { Text("settings_replace_sk").lineLimit(1).frame(maxWidth: .infinity) }
                            .buttonStyle(TonalButtonStyle(horizontal: 12))
                            .accessibilityIdentifier("settings-replace-sk")
                        Button { newIdentity() } label: { Text("settings_new_config").lineLimit(1).frame(maxWidth: .infinity) }
                            .buttonStyle(TonalButtonStyle(horizontal: 12))
                            .disabled(app.publicKey == nil)
                            .accessibilityIdentifier("settings-new-identity")
                    }
                }
            }
            .padding(.top, 12)
        }
    }

    private func enterKey() {
        dialogs.showCustom {
            SecretKeyDialog { entered in
                let pk: String
                do {
                    pk = try Identity.publicKey(of: entered)
                } catch {
                    model.notice = error.localizedDescription
                    return
                }
                if pk == app.publicKey {
                    model.notice = L10n.text("settings_sk_unchanged")
                } else if let current = app.publicKey {
                    destructive(title: Text("settings_replace_title"),
                                body: L10n.format("settings_identity_loss", Format.shortPK(current)),
                                extra: L10n.format("settings_replace_new_pk", Format.shortPK(pk)),
                                confirm: L10n.key("settings_continue")) { replaceFinal(entered, pk) }
                } else {
                    // No identity yet: choosing the first one, with nothing to lose but the last word.
                    replaceFinal(entered, pk)
                }
            }
        }
    }

    private func replaceFinal(_ secretKey: String, _ pk: String) {
        destructive(title: Text("settings_replace_final_title"),
                    body: L10n.format("settings_replace_final_body", Format.shortPK(pk)),
                    confirm: L10n.key("settings_replace_action")) { model.replaceSecretKey(secretKey, app) }
    }

    private func newIdentity() {
        guard let current = app.publicKey else { return }
        destructive(title: Text("settings_new_title"),
                    body: L10n.format("settings_identity_loss", Format.shortPK(current)),
                    extra: L10n.text("settings_new_extra"),
                    confirm: L10n.key("settings_continue")) {
            destructive(title: Text("settings_new_final_title"), body: L10n.text("settings_new_final_body"),
                        confirm: L10n.key("settings_new_action")) { model.newIdentity(app) }
        }
    }

    /// Android's DestructiveDialog: the body, an optional extra, the confirm in the error colour.
    private func destructive(title: Text, body: String, extra: String? = nil, confirm: LocalizedStringKey, action: @escaping () -> Void) {
        dialogs.showCustom {
            DialogFrame(title: title, confirm: confirm, destructive: true, onConfirm: action) {
                VStack(alignment: .leading, spacing: 12) {
                    Text(verbatim: body)
                    if let extra { Text(verbatim: extra) }
                }
                .skyText(.bodyMedium)
                .foregroundStyle(Color.skyOnSurfaceVariant)
            }
        }
    }
}

/// The secret key to run as: checked here before anything changes.
private struct SecretKeyDialog: View {
    let submit: (String) -> Void
    @State private var text = ""

    var body: some View {
        DialogFrame(title: Text("settings_replace_sk"), confirm: L10n.key("settings_continue"),
                    enabled: !text.isEmpty, onConfirm: { submit(text) }) {
            VStack(alignment: .leading, spacing: 12) {
                Text("settings_sk_hint").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                SkyOutlinedTextField(label: L10n.key("settings_sk_label"), text: $text, mono: true,
                                     notch: .skyContainerHigh, identifier: "settings-sk-field")
                    .onChange(of: text) { value in
                        let trimmed = value.filter { !$0.isWhitespace }
                        if trimmed != value { text = trimmed }
                    }
            }
        }
    }
}

// MARK: Config

private struct ConfigCard: View {
    @EnvironmentObject private var settings: AppSettings
    @ObservedObject var model: SettingsModel
    let export: () -> Void
    @EnvironmentObject private var app: AppModel

    var body: some View {
        SectionCard {
            CardHeader(title: L10n.key("settings_config"), hint: L10n.key("settings_config_hint"))
            Button(action: export) { Text("settings_export") }
                .buttonStyle(.tonal)
                .disabled(app.publicKey == nil || model.identityBusy)
                .padding(.top, 12)
                .accessibilityIdentifier("settings-export")
            SkyDivider().padding(.vertical, 16)
            SwitchRow(title: L10n.key("settings_encrypt"), hint: L10n.key("settings_encrypt_hint"), titleStyle: .titleSmall,
                      isOn: Binding(get: { settings.configEncrypted }, set: { setEncrypted($0) }),
                      identifier: "settings-encrypt")
                .disabled(model.identityBusy)
            if settings.configEncrypted {
                Note(text: L10n.key("settings_encrypt_warning_ios")).padding(.top, 10)
            }
        }
    }

    /// On needs no ceremony. Off puts the key back on disk in the clear, so it is confirmed.
    private func setEncrypted(_ wanted: Bool) {
        if wanted {
            model.setConfigEncrypted(true, app)
        } else {
            Task {
                if await WalletAuth.confirm(reason: L10n.text("settings_encrypt_disable_prompt")) {
                    model.setConfigEncrypted(false, app)
                }
            }
        }
    }
}

// MARK: Network

private struct AutoconnectCard: View {
    @EnvironmentObject private var settings: AppSettings
    @EnvironmentObject private var app: AppModel

    var body: some View {
        SectionCard {
            SwitchRow(title: L10n.key("settings_autoconnect"), hint: L10n.key("settings_autoconnect_hint"),
                      isOn: Binding(get: { settings.publicAutoconnect }, set: { settings.publicAutoconnect = $0 }),
                      identifier: "settings-autoconnect")
            Note(text: L10n.key("settings_autoconnect_restart")).padding(.top, 10)
        }
    }
}

private struct RemoteCard: View {
    @EnvironmentObject private var settings: AppSettings
    @ObservedObject var model: SettingsModel
    @EnvironmentObject private var app: AppModel
    @EnvironmentObject private var dialogs: SkyDialogs

    var body: some View {
        SectionCard {
            CardHeader(title: L10n.key("settings_remote"), hint: L10n.key("settings_remote_hint"))
            Group {
                if let pk = settings.remoteManagementPK {
                    HStack(spacing: 12) {
                        Text(verbatim: Format.shortPK(pk)).skyText(.bodyMedium, mono: true)
                            .frame(maxWidth: .infinity, alignment: .leading)
                        Button { model.revokeRemoteManagement(app) } label: { Text("settings_remote_revoke") }
                            .buttonStyle(.tonal)
                            .accessibilityIdentifier("settings-remote-revoke")
                    }
                } else {
                    Button { grant() } label: { Text("settings_remote_grant") }
                        .buttonStyle(.tonal)
                        .accessibilityIdentifier("settings-remote-grant")
                }
            }
            .padding(.top, 12)
            Note(text: L10n.key("settings_remote_restart")).padding(.top, 6)
        }
    }

    private func grant() {
        dialogs.showCustom {
            RemoteKeyDialog { entered in
                if !model.grantRemoteManagement(entered, app) {
                    model.notice = L10n.text("settings_remote_invalid")
                }
            }
        }
    }
}

private struct RemoteKeyDialog: View {
    let submit: (String) -> Void
    @State private var text = ""

    var body: some View {
        DialogFrame(title: Text("settings_remote_dialog_title"), confirm: L10n.key("settings_remote_grant_confirm"),
                    enabled: !text.trimmingCharacters(in: .whitespaces).isEmpty, onConfirm: { submit(text) }) {
            VStack(alignment: .leading, spacing: 12) {
                Text("settings_remote_dialog_hint").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                SkyOutlinedTextField(label: L10n.key("settings_remote_label"), text: $text, mono: true,
                                     notch: .skyContainerHigh, identifier: "settings-remote-field")
            }
        }
    }
}

// MARK: The app

private struct AppLockCard: View {
    @EnvironmentObject private var settings: AppSettings
    @EnvironmentObject private var app: AppModel
    @EnvironmentObject private var lock: AppLock

    var body: some View {
        SectionCard {
            SwitchRow(title: L10n.key("settings_app_lock"), hint: L10n.key("settings_app_lock_hint_ios"),
                      isOn: Binding(get: { settings.appLockEnabled }, set: { setLock($0) }),
                      identifier: "app-lock-toggle")
                .disabled(!AppLock.available)
            if !AppLock.available {
                // iOS has no page to send this to: a passcode is set in the Settings app.
                Note(text: L10n.key("settings_app_lock_unavailable_ios")).padding(.top, 10)
            }
        }
    }

    /// Both ways ask first, so the lock can be neither set up nor removed by someone who cannot pass it.
    private func setLock(_ enabled: Bool) {
        Task {
            let reason = enabled ? L10n.text("settings_lock_enable_prompt") : L10n.text("settings_lock_disable_prompt")
            if await lock.confirm(reason: reason) {
                settings.appLockEnabled = enabled
            }
        }
    }
}

private struct ThemeCard: View {
    @EnvironmentObject private var settings: AppSettings
    @EnvironmentObject private var app: AppModel

    var body: some View {
        SectionCard {
            CardHeader(title: L10n.key("settings_theme"))
            HStack(spacing: 8) {
                chip(L10n.key("settings_theme_system"), .system)
                chip(L10n.key("settings_theme_light"), .light)
                chip(L10n.key("settings_theme_dark"), .dark)
            }
            .padding(.top, 4)
        }
    }

    private func chip(_ label: LocalizedStringKey, _ mode: ThemeMode) -> some View {
        SkyFilterChip(label: Text(label), selected: settings.themeMode == mode) { settings.themeMode = mode }
            .accessibilityIdentifier("theme-\(mode.rawValue.lowercased())")
    }
}

private struct LanguageCard: View {
    @EnvironmentObject private var settings: AppSettings
    @EnvironmentObject private var app: AppModel

    var body: some View {
        SectionCard {
            CardHeader(title: L10n.key("settings_language"), hint: L10n.key("settings_language_hint"))
            FlowLayout(spacing: 8) {
                chip(L10n.key("settings_language_system"), .system)
                chip(L10n.key("settings_language_en"), .english)
                chip(L10n.key("settings_language_zh_cn"), .chineseSimplified)
                chip(L10n.key("settings_language_es"), .spanish)
            }
            .padding(.top, 4)
        }
    }

    private func chip(_ label: LocalizedStringKey, _ language: AppLanguage) -> some View {
        SkyFilterChip(label: Text(label), selected: settings.language == language) { settings.language = language }
            .accessibilityIdentifier("language-\(language.rawValue.lowercased())")
    }
}

private struct DiagnosticsRow: View {
    let open: () -> Void

    var body: some View {
        SectionCard {
            Button(action: open) {
                HStack(spacing: 12) {
                    CardHeader(title: L10n.key("settings_diagnostics"), hint: L10n.key("settings_diagnostics_hint"))
                    MaterialIcon(MI.filledKeyboardArrowRight).foregroundStyle(Color.skyOnSurfaceVariant)
                }
                .foregroundStyle(Color.skyOnSurface)
                .contentShape(Rectangle())
            }
            .buttonStyle(PressStyle(layer: .skyOnSurface))
            .accessibilityIdentifier("diagnostics-link")
        }
    }
}

// MARK: Updates and about

private struct UpdatesCard: View {
    var body: some View {
        SectionCard {
            Text("settings_updates").skyText(.titleMedium)
            SkyInfoRow(label: Text("settings_update_installed"), value: AboutCard.appVersion).padding(.top, 8)
            Note(text: L10n.key("settings_update_ios")).padding(.top, 10)
        }
    }
}

private struct AboutCard: View {
    @ObservedObject var model: SettingsModel
    @Environment(\.openURL) private var openURL

    static let sourcePage = URL(string: "https://github.com/skycoin/skywire")!

    var body: some View {
        SectionCard {
            Text("settings_about").skyText(.titleMedium)
            VStack(spacing: 0) {
                SkyInfoRow(label: Text("settings_app_version"), value: Self.appVersion)
                SkyInfoRow(label: Text("settings_core_version"), value: model.coreVersion ?? "—")
                SkyInfoRow(label: Text("settings_license"), value: "AGPL-3.0")
            }
            .padding(.top, 8)
            Button { openURL(Self.sourcePage) } label: { Text("settings_source_code") }
                .buttonStyle(.tonal)
                .padding(.top, 12)
        }
    }

    static var appVersion: String {
        let info = Bundle.main.infoDictionary
        let version = info?["CFBundleShortVersionString"] as? String ?? "?"
        let build = info?["CFBundleVersion"] as? String ?? "?"
        return "\(version) (\(build))"
    }
}
