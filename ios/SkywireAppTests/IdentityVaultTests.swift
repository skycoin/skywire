import CoreBridge
import CoreClient
@testable import Skywire
import XCTest

/// The config vault and the identity changes, on files in a temporary directory and the
/// Simulator's Keychain (each test its own service). The replace tests run the core's real
/// generator, which is what has to honour the key written into the file.
final class IdentityVaultTests: XCTestCase {
    private var paths: CorePaths!
    private var secrets: SecretStore!
    private var vault: ConfigVault!

    override func setUp() async throws {
        try await super.setUp()
        paths = CorePaths(dataDir: FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString))
        try paths.createDirectories()
        let group = Bundle.main.object(forInfoDictionaryKey: "SkywireKeychainGroup") as? String
        secrets = SecretStore(service: "com.skycoin.skywire.tests.\(UUID().uuidString)", accessGroup: group)
        vault = ConfigVault(paths: paths, secrets: secrets)
    }

    override func tearDown() async throws {
        try? secrets.delete(.configSealKey)
        try? FileManager.default.removeItem(at: paths.dataDir)
        try await super.tearDown()
    }

    private func writeConfig(_ text: String) throws {
        try Data(text.utf8).write(to: paths.configFile)
    }

    private func exists(_ url: URL) -> Bool { FileManager.default.fileExists(atPath: url.path) }

    // MARK: The vault

    func testSealingReplacesThePlaintextAndReadsBack() throws {
        let config = #"{"sk":"secret-material","pk":"02ab"}"#
        try writeConfig(config)
        try vault.seal(enabled: true)
        XCTAssertFalse(exists(paths.configFile))
        XCTAssertTrue(exists(vault.sealedFile))
        let sealed = try Data(contentsOf: vault.sealedFile)
        XCTAssertNil(sealed.range(of: Data("secret-material".utf8)), "the key is in the sealed file in the clear")
        XCTAssertEqual(try vault.readText(), config)
        XCTAssertFalse(exists(paths.configFile), "reading a sealed config put a plaintext on disk")
        try vault.unseal()
        XCTAssertEqual(try String(contentsOf: paths.configFile, encoding: .utf8), config)
        XCTAssertFalse(exists(vault.sealedFile))
    }

    func testOffSealsNothing() throws {
        try writeConfig("{}")
        try vault.seal(enabled: false)
        XCTAssertTrue(exists(paths.configFile))
        XCTAssertFalse(exists(vault.sealedFile))
    }

    /// A plaintext beside the ciphertext is what the core left running: it is the newer.
    func testAPlaintextBesideASealedFileWins() throws {
        try writeConfig("old")
        try vault.seal(enabled: true)
        try writeConfig("newer")
        try vault.unseal()
        XCTAssertEqual(try String(contentsOf: paths.configFile, encoding: .utf8), "newer")
    }

    /// Without its key the sealed file is refused, not replaced: a missing config would read
    /// as a first run, and a start would make a new identity over it.
    func testASealedConfigWithoutItsKeyIsRefused() throws {
        try writeConfig("{}")
        try vault.seal(enabled: true)
        try secrets.delete(.configSealKey)
        XCTAssertThrowsError(try vault.unseal())
        XCTAssertTrue(exists(vault.sealedFile))
        XCTAssertFalse(exists(paths.configFile))
    }

    func testTurningItOffUnsealsAtOnce() throws {
        try writeConfig("{}")
        try vault.apply(enabled: true, coreRunning: false)
        XCTAssertTrue(exists(vault.sealedFile))
        try vault.apply(enabled: false, coreRunning: false)
        XCTAssertTrue(exists(paths.configFile))
        XCTAssertFalse(exists(vault.sealedFile))
    }

    /// While the core runs the visor holds the file: turning it on waits for the stop.
    func testTurningItOnWhileRunningWaits() throws {
        try writeConfig("{}")
        try vault.apply(enabled: true, coreRunning: true)
        XCTAssertTrue(exists(paths.configFile))
        XCTAssertFalse(exists(vault.sealedFile))
    }

    // MARK: Keys

    /// secp256k1's generator point, and twice it: the public keys of secret keys 1 and 2.
    func testThePublicKeyIsDerived() throws {
        XCTAssertEqual(try Identity.publicKey(of: String(repeating: "0", count: 63) + "1"),
                       "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798")
        XCTAssertEqual(try Identity.publicKey(of: "  " + String(repeating: "0", count: 63) + "2\n"),
                       "02c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5")
    }

    func testNotAKeyIsAnError() {
        XCTAssertThrowsError(try Identity.publicKey(of: "abc"))
        XCTAssertThrowsError(try Identity.publicKey(of: String(repeating: "g", count: 64)))
        XCTAssertThrowsError(try Identity.publicKey(of: String(repeating: "0", count: 64)), "zero is not a key")
        XCTAssertThrowsError(try Identity.publicKey(of: String(repeating: "f", count: 64)), "past the curve's order")
    }

    // MARK: Changing the identity

    private let key2 = String(repeating: "0", count: 63) + "2"
    private let pk2 = "02c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5"

    func testReplacingKeepsTheKeyItWasGiven() async throws {
        try await CoreBridge.shared.configGen(outPath: paths.configFile.path, options: ConfigProfile.genOptions(paths: paths))
        let before = Identity.publicKey(inConfig: paths.configFile)
        XCTAssertNotNil(before)
        let marker = paths.localDir.appendingPathComponent("chat-history")
        try Data("x".utf8).write(to: marker)
        let pk = try await Identity.replace(secretKey: key2, paths: paths, vault: vault)
        XCTAssertEqual(pk, pk2)
        XCTAssertEqual(Identity.publicKey(inConfig: paths.configFile), pk2)
        XCTAssertNotEqual(before, pk2)
        XCTAssertFalse(exists(marker), "the old identity's data is still there")
        XCTAssertTrue(exists(paths.localDir))
    }

    /// With no config yet the key becomes the first identity.
    func testReplacingWithNoConfigMakesTheFirst() async throws {
        let pk = try await Identity.replace(secretKey: key2, paths: paths, vault: vault)
        XCTAssertEqual(pk, pk2)
        XCTAssertEqual(Identity.publicKey(inConfig: paths.configFile), pk2)
    }

    func testReplacingASealedConfig() async throws {
        try await CoreBridge.shared.configGen(outPath: paths.configFile.path, options: ConfigProfile.genOptions(paths: paths))
        try vault.seal(enabled: true)
        _ = try await Identity.replace(secretKey: key2, paths: paths, vault: vault)
        XCTAssertEqual(Identity.publicKey(inConfig: paths.configFile), pk2)
    }

    func testAnInvalidKeyChangesNothing() async throws {
        try writeConfig(#"{"pk":"02ab"}"#)
        do {
            _ = try await Identity.replace(secretKey: "nope", paths: paths, vault: vault)
            XCTFail("an invalid key was accepted")
        } catch {}
        XCTAssertEqual(try String(contentsOf: paths.configFile, encoding: .utf8), #"{"pk":"02ab"}"#)
    }

    func testResettingRemovesTheConfigInBothForms() throws {
        try writeConfig("{}")
        try vault.seal(enabled: true)
        try writeConfig("{}")
        try Identity.reset(paths: paths, vault: vault)
        XCTAssertFalse(exists(paths.configFile))
        XCTAssertFalse(exists(vault.sealedFile))
        XCTAssertTrue(exists(paths.localDir))
    }
}
