import CoreBridge
import CoreClient
import Foundation

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
        if !FileManager.default.fileExists(atPath: paths.configFile.path) {
            try await CoreBridge.shared.configGen(
                outPath: paths.configFile.path,
                options: GenOptions(hypervisorAddr: apiAddress, binPath: paths.binDir.path)
            )
        }
        try PasswordFile.ensure(at: paths.skychatPasswordFile, password: secrets.password(.skychatPassword))
        try PasswordFile.ensure(at: paths.skydexPasswordFile, password: secrets.password(.skydexPassword))
        try PhoneProfile.apply(to: paths, settings: settings)
    }
}
