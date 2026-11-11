import CoreBridge
import CoreClient
@testable import Skywire
import XCTest

/// The backup with groups (Settings, Android's ConfigManager.writeBackup): the key and
/// local/skychat in one zip, restored on another install. The restores run the core's generator.
final class BackupTests: XCTestCase {
    private var made: [URL] = []
    private let key2 = String(repeating: "0", count: 63) + "2"
    private let pk2 = "02c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5"

    /// What `jar cfM` (java.util.zip.ZipOutputStream, as Android writes) made of
    /// skywire-config.json {"sk": key2}, skychat/groups.db ("group-store-" x 200) and
    /// skychat/feeds/f1.db ("feed"): deflated, sizes in data descriptors, directory entries.
    private static let javaZip = """
    UEsDBBQACAgIAOVFa10AAAAAAAAAAAAAAAATAAQAc2t5d2lyZS1jb25maWcuanNvbv7KAACrVirOVrJSMqAMGCnpKBWAzKlQqgUA\
    UEsHCHQNY7IWAAAAUgAAAFBLAwQKAAAIAADlRWtdAAAAAAAAAAAAAAAACAAAAHNreWNoYXQvUEsDBAoAAAgAAOVFa10AAAAAAAAA\
    AAAAAAAOAAAAc2t5Y2hhdC9mZWVkcy9QSwMEFAAICAgA5UVrXQAAAAAAAAAAAAAAABMAAABza3ljaGF0L2ZlZWRzL2YxLmRiS0tN\
    TQEAUEsHCKtEQCMGAAAABAAAAFBLAwQUAAgICADlRWtdAAAAAAAAAAAAAAAAEQAAAHNreWNoYXQvZ3JvdXBzLmRiSy/KLy3QLS7J\
    L0rVTR9lj7JH2aPsUfYoe5Q9yh5lU8wGAFBLBwiVx+AlIgAAAGAJAABQSwECFAAUAAgICADlRWtddA1jshYAAABSAAAAEwAEAAAA\
    AAAAAAAAAAAAAAAAc2t5d2lyZS1jb25maWcuanNvbv7KAABQSwECCgAKAAAIAADlRWtdAAAAAAAAAAAAAAAACAAAAAAAAAAAAAAA\
    AABbAAAAc2t5Y2hhdC9QSwECCgAKAAAIAADlRWtdAAAAAAAAAAAAAAAADgAAAAAAAAAAAAAAAACBAAAAc2t5Y2hhdC9mZWVkcy9Q\
    SwECFAAUAAgICADlRWtdq0RAIwYAAAAEAAAAEwAAAAAAAAAAAAAAAACtAAAAc2t5Y2hhdC9mZWVkcy9mMS5kYlBLAQIUABQACAgI\
    AOVFa12Vx+AlIgAAAGAJAAARAAAAAAAAAAAAAAAAAPQAAABza3ljaGF0L2dyb3Vwcy5kYlBLBQYAAAAABQAFADcBAABVAQAAAAA=
    """

    override func tearDown() async throws {
        for url in made { try? FileManager.default.removeItem(at: url) }
        try await super.tearDown()
    }

    /// A data dir and its vault, each test's own Keychain service.
    private func install() throws -> (CorePaths, ConfigVault) {
        let paths = CorePaths(dataDir: FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString))
        try paths.createDirectories()
        made.append(paths.dataDir)
        let group = Bundle.main.object(forInfoDictionaryKey: "SkywireKeychainGroup") as? String
        let secrets = SecretStore(service: "com.skycoin.skywire.tests.\(UUID().uuidString)", accessGroup: group)
        return (paths, ConfigVault(paths: paths, secrets: secrets))
    }

    private func file(_ url: URL) -> String? { (try? Data(contentsOf: url)).map { String(decoding: $0, as: UTF8.self) } }

    private func scratch(_ data: Data) throws -> URL {
        let url = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString + ".zip")
        try data.write(to: url)
        made.append(url)
        return url
    }

    func testABackupBringsTheKeyAndTheGroupsToAnotherInstall() async throws {
        let (old, oldVault) = try install()
        _ = try await Identity.replace(secretKey: key2, paths: old, vault: oldVault)
        let skychat = old.localDir.appendingPathComponent("skychat", isDirectory: true)
        try FileManager.default.createDirectory(at: skychat.appendingPathComponent("feeds"), withIntermediateDirectories: true)
        try Data("groups".utf8).write(to: skychat.appendingPathComponent("groups.db"))
        try Data("feed".utf8).write(to: skychat.appendingPathComponent("feeds/f1.db"))

        let zip = try Backup.write(config: try oldVault.readText(), paths: old)
        made.append(zip)
        XCTAssertEqual(Backup.secretKey(in: zip), key2)
        let names = try ZipReader.entries(try Data(contentsOf: zip)).map(\.name).sorted()
        XCTAssertEqual(names, ["skychat/feeds/f1.db", "skychat/groups.db", "skywire-config.json"])

        let (new, newVault) = try install()
        _ = try await Identity.replace(secretKey: String(repeating: "0", count: 63) + "3", paths: new, vault: newVault)
        let stale = new.localDir.appendingPathComponent("skychat/stale.db")
        try FileManager.default.createDirectory(at: stale.deletingLastPathComponent(), withIntermediateDirectories: true)
        try Data("x".utf8).write(to: stale)
        let pk = try await Backup.restore(from: zip, paths: new, vault: newVault)
        XCTAssertEqual(pk, pk2)
        XCTAssertEqual(Identity.publicKey(inConfig: new.configFile), pk2)
        XCTAssertEqual(file(new.localDir.appendingPathComponent("skychat/groups.db")), "groups")
        XCTAssertEqual(file(new.localDir.appendingPathComponent("skychat/feeds/f1.db")), "feed")
        XCTAssertFalse(FileManager.default.fileExists(atPath: stale.path), "the replaced identity's group state is still there")
    }

    /// A backup written by the Android app restores here.
    func testAnAndroidBackupRestores() async throws {
        let zip = try scratch(try XCTUnwrap(Data(base64Encoded: Self.javaZip)))
        XCTAssertEqual(Backup.secretKey(in: zip), key2)
        let (paths, vault) = try install()
        let pk = try await Backup.restore(from: zip, paths: paths, vault: vault)
        XCTAssertEqual(pk, pk2)
        XCTAssertEqual(file(paths.localDir.appendingPathComponent("skychat/groups.db")), String(repeating: "group-store-", count: 200))
        XCTAssertEqual(file(paths.localDir.appendingPathComponent("skychat/feeds/f1.db")), "feed")
    }

    func testAnEntryOutsideItsFolderIsRefused() async throws {
        let url = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString + ".zip")
        made.append(url)
        FileManager.default.createFile(atPath: url.path, contents: nil)
        let handle = try FileHandle(forWritingTo: url)
        var zip = ZipWriter(handle: handle)
        try zip.add(name: "skywire-config.json", data: Data(#"{"sk":"\#(key2)"}"#.utf8))
        try zip.add(name: "skychat/../../escaped", data: Data("x".utf8))
        try zip.finish()
        try handle.close()
        let (paths, vault) = try install()
        do {
            _ = try await Backup.restore(from: url, paths: paths, vault: vault)
            XCTFail("an entry naming ../ was restored")
        } catch {}
        XCTAssertNil(Identity.publicKey(inConfig: paths.configFile), "a refused backup changed the identity")
    }

    func testAnotherFileIsNoBackup() throws {
        XCTAssertNil(Backup.secretKey(in: try scratch(Data("not a zip".utf8))))
    }
}
