import CoreClient
import Foundation

/// Settings' live half: the router settings over the API while the core runs,
/// and the outcome of each change. The pinned half (what the profile writes)
/// is AppSettings.
@MainActor
final class SettingsModel: ObservableObject {
    /// The visor's route length, nil until it reports one.
    @Published private(set) var minHops: Int?
    /// The core's version, from /api/about while it runs.
    @Published private(set) var coreVersion: String?
    /// One line to show after a change (or its failure).
    @Published var notice: String?
    /// An identity change is running: the core restarts around it.
    @Published private(set) var identityBusy = false

    /// The route lengths offered (Android: MinHopsCard's presets).
    static let hopChoices = [1, 2, 3]

    /// Reads what only the running core knows. Called when the screen shows
    /// and whenever the connection comes up.
    func load(_ app: AppModel) async {
        guard app.connected else {
            minHops = nil
            coreVersion = nil
            return
        }
        do {
            minHops = try await app.client.routerSettings().minHops
            coreVersion = try await app.client.about().build?.version
        } catch {
            if !app.handle(error) { notice = error.localizedDescription }
        }
    }

    /// The transport type tried first: pinned for every start and, while the
    /// core runs, applied live (the visor also persists its own copy).
    func setTransportPrimary(_ type: String, _ app: AppModel) {
        app.settings.transportPrimary = type
        guard app.connected else { return }
        Task {
            do {
                _ = try await app.client.setTransportPreference(TransportPreference.order(primary: type))
            } catch {
                if !app.handle(error) { notice = error.localizedDescription }
            }
        }
    }

    /// The route length, live (the visor persists it; the phone does not pin
    /// it). Applies to routes built from now on.
    func setMinHops(_ hops: Int, _ app: AppModel) {
        guard app.connected else { return }
        Task {
            do {
                minHops = try await app.client.setMinHops(hops).minHops
            } catch {
                if !app.handle(error) { notice = error.localizedDescription }
            }
        }
    }

    /// Fleet: saved first, so the choice holds while the core is down; when
    /// it runs, the restart is what applies it (Android: FleetViewModel).
    func setFleet(_ enabled: Bool, _ app: AppModel) {
        app.settings.fleetEnabled = enabled
        app.applyPinnedSettingNow()
    }

    /// The core reads its log level only when it starts, so a change restarts
    /// a running core (Android: DiagnosticsViewModel.setLogLevel).
    func setLogLevel(_ level: String, _ app: AppModel) {
        app.settings.logLevel = level
        app.applyPinnedSettingNow()
    }

    /// The resolver for ordinary names, or each app's default when `address` is blank: pinned for
    /// every start and, while the core runs, written into vpn-client's argv at once (Android:
    /// SettingsViewModel.setDnsServer, which also rewrites skydns, an app the iOS core lacks).
    func setDnsServer(_ address: String, _ app: AppModel) {
        let server = DnsServer.sanitize(address)
        guard server.isEmpty == address.trimmingCharacters(in: .whitespaces).isEmpty else {
            notice = L10n.text("settings_dns_invalid")
            return
        }
        app.settings.dnsServer = server
        let done = server.isEmpty ? L10n.text("settings_dns_cleared") : L10n.format("settings_dns_saved", server)
        guard app.connected else {
            notice = done
            return
        }
        Task {
            do {
                try await app.rewriteVpnArgs { DnsServer.args($0, server: server) }
                notice = done
            } catch {
                if !app.handle(error) { notice = error.localizedDescription }
            }
        }
    }

    /// Grants remote management to `raw`, if it is a visor key. Applies at the
    /// next start, as on Android: the grant admits another machine, and
    /// restarting under the user's feet for it would be the wrong surprise.
    /// Returns whether the key was accepted.
    func grantRemoteManagement(_ raw: String, _ app: AppModel) -> Bool {
        guard let pk = RemoteManagement.sanitize(raw) else { return false }
        app.settings.remoteManagementPK = pk
        notice = L10n.text("settings_remote_granted")
        return true
    }

    func revokeRemoteManagement(_ app: AppModel) {
        app.settings.remoteManagementPK = nil
        notice = L10n.text("settings_remote_revoked")
    }

    // MARK: Identity and the config (Android: SettingsViewModel)

    /// Installs `secretKey` as this visor's identity, the core stopped around it.
    func replaceSecretKey(_ secretKey: String, _ app: AppModel) {
        let paths = app.paths, vault = app.vault
        changeIdentity(app, done: L10n.text("settings_sk_replaced")) {
            _ = try await Identity.replace(secretKey: secretKey, paths: paths, vault: vault)
        }
    }

    /// Throws the identity away; the next start generates one.
    func newIdentity(_ app: AppModel) {
        let paths = app.paths, vault = app.vault
        changeIdentity(app, done: L10n.text("settings_identity_reset")) {
            try Identity.reset(paths: paths, vault: vault)
        }
    }

    private func changeIdentity(_ app: AppModel, done: String, _ change: @escaping @Sendable () async throws -> Void) {
        identityBusy = true
        Task {
            defer { identityBusy = false }
            do {
                try await app.changeIdentity(change)
                notice = done
            } catch {
                notice = error.localizedDescription
            }
        }
    }

    /// On seals now, or once a running core stops; off unseals now.
    func setConfigEncrypted(_ enabled: Bool, _ app: AppModel) {
        let running = app.coreState != .stopped && app.coreState != .failed
        do {
            try app.vault.apply(enabled: enabled, coreRunning: running)
            app.settings.configEncrypted = enabled
            if !enabled {
                notice = L10n.text("settings_encrypt_off_done")
            } else {
                notice = running ? L10n.text("settings_encrypt_on_pending") : L10n.text("settings_encrypt_on_done")
            }
        } catch {
            notice = error.localizedDescription
        }
    }
}
