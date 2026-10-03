@testable import CoreClient
import XCTest

/// The phone profile against Android's (playbook item 2.2). Two fixtures:
/// `config-gen-mac.json`, what `skywire-mobile config gen` writes with the
/// phone's argv on the Mac, and `config-android-kotlin.json`, the config the
/// Android app's own profile wrote on the emulator (secrets zeroed in both).
final class PhoneProfileTests: XCTestCase {
    /// Where the Android app keeps the core, as the Kotlin fixture's paths say.
    static let androidPaths = CorePaths(dataDir: URL(fileURLWithPath: "/data/user/0/com.skycoin.skywire/files/skywire"))

    /// Where an iOS app container keeps it: note the space.
    static let iosPaths = CorePaths(dataDir: URL(fileURLWithPath:
        "/var/mobile/Containers/Data/Application/0A1B/Library/Application Support/skywire"))

    /// The edits only iOS makes; everything else must match Kotlin.
    static let iosOnly = [".dmsg.local_relay", ".memory_limit"]

    static func fixture(_ name: String) throws -> [String: Any] {
        let url = try XCTUnwrap(Bundle.module.url(forResource: name, withExtension: "json", subdirectory: "Fixtures/profile"))
        return try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: url)) as? [String: Any])
    }

    /// The settings the Kotlin fixture was written with, read back from it.
    static func settings(of config: [String: Any]) -> ProfileSettings {
        let routing = config["routing"] as? [String: Any]
        let hypervisor = config["hypervisor"] as? [String: Any]
        let transport = config["transport"] as? [String: Any]
        return ProfileSettings(
            transportPrimary: (routing?["transport_preference"] as? [String])?.first ?? "",
            fleetEnabled: hypervisor?["dmsg_ingest"] as? Bool ?? false,
            publicAutoconnect: transport?["public_autoconnect"] as? Bool ?? false,
            logLevel: config["log_level"] as? String ?? "",
            remoteManagementPK: (config["hypervisors"] as? [String])?.first
        )
    }

    // MARK: Against Kotlin

    /// Kotlin's output through the Swift profile comes back unchanged but for
    /// the iOS-only edits: every pin Swift makes, Kotlin had already made the
    /// same way, and everything Kotlin passed through (the user's `--srv`
    /// keys, the log store's type) Swift passes through too.
    func testKotlinsOutputIsAFixpoint() throws {
        let kotlin = try Self.fixture("config-android-kotlin")
        let swift = try PhoneProfile.edit(kotlin, settings: Self.settings(of: kotlin), paths: Self.androidPaths)
        XCTAssertEqual(differences(kotlin, swift), Self.iosOnly)
    }

    /// From a freshly generated config, Swift makes every pin Kotlin made.
    func testAFreshConfigGetsKotlinsPins() throws {
        let kotlin = try Self.fixture("config-android-kotlin")
        let generated = try Self.fixture("config-gen-mac")
        let swift = try PhoneProfile.edit(generated, settings: Self.settings(of: kotlin), paths: Self.androidPaths)

        let owned = [
            ".cli_addr", ".hypervisors", ".log_level", ".local_path", ".pty", ".skywire-tcp", ".dmsgscp",
            ".transport.public_autoconnect", ".transport.log_store",
            ".hypervisor.db_path", ".hypervisor.dmsg_ingest", ".hypervisor.tp_viz", ".hypervisor.lan_dmsg_server",
            ".launcher.bin_path", ".routing.transport_preference", ".routing.mux_routes",
        ]
        for path in owned {
            XCTAssertEqual(differences(value(kotlin, path), value(swift, path)), [], path)
        }

        // The apps: Kotlin's argv minus what the user added since (the server
        // keys the SOCKS and VPN screens wrote) is Swift's.
        for name in ["skychat", "skydex-client", "skysocks-client"] {
            let kotlinApp = try XCTUnwrap(app(kotlin, name))
            let swiftApp = try XCTUnwrap(app(swift, name))
            let kotlinArgs = withoutServerKey(try XCTUnwrap(AppArgs.split(kotlinApp["args"] as? String ?? "")))
            XCTAssertEqual(AppArgs.split(swiftApp["args"] as? String ?? ""), kotlinArgs, name)
            XCTAssertEqual(swiftApp["auto_start"] as? Bool, kotlinApp["auto_start"] as? Bool, name)
        }
        // vpn-client is not the profile's: untouched.
        XCTAssertEqual(differences(app(generated, "vpn-client"), app(swift, "vpn-client")), [])

        // And nothing the profile does not own changed.
        let untouched = differences(generated, swift).filter { path in
            !(owned + Self.iosOnly + [".launcher.apps"]).contains { path == $0 || path.hasPrefix($0 + ".") || path.hasPrefix($0 + "[") }
        }
        XCTAssertEqual(untouched, [])
    }

    // MARK: The settings

    func testSettingsLandWhereKotlinPutsThem() throws {
        let pk = "02" + String(repeating: "ab", count: 32)
        let settings = ProfileSettings(
            transportPrimary: "stcpr", fleetEnabled: true, publicAutoconnect: true, logLevel: "DEBUG",
            remoteManagementPK: " \(pk.uppercased()) ", memoryLimit: "64MiB"
        )
        let config = try PhoneProfile.edit(try Self.fixture("config-gen-mac"), settings: settings, paths: Self.iosPaths)
        XCTAssertEqual(value(config, ".hypervisor.dmsg_ingest") as? Bool, true)
        XCTAssertEqual(value(config, ".transport.public_autoconnect") as? Bool, true)
        XCTAssertEqual(config["log_level"] as? String, "debug")
        XCTAssertEqual(config["hypervisors"] as? [String], [pk])
        XCTAssertEqual(config["memory_limit"] as? String, "64MiB")
        XCTAssertEqual(
            value(config, ".routing.transport_preference") as? [String],
            ["stcpr", "squicr", "sudph", "stcp", "webrtc", "swsr", "swtr", "dmsg"]
        )
    }

    func testUnusableSettingsFallBackToTheDefaults() throws {
        let settings = ProfileSettings(transportPrimary: "quic", logLevel: "loud", remoteManagementPK: "not-a-key", memoryLimit: nil)
        let config = try PhoneProfile.edit(try Self.fixture("config-gen-mac"), settings: settings, paths: Self.iosPaths)
        XCTAssertEqual((value(config, ".routing.transport_preference") as? [String])?.first, "dmsg")
        XCTAssertEqual(config["log_level"] as? String, "info")
        XCTAssertEqual(config["hypervisors"] as? [String], [])
        XCTAssertNil(config["memory_limit"], "no limit writes no key")
    }

    func testTheProfileIsIdempotent() throws {
        let once = try PhoneProfile.edit(try Self.fixture("config-gen-mac"), settings: ProfileSettings(), paths: Self.iosPaths)
        let twice = try PhoneProfile.edit(once, settings: ProfileSettings(), paths: Self.iosPaths)
        XCTAssertEqual(differences(once, twice), [])
    }

    /// An iOS path has a space in it ("Application Support"); the argv string
    /// must carry it the way the visor's joinArgs writes it, or skychat reads
    /// half a path.
    func testPathsWithASpaceAreQuotedAsTheVisorQuotes() throws {
        var config = try Self.fixture("config-gen-mac")
        var launcher = try XCTUnwrap(config["launcher"] as? [String: Any])
        launcher["apps"] = [["name": "skychat", "args": "--addr 127.0.0.1:8001", "auto_start": false, "port": 1]]
        config["launcher"] = launcher
        let edited = try PhoneProfile.edit(config, settings: ProfileSettings(), paths: Self.iosPaths)
        let vector = try goVectors().joined[0]
        XCTAssertEqual(app(edited, "skychat")?["args"] as? String, vector.joined)
    }

    func testAMalformedSectionIsAnError() {
        XCTAssertThrowsError(try PhoneProfile.edit(["hypervisor": "on"], settings: ProfileSettings(), paths: Self.iosPaths)) { error in
            XCTAssertEqual(error as? PhoneProfile.ProfileError, .notAnObject("hypervisor"))
        }
    }

    /// The file round trip, and the directories the core needs up front.
    func testApplyRewritesTheFileInPlace() throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("profile-\(UUID().uuidString)/Application Support/skywire")
        let paths = CorePaths(dataDir: dir)
        try paths.createDirectories()
        defer { try? FileManager.default.removeItem(at: dir.deletingLastPathComponent().deletingLastPathComponent()) }
        let url = try XCTUnwrap(Bundle.module.url(forResource: "config-gen-mac", withExtension: "json", subdirectory: "Fixtures/profile"))
        try FileManager.default.copyItem(at: url, to: paths.configFile)
        try PhoneProfile.apply(to: paths, settings: ProfileSettings())
        let written = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: paths.configFile)) as? [String: Any])
        XCTAssertEqual(written["local_path"] as? String, paths.localDir.path)
        XCTAssertTrue(FileManager.default.fileExists(atPath: paths.localDir.path))
        XCTAssertTrue(FileManager.default.fileExists(atPath: paths.binDir.path))
    }

    // MARK: Argv

    struct GoVectors: Decodable {
        struct Joined: Decodable {
            let tokens: [String]
            let joined: String
        }

        struct Split: Decodable {
            let `in`: String
            let out: [String]?
            let err: Bool
        }

        let joined: [Joined]
        let split: [Split]
    }

    func goVectors() throws -> GoVectors {
        let url = try XCTUnwrap(Bundle.module.url(forResource: "go-args-vectors", withExtension: "json", subdirectory: "Fixtures/profile"))
        return try JSONDecoder().decode(GoVectors.self, from: Data(contentsOf: url))
    }

    /// Split and join agree with the visor's own splitArgs and joinArgs, on
    /// strings Go produced.
    func testArgsQuoteAsTheVisorDoes() throws {
        let vectors = try goVectors()
        for vector in vectors.joined {
            XCTAssertEqual(AppArgs.join(vector.tokens), vector.joined)
            XCTAssertEqual(AppArgs.split(vector.joined), vector.tokens)
        }
        for vector in vectors.split {
            XCTAssertEqual(AppArgs.split(vector.in), vector.err ? nil : vector.out, vector.in)
        }
    }

    func testPinValue() {
        func pinned(_ args: [String], _ next: @escaping (String?) -> String?) -> [String] {
            var args = args
            AppArgs.pinValue(&args, ["--addr", "-addr"], next)
            return args
        }
        let loopback: (String?) -> String? = { AppArgs.loopbackAddress($0, defaultPort: 1080) }
        XCTAssertEqual(pinned(["--addr", ":1080", "--x"], loopback), ["--addr", "127.0.0.1:1080", "--x"])
        XCTAssertEqual(pinned(["-addr=0.0.0.0:2000"], loopback), ["--addr=127.0.0.1:2000"])
        XCTAssertEqual(pinned(["--x"], loopback), ["--x", "--addr", "127.0.0.1:1080"])
        XCTAssertEqual(pinned(["--addr"], loopback), ["--addr", "127.0.0.1:1080"])
        // An address it cannot read is left for the visor to report.
        XCTAssertEqual(pinned(["--addr", "nonsense"], loopback), ["--addr", "nonsense"])
        XCTAssertEqual(AppArgs.value(["--srv", "pk", "--addr=:1"], ["--addr"]), ":1")
    }

    func testSocksPins() {
        XCTAssertEqual(SocksProfile.phoneArgs(["--addr", ":1080"]), ["--addr", "127.0.0.1:1080", "--reconnect"])
        XCTAssertEqual(SocksProfile.phoneArgs(["--reconnect=true", "--addr", ":1090"]), ["--reconnect=true", "--addr", "127.0.0.1:1090"])
        XCTAssertEqual(SocksProfile.listenPort(["--addr", "127.0.0.1:1090"]), 1090)
        XCTAssertEqual(SocksProfile.listenPort([]), 1080)
    }

    /// The server key the visor wrote, in either spelling; none when empty.
    func testSocksServerKey() {
        XCTAssertEqual(SocksProfile.serverPK(["--srv", "02ab", "--addr", "127.0.0.1:1080"]), "02ab")
        XCTAssertEqual(SocksProfile.serverPK(["--srv=03cd"]), "03cd")
        XCTAssertNil(SocksProfile.serverPK(["--srv", ""]))
        XCTAssertNil(SocksProfile.serverPK(["--addr", "127.0.0.1:1080"]))
    }

    /// A port change rewrites the listener only (on loopback, whatever host
    /// it had), keeps the server and the pins, and quotes as Go's splitArgs
    /// reads (checked back through AppArgs.split, the port of it).
    func testSocksPortChangeKeepsTheRest() {
        let args = ["--srv", "02ab", "--addr", "127.0.0.1:1080", "--reconnect"]
        let written = SocksProfile.args(args, withPort: 1090)
        XCTAssertEqual(written, "--srv 02ab --addr 127.0.0.1:1090 --reconnect")
        XCTAssertEqual(AppArgs.split(written), ["--srv", "02ab", "--addr", "127.0.0.1:1090", "--reconnect"])
        XCTAssertEqual(SocksProfile.args(["--addr=:1080"], withPort: 2000), "--addr=127.0.0.1:2000")
        XCTAssertEqual(SocksProfile.args(["--srv", "02ab"], withPort: 2000), "--srv 02ab --addr 127.0.0.1:2000")
        XCTAssertEqual(SocksProfile.args(["--note", "two words", "--addr", ":1"], withPort: 1080), #"--note "two words" --addr 127.0.0.1:1080"#)
    }

    /// The list the phone caches reads back as it was.
    func testServiceEntriesRoundTrip() throws {
        let entries = [
            ServiceEntry(address: "02ab:44", type: "proxy", geo: GeoInfo(country: "DE", region: "Hesse"), version: "v1.3.97"),
            ServiceEntry(address: "03cd:44"),
        ]
        let decoded = try JSONDecoder().decode([ServiceEntry].self, from: JSONEncoder().encode(entries))
        XCTAssertEqual(decoded, entries)
        XCTAssertEqual(decoded[0].pk, "02ab")
    }

    func testSkychatDropsPortlessAndKeepsTheRest() {
        let args = SkychatProfile.phoneArgs(
            ["--portless", "--pair-enable", "--persist=false", "--addr", ":8002"], passwordFile: "/p", historyFile: "/h"
        )
        XCTAssertEqual(args, ["--pair-enable", "--persist=false", "--addr", "127.0.0.1:8002", "--persist-db", "/h", "--password-file", "/p"])
    }

    // MARK: Password file

    /// A record computed outside Swift (Python's hashlib) for a known salt.
    func testPasswordFileMatchesAnIndependentHash() {
        let record = "000102030405060708090a0b0c0d0e0f:0b5d4ab2eccc67581959aa8141064fc6aa96e6481dae773137d3da95c29bfd0b"
        XCTAssertTrue(PasswordFile.matches(record, "Rec0rd!ng-Pass"))
        XCTAssertTrue(PasswordFile.matches(record.uppercased() + "\n", "Rec0rd!ng-Pass"))
        XCTAssertFalse(PasswordFile.matches(record, "Rec0rd!ng-Pas"))
        XCTAssertFalse(PasswordFile.matches("zz:00", "x"))
        let fresh = PasswordFile.record("s3cret")
        XCTAssertTrue(PasswordFile.matches(fresh, "s3cret"))
        XCTAssertNotEqual(fresh, PasswordFile.record("s3cret"), "the salt is fresh each time")
    }

    /// Left alone while it stands for the password; rewritten when the
    /// password rotated.
    func testEnsureRewritesOnlyAStaleFile() throws {
        let url = FileManager.default.temporaryDirectory.appendingPathComponent("gate-\(UUID().uuidString)/skychat-password")
        defer { try? FileManager.default.removeItem(at: url.deletingLastPathComponent()) }
        try PasswordFile.ensure(at: url, password: "one")
        let first = try String(contentsOf: url, encoding: .utf8)
        try PasswordFile.ensure(at: url, password: "one")
        XCTAssertEqual(try String(contentsOf: url, encoding: .utf8), first)
        try PasswordFile.ensure(at: url, password: "two")
        XCTAssertTrue(PasswordFile.matches(try String(contentsOf: url, encoding: .utf8), "two"))
        let permissions = try FileManager.default.attributesOfItem(atPath: url.path)[.posixPermissions] as? Int
        XCTAssertEqual(permissions, 0o600)
    }

    // MARK: Helpers

    private func app(_ config: [String: Any], _ name: String) -> [String: Any]? {
        ((config["launcher"] as? [String: Any])?["apps"] as? [[String: Any]])?.first { $0["name"] as? String == name }
    }

    /// `--srv <pk>`: the server key the app screens write, user state.
    private func withoutServerKey(_ args: [String]) -> [String] {
        var out: [String] = []
        var skip = false
        for token in args {
            if skip { skip = false; continue }
            if token == "--srv" { skip = true; continue }
            if token.hasPrefix("--srv=") { continue }
            out.append(token)
        }
        return out
    }

    /// The value at a dotted path (".a.b"), or nil.
    private func value(_ config: [String: Any], _ path: String) -> Any? {
        var current: Any? = config
        for key in path.split(separator: ".") {
            current = (current as? [String: Any])?[String(key)]
        }
        return current
    }
}

/// The dotted paths at which two decoded JSON values differ.
func differences(_ a: Any?, _ b: Any?, at path: String = "") -> [String] {
    switch (a, b) {
    case (nil, nil):
        return []
    case let (a as [String: Any], b as [String: Any]):
        return Set(a.keys).union(b.keys).sorted().flatMap { differences(a[$0], b[$0], at: "\(path).\($0)") }
    case let (a as [Any], b as [Any]):
        guard a.count == b.count else { return [path] }
        return zip(a, b).enumerated().flatMap { index, pair in differences(pair.0, pair.1, at: "\(path)[\(index)]") }
    case let (a?, b?):
        return (a as AnyObject).isEqual(b) ? [] : [path]
    default:
        return [path]
    }
}

/// The generated passwords meet the API's policy (the Keychain half of
/// SecretStore is exercised on the Simulator, in SkywireTests).
final class SecretStorePolicyTests: XCTestCase {
    func testGeneratedPasswordsMeetTheAPIsPolicy() {
        for _ in 0..<200 {
            let password = SecretStore.generatePassword()
            XCTAssertEqual(password.count, 24)
            XCTAssertTrue(password.allSatisfy { $0.isASCII && $0 > " " && $0 != "\u{7F}" }, password)
            XCTAssertTrue(password.contains { $0.isUppercase }, password)
            XCTAssertTrue(password.contains { $0.isLowercase }, password)
            XCTAssertTrue(password.contains { $0.isNumber }, password)
            XCTAssertTrue(password.contains { !$0.isLetter && !$0.isNumber }, password)
        }
        XCTAssertNotEqual(SecretStore.generatePassword(), SecretStore.generatePassword())
    }
}
