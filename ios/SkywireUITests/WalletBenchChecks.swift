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
        tapWhenUp("Unspent outputs", in: app)
        let outputs = app.descendants(matching: .any).matching(identifier: "wallet-output")
        let three = XCTNSPredicateExpectation(predicate: NSPredicate(format: "count == 3"), object: outputs)
        XCTAssertEqual(XCTWaiter().wait(for: [three], timeout: 15), .completed, "address 1 should list its three outputs")
        attachScreenshot(app, "bench-outputs")
        closeSheet(app)

        app.buttons["wallet-other-addresses"].tap()
        // The sheet is still rising; a tap now lands a row lower.
        tapWhenUp("wallet-address-actions-2", in: app)
        tapWhenUp("History", in: app)
        let chip = app.buttons["wallet-history-address"]
        XCTAssertTrue(chip.waitForExistence(timeout: 5))
        XCTAssertEqual(chip.label, "\(addresses[1].prefix(8))…\(addresses[1].suffix(6))")
        let rows = app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "4.000"))
        XCTAssertTrue(rows.firstMatch.waitForExistence(timeout: 5), "the second address's receipt is not listed")
        XCTAssertFalse(app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "10.000")).firstMatch.exists,
                       "the history shows another address's transaction")
        attachScreenshot(app, "bench-history-of-address")
        chip.tap()
        tapWhenUp("All addresses", in: app)
        XCTAssertTrue(app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "10.000")).firstMatch.waitForExistence(timeout: 5))
    }

    /// A send spends from one chosen address, or from outputs picked by hand with the hours set
    /// exactly; the fake node checks each with skycoin's rules.
    func testSendFromAnAddressAndPickedCoins() throws {
        let app = try benchWallet()

        // From the second address: its 4 SKY is all that is available, and only it pays.
        openSend(to: addresses[2], amount: "1", app)
        app.buttons["wallet-send-from"].tap()
        tapWhenUp("wallet-from-2", in: app)
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

    /// One fiber transaction pays several destinations, typed one by one or pasted as bulk text,
    /// and the review lists each; the fake node checks the outputs it gets.
    func testSendToSeveralDestinationsAndInBulk() throws {
        let app = try benchWallet()
        openSend(to: addresses[2], amount: "1", app)
        app.buttons["wallet-send-add-destination"].tap()
        let to2 = app.textFields["wallet-dest-to-2"]
        XCTAssertTrue(to2.waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["wallet-send-max"].exists, "Max stays with several destinations")
        to2.tap()
        to2.typeText(addresses[1])
        let amount2 = app.textFields["wallet-dest-amount-2"]
        amount2.tap()
        amount2.typeText("0.5")
        try signAndSend(app, screenshot: "bench-review-many")
        let typed = try lastTransaction()
        XCTAssertEqual(typed.outputs.first { $0.dst == addresses[2] }?.coins, "1.000000")
        XCTAssertEqual(typed.outputs.filter { $0.dst == addresses[1] }.map(\.coins).contains("0.500000"), true, "destination 2 was not paid 0.5")

        try benchReset(app)
        let send = app.buttons["wallet-send"]
        XCTAssertTrue(waitEnabled(send, timeout: 60))
        send.tap()
        app.buttons["wallet-send-bulk"].tap()
        let bulk = app.textFields["wallet-bulk-text"]
        XCTAssertTrue(bulk.waitForExistence(timeout: 5))
        bulk.tap()
        bulk.typeText("\(addresses[2]), 1, 4\n\(addresses[1]), 2")
        app.buttons["wallet-bulk-apply"].tap()
        let error = app.staticTexts["wallet-bulk-error"]
        XCTAssertTrue(error.waitForExistence(timeout: 5))
        XCTAssertTrue(error.label.hasPrefix("Line 2"), error.label)
        bulk.typeText(", 3")
        app.buttons["wallet-bulk-apply"].tap()
        XCTAssertTrue(waitGone(bulk, timeout: 5), "the corrected list was not taken")
        XCTAssertEqual(app.textFields["wallet-send-to"].value as? String, addresses[2])
        XCTAssertEqual(app.textFields["wallet-dest-to-2"].value as? String, addresses[1])
        try signAndSend(app, screenshot: "bench-review-bulk")
        let pasted = try lastTransaction()
        XCTAssertEqual(pasted.outputs.first { $0.dst == addresses[2] }?.hours, 4)
        XCTAssertEqual(pasted.outputs.first { $0.dst == addresses[1] && $0.coins == "2.000000" }?.hours, 3)
    }

    /// Advanced: the change goes to a chosen address of the wallet, and the hours share sends
    /// every hour left after the burn on to the destination.
    func testChangeAddressAndHoursShare() throws {
        let app = try benchWallet()
        openSend(to: addresses[2], amount: "1", app)
        let advanced = app.buttons["wallet-send-advanced"]
        scroll(to: advanced, in: app)
        advanced.tap()
        let select = app.buttons["wallet-send-change-select"]
        XCTAssertTrue(select.waitForExistence(timeout: 5))
        select.tap()
        tapWhenUp("wallet-change-2", in: app)
        let picked = XCTNSPredicateExpectation(predicate: NSPredicate(format: "value == %@", addresses[1]), object: app.textFields["wallet-send-change"])
        XCTAssertEqual(XCTWaiter().wait(for: [picked], timeout: 5), .completed, "the picked address is not the change address")
        let value = app.staticTexts["50%"]
        scroll(to: value, in: app)
        // Drag the handle from the middle past the right end, which is just left of the 52 pt
        // value and the 12 pt gap before it; the track starts at the card's 36 pt inset. (The
        // slider's accessibility stand-in has a frame of its own, so this goes by the drawing.)
        let origin = app.coordinate(withNormalizedOffset: .zero)
        let end = value.frame.maxX - 52 - 12
        let rowY = value.frame.midY
        origin.withOffset(CGVector(dx: (36 + end) / 2, dy: value.frame.midY))
            .press(forDuration: 0.2, thenDragTo: origin.withOffset(CGVector(dx: end + 30, dy: value.frame.midY)))
        XCTAssertTrue(app.staticTexts["100%"].waitForExistence(timeout: 3), "the share did not reach 100 %")
        attachScreenshot(app, "bench-advanced")
        // Up the page from above the slider, so the swipe is the page's.
        for _ in 0..<3 where !app.buttons["wallet-send-review"].isHittable {
            origin.withOffset(CGVector(dx: 200, dy: 260)).press(forDuration: 0.05, thenDragTo: origin.withOffset(CGVector(dx: 200, dy: 0)))
        }
        try signAndSend(app, screenshot: "bench-review-change")
        let sent = try lastTransaction()
        let change = try XCTUnwrap(sent.outputs.first { $0.dst == addresses[1] }, "no change to the chosen address")
        XCTAssertEqual(change.hours, 0, "a 100 % share left hours in the change")
        XCTAssertGreaterThan(sent.outputs.first { $0.dst == addresses[2] }?.hours ?? 0, 0)
    }

    /// Offline: the review gives the planned send unsigned, Sign a transaction signs it here behind
    /// Face ID without sending, and Broadcast sends the signed one; the fake node accepts it.
    func testUnsignedSignAndBroadcastOffline() throws {
        let app = try benchWallet()
        openSend(to: addresses[2], amount: "1", app)
        let review = app.buttons["wallet-send-review"]
        scroll(to: review, in: app)
        review.tap()
        let unsigned = app.buttons["wallet-review-unsigned"]
        XCTAssertTrue(unsigned.waitForExistence(timeout: 30), "no review")
        tapWhenUp("wallet-review-unsigned", in: app)
        let hex = app.staticTexts["wallet-raw-hex"]
        XCTAssertTrue(hex.waitForExistence(timeout: 5), "no unsigned transaction")
        let unsignedHex = hex.label
        attachScreenshot(app, "bench-unsigned")
        app.buttons["wallet-raw-copy"].tap()
        tapWhenUp("Done", in: app)

        let advanced = app.buttons["wallet-send-advanced"]
        scroll(to: advanced, in: app)
        advanced.tap()
        offline("wallet-offline-sign", app)
        answeringFaceID {
            app.buttons["wallet-offline-run"].tap()
            XCTAssertTrue(app.staticTexts["wallet-raw-hex"].waitForExistence(timeout: 20), "the transaction was not signed")
        }
        let signedHex = app.staticTexts["wallet-raw-hex"].label
        // An unsigned Skycoin transaction carries empty signature slots: the same length, other bytes.
        XCTAssertEqual(signedHex.count, unsignedHex.count)
        XCTAssertNotEqual(signedHex, unsignedHex, "the signed transaction carries no signature")
        attachScreenshot(app, "bench-signed")
        app.buttons["wallet-raw-copy"].tap()
        tapWhenUp("Done", in: app)
        XCTAssertNil(try? lastTransaction(), "signing offline sent the transaction")

        offline("wallet-offline-broadcast", app)
        app.buttons["wallet-offline-run"].tap()
        let sent = app.staticTexts["wallet-offline-sent"]
        XCTAssertTrue(sent.waitForExistence(timeout: 20), "the signed transaction was not broadcast")
        let txid = try lastTransaction().txid
        XCTAssertTrue(sent.label.contains(txid), "the dialog names \(sent.label), the node accepted \(txid)")
        attachScreenshot(app, "bench-broadcast")
    }

    /// A request made on Receive carries the amount, hours and message in the QR and the copy, and
    /// pasted on Send it fills the recipient, the amount and the hours.
    func testPaymentRequestRoundTrip() throws {
        let app = try benchWallet()
        app.buttons["wallet-receive"].tap()
        let request = app.buttons["wallet-request"]
        scroll(to: request, in: app)
        request.tap()
        for (field, text) in [("wallet-request-amount", "1.5"), ("wallet-request-hours", "20"), ("wallet-request-message", "lunch")] {
            let input = app.textFields[field]
            scroll(to: input, in: app)
            input.tap()
            input.typeText(text)
        }
        let copy = app.buttons["wallet-receive-copy"]
        scroll(to: copy, in: app)
        XCTAssertEqual(copy.label, "Copy request")
        attachScreenshot(app, "bench-request")
        copy.tap()
        back(app)
        app.buttons["wallet-send"].tap()
        tapWhenUp("Paste", in: app)
        XCTAssertEqual(app.textFields["wallet-send-to"].value as? String, addresses[0])
        XCTAssertEqual(app.textFields["wallet-send-amount"].value as? String, "1.5")
        let hours = app.textFields["wallet-send-hours"]
        scroll(to: hours, in: app)
        XCTAssertEqual(hours.value as? String, "20")
        attachScreenshot(app, "bench-request-pasted")
    }

    /// An address that has sent coins has its public key on the chain: with coins left on it the
    /// wallet warns, and the addresses sheet marks it.
    func testExposedAddressWarning() throws {
        let app = try benchWallet()
        XCTAssertFalse(app.descendants(matching: .any)["wallet-exposed-banner"].exists, "a fresh bench wallet is not exposed")
        openSend(to: addresses[2], amount: "1", app)
        app.buttons["wallet-send-from"].tap()
        tapWhenUp("wallet-from-2", in: app)
        try signAndSend(app)
        // The send's own refresh brings the history that names the address.
        let banner = app.descendants(matching: .any)["wallet-exposed-banner"]
        XCTAssertTrue(banner.waitForExistence(timeout: 60), "no warning for coins left at an address that sent")
        attachScreenshot(app, "bench-exposed")
        app.buttons["wallet-receive"].tap()
        app.buttons["wallet-other-addresses"].tap()
        let second = app.buttons["wallet-address-2"]
        XCTAssertTrue(second.waitForExistence(timeout: 10))
        XCTAssertTrue(second.label.contains("still holds some"), second.label)
        XCTAssertFalse(app.buttons["wallet-address-1"].label.contains("Has sent coins"), "address 1 never sent")
    }

    /// Opens an offline dialog from Advanced and pastes what the app copied last.
    private func offline(_ button: String, _ app: XCUIApplication) {
        let open = app.buttons[button]
        scroll(to: open, in: app)
        open.tap()
        tapWhenUp("wallet-offline-paste", in: app)
        XCTAssertTrue(waitEnabled(app.buttons["wallet-offline-run"], timeout: 5), "nothing was pasted")
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
    func signAndSend(_ app: XCUIApplication, screenshot: String = "bench-review") throws {
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
        attachScreenshot(app, screenshot)
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
