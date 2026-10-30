import WalletCore
import XCTest

/// The wallet tab's steps, shared by WalletChecks and WalletBenchChecks.
@MainActor
extension XCTestCase {

    func nodeRow(_ app: XCUIApplication) -> XCUIElement {
        let row = app.buttons["wallet-node-row"]
        XCTAssertTrue(row.waitForExistence(timeout: 180), "no Node row on the balance screen")
        return row
    }

    func openNode(_ app: XCUIApplication) {
        let row = nodeRow(app)
        scroll(to: row, in: app)
        row.tap()
        XCTAssertTrue(app.textFields["wallet-node-add-url"].waitForExistence(timeout: 5))
    }

    /// The node screen with none of the user's nodes, whatever an earlier run left.
    func openShippedNode(_ app: XCUIApplication) {
        openNode(app)
        let remove = app.buttons["wallet-node-remove-1"]
        while remove.exists {
            remove.tap()
            usleep(400_000)
        }
    }

    /// From the node screen: the address typed, checked, and with `use` its properties accepted,
    /// which adds it at the end of the list.
    func addNode(_ url: String, _ app: XCUIApplication, use: Bool = true) {
        let field = app.textFields["wallet-node-add-url"]
        scroll(to: field, in: app)
        field.tap()
        field.typeText(url)
        app.buttons["wallet-node-add"].tap()
        guard use else { return }
        let accept = app.buttons["wallet-node-check-use"]
        let error = app.staticTexts["wallet-node-error"]
        XCTAssertTrue(accept.waitForExistence(timeout: 30), "the node check did not pass: \(error.exists ? error.label : "no answer")")
        accept.tap()
    }

    /// The user's nodes as the node screen lists them, in order.
    func listedNodes(_ app: XCUIApplication) -> [String] {
        (1...10).map { app.staticTexts["wallet-node-\($0)"] }.prefix { $0.exists }.map(\.label)
    }

    func replaceText(_ identifier: String, with text: String, _ app: XCUIApplication) {
        let field = app.textFields[identifier]
        // At the end of the text, so the deletes take all of it.
        field.coordinate(withNormalizedOffset: CGVector(dx: 0.97, dy: 0.5)).tap()
        let current = field.value as? String ?? ""
        field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: current.count) + text)
    }

    func selectCoin(_ id: String, _ app: XCUIApplication) {
        let chip = app.buttons["wallet-coin-chip"]
        XCTAssertTrue(chip.waitForExistence(timeout: 10))
        chip.tap()
        let coin = app.buttons["wallet-coin-\(id)"]
        XCTAssertTrue(coin.waitForExistence(timeout: 5), "no \(id) in the coin list")
        coin.tap()
        XCTAssertTrue(chip.waitForExistence(timeout: 5))
    }

    /// From the intro when the coin has no wallet, else from the wallets list.
    func startCreate(_ app: XCUIApplication) {
        let create = app.buttons["wallet-create"]
        if create.waitForExistence(timeout: 3) {
            create.tap()
        } else {
            openWallets(app)
            app.buttons["wallet-manage-create"].tap()
        }
    }

    func startRestore(_ app: XCUIApplication) {
        let restore = app.buttons["wallet-restore"]
        if restore.waitForExistence(timeout: 3) {
            restore.tap()
        } else {
            openWallets(app)
            app.buttons["wallet-manage-restore"].tap()
        }
    }

    func openWallets(_ app: XCUIApplication) {
        let row = app.buttons["wallet-wallets-row"]
        XCTAssertTrue(row.waitForExistence(timeout: 10))
        scroll(to: row, in: app)
        row.tap()
    }

    func readSeedWords(_ app: XCUIApplication) throws -> [String] {
        XCTAssertTrue(app.staticTexts["seed-word-1"].waitForExistence(timeout: 10))
        return try (1...24).compactMap { i -> String? in
            let word = app.staticTexts["seed-word-\(i)"]
            guard word.exists else { return nil }
            return try XCTUnwrap(word.label.isEmpty ? nil : word.label)
        }
    }

    func typePhrase(_ words: [String], _ app: XCUIApplication) {
        let input = app.textFields["wallet-restore-input"]
        XCTAssertTrue(input.waitForExistence(timeout: 10))
        input.tap()
        for word in words { input.typeText(word + " ") }
        XCTAssertTrue(app.buttons["wallet-restore-word-\(words.count)"].waitForExistence(timeout: 5), "not every word was taken")
    }

    /// Continue from the backup, the three asked words typed, and Activate.
    func passQuiz(_ words: [String], _ app: XCUIApplication) {
        let next = app.buttons["wallet-seed-continue"]
        scroll(to: next, in: app)
        next.tap()
        let positions = (1...words.count).filter { app.textFields["wallet-quiz-\($0)"].waitForExistence(timeout: $0 == 1 ? 5 : 0.2) }
        XCTAssertEqual(positions.count, 3)
        for pos in positions {
            let field = app.textFields["wallet-quiz-\(pos)"]
            field.tap()
            field.typeText(words[pos - 1])
        }
        app.buttons["wallet-quiz-activate"].tap()
    }

    /// Opens Receive from the balance screen and reads the address on it.
    func receiveAddress(_ app: XCUIApplication, timeout: TimeInterval = 20) throws -> String {
        let receive = app.buttons["wallet-receive"]
        XCTAssertTrue(receive.waitForExistence(timeout: timeout), "no balance screen")
        receive.tap()
        let address = app.buttons["wallet-receive-address"]
        XCTAssertTrue(address.waitForExistence(timeout: 10))
        return try XCTUnwrap(address.value as? String)
    }

    /// From the balance screen: the wallets list, the wallet's row, Remove,
    /// confirmed; back on the balance screen (or the intro).
    func removeWallet(_ firstAddress: String, _ app: XCUIApplication) {
        if !app.buttons["wallet-row-\(firstAddress)"].exists { openWallets(app) }
        let row = app.buttons["wallet-row-\(firstAddress)"]
        XCTAssertTrue(row.waitForExistence(timeout: 10))
        row.tap()
        tapWhenUp("wallet-action-remove", in: app)
        tapWhenUp("Remove", in: app)
        XCTAssertTrue(waitGone(row, timeout: 10), "the wallet stayed in the list")
        back(app)
    }

    /// Closes the sheet on screen by tapping the scrim above it.
    func closeSheet(_ app: XCUIApplication) {
        app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.08)).tap()
        usleep(500_000)
    }

    func back(_ app: XCUIApplication) {
        goBack(app)
    }

    func waitEnabled(_ element: XCUIElement, timeout: TimeInterval) -> Bool {
        let enabled = XCTNSPredicateExpectation(predicate: NSPredicate(format: "isEnabled == true"), object: element)
        return XCTWaiter().wait(for: [enabled], timeout: timeout) == .completed
    }

    func waitGone(_ element: XCUIElement, timeout: TimeInterval) -> Bool {
        let gone = XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: element)
        return XCTWaiter().wait(for: [gone], timeout: timeout) == .completed
    }

    func skycoinAddress(_ words: [String]) throws -> String {
        let keys = try SkycoinCrypto.generateKeyPairs(seed: Array(words.joined(separator: " ").utf8), count: 1)
        return SkycoinCrypto.addressFromPubKey(keys[0].public)
    }

    func attachScreenshot(_ app: XCUIApplication, _ name: String) {
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = name
        shot.lifetime = .keepAlways
        add(shot)
    }

    /// Has faceid-matcher.sh (on the Mac) answer every Face ID prompt with a
    /// match while `step` runs; a prompt raised after it returns waits. The
    /// flag file stays for the whole step because a match sent while no
    /// prompt is up is dropped: asking for one match per prompt lost it in
    /// the gap between one sheet closing and the next opening.
    func answeringFaceID(_ step: () -> Void) {
        FileManager.default.createFile(atPath: Self.walletFaceIDFlag, contents: nil)
        defer { try? FileManager.default.removeItem(atPath: Self.walletFaceIDFlag) }
        step()
    }

    nonisolated static let walletFaceIDFlag = NSTemporaryDirectory() + "faceid-match"
}
