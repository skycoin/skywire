import Foundation

/// The phone profile: the edits to a generated config that `config gen` has
/// no flags for, re-applied before every start so the pins survive the
/// visor rewriting the file at runtime. The port of Android's
/// ConfigManager.applyPhoneProfile, edit for edit, plus two iOS-only edits
/// (marked below). `config gen` itself runs in the core with the phone's
/// argv (mobilecore.PhoneGenOptions).
///
/// The edits:
///  - `cli_addr: ""`: no RPC listener (an empty flag value falls back to
///    localhost:3435, so this can only be a post-edit);
///  - `pty` dropped: the phone has no use for dmsgpty;
///  - `skywire-tcp` dropped: the STCP listener on :7777;
///  - `hypervisor.lan_dmsg_server` dropped: forced on by `-i`, a
///    LAN-reachable listener;
///  - `hypervisor.dmsg_ingest`: the Fleet opt-in;
///  - absolute `local_path`, `transport.log_store.location` and
///    `hypervisor.db_path`, so nothing depends on the working directory;
///  - `launcher.bin_path` inside the container, and the per-app flags the
///    phone owns (`SocksProfile`, `SkydexProfile`, `SkychatProfile`), with
///    skychat autostarted: a chat app that only runs while its screen is open
///    receives nothing;
///  - `dmsgscp.disabled`: on when absent, it would serve an scp root;
///  - `hypervisor.tp_viz.enable = false`;
///  - `routing.transport_preference` from the user's primary, and
///    `routing.mux_routes` = 2: the phone's policy, not a setting;
///  - `transport.public_autoconnect`, `log_level` and `hypervisors` (the
///    remote-management grant, pinned to exactly the granted key: a key
///    planted by a hand edit or a persisted `hv add` must not outlive the
///    user's answer);
///  - iOS only: `memory_limit` (Go's limit; ProfileSettings.iosMemoryLimit),
///    and `dmsg.local_relay.enabled = false`. The local relay serves
///    standalone dmsg clients beside a desktop visor, nothing on a phone
///    attaches, and its unix socket under an app container path is longer
///    than sun_path's 103 bytes, so it fails to bind anyway.
///
/// Objects the phone edits one field of (`log_store`, `tp_viz`,
/// `local_relay`) are merged, not replaced, as Kotlin does: their other
/// fields (the log store's type, say) keep what the generator wrote.
public enum PhoneProfile {
    /// Parallel routes a client app's dial asks for: the second leg buys a
    /// disjoint path, and a further one would cost another setup dial on every
    /// connect for a return a single-radio phone cannot use.
    public static let muxRoutes = 2

    public enum ProfileError: LocalizedError, Equatable {
        case notAnObject(String)

        public var errorDescription: String? {
            switch self {
            case let .notAnObject(key):
                key.isEmpty ? "The visor config is not a JSON object." : "The visor config's \"\(key)\" is not a JSON object."
            }
        }
    }

    /// Applies the profile to the config at `paths.configFile`, in place.
    public static func apply(to paths: CorePaths, settings: ProfileSettings) throws {
        let data = try Data(contentsOf: paths.configFile)
        guard let config = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            throw ProfileError.notAnObject("")
        }
        let edited = try edit(config, settings: settings, paths: paths)
        let out = try JSONSerialization.data(withJSONObject: edited, options: [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes])
        try out.write(to: paths.configFile, options: .atomic)
    }

    /// The edits, on a decoded config. A key the config does not have is not
    /// added, as in Kotlin, except `dmsgscp` and `memory_limit`.
    public static func edit(_ root: [String: Any], settings: ProfileSettings, paths: CorePaths) throws -> [String: Any] {
        var out: [String: Any] = [:]
        for (key, value) in root {
            switch key {
            case "pty", "skywire-tcp":
                continue
            case "cli_addr":
                out[key] = ""
            case "hypervisors":
                out[key] = RemoteManagement.sanitize(settings.remoteManagementPK).map { [$0] } ?? []
            case "log_level":
                out[key] = CoreLogLevel.sanitize(settings.logLevel)
            case "local_path":
                out[key] = paths.localDir.path
            case "transport":
                var transport = try object(value, key)
                transport["public_autoconnect"] = settings.publicAutoconnect
                transport["log_store"] = merged(transport["log_store"], ["location": paths.transportLogs.path])
                out[key] = transport
            case "hypervisor":
                var hypervisor = try object(value, key)
                hypervisor["lan_dmsg_server"] = nil
                hypervisor["db_path"] = paths.usersDB.path
                hypervisor["dmsg_ingest"] = settings.fleetEnabled
                hypervisor["tp_viz"] = merged(hypervisor["tp_viz"], ["enable": false])
                out[key] = hypervisor
            case "launcher":
                var launcher = try object(value, key)
                launcher["bin_path"] = paths.binDir.path
                if let apps = launcher["apps"] {
                    launcher["apps"] = pinAppArgs(apps, paths: paths)
                }
                out[key] = launcher
            case "routing":
                var routing = try object(value, key)
                routing["transport_preference"] = TransportPreference.order(
                    primary: TransportPreference.sanitize(settings.transportPrimary)
                )
                routing["mux_routes"] = muxRoutes
                out[key] = routing
            case "dmsg":
                // iOS only (see above).
                var dmsg = try object(value, key)
                dmsg["local_relay"] = merged(dmsg["local_relay"], ["enabled": false])
                out[key] = dmsg
            default:
                out[key] = value
            }
        }
        out["dmsgscp"] = ["disabled": true]
        // iOS only (see above); nil writes no limit.
        out["memory_limit"] = settings.memoryLimit
        return out
    }

    /// The flags the phone owns on each app it pins; everything else in each
    /// argv (the server key the SkySOCKS screen writes, above all) passes
    /// through, so this never undoes a user's choice. An argv the visor could
    /// not parse either (an unclosed quote) is left for the visor to report.
    static func pinAppArgs(_ apps: Any, paths: CorePaths) -> Any {
        guard let list = apps as? [Any] else { return apps }
        return list.map { entry -> Any in
            guard var app = entry as? [String: Any], let name = app["name"] as? String else { return entry }
            let tokens: [String]
            switch app["args"] {
            case let text as String:
                guard let split = AppArgs.split(text) else { return entry }
                tokens = split
            case let array as [String]:
                // The visor still reads the legacy array form.
                tokens = array
            default:
                tokens = []
            }
            let pinned: [String]
            switch name {
            case SocksProfile.app:
                pinned = SocksProfile.phoneArgs(tokens)
            case SkydexProfile.app:
                pinned = SkydexProfile.phoneArgs(tokens, passwordFile: paths.skydexPasswordFile.path)
            case SkychatProfile.app:
                pinned = SkychatProfile.phoneArgs(
                    tokens, passwordFile: paths.skychatPasswordFile.path, historyFile: paths.skychatHistoryFile.path
                )
            default:
                return entry
            }
            app["args"] = AppArgs.join(pinned)
            if name == SkychatProfile.app {
                app["auto_start"] = true
            }
            return app
        }
    }

    private static func object(_ value: Any, _ key: String) throws -> [String: Any] {
        guard let object = value as? [String: Any] else { throw ProfileError.notAnObject(key) }
        return object
    }

    private static func merged(_ existing: Any?, _ fields: [String: Any]) -> [String: Any] {
        (existing as? [String: Any] ?? [:]).merging(fields) { _, new in new }
    }
}
