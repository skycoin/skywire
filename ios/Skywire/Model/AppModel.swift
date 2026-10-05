import CoreBridge
import CoreClient
import Foundation
import os
import UIKit

/// The app's model of the core: its lifecycle, whether its local API is up,
/// the session, and the user's Connect. Every screen reads it; only it starts
/// and stops the core. The Swift side of Android's SkywireCoreService plus the
/// connect half of HomeViewModel.
@MainActor
final class AppModel: ObservableObject {
    @Published private(set) var coreState: CoreState = .stopped
    /// The local API has answered /api/ping since the core last came up.
    @Published private(set) var apiUp = false
    /// A Connect, Disconnect or restart is in progress.
    @Published private(set) var busy = false
    /// Why the last of those failed, in the core's words.
    @Published private(set) var lastError: String?
    /// The visor's public key, read from the config on disk, so it is known
    /// before the core runs and right after an operation took it down.
    @Published private(set) var publicKey: String?
    /// The process's phys_footprint (Footprint), refreshed once a minute.
    @Published private(set) var footprint: UInt64?

    /// The core runs and its API answers.
    var connected: Bool { coreState == .running && apiUp }

    /// How many times the core's API has come up in this process: a screen
    /// that keeps state across its own disappearances (the chat page) tells
    /// "the same core" from "a core that restarted meanwhile" by it.
    private(set) var coreSession = 0

    let settings: AppSettings
    let client: CoreClient
    let log: CoreLog
    let paths: CorePaths
    let vault: ConfigVault
    private let host: any CoreHost
    private let footprintLog = Logger(subsystem: Bundle.main.bundleIdentifier ?? "skywire", category: "footprint")
    private let lifecycleLog = Logger(subsystem: Bundle.main.bundleIdentifier ?? "skywire", category: "lifecycle")
    /// The background grace in progress, if any (see `enteredBackground`).
    private var backgroundTask = UIBackgroundTaskIdentifier.invalid
    private var launched = false
    /// The account reset is tried once per process, as on Android: a second
    /// rejection means something else is wrong, and a loop would hide it.
    private var accountResetTried = false

    init(host: any CoreHost, client: CoreClient, settings: AppSettings, log: CoreLog, paths: CorePaths, vault: ConfigVault) {
        self.host = host
        self.client = client
        self.settings = settings
        self.log = log
        self.paths = paths
        self.vault = vault
    }

    /// The app's model: the core in this process, under Application Support,
    /// its API password in the Keychain.
    static func inApp() -> AppModel {
        let log = CoreLog()
        // Before any start, so the core's first lines reach the log.
        CoreBridge.shared.setLogSink(log.sink)
        let settings = AppSettings()
        let paths = CorePaths.appSupport()
        let secrets = SecretStore.app()
        let client = CoreClient(transport: LoopbackTransport(origin: ConfigProfile.apiOrigin)) {
            try secrets.password(.apiPassword)
        }
        let host = InAppCoreHost(paths: paths, secrets: secrets) { settings.profileSettings }
        return AppModel(host: host, client: client, settings: settings, log: log, paths: paths,
                        vault: ConfigVault(paths: paths, secrets: secrets))
    }

    /// Starts the model's loops once, and connects if the user left the core
    /// connected.
    func launch() {
        // Under XCTest the app is only the host of SkywireAppTests: it must not
        // start a core, which would take 127.0.0.1:8000 from CoreBridgeTests.
        guard !launched, ProcessInfo.processInfo.environment["XCTestConfigurationFilePath"] == nil else { return }
        launched = true
        publicKey = readPublicKey()
        Task { [weak self, host] in
            for await state in host.state {
                guard let self else { return }
                self.coreStateChanged(state)
            }
        }
        Task { [weak self] in
            while let self {
                self.refreshFootprint()
                try? await Task.sleep(for: .seconds(60))
            }
        }
        if settings.wantsConnected {
            connect()
        } else {
            // A plaintext left by a core that never stopped cleanly (the app died under it).
            sealIfAsked()
        }
    }

    func connect() {
        settings.wantsConnected = true
        perform { [host] in try await host.start() }
    }

    func disconnect() {
        settings.wantsConnected = false
        perform { [host] in try await host.stop() }
        sealAfterStop = true
    }

    /// Runs an identity change with the core stopped, then starts it again if it ran (Android:
    /// SettingsViewModel.identityAction). The error is the caller's to show.
    func changeIdentity(_ change: @escaping @Sendable () async throws -> Void) async throws {
        let wasRunning = coreState != .stopped && coreState != .failed
        busy = true
        defer { busy = false }
        if wasRunning { try await host.stop() }
        do {
            try await change()
        } catch {
            if wasRunning { try? await host.start() }
            throw error
        }
        // The client's cached key belongs to a visor that no longer exists.
        await client.forgetIdentity()
        publicKey = readPublicKey()
        if wasRunning {
            try await host.start()
        } else {
            try vault.seal(enabled: settings.configEncrypted)
        }
    }

    /// Seals the config when the user asked for it and the core is down.
    private func sealIfAsked() {
        guard settings.configEncrypted, coreState == .stopped || coreState == .failed else { return }
        do {
            try vault.seal(enabled: true)
        } catch {
            lastError = error.localizedDescription
        }
    }

    /// Set by Disconnect, so the config is sealed once that stop has finished.
    private var sealAfterStop = false

    /// Stops the core and starts it again. The start re-applies the profile,
    /// so this is also how a pinned setting (log level, Fleet) takes effect.
    func restartCore() {
        perform { [host] in try await host.restart() }
    }

    /// A pinned setting changed: restart the core if it runs, so the change
    /// applies now (Android does the same for the log level and Fleet).
    func applyPinnedSettingNow() {
        guard coreState == .running || coreState == .starting else { return }
        restartCore()
    }

    /// Left for the background with the core running: asks iOS for the
    /// standard background grace (about 30 s), so the core keeps receiving
    /// for that long and a message that arrives just after the user switched
    /// away still becomes a notification. After it iOS suspends the app, and
    /// the in-process core with it; receiving while suspended is the
    /// packet-tunnel extension's (Lane D), where the core outlives the app.
    /// G3 found the app suspended 5 s after the home gesture without this.
    func enteredBackground() {
        guard backgroundTask == .invalid, coreState == .running else { return }
        backgroundTask = UIApplication.shared.beginBackgroundTask(withName: "Skywire core") { [weak self] in
            self?.endBackgroundGrace(expired: true)
        }
        // The time left is not known yet here (UIKit reports no limit until
        // the app is fully in the background), so the log says how long the
        // grace lasted when it ends instead.
        graceBegan = .now
        lifecycleLog.notice("background grace began")
    }

    /// Ends the grace: back in the foreground, or iOS called time.
    func endBackgroundGrace(expired: Bool = false) {
        guard backgroundTask != .invalid else { return }
        UIApplication.shared.endBackgroundTask(backgroundTask)
        backgroundTask = .invalid
        let lasted = graceBegan.map { ContinuousClock.now - $0 } ?? .zero
        let seconds = Double(lasted.components.seconds) + Double(lasted.components.attoseconds) / 1e18
        lifecycleLog.notice("background grace \(expired ? "expired" : "ended", privacy: .public) after \(seconds, format: .fixed(precision: 1), privacy: .public) s")
    }

    private var graceBegan: ContinuousClock.Instant?

    /// Back in the foreground: the footprint on screen is minutes old, and a
    /// Simulator app that was suspended may have missed a state change.
    func becameActive() {
        endBackgroundGrace()
        refreshFootprint()
        if coreState == .running, !apiUp {
            Task { await waitForAPI() }
        }
    }

    /// Hands a failed call's error to the one place that can act on it: a
    /// rejected password is recovered here, anything else is the caller's.
    /// Returns whether it was handled.
    @discardableResult
    func handle(_ error: any Error) -> Bool {
        guard case CoreClientError.authFailed = error else { return false }
        resetAccount(after: error)
        return true
    }

    private func coreStateChanged(_ state: CoreState) {
        let wasRunning = coreState == .running
        coreState = state
        if state != .running {
            apiUp = false
        } else if !wasRunning {
            publicKey = readPublicKey()
            Task { await waitForAPI() }
        }
    }

    /// Pings until the API answers (a start returns once it is up, but the
    /// core also restarts itself through its API), then makes sure there is a
    /// session, so the first screen that asks has one.
    private func waitForAPI() async {
        while coreState == .running, !apiUp {
            if await client.ping() {
                coreSession += 1
                apiUp = true
                break
            }
            try? await Task.sleep(for: .milliseconds(700))
        }
        guard apiUp else { return }
        do {
            try await client.ensureSession()
        } catch {
            if !handle(error) {
                lastError = error.localizedDescription
            }
        }
    }

    /// The Keychain's password no longer opens users.db (the Keychain lost
    /// its item under a surviving data dir). Stop, drop users.db, start: the
    /// login then creates the account with the current password.
    private func resetAccount(after error: any Error) {
        guard !accountResetTried else {
            lastError = error.localizedDescription
            return
        }
        accountResetTried = true
        perform { [host] in
            try await host.stop()
            try await host.resetAccount()
            try await host.start()
        }
    }

    private func perform(_ operation: @escaping @Sendable () async throws -> Void) {
        busy = true
        lastError = nil
        Task {
            do {
                try await operation()
            } catch {
                // A start that Disconnect aborted fails with "stopped while
                // starting": the user's doing, not an error to show.
                if settings.wantsConnected || CoreBridge.shared.state != .stopped {
                    lastError = error.localizedDescription
                }
            }
            publicKey = readPublicKey()
            busy = false
            if sealAfterStop {
                sealAfterStop = false
                sealIfAsked()
            }
        }
    }

    private func refreshFootprint() {
        footprint = Footprint.current()
        // Into the unified log as well, so a measurement run can be read back
        // afterwards (`log show`), as the M1 gauge did.
        if let footprint {
            let mib = Double(footprint) / 1_048_576
            let core = coreState.description
            footprintLog.notice("phys_footprint \(mib, format: .fixed(precision: 1), privacy: .public) MiB, core \(core, privacy: .public)")
        }
    }

    /// `pk` from the config, sealed or not, or nil before the first start.
    private func readPublicKey() -> String? {
        guard let text = try? vault.readText(),
              let config = try? JSONSerialization.jsonObject(with: Data(text.utf8)) as? [String: Any],
              let pk = config["pk"] as? String, !pk.isEmpty
        else { return nil }
        return pk
    }
}

extension SecretStore {
    /// The app's store: the bundle's service name and the Keychain access
    /// group the xcconfig names (Info.plist `SkywireKeychainGroup`).
    static func app() -> SecretStore {
        let bundle = Bundle.main
        let group = (bundle.object(forInfoDictionaryKey: "SkywireKeychainGroup") as? String).flatMap { $0.isEmpty ? nil : $0 }
        return SecretStore(service: "\(bundle.bundleIdentifier ?? "com.skycoin.skywire").core", accessGroup: group)
    }
}
