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

    /// A send spends from one chosen address, or from outputs picked by hand with the hours set
    /// exactly; the fake node checks each with skycoin's rules.
    func testSendFromAnAddressAndPickedCoins() throws {
        let app = try benchWallet()

        // From the second address: its 4 SKY is all that is available, and only it pays.
        openSend(to: addresses[2], amount: "1", app)
        app.buttons["wallet-send-from"].tap()
        app.buttons["wallet-from-2"].tap()
        XCTAssertTrue(app.staticTexts["Available 4.000 SKY"].waitForExistence(timeout: 5), "Available does not follow the address")
        attachScreenshot(app, "bench-send-from")
        try signAndSend(app)
        let fromSecond = try lastTransaction()
        XCTAssertEqual(Set(fromSecond.inputs.map(\.owner)), [addresses[1]], "a send from address 2 spent another address's coins")
        // Change goes back to the address the coins came from.
        XCTAssertTrue(fromSecond.outputs.contains { $0.dst == addresses[1] }, "the change left address 2")

        // One picked output, and the hours to send typed by hand.
        try benchReset(app)
        openSend(to: addresses[2], amount: "1", app)
        app.buttons["wallet-send-coins"].tap()
        let picked = app.buttons.matching(identifier: "wallet-utxo").matching(NSPredicate(format: "label BEGINSWITH %@", "2.500")).firstMatch
        XCTAssertTrue(picked.waitForExistence(timeout: 10), "the 2.5 SKY output is not offered")
        picked.tap()
        app.buttons["wallet-utxo-done"].tap()
        XCTAssertTrue(app.buttons["wallet-send-coins"].label.contains("1 of 4 outputs"), app.buttons["wallet-send-coins"].label)
        let hours = app.textFields["wallet-send-hours"]
        scroll(to: hours, in: app)
        hours.tap()
        hours.typeText("12.5")
        app.buttons["wallet-send-review"].tap()
        XCTAssertTrue(app.staticTexts["Not a number of hours."].waitForExistence(timeout: 5), "12.5 hours was taken")
        hours.tap()
        hours.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: 4) + "5")
        attachScreenshot(app, "bench-send-coins")
        try signAndSend(app)
        let picks = try lastTransaction()
        XCTAssertEqual(picks.inputs.map(\.coins), ["2.500000"], "the send did not spend exactly the picked output")
        XCTAssertEqual(picks.outputs.first { $0.dst == addresses[2] }?.hours, 5, "the destination did not get 5 hours")
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

    /// The fake node's funding again, and the wallet refreshed onto it.
    func benchReset(_ app: XCUIApplication) throws {
        try bench("reset")
        attachScreenshot(app, "bench-before-reset")
        // Pull to refresh is not on the balance screen; a coin switch re-reads at once.
        selectCoin("BTC", app)
        selectCoin("SKY", app)
        XCTAssertTrue(waitEnabled(app.buttons["wallet-send"], timeout: 60))
    }

    /// From the balance screen: Send, with the recipient and amount typed.
    func openSend(to address: String, amount: String, _ app: XCUIApplication) {
        let send = app.buttons["wallet-send"]
        XCTAssertTrue(waitEnabled(send, timeout: 60))
        send.tap()
        let to = app.textFields["wallet-send-to"]
        XCTAssertTrue(to.waitForExistence(timeout: 5))
        to.tap()
        to.typeText(address)
        let field = app.textFields["wallet-send-amount"]
        field.tap()
        field.typeText(amount)
    }

    /// Review, Sign and send behind Face ID, and the result screen.
    func signAndSend(_ app: XCUIApplication) throws {
        let review = app.buttons["wallet-send-review"]
        scroll(to: review, in: app)
        let sign = app.buttons["wallet-review-sign"]
        let error = app.staticTexts["wallet-send-error"]
        // A tap while the page still glides from the scroll only stops it.
        for _ in 0..<3 where !sign.exists && !error.exists {
            usleep(700_000)
            review.tap()
            _ = sign.waitForExistence(timeout: 15)
        }
        XCTAssertTrue(sign.exists, "no review: \(error.exists ? error.label : "nothing said")")
        attachScreenshot(app, "bench-review")
        answeringFaceID {
            sign.tap()
            XCTAssertTrue(app.buttons["wallet-result-txid"].waitForExistence(timeout: 30), "the send did not reach the result screen")
        }
        tapWhenUp("wallet-result-done", in: app)
    }

    struct BenchTxn: Decodable {
        struct Input: Decodable { let owner: String; let coins: String }
        struct Output: Decodable { let dst: String; let coins: String; let hours: UInt64 }
        let txid: String
        let inputs: [Input]
        let outputs: [Output]
    }

    /// The last transaction the fake node accepted.
    func lastTransaction() throws -> BenchTxn {
        try JSONDecoder().decode(BenchTxn.self, from: try bench("last"))
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
