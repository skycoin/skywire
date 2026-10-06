import CoreClient
import Foundation

/// The user's choices, in UserDefaults: the Swift side of Android's
/// AppPreferences with the keys its settings objects use. Nothing here is
/// secret (the passwords are SecretStore's). The config pins among them are
/// written into the visor config by the profile on every start
/// (`profileSettings`), because the app, not the file, owns them.
@MainActor
final class AppSettings: ObservableObject {
    private let defaults: UserDefaults

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
    }

    /// The transport type tried first (Android: TransportPreference.PREF_KEY).
    var transportPrimary: String {
        get { TransportPreference.sanitize(defaults.string(forKey: Keys.transportPrimary)) }
        set { set(newValue, Keys.transportPrimary) }
    }

    /// Fleet: remote visors may dial in (Android: Fleet.PREF_KEY).
    var fleetEnabled: Bool {
        get { defaults.bool(forKey: Keys.fleetEnabled) }
        set { set(newValue, Keys.fleetEnabled) }
    }

    /// Automatic transports to public visors (Android: PublicAutoconnect).
    var publicAutoconnect: Bool {
        get { defaults.bool(forKey: Keys.publicAutoconnect) }
        set { set(newValue, Keys.publicAutoconnect) }
    }

    /// The visor's log level (Android: CoreLogLevel.PREF_KEY).
    var logLevel: String {
        get { CoreLogLevel.sanitize(defaults.string(forKey: Keys.logLevel)) }
        set { set(CoreLogLevel.sanitize(newValue), Keys.logLevel) }
    }

    /// The remote-management grant, or nil (Android: RemoteManagement).
    var remoteManagementPK: String? {
        get { RemoteManagement.sanitize(defaults.string(forKey: Keys.remoteManagementPK)) }
        set { set(RemoteManagement.sanitize(newValue), Keys.remoteManagementPK) }
    }

    /// SkyDNS inside SkyVPN's tunnel, on unless turned off (Android: SkyDns.PREF_IN_VPN).
    var skyDnsInVpn: Bool {
        get { defaults.object(forKey: Keys.skyDnsInVpn) as? Bool ?? SkyDNS.defaultInVpn }
        set { set(newValue, Keys.skyDnsInVpn) }
    }

    /// The resolver SkyVPN and SkyDNS ask for ordinary names, blank for their defaults
    /// (Android: DnsServer.PREF_KEY).
    var dnsServer: String {
        get { DnsServer.sanitize(defaults.string(forKey: Keys.dnsServer)) }
        set { set(DnsServer.sanitize(newValue), Keys.dnsServer) }
    }

    /// The biometric lock on launch and on return (Android: AppLock).
    var appLockEnabled: Bool {
        get { defaults.bool(forKey: Keys.appLockEnabled) }
        set { set(newValue, Keys.appLockEnabled) }
    }

    /// Seal the config while the core is stopped (Android: ConfigVault.PREF_KEY).
    var configEncrypted: Bool {
        get { defaults.bool(forKey: Keys.configEncrypted) }
        set { set(newValue, Keys.configEncrypted) }
    }

    /// Settings ▸ Theme (Android: ThemeMode.PREF_KEY).
    var themeMode: ThemeMode {
        get { defaults.string(forKey: Keys.themeMode).flatMap(ThemeMode.init(rawValue:)) ?? .system }
        set { set(newValue.rawValue, Keys.themeMode) }
    }

    /// Settings ▸ Language. Read from AppleLanguages, which iOS's own page for the app also writes.
    var language: AppLanguage {
        get { AppLanguage.current(in: defaults) }
        set {
            objectWillChange.send()
            newValue.persist(in: defaults)
            L10n.bundle = newValue.bundle
        }
    }

    /// Whether the user left the core connected, so a relaunch (after a
    /// force-quit, or iOS ending the suspended app) connects again, as
    /// Android's sticky core service comes back after its process dies.
    var wantsConnected: Bool {
        get { defaults.bool(forKey: Keys.wantsConnected) }
        set { set(newValue, Keys.wantsConnected) }
    }

    /// What the profile writes into the config at the next start.
    var profileSettings: ProfileSettings {
        ProfileSettings(
            transportPrimary: transportPrimary,
            fleetEnabled: fleetEnabled,
            publicAutoconnect: publicAutoconnect,
            logLevel: logLevel,
            remoteManagementPK: remoteManagementPK,
            memoryLimit: ConfigProfile.launchMemoryLimit,
            skyDnsInVpn: skyDnsInVpn,
            dnsServer: dnsServer
        )
    }

    private func set(_ value: Any?, _ key: String) {
        objectWillChange.send()
        if let value {
            defaults.set(value, forKey: key)
        } else {
            defaults.removeObject(forKey: key)
        }
    }

    /// Android's preference keys, so the two apps read the same way.
    enum Keys {
        static let transportPrimary = "transport_primary"
        static let fleetEnabled = "fleet_enabled"
        static let publicAutoconnect = "public_autoconnect"
        static let logLevel = "core_log_level"
        static let remoteManagementPK = "remote_management_pk"
        static let skyDnsInVpn = SkyDNS.prefInVpn
        static let dnsServer = DnsServer.prefKey
        static let appLockEnabled = "app_lock_enabled"
        static let configEncrypted = "config_encrypted"
        static let themeMode = "theme_mode"
        /// M1's key, kept so a phone that had the spike connects again.
        static let wantsConnected = "core.wantsConnected"
    }
}
