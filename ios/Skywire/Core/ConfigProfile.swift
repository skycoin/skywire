import CoreBridge
import CoreClient
import Foundation
import os

/// The app's side of the phone profile: the local API's address, the memory
/// limit this launch writes, and what a start puts on disk before the core
/// reads its config. The edits themselves are CoreClient's `PhoneProfile`,
/// the port of Android's applyPhoneProfile, tested there against the Kotlin
/// output.
enum ConfigProfile {
    /// The visor's local API. Loopback only: the compiled default ":8000"
    /// would listen on every interface.
    static let apiAddress = "127.0.0.1:8000"

    static let apiOrigin = URL(string: "http://\(apiAddress)")!

    /// The Go memory limit this launch writes: `ProfileSettings.iosMemoryLimit`
    /// unless the launch arguments say otherwise. `-SkywireMemoryLimit none`
    /// (no limit) or `-SkywireMemoryLimit 64MiB` is for measuring the core
    /// against the default; launch arguments live in UserDefaults' volatile
    /// argument domain, so the override lasts one launch and is never saved.
    static var launchMemoryLimit: String? {
        switch UserDefaults.standard.string(forKey: "SkywireMemoryLimit") {
        case nil: ProfileSettings.iosMemoryLimit
        case "none": nil
        case let other?: other
        }
    }

    /// Everything a start needs on disk: the directories, a config (generated
    /// once, like Android's ensureConfig: a new config is a new identity), the
    /// gated apps' password files (written before either app first starts, so
    /// their surfaces are never open), and the profile, re-applied every time
    /// so its pins survive the visor rewriting the file.
    static func prepare(paths: CorePaths, settings: ProfileSettings, secrets: SecretStore) async throws {
        try paths.createDirectories()
        // A sealed config must open before anything checks for one: missing, it reads as a
        // first run, and the generator would make a new identity over the sealed one.
        try ConfigVault(paths: paths, secrets: secrets).unseal()
        restoreInterruptedRegen(paths)
        if !FileManager.default.fileExists(atPath: paths.configFile.path) {
            try await CoreBridge.shared.configGen(outPath: paths.configFile.path, options: genOptions(paths: paths))
            markGenerated(paths)
        } else if updatedSinceGenerated(paths) {
            await regenerateAfterUpdate(paths)
        }
        try PasswordFile.ensure(at: paths.skychatPasswordFile, password: secrets.password(.skychatPassword))
        try PasswordFile.ensure(at: paths.skydexPasswordFile, password: secrets.password(.skydexPassword))
        try PhoneProfile.apply(to: paths, settings: settings)
    }

    /// The generator's options: the first run's, or with `regen` the same around the file's key.
    static func genOptions(paths: CorePaths, regen: Bool? = nil) -> GenOptions {
        GenOptions(regen: regen, hypervisorAddr: apiAddress, binPath: paths.binDir.path)
    }

    // MARK: App updates (Android: ConfigManager.regenerateAfterUpdate)

    /// This build of the app, and so of the core linked into it. Android stamps the install
    /// time, which every update changes; the executable's build time does the same here.
    static var appBuildStamp: String {
        let info = Bundle.main.infoDictionary
        let built = Bundle.main.executableURL
            .flatMap { try? $0.resourceValues(forKeys: [.contentModificationDateKey]).contentModificationDate }
            .map { String(Int($0.timeIntervalSince1970)) } ?? "?"
        return "\(info?["CFBundleShortVersionString"] as? String ?? "?") (\(info?["CFBundleVersion"] as? String ?? "?")) \(built)"
    }

    static func updatedSinceGenerated(_ paths: CorePaths, stamp: String = appBuildStamp) -> Bool {
        let written = try? String(contentsOf: paths.configStampFile, encoding: .utf8)
        return written?.trimmingCharacters(in: .whitespacesAndNewlines) != stamp
    }

    static func markGenerated(_ paths: CorePaths, stamp: String = appBuildStamp) {
        try? Data(stamp.utf8).write(to: paths.configStampFile, options: .atomic)
    }

    /// Regenerates the config for the core this build carries, so it runs on that core's
    /// defaults. `regen` keeps the secret key, the apps' argv and the routing choices; the
    /// profile then re-applies the phone's own settings. A failed regenerate, or one that would
    /// change the key, puts the old config back. Tried once per build, so a failing generator
    /// cannot slow every start.
    static func regenerateAfterUpdate(_ paths: CorePaths) async {
        await regenerateAfterUpdate(paths, stamp: appBuildStamp) {
            try await CoreBridge.shared.configGen(outPath: $0.configFile.path, options: genOptions(paths: $0, regen: true))
        }
    }

    /// `regenerateAfterUpdate` with the build's stamp and the generator given (the tests').
    static func regenerateAfterUpdate(_ paths: CorePaths, stamp: String, generate: (CorePaths) async throws -> Void) async {
        markGenerated(paths, stamp: stamp)
        guard let before = Identity.publicKey(inConfig: paths.configFile),
              let previous = try? Data(contentsOf: paths.configFile),
              (try? ConfigVault.writePrivate(previous, to: paths.configBackupFile)) != nil
        else { return }
        do {
            try await generate(paths)
            if Identity.publicKey(inConfig: paths.configFile) != before {
                throw CocoaError(.fileWriteUnknown)
            }
            log.notice("config regenerated for this build of the app")
        } catch {
            log.error("config regenerate after an update failed, keeping the old config: \(error.localizedDescription, privacy: .public)")
            try? ConfigVault.writePrivate(previous, to: paths.configFile)
        }
        try? FileManager.default.removeItem(at: paths.configBackupFile)
    }

    /// A backup still on disk means a regenerate never finished; its config is the good one.
    static func restoreInterruptedRegen(_ paths: CorePaths) {
        guard let backup = try? Data(contentsOf: paths.configBackupFile) else { return }
        try? ConfigVault.writePrivate(backup, to: paths.configFile)
        try? FileManager.default.removeItem(at: paths.configBackupFile)
    }

    private static let log = Logger(subsystem: Bundle.main.bundleIdentifier ?? "skywire", category: "config")
}
