import WalletCore
import XCTest

/// The wallet's Skycoin flows against a local fake node, which holds a fixed set of outputs for
/// the phrase below and checks each injected transaction with skycoin's own signature and fee
/// rules (the bench's fakenode, see ios/README.md). Skipped unless the runner names it:
///
///   TEST_RUNNER_SKYWIRE_FAKE_NODE=http://127.0.0.1:6420 … -only-testing:SkywireUITests/WalletBenchChecks
///
/// The fake node funds the phrase's first address with 10, 2.5 and 1 SKY and its second with 4.
@MainActor
final class WalletBenchChecks: XCTestCase {
    static let phrase = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

    private var node = ""
    private var addresses: [String] = []

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let node = ProcessInfo.processInfo.environment["SKYWIRE_FAKE_NODE"] else {
            throw XCTSkip("no fake node: set TEST_RUNNER_SKYWIRE_FAKE_NODE")
        }
        self.node = node
        let keys = try SkycoinCrypto.generateKeyPairs(seed: Array(Self.phrase.utf8), count: 3)
        addresses = keys.map { SkycoinCrypto.addressFromPubKey($0.public) }
    }

    override func tearDown() {
        try? FileManager.default.removeItem(atPath: Self.walletFaceIDFlag)
    }

    /// The addresses sheet shows what each address holds, hides the empty ones on request, and an
    /// address's menu opens its unspent outputs and its own history.
    func testAddressBalancesOutputsAndHistory() throws {
        let app = try benchWallet()
        app.buttons["wallet-receive"].tap()
        app.buttons["wallet-other-addresses"].tap()
        let first = app.buttons["wallet-address-1"], second = app.buttons["wallet-address-2"]
        XCTAssertTrue(first.waitForExistence(timeout: 10))
        XCTAssertTrue(first.label.contains("13.500"), "address 1 reads \(first.label)")
        XCTAssertFalse(first.label.contains("Never used"))
        XCTAssertTrue(second.label.contains("4.000"), "address 2 reads \(second.label)")
        attachScreenshot(app, "bench-addresses")

        let generated = app.buttons["wallet-address-3"]
        XCTAssertTrue(generated.exists)
        app.buttons["wallet-addresses-hide-empty"].tap()
        XCTAssertTrue(waitGone(generated, timeout: 5), "the empty third address is still listed")
        XCTAssertTrue(second.exists, "hiding empty addresses hid one with coins")
        app.buttons["wallet-addresses-hide-empty"].tap()

        app.buttons["wallet-address-actions-1"].tap()
        app.buttons["Unspent outputs"].tap()
        let outputs = app.descendants(matching: .any).matching(identifier: "wallet-output")
        let three = XCTNSPredicateExpectation(predicate: NSPredicate(format: "count == 3"), object: outputs)
        XCTAssertEqual(XCTWaiter().wait(for: [three], timeout: 15), .completed, "address 1 should list its three outputs")
        attachScreenshot(app, "bench-outputs")
        closeSheet(app)

        app.buttons["wallet-other-addresses"].tap()
        // The sheet is still rising; a tap now lands a row lower.
        tapWhenUp("wallet-address-actions-2", in: app)
        app.buttons["History"].tap()
        let chip = app.buttons["wallet-history-address"]
        XCTAssertTrue(chip.waitForExistence(timeout: 5))
        XCTAssertEqual(chip.label, "\(addresses[1].prefix(8))…\(addresses[1].suffix(6))")
        let rows = app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "4.000"))
        XCTAssertTrue(rows.firstMatch.waitForExistence(timeout: 5), "the second address's receipt is not listed")
        XCTAssertFalse(app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "10.000")).firstMatch.exists,
                       "the history shows another address's transaction")
        attachScreenshot(app, "bench-history-of-address")
        chip.tap()
        app.buttons["All addresses"].tap()
        XCTAssertTrue(app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "10.000")).firstMatch.waitForExistence(timeout: 5))
    }

    // MARK: Bench

    /// The app on SKY with the bench phrase's wallet, its node pointed at the fake one, refreshed.
    func benchWallet() throws -> XCUIApplication {
        try bench("reset")
        let app = XCUIApplication()
        app.launchArguments = ["-AppleLanguages", "(en)", "-AppleLocale", "en_US"]
        app.launch()
        openTab(.wallet, in: app)
        selectCoin("SKY", app)
        if !app.buttons["wallet-receive"].waitForExistence(timeout: 3) || !walletHolds(addresses[0], app) {
            startRestore(app)
            typePhrase(Self.phrase.split(separator: " ").map(String.init), app)
            app.buttons["wallet-restore-action"].tap()
            XCTAssertTrue(app.buttons["wallet-receive"].waitForExistence(timeout: 120))
        }
        useBenchNode(app)
        let send = app.buttons["wallet-send"]
        XCTAssertTrue(waitEnabled(send, timeout: 60), "the fake node did not answer the refresh")
        // The restore scanned the shipped node, which knows none of the phrase's addresses: the
        // bench's three are derived by hand, the same chain either way.
        app.buttons["wallet-receive"].tap()
        app.buttons["wallet-other-addresses"].tap()
        for index in 2...3 where !app.buttons["wallet-address-\(index)"].waitForExistence(timeout: 2) {
            app.buttons["wallet-address-new"].tap()
            XCTAssertTrue(app.buttons["wallet-address-\(index)"].waitForExistence(timeout: 10))
        }
        app.buttons["wallet-address-1"].tap()
        back(app)
        // Read the balances again, now over all three.
        selectCoin("BTC", app)
        selectCoin("SKY", app)
        XCTAssertTrue(waitEnabled(send, timeout: 60))
        return app
    }

    /// Whether the coin has the bench phrase's wallet, which is then the one in use.
    private func walletHolds(_ address: String, _ app: XCUIApplication) -> Bool {
        openWallets(app)
        let row = app.buttons["wallet-row-\(address)"]
        let found = row.waitForExistence(timeout: 3)
        if found && !row.label.lowercased().contains("active") {
            row.tap()
            tapWhenUp("wallet-action-use", in: app)
            usleep(600_000)
        }
        back(app)
        return found
    }

    /// The coin's node is the fake one.
    private func useBenchNode(_ app: XCUIApplication) {
        let row = nodeRow(app)
        if row.label.contains(node.replacingOccurrences(of: "http://", with: "")) { return }
        openNode(app)
        replaceText("wallet-node-url", with: node, app)
        app.buttons["wallet-node-save"].tap()
    }

    @discardableResult
    func bench(_ path: String) throws -> Data {
        let url = try XCTUnwrap(URL(string: node + "/bench/" + path))
        let done = expectation(description: path)
        nonisolated(unsafe) var body = Data()
        URLSession.shared.dataTask(with: url) { data, _, _ in
            body = data ?? Data()
            done.fulfill()
        }.resume()
        wait(for: [done], timeout: 10)
        return body
    }
}
