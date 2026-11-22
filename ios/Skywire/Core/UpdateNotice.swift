import Foundation
import os

/// The banner that says, once, that a newer version is out (Android: core/UpdateNotice.kt).
///
/// Asked once per process, after the app starts. Update and Later both mark the version as
/// seen, so the banner never comes back for it; a newer release is offered again.
///
/// iOS cannot update an app by itself, so Update opens the store that installed it. An App
/// Store install is checked against the App Store's own listing, which a release reaches only
/// after review. A TestFlight install is checked against the `mobile-v*` tags, each of which
/// ios-release.yml uploads there. A build from Xcode has neither and is never told.
@MainActor
final class UpdateNotice: ObservableObject {
    static let shared = UpdateNotice()

    enum Source: Equatable {
        case appStore, testFlight

        /// The store's own name, never translated.
        var storeName: String { self == .appStore ? "App Store" : "TestFlight" }
    }

    struct Offer: Equatable {
        let version: String
        let source: Source
        /// What Update opens.
        let url: URL
        /// The version it was compared with, which the banner names.
        let installed: String
    }

    /// What the banner shows, nil when there is nothing to say.
    @Published private(set) var offer: Offer?

    /// The version the banner was last answered for.
    nonisolated static let seenKey = "updates_notice_seen"
    nonisolated static let lookupURL = URL(string: "https://itunes.apple.com/lookup")!
    nonisolated static let releasesURL = URL(string: "https://api.github.com/repos/skycoin/skywire/releases?per_page=30")!
    /// The TestFlight app, and its web page when the app is gone.
    nonisolated static let testFlightApp = URL(string: "itms-beta://")!
    nonisolated static let testFlightWeb = URL(string: "https://testflight.apple.com/")!

    private static let log = Logger(subsystem: Bundle.main.bundleIdentifier ?? "skywire", category: "updates")
    private var asked = false

    /// Asks once per process. A failed check stays quiet.
    func checkOnce() async {
        guard !asked else { return }
        asked = true
        let override = Self.debugOverride()
        guard let source = override?.source ?? Self.installSource(),
              let installed = AppVersion(override?.installed ?? Self.installedVersion)
        else { return }
        let found: Offer?
        do {
            switch source {
            case .appStore:
                let region = Locale.current.region?.identifier.lowercased() ?? "us"
                var url = URLComponents(url: override?.lookup ?? Self.lookupURL, resolvingAgainstBaseURL: false)!
                url.queryItems = [URLQueryItem(name: "bundleId", value: Bundle.main.bundleIdentifier), URLQueryItem(name: "country", value: region)]
                found = Self.selectAppStore(try await Self.fetch(url.url!), installed: installed, os: ProcessInfo.processInfo.operatingSystemVersion)
            case .testFlight:
                found = Self.selectTestFlight(try await Self.fetch(override?.releases ?? Self.releasesURL), installed: installed)
            }
        } catch {
            Self.log.warning("update check failed: \(error.localizedDescription, privacy: .public)")
            return
        }
        guard let found, UserDefaults.standard.string(forKey: Self.seenKey) != found.version else { return }
        offer = found
    }

    /// Update or Later: the banner goes and this version is not offered again.
    func dismiss(_ offer: Offer) {
        if self.offer == offer { self.offer = nil }
        UserDefaults.standard.set(offer.version, forKey: Self.seenKey)
    }

    // MARK: Where updates come from

    /// A development or ad hoc build carries its provisioning profile; App Store and TestFlight
    /// builds do not, and TestFlight's receipt is the sandbox one.
    nonisolated static func source(simulator: Bool, provisioned: Bool, receipt: String?) -> Source? {
        if simulator || provisioned { return nil }
        return receipt == "sandboxReceipt" ? .testFlight : .appStore
    }

    private static func installSource() -> Source? {
        #if targetEnvironment(simulator)
        let simulator = true
        #else
        let simulator = false
        #endif
        return source(
            simulator: simulator,
            provisioned: Bundle.main.path(forResource: "embedded", ofType: "mobileprovision") != nil,
            receipt: Bundle.main.appStoreReceiptURL?.lastPathComponent
        )
    }

    private static var installedVersion: String {
        Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? ""
    }

    // MARK: The checks

    /// The App Store's version when it is newer than `installed` and runs on this iOS, else nil.
    /// An app not listed in this country answers no results.
    nonisolated static func selectAppStore(_ body: Data, installed: AppVersion, os: OperatingSystemVersion) -> Offer? {
        struct Lookup: Decodable {
            struct Result: Decodable {
                let version: String
                let trackViewUrl: String
                let minimumOsVersion: String?
            }
            let results: [Result]
        }
        guard let listed = (try? JSONDecoder().decode(Lookup.self, from: body))?.results.first,
              let version = AppVersion(listed.version), version > installed,
              let url = URL(string: listed.trackViewUrl)
        else { return nil }
        if let minimum = listed.minimumOsVersion.flatMap(AppVersion.init), minimum > AppVersion(os) { return nil }
        return Offer(version: listed.version, source: .appStore, url: url, installed: installed.description)
    }

    /// The newest `mobile-v*` release above `installed`, drafts and pre-releases skipped. Not
    /// `/releases/latest`, which is usually a desktop release.
    nonisolated static func selectTestFlight(_ body: Data, installed: AppVersion) -> Offer? {
        struct Release: Decodable {
            let tag_name: String
            let draft: Bool
            let prerelease: Bool
        }
        let newest = ((try? JSONDecoder().decode([Release].self, from: body)) ?? [])
            .filter { !$0.draft && !$0.prerelease && $0.tag_name.lowercased().hasPrefix("mobile-v") }
            .compactMap { AppVersion($0.tag_name) }
            .filter { $0 > installed }
            .max()
        return newest.map { Offer(version: $0.description, source: .testFlight, url: testFlightApp, installed: installed.description) }
    }

    private static func fetch(_ url: URL) async throws -> Data {
        var request = URLRequest(url: url, timeoutInterval: 15)
        // GitHub refuses an API request that does not say who is asking.
        request.setValue("skywire-ios", forHTTPHeaderField: "User-Agent")
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        let (data, response) = try await URLSession.shared.data(for: request)
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard (200..<300).contains(status) else { throw URLError(.badServerResponse) }
        return data
    }

    // MARK: Testing

    private struct Override {
        let source: Source?
        let installed: String?
        let lookup: URL?
        let releases: URL?
    }

    /// Debug builds only: the UI test stands in for a store install and its store
    /// (`-UpdateNoticeSource testflight -UpdateNoticeReleases <url>`).
    private static func debugOverride() -> Override? {
        #if DEBUG
        let defaults = UserDefaults.standard
        guard let name = defaults.string(forKey: "UpdateNoticeSource") else { return nil }
        return Override(
            source: name == "appstore" ? .appStore : name == "testflight" ? .testFlight : nil,
            installed: defaults.string(forKey: "UpdateNoticeInstalled"),
            lookup: defaults.string(forKey: "UpdateNoticeLookup").flatMap(URL.init(string:)),
            releases: defaults.string(forKey: "UpdateNoticeReleases").flatMap(URL.init(string:))
        )
        #else
        return nil
        #endif
    }
}

/// A release version, compared as numbers: 0.0.10 is above 0.0.9 (Android: AppUpdates.Version).
struct AppVersion: Comparable, CustomStringConvertible, Sendable {
    let major: Int, minor: Int, patch: Int

    /// `X.Y.Z` out of `1.2.3`, `v1.2.3` or `mobile-v1.2.3`; a suffix is ignored and a missing
    /// patch reads as 0, as App Store versions may have two parts.
    init?(_ text: String) {
        var bare = Substring(text.lowercased())
        if bare.hasPrefix("mobile-") { bare = bare.dropFirst("mobile-".count) }
        if bare.hasPrefix("v") { bare = bare.dropFirst() }
        let parts = bare.split(separator: ".", maxSplits: 2).map { $0.prefix { $0.isNumber } }
        guard parts.count >= 2, let major = Int(parts[0]), let minor = Int(parts[1]) else { return nil }
        self.major = major
        self.minor = minor
        patch = parts.count > 2 ? Int(parts[2]) ?? 0 : 0
    }

    init(_ os: OperatingSystemVersion) {
        major = os.majorVersion
        minor = os.minorVersion
        patch = os.patchVersion
    }

    static func < (a: AppVersion, b: AppVersion) -> Bool {
        (a.major, a.minor, a.patch) < (b.major, b.minor, b.patch)
    }

    var description: String { "\(major).\(minor).\(patch)" }
}
