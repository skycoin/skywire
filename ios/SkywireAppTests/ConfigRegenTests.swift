import CoreClient
@testable import Skywire
import XCTest

/// The config regenerated once per build of the app (Android: ConfigManager.regenerateAfterUpdate),
/// with a stand-in for the generator so each way it can end is tried.
final class ConfigRegenTests: XCTestCase {
    private var paths: CorePaths!
    private let pk = "03710d1ddb29f9efbd183e7a41ef6f68df2792f96803a3094ea2e12e9f878eaf22"

    override func setUpWithError() throws {
        paths = CorePaths(dataDir: FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true))
        try paths.createDirectories()
        try write(["pk": pk, "version": "v1.3.99"])
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: paths.dataDir)
    }

    private func write(_ config: [String: Any]) throws {
        try JSONSerialization.data(withJSONObject: config).write(to: paths.configFile)
    }

    private func config() throws -> [String: Any] {
        try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: paths.configFile)) as? [String: Any])
    }

    /// No stamp yet (an install from before this) or another build's: regenerate, once.
    func testRegeneratesOncePerBuild() async throws {
        XCTAssertTrue(ConfigProfile.updatedSinceGenerated(paths, stamp: "b1"))
        var runs = 0
        await ConfigProfile.regenerateAfterUpdate(paths, stamp: "b1") { paths in
            runs += 1
            try JSONSerialization.data(withJSONObject: ["pk": self.pk, "version": "v1.3.100"]).write(to: paths.configFile)
        }
        XCTAssertEqual(runs, 1)
        XCTAssertEqual(try config()["version"] as? String, "v1.3.100")
        XCTAssertFalse(ConfigProfile.updatedSinceGenerated(paths, stamp: "b1"))
        XCTAssertTrue(ConfigProfile.updatedSinceGenerated(paths, stamp: "b2"))
        XCTAssertFalse(FileManager.default.fileExists(atPath: paths.configBackupFile.path))
    }

    func testAFailedRegenerateKeepsTheOldConfig() async throws {
        struct Broken: Error {}
        await ConfigProfile.regenerateAfterUpdate(paths, stamp: "b1") { paths in
            try Data("half a config".utf8).write(to: paths.configFile)
            throw Broken()
        }
        XCTAssertEqual(try config()["version"] as? String, "v1.3.99")
        // Tried once: the next start does not run a failing generator again.
        XCTAssertFalse(ConfigProfile.updatedSinceGenerated(paths, stamp: "b1"))
        XCTAssertFalse(FileManager.default.fileExists(atPath: paths.configBackupFile.path))
    }

    /// A new key would be a new identity: the old config goes back.
    func testARegenerateThatChangesTheKeyIsUndone() async throws {
        await ConfigProfile.regenerateAfterUpdate(paths, stamp: "b1") { paths in
            try JSONSerialization.data(withJSONObject: ["pk": "02" + String(repeating: "ab", count: 32), "version": "v1.3.100"])
                .write(to: paths.configFile)
        }
        XCTAssertEqual(try config()["pk"] as? String, pk)
        XCTAssertEqual(try config()["version"] as? String, "v1.3.99")
    }

    /// The app died mid-regenerate: the backup is the good config.
    func testAnInterruptedRegenerateIsUndoneAtTheNextStart() throws {
        try FileManager.default.copyItem(at: paths.configFile, to: paths.configBackupFile)
        try Data("half a config".utf8).write(to: paths.configFile)
        ConfigProfile.restoreInterruptedRegen(paths)
        XCTAssertEqual(try config()["version"] as? String, "v1.3.99")
        XCTAssertFalse(FileManager.default.fileExists(atPath: paths.configBackupFile.path))
    }

    /// The real stamp names this build.
    func testTheStampNamesTheBuild() {
        XCTAssertFalse(ConfigProfile.appBuildStamp.hasSuffix("?"))
    }
}
