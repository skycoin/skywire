import CoreClient
import XCTest

/// SecretStore against the Simulator's real Keychain, inside the app (this
/// bundle is hosted by Skywire.app, so it runs with the app's entitlements;
/// the unhosted SkywireTests process has none and gets
/// errSecMissingEntitlement). The password policy is tested on the Mac, in
/// CoreClient. Each test uses its own service name and removes its items.
final class SecretStoreTests: XCTestCase {
    private var service = ""
    private var group: String?
    private var store: SecretStore!

    override func setUp() {
        super.setUp()
        service = "com.skycoin.skywire.tests.\(UUID().uuidString)"
        group = Bundle.main.object(forInfoDictionaryKey: "SkywireKeychainGroup") as? String
        store = SecretStore(service: service, accessGroup: group)
    }

    override func tearDown() {
        for secret in SecretStore.Secret.allCases {
            try? store.delete(secret)
        }
        super.tearDown()
    }

    /// The app's group is the xcconfig's, with the prefix Xcode signs the
    /// Simulator build with.
    func testTheAppHasItsKeychainGroup() {
        XCTAssertEqual(group?.hasSuffix(".com.skycoin.skywire.shared"), true, "\(group ?? "nil")")
    }

    /// Generated once, then the same every time: what lets a relaunch log in
    /// with the password the first launch created the account with.
    func testAPasswordIsGeneratedOnceAndKept() throws {
        let first = try store.password(.apiPassword)
        XCTAssertEqual(first.count, 24)
        XCTAssertEqual(try store.password(.apiPassword), first)
        // Another store on the same service and group reads the same item.
        let again = SecretStore(service: service, accessGroup: group)
        XCTAssertEqual(try again.password(.apiPassword), first)
    }

    /// The group is enforced: one the app is not entitled to is refused. What
    /// makes the Simulator a proxy for the device's shared group.
    func testAGroupTheAppIsNotEntitledToIsRefused() {
        let foreign = SecretStore(service: service, accessGroup: "OTHERTEAM.com.example.shared")
        XCTAssertThrowsError(try foreign.password(.apiPassword)) { error in
            XCTAssertEqual((error as? SecretStore.KeychainError)?.status, errSecMissingEntitlement)
        }
    }

    func testEachSecretIsItsOwn() throws {
        let passwords = try SecretStore.Secret.allCases.map { try store.password($0) }
        XCTAssertEqual(Set(passwords).count, passwords.count)
    }

    func testDeleteRotates() throws {
        let first = try store.password(.skychatPassword)
        try store.delete(.skychatPassword)
        XCTAssertNotEqual(try store.password(.skychatPassword), first)
        // Deleting what is not there is fine.
        try store.delete(.skydexPassword)
        try store.delete(.skydexPassword)
    }

    /// Concurrent first calls agree on one password.
    func testConcurrentFirstCallsAgree() async throws {
        let store = try XCTUnwrap(self.store)
        let passwords = try await withThrowingTaskGroup(of: String.self) { group in
            for _ in 0..<8 {
                group.addTask { try store.password(.skydexPassword) }
            }
            return try await group.reduce(into: []) { $0.append($1) }
        }
        XCTAssertEqual(Set(passwords).count, 1)
    }
}
