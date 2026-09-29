import Foundation

/// The phone profile: the edits to a generated config that `config gen` has
/// no flags for, the Swift side of Android's ConfigManager.applyPhoneProfile.
///
/// M1 carries the structural part, the edits that depend on no user setting:
/// no RPC, pty or local dmsg relay listener, no LAN listeners, every path
/// absolute under the data dir, and the Go memory limit. The settings-driven pins (Fleet's
/// dmsg_ingest, public autoconnect, transport order, mux routes, log level,
/// the remote-management grant, the per-app argv) arrive with the Settings
/// screen in M2.
enum ConfigProfile {
    /// The visor's local API. Loopback only: the compiled default ":8000"
    /// would listen on every interface.
    static let apiAddress = "127.0.0.1:8000"

    /// The Go memory limit the core runs under (playbook item 1.7). The
    /// packet-tunnel extension's budget is about 50 MB of phys_footprint;
    /// at 40 MiB the idle core holds ~42–45 MB on the host. The app uses the
    /// extension's value so the Simulator's numbers predict the device's.
    /// Not "auto": that reads /proc/meminfo, which iOS does not have.
    static let memoryLimit = "40MiB"

    /// The limit this launch writes: `memoryLimit`, unless the launch
    /// arguments say otherwise. `-SkywireMemoryLimit none` (no limit) or
    /// `-SkywireMemoryLimit 64MiB` is for measuring the core against the
    /// default; launch arguments live in UserDefaults' volatile argument
    /// domain, so the override lasts one launch and is never saved.
    static var launchMemoryLimit: String? {
        switch UserDefaults.standard.string(forKey: "SkywireMemoryLimit") {
        case nil: memoryLimit
        case "none": nil
        case let other?: other
        }
    }

    /// Applies the profile to the config at `paths.configFile`, in place.
    /// Idempotent: it runs before every start.
    static func apply(to paths: CorePaths) throws {
        let data = try Data(contentsOf: paths.configFile)
        guard var config = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            throw ProfileError.notAnObject
        }
        edit(&config, paths: paths)
        let out = try JSONSerialization.data(
            withJSONObject: config, options: [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes]
        )
        try out.write(to: paths.configFile, options: .atomic)
    }

    /// The edits themselves, on the decoded config.
    static func edit(_ config: inout [String: Any], paths: CorePaths) {
        // No RPC listener: an empty flag value falls back to localhost:3435,
        // so this can only be a post-edit.
        config["cli_addr"] = ""
        // dmsgpty would open a unix socket the phone has no use for.
        config["pty"] = nil
        // The STCP listener on :7777.
        config["skywire-tcp"] = nil
        // On when absent; it would serve and write an scp root.
        config["dmsgscp"] = ["disabled": true]
        config["memory_limit"] = launchMemoryLimit
        config["local_path"] = paths.localDir.path

        // The local dmsg relay serves standalone dmsg clients running beside
        // the visor (a desktop's dmsgweb proxies); nothing on a phone
        // attaches. Its unix socket under local_path cannot bind here anyway:
        // an app container path is longer than sun_path's 103 bytes (220 on
        // the Simulator, over 130 on a device).
        if var dmsg = config["dmsg"] as? [String: Any] {
            dmsg["local_relay"] = ["enabled": false]
            config["dmsg"] = dmsg
        }
        if var transport = config["transport"] as? [String: Any] {
            transport["log_store"] = ["location": paths.localDir.appendingPathComponent("transport_logs").path]
            config["transport"] = transport
        }
        if var hypervisor = config["hypervisor"] as? [String: Any] {
            // Forced on by -i; a LAN-reachable listener.
            hypervisor["lan_dmsg_server"] = nil
            hypervisor["db_path"] = paths.usersDB.path
            hypervisor["tp_viz"] = ["enable": false]
            config["hypervisor"] = hypervisor
        }
        // The launcher creates bin_path at startup; a path outside the
        // container would abort the visor.
        if var launcher = config["launcher"] as? [String: Any] {
            launcher["bin_path"] = paths.binDir.path
            config["launcher"] = launcher
        }
    }

    enum ProfileError: LocalizedError {
        case notAnObject

        var errorDescription: String? {
            switch self {
            case .notAnObject: "The visor config is not a JSON object."
            }
        }
    }
}
