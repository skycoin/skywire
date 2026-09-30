import Foundation

/// Every file the core reads or writes, under one directory. The app (and,
/// later, the packet-tunnel extension) passes `dataDir` to the core as its
/// working directory, so a relative path in the config would resolve here
/// too; the profile makes every path absolute anyway.
public struct CorePaths: Sendable, Equatable {
    public let dataDir: URL

    public init(dataDir: URL) {
        self.dataDir = dataDir
    }

    public var configFile: URL { dataDir.appendingPathComponent("skywire-config.json") }
    /// The config's `local_path`: app work dirs, log stores, the uptime DB.
    public var localDir: URL { dataDir.appendingPathComponent("local", isDirectory: true) }
    /// The launcher's `bin_path`. The apps run in-process, so nothing is ever
    /// executed from it; the launcher only creates it at startup, which is why
    /// it has to be inside the container.
    public var binDir: URL { dataDir.appendingPathComponent("bin", isDirectory: true) }
    /// The local API's account store (`hypervisor.db_path`).
    public var usersDB: URL { dataDir.appendingPathComponent("users.db") }
    public var transportLogs: URL { localDir.appendingPathComponent("transport_logs") }
    public var skychatPasswordFile: URL { localDir.appendingPathComponent(SkychatProfile.passwordFileName) }
    public var skychatHistoryFile: URL { localDir.appendingPathComponent(SkychatProfile.historyFileName) }
    public var skydexPasswordFile: URL { localDir.appendingPathComponent(SkydexProfile.passwordFileName) }

    /// `Library/Application Support/skywire/` in this process's container.
    public static func appSupport() -> CorePaths {
        let base = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        return CorePaths(dataDir: base.appendingPathComponent("skywire", isDirectory: true))
    }

    /// Creates the directories the core expects to find. `local/` has to
    /// exist before the visor starts: its uptime recorder opens a database in
    /// it at startup and gives up (a warning, and no uptime) if it cannot.
    public func createDirectories() throws {
        for directory in [dataDir, localDir, binDir] {
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        }
    }
}
