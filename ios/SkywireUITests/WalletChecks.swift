import WalletCore
import XCTest

/// The wallet tab's G5 flows, driven here so the author can check them before
/// the reviewer does. Part of the SkywireTour scheme (the restore scans and
/// the send planning ask the real nodes), not of CI; in English, as the other
/// flow checks.
///
/// The wallet needs no core: it talks to the coins' nodes directly. The words
/// a test reads off the screen are checked against WalletCore, linked into
/// this bundle, so "the address on Receive is the phrase's first address" is
/// verified rather than assumed.
///
/// The reveal asserts its Face ID gate: Face ID enrolled and
/// ios/scripts/faceid-matcher.sh running, as for FlowChecks' app lock:
///
///   xcrun simctl spawn booted notifyutil -s com.apple.BiometricKit.enrollmentChanged 1
///   xcrun simctl spawn booted notifyutil -p com.apple.BiometricKit.enrollmentChanged
///   ios/scripts/faceid-matcher.sh &
@MainActor
final class WalletChecks: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    override func tearDown() {
        try? FileManager.default.removeItem(atPath: Self.faceIDFlag)
    }

    /// Create → back up → quiz → receive → reveal (behind Face ID) → remove →
    /// restore the same phrase → the same receive address; then a send the
    /// node refuses for want of funds, planned against the live Skycoin node.
    func testSkycoinCreateBackupRestoreReceive() throws {
        let app = XCUIApplication()
        app.launch()
        openTab(.wallet, in: app)
        selectCoin("SKY", app)

        // Create, from the intro or from the wallets list.
        startCreate(app)
        let words = try readSeedWords(app)
        XCTAssertEqual(words.count, 12)
        XCTAssertTrue(Bip39.validate(words.joined(separator: " ")), "the backup screen shows a valid phrase")
        attachScreenshot(app, "wallet-1-backup")
        app.buttons["wallet-seed-continue"].tap()

        // The quiz: three of the twelve, by position.
        let positions = (1...12).filter { app.textFields["wallet-quiz-\($0)"].waitForExistence(timeout: $0 == 1 ? 5 : 0.2) }
        XCTAssertEqual(positions.count, 3, "the quiz asks for three words")
        for pos in positions {
            let field = app.textFields["wallet-quiz-\(pos)"]
            field.tap()
            field.typeText(words[pos - 1])
        }
        attachScreenshot(app, "wallet-2-quiz")
        app.buttons["wallet-quiz-activate"].tap()

        // Receive: the phrase's first Skycoin address.
        let expected = try skycoinAddress(words)
        XCTAssertEqual(try receiveAddress(app), expected, "Receive shows the phrase's first address")
        attachScreenshot(app, "wallet-3-receive")
        back(app)

        // Reveal: nothing until Face ID passes, then the same twelve words.
        openWallets(app)
        app.buttons["wallet-row-\(expected)"].tap()
        tapWhenUp("wallet-action-reveal", in: app)
        // Not longer: the Simulator's Face ID prompt gives up on its own when
        // left unanswered, and the reveal then (rightly) never happens. With
        // nothing to ask with, the words would be up at once.
        XCTAssertFalse(app.staticTexts["seed-word-1"].waitForExistence(timeout: 1.5), "the phrase showed before Face ID passed")
        answeringFaceID {
            XCTAssertTrue(app.staticTexts["seed-word-1"].waitForExistence(timeout: 20), "the phrase never showed after Face ID")
        }
        XCTAssertEqual(try readSeedWords(app), words, "the reveal shows the phrase that was backed up")
        app.buttons["wallet-reveal-hide"].tap()

        // Remove it, then restore the same phrase: the same address comes back.
        removeWallet(expected, app)
        startRestore(app)
        typePhrase(words, app)
        app.buttons["wallet-restore-action"].tap()
        XCTAssertEqual(try receiveAddress(app, timeout: 120), expected, "the restored wallet receives on the same address")
        back(app)

        // A send the wallet cannot fund: planned against the live node, which
        // finds no outputs on a fresh phrase.
        let send = app.buttons["wallet-send"]
        XCTAssertTrue(send.waitForExistence(timeout: 10))
        XCTAssertTrue(waitEnabled(send, timeout: 60), "Send stayed off: the node did not answer the refresh")
        send.tap()
        let to = app.textFields["wallet-send-to"]
        to.tap()
        to.typeText(expected)
        let amount = app.textFields["wallet-send-amount"]
        amount.tap()
        amount.typeText("1")
        app.buttons["wallet-send-review"].tap()
        let error = app.staticTexts["wallet-send-error"]
        XCTAssertTrue(error.waitForExistence(timeout: 60), "no answer from planning")
        XCTAssertEqual(error.label, "balance is not sufficient")
        attachScreenshot(app, "wallet-4-send-refused")
        back(app)

        removeWallet(expected, app)
    }

    /// A fresh phrase restored on Bitcoin and on Ethereum shows the BIP 84
    /// and BIP 44 first addresses WalletCore derives, and the Ethereum wallet
    /// arrives on USDT by itself (one account for the whole family).
    func testBitcoinAndEthereumRestore() throws {
        let words = Bip39.newMnemonic().split(separator: " ").map(String.init)
        let phrase = words.joined(separator: " ")
        let app = XCUIApplication()
        app.launch()
        openTab(.wallet, in: app)

        selectCoin("BTC", app)
        startRestore(app)
        typePhrase(words, app)
        app.buttons["wallet-restore-action"].tap()
        let btc = Bip84.address(pubKey: try Bip84.key(Bip84.accountKey(mnemonic: phrase), change: 0, index: 0).pubKey())
        XCTAssertEqual(try receiveAddress(app, timeout: 180), btc)
        attachScreenshot(app, "wallet-5-btc-receive")
        back(app)
        removeWallet(btc, app)

        selectCoin("ETH", app)
        startRestore(app)
        typePhrase(words, app)
        app.buttons["wallet-restore-action"].tap()
        let eth = try EthCrypto.address(pubCompressed: EthCrypto.key(EthCrypto.accountKey(mnemonic: phrase), index: 0).pubKey())
        XCTAssertEqual(try receiveAddress(app, timeout: 180), eth)
        back(app)

        selectCoin("USDT", app)
        XCTAssertEqual(try receiveAddress(app), eth, "USDT holds the ETH account without a second restore")
        attachScreenshot(app, "wallet-6-usdt-mirror")
        back(app)
        removeWallet(eth, app)
        selectCoin("ETH", app)
        removeWallet(eth, app)
        selectCoin("SKY", app)
    }

    /// Every coin's node can be moved, and the Ethereum family's history indexer beside it,
    /// saved and reset together (Android: WalletNode.kt). A fresh phrase on Bitcoin, then Ethereum.
    func testEveryCoinsNodeCanBeMoved() throws {
        let words = Bip39.newMnemonic().split(separator: " ").map(String.init)
        let phrase = words.joined(separator: " ")
        let app = XCUIApplication()
        app.launch()
        openTab(.wallet, in: app)

        selectCoin("BTC", app)
        startRestore(app)
        typePhrase(words, app)
        app.buttons["wallet-restore-action"].tap()
        openShippedNode(app)
        XCTAssertFalse(app.textFields["wallet-indexer-url"].exists, "Bitcoin has no indexer")
        attachScreenshot(app, "wallet-7-btc-node")
        replaceText("wallet-node-url", with: "https://blockstream.info/api", app)
        app.buttons["wallet-node-save"].tap()
        XCTAssertTrue(nodeRow(app).label.contains("blockstream.info/api"), nodeRow(app).label)
        openNode(app)
        tapWhenUp("wallet-node-default", in: app)
        XCTAssertTrue(nodeRow(app).label.contains("mempool.space"), nodeRow(app).label)
        removeWallet(Bip84.address(pubKey: try Bip84.key(Bip84.accountKey(mnemonic: phrase), change: 0, index: 0).pubKey()), app)

        selectCoin("ETH", app)
        startRestore(app)
        typePhrase(words, app)
        app.buttons["wallet-restore-action"].tap()
        openShippedNode(app)
        let indexer = app.textFields["wallet-indexer-url"]
        XCTAssertTrue(indexer.exists, "Ethereum reads history from an indexer")
        XCTAssertEqual(indexer.value as? String, "https://eth.blockscout.com")
        attachScreenshot(app, "wallet-8-eth-node")
        replaceText("wallet-indexer-url", with: "https://scout.example", app)
        app.buttons["wallet-node-save"].tap()
        // The row names the node; the indexer moved alone.
        XCTAssertTrue(nodeRow(app).label.contains("ethereum-rpc.publicnode.com"), nodeRow(app).label)
        openNode(app)
        XCTAssertEqual(indexer.value as? String, "https://scout.example")
        tapWhenUp("wallet-node-default", in: app)
        openNode(app)
        XCTAssertEqual(indexer.value as? String, "https://eth.blockscout.com")
        XCTAssertFalse(app.buttons["wallet-node-default"].exists, "the shipped addresses offer no way back to themselves")
        back(app)
        let eth = try EthCrypto.address(pubCompressed: EthCrypto.key(EthCrypto.accountKey(mnemonic: phrase), index: 0).pubKey())
        removeWallet(eth, app)
        selectCoin("SKY", app)
    }

    // MARK: Steps

    private func nodeRow(_ app: XCUIApplication) -> XCUIElement {
        let row = app.buttons["wallet-node-row"]
        XCTAssertTrue(row.waitForExistence(timeout: 180), "no Node row on the balance screen")
        return row
    }

    private func openNode(_ app: XCUIApplication) {
        let row = nodeRow(app)
        scroll(to: row, in: app)
        row.tap()
        XCTAssertTrue(app.textFields["wallet-node-url"].waitForExistence(timeout: 5))
    }

    /// The node screen on the shipped addresses, whatever an earlier run left.
    private func openShippedNode(_ app: XCUIApplication) {
        openNode(app)
        if app.buttons["wallet-node-default"].exists {
            tapWhenUp("wallet-node-default", in: app)
            openNode(app)
        }
    }

    private func replaceText(_ identifier: String, with text: String, _ app: XCUIApplication) {
        let field = app.textFields[identifier]
        // At the end of the text, so the deletes take all of it.
        field.coordinate(withNormalizedOffset: CGVector(dx: 0.97, dy: 0.5)).tap()
        let current = field.value as? String ?? ""
        field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: current.count) + text)
    }

    private func selectCoin(_ id: String, _ app: XCUIApplication) {
        let chip = app.buttons["wallet-coin-chip"]
        XCTAssertTrue(chip.waitForExistence(timeout: 10))
        chip.tap()
        let coin = app.buttons["wallet-coin-\(id)"]
        XCTAssertTrue(coin.waitForExistence(timeout: 5), "no \(id) in the coin list")
        coin.tap()
        XCTAssertTrue(chip.waitForExistence(timeout: 5))
    }

    /// From the intro when the coin has no wallet, else from the wallets list.
    private func startCreate(_ app: XCUIApplication) {
        let create = app.buttons["wallet-create"]
        if create.waitForExistence(timeout: 3) {
            create.tap()
        } else {
            openWallets(app)
            app.buttons["wallet-manage-create"].tap()
        }
    }

    private func startRestore(_ app: XCUIApplication) {
        let restore = app.buttons["wallet-restore"]
        if restore.waitForExistence(timeout: 3) {
            restore.tap()
        } else {
            openWallets(app)
            app.buttons["wallet-manage-restore"].tap()
        }
    }

    private func openWallets(_ app: XCUIApplication) {
        let row = app.buttons["wallet-wallets-row"]
        XCTAssertTrue(row.waitForExistence(timeout: 10))
        scroll(to: row, in: app)
        row.tap()
    }

    private func readSeedWords(_ app: XCUIApplication) throws -> [String] {
        XCTAssertTrue(app.staticTexts["seed-word-1"].waitForExistence(timeout: 10))
        return try (1...24).compactMap { i -> String? in
            let word = app.staticTexts["seed-word-\(i)"]
            guard word.exists else { return nil }
            return try XCTUnwrap(word.label.isEmpty ? nil : word.label)
        }
    }

    private func typePhrase(_ words: [String], _ app: XCUIApplication) {
        let input = app.textFields["wallet-restore-input"]
        XCTAssertTrue(input.waitForExistence(timeout: 10))
        input.tap()
        for word in words { input.typeText(word + " ") }
        XCTAssertTrue(app.buttons["wallet-restore-word-\(words.count)"].waitForExistence(timeout: 5), "not every word was taken")
    }

    /// Opens Receive from the balance screen and reads the address on it.
    private func receiveAddress(_ app: XCUIApplication, timeout: TimeInterval = 20) throws -> String {
        let receive = app.buttons["wallet-receive"]
        XCTAssertTrue(receive.waitForExistence(timeout: timeout), "no balance screen")
        receive.tap()
        let address = app.buttons["wallet-receive-address"]
        XCTAssertTrue(address.waitForExistence(timeout: 10))
        return try XCTUnwrap(address.value as? String)
    }

    /// From the balance screen: the wallets list, the wallet's row, Remove,
    /// confirmed; back on the balance screen (or the intro).
    private func removeWallet(_ firstAddress: String, _ app: XCUIApplication) {
        if !app.buttons["wallet-row-\(firstAddress)"].exists { openWallets(app) }
        let row = app.buttons["wallet-row-\(firstAddress)"]
        XCTAssertTrue(row.waitForExistence(timeout: 10))
        row.tap()
        tapWhenUp("wallet-action-remove", in: app)
        tapWhenUp("Remove", in: app)
        XCTAssertTrue(waitGone(row, timeout: 10), "the wallet stayed in the list")
        back(app)
    }

    private func back(_ app: XCUIApplication) {
        goBack(app)
    }

    private func waitEnabled(_ element: XCUIElement, timeout: TimeInterval) -> Bool {
        let enabled = XCTNSPredicateExpectation(predicate: NSPredicate(format: "isEnabled == true"), object: element)
        return XCTWaiter().wait(for: [enabled], timeout: timeout) == .completed
    }

    private func waitGone(_ element: XCUIElement, timeout: TimeInterval) -> Bool {
        let gone = XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: element)
        return XCTWaiter().wait(for: [gone], timeout: timeout) == .completed
    }

    private func skycoinAddress(_ words: [String]) throws -> String {
        let keys = try SkycoinCrypto.generateKeyPairs(seed: Array(words.joined(separator: " ").utf8), count: 1)
        return SkycoinCrypto.addressFromPubKey(keys[0].public)
    }

    private func attachScreenshot(_ app: XCUIApplication, _ name: String) {
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = name
        shot.lifetime = .keepAlways
        add(shot)
    }

    /// Has faceid-matcher.sh (on the Mac) answer Face ID prompts while `step`
    /// runs (FlowChecks' mechanism: a flag file in the runner's tmp).
    private func answeringFaceID(_ step: () -> Void) {
        FileManager.default.createFile(atPath: Self.faceIDFlag, contents: nil)
        defer { try? FileManager.default.removeItem(atPath: Self.faceIDFlag) }
        step()
    }

    private nonisolated static let faceIDFlag = NSTemporaryDirectory() + "faceid-match"
}
