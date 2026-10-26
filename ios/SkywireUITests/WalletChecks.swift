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
        try? FileManager.default.removeItem(atPath: Self.walletFaceIDFlag)
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

        // In USDT's list, whichever of its wallets is in use: an earlier one stays so.
        selectCoin("USDT", app)
        openWallets(app)
        XCTAssertTrue(app.buttons["wallet-row-\(eth)"].waitForExistence(timeout: 10), "USDT does not hold the ETH account")
        attachScreenshot(app, "wallet-6-usdt-mirror")
        back(app)
        removeWallet(eth, app)
        selectCoin("ETH", app)
        removeWallet(eth, app)
        selectCoin("SKY", app)
    }

    /// Every coin's node can be moved, and the Ethereum family's history indexer beside it, saved
    /// on its own (Android: WalletNode.kt). A fresh phrase on Bitcoin, then Ethereum.
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
        XCTAssertTrue(app.descendants(matching: .any)["wallet-node-shipped"].label.contains("mempool.space"))
        addNode("https://blockstream.info", app)
        XCTAssertTrue(app.staticTexts["wallet-node-1"].waitForExistence(timeout: 30), "the node was not added")
        attachScreenshot(app, "wallet-7-btc-node")
        back(app)
        XCTAssertTrue(nodeRow(app).label.contains("blockstream.info"), nodeRow(app).label)
        openNode(app)
        app.buttons["wallet-node-remove-1"].tap()
        XCTAssertTrue(waitGone(app.staticTexts["wallet-node-1"], timeout: 5))
        back(app)
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
        scroll(to: indexer, in: app)
        replaceText("wallet-indexer-url", with: "https://scout.example", app)
        app.buttons["wallet-indexer-save"].tap()
        back(app)
        // The row names the node; the indexer moved alone.
        XCTAssertTrue(nodeRow(app).label.contains("ethereum-rpc.publicnode.com"), nodeRow(app).label)
        openNode(app)
        XCTAssertEqual(indexer.value as? String, "https://scout.example")
        scroll(to: indexer, in: app)
        replaceText("wallet-indexer-url", with: "https://eth.blockscout.com", app)
        app.buttons["wallet-indexer-save"].tap()
        back(app)
        openNode(app)
        XCTAssertEqual(indexer.value as? String, "https://eth.blockscout.com")
        back(app)
        let eth = try EthCrypto.address(pubCompressed: EthCrypto.key(EthCrypto.accountKey(mnemonic: phrase), index: 0).pubKey())
        removeWallet(eth, app)
        selectCoin("SKY", app)
    }

    /// A node named by its key on the Skywire mesh answers through the core's .dmsg resolver: the
    /// deployment's Skycoin node, written in hex as it is shared, checked and listed by its label.
    func testADmsgNodeOverTheMesh() throws {
        let app = connectedApp(arguments: ["-AppleLanguages", "(en)", "-AppleLocale", "en_US"])
        openTab(.wallet, in: app)
        selectCoin("SKY", app)
        if !app.buttons["wallet-node-row"].waitForExistence(timeout: 5) {
            startRestore(app)
            typePhrase(Bip39.newMnemonic().split(separator: " ").map(String.init), app)
            app.buttons["wallet-restore-action"].tap()
        }
        openShippedNode(app)
        let hex = "039a6d1e3c237f5f05b78ec19e9f31a007f84835d7ef1e812876102281d1db74c1"
        let field = app.textFields["wallet-node-add-url"]
        field.tap()
        field.typeText("http://\(hex).dmsg:6420")
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", "Reached over the Skywire mesh")).firstMatch.exists,
                      "the transport note does not say the mesh")
        // The resolver binds a while after the core says connected; a user would try again.
        let accept = app.buttons["wallet-node-check-use"]
        let error = app.staticTexts["wallet-node-error"]
        for attempt in 1...4 where !accept.exists {
            if attempt > 1 { sleep(15) }
            app.buttons["wallet-node-add"].tap()
            let deadline = Date().addingTimeInterval(90)
            while !accept.exists && !error.exists && Date() < deadline { usleep(500_000) }
        }
        XCTAssertTrue(accept.exists, "the .dmsg node did not answer: \(error.exists ? error.label : "no answer")")
        XCTAssertTrue(app.staticTexts["Skycoin"].exists, "the properties do not name the node's coin")
        attachScreenshot(app, "wallet-dmsg-node-check")
        accept.tap()
        let row = app.staticTexts["wallet-node-1"]
        XCTAssertTrue(row.waitForExistence(timeout: 10))
        XCTAssertEqual(row.label, "http://aong2hr4…r3n2mc.dmsg:6420")
        attachScreenshot(app, "wallet-dmsg-node-list")
        app.buttons["wallet-node-remove-1"].tap()
        XCTAssertTrue(waitGone(row, timeout: 5))
    }

    /// Ethereum offers three tips, as skycoin-web does; picking one is what the plan prices, and an
    /// empty account is refused at review rather than signed.
    func testEthereumFeeLevels() throws {
        let words = Bip39.newMnemonic().split(separator: " ").map(String.init)
        let phrase = words.joined(separator: " ")
        let app = XCUIApplication()
        app.launchArguments = ["-AppleLanguages", "(en)", "-AppleLocale", "en_US"]
        app.launch()
        openTab(.wallet, in: app)
        selectCoin("ETH", app)
        startRestore(app)
        typePhrase(words, app)
        app.buttons["wallet-restore-action"].tap()
        let eth = try EthCrypto.address(pubCompressed: EthCrypto.key(EthCrypto.accountKey(mnemonic: phrase), index: 0).pubKey())
        let send = app.buttons["wallet-send"]
        XCTAssertTrue(waitEnabled(send, timeout: 180), "the Ethereum node did not answer")
        send.tap()
        let normal = app.buttons["wallet-fee-normal"], fast = app.buttons["wallet-fee-fast"], slow = app.buttons["wallet-fee-slow"]
        scroll(to: fast, in: app)
        XCTAssertTrue(normal.isSelected, "Normal is not the default")
        fast.tap()
        XCTAssertTrue(fast.isSelected && !normal.isSelected)
        slow.tap()
        XCTAssertTrue(slow.isSelected && !fast.isSelected)
        attachScreenshot(app, "wallet-eth-fees")
        let to = app.textFields["wallet-send-to"]
        scroll(to: to, in: app)
        to.tap()
        to.typeText(eth)
        let amount = app.textFields["wallet-send-amount"]
        amount.tap()
        amount.typeText("0.001")
        let review = app.buttons["wallet-send-review"]
        scroll(to: review, in: app)
        review.tap()
        XCTAssertTrue(app.staticTexts["wallet-send-error"].waitForExistence(timeout: 60), "planning an empty account gave no answer")
        back(app)
        removeWallet(eth, app)
        selectCoin("USDT", app)
        removeWallet(eth, app)
        selectCoin("SKY", app)
    }
}
