import XCTest

/// The wallet's screens in each of the app's languages, a screenshot of
/// each, for G5's exit evidence and the clipping check (Spanish runs ~30%
/// longer than English). No core: the wallet talks to the coins' nodes
/// directly. Part of the SkywireTour scheme, like ScreenTour:
///
///   xcodebuild -project ios/Skywire.xcodeproj -scheme SkywireTour \
///     -destination 'platform=iOS Simulator,name=iPhone 18 Pro' \
///     -resultBundlePath ios/build/WalletTour.xcresult test \
///     -only-testing:SkywireUITests/WalletTour
///
/// The phrase is BIP 39's public test vector ("abandon … about"), so no
/// screenshot shows words that could ever hold anything of anyone's. The
/// tests run in name order and the last, in English, removes the wallet the
/// others leave.
@MainActor
final class WalletTour: XCTestCase {
    private static let phrase = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

    override func setUp() {
        continueAfterFailure = false
    }

    func testWalletTourEnglish() throws { try tour(language: "en", locale: "en_US") }

    func testWalletTourSimplifiedChinese() throws { try tour(language: "zh-Hans", locale: "zh_CN") }

    func testWalletTourSpanish() throws { try tour(language: "es", locale: "es_ES") }

    /// Removes the tour's wallet (English labels): last by name.
    func testWalletTourZRemove() {
        let app = XCUIApplication()
        app.launchArguments = ["-AppleLanguages", "(en)", "-AppleLocale", "en_US"]
        app.launch()
        openTab(.wallet, in: app)
        pick("SKY", app)
        guard app.buttons["wallet-wallets-row"].waitForExistence(timeout: 5) else { return }
        app.buttons["wallet-wallets-row"].tap()
        // One at a time, re-reading the list after each: every Skycoin wallet
        // on this Simulator goes, including any an interrupted run left.
        let rows = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'wallet-row-'"))
        for _ in 0..<20 where rows.firstMatch.waitForExistence(timeout: 3) {
            let row = app.buttons[rows.firstMatch.identifier]
            row.tap()
            tapWhenUp("wallet-action-remove", in: app)
            tapWhenUp("Remove", in: app)
            let gone = XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: row)
            XCTAssertEqual(XCTWaiter().wait(for: [gone], timeout: 10), .completed, "a wallet stayed in the list")
        }
        XCTAssertFalse(rows.firstMatch.exists, "Skycoin wallets left behind")
    }

    private func tour(language: String, locale: String) throws {
        let app = XCUIApplication()
        app.launchArguments = ["-AppleLanguages", "(\(language))", "-AppleLocale", locale]
        app.launch()
        openTab(.wallet, in: app)

        // A coin with no wallet opens on setup; the coin list over it.
        pick("BTC", app)
        XCTAssertTrue(app.buttons["wallet-create"].waitForExistence(timeout: 10))
        snap("\(language)-w1-intro")
        app.buttons["wallet-coin-chip"].tap()
        XCTAssertTrue(app.buttons["wallet-coin-SKY"].waitForExistence(timeout: 5))
        snap("\(language)-w2-coins")
        app.buttons["wallet-coin-SKY"].tap()

        // Skycoin: restored from the test phrase unless an earlier language
        // already did (the store adopts the same account rather than
        // duplicating it, so either way one wallet).
        if app.buttons["wallet-restore"].waitForExistence(timeout: 5) {
            app.buttons["wallet-restore"].tap()
            let input = app.textFields["wallet-restore-input"]
            XCTAssertTrue(input.waitForExistence(timeout: 5))
            input.tap()
            for word in Self.phrase.split(separator: " ") { input.typeText(word + " ") }
            snap("\(language)-w3-restore")
            app.buttons["wallet-restore-action"].tap()
        }
        let receive = app.buttons["wallet-receive"]
        XCTAssertTrue(receive.waitForExistence(timeout: 120), "no balance screen")
        // One refresh's worth for the numbers.
        sleep(4)
        snap("\(language)-w4-balance")

        receive.tap()
        XCTAssertTrue(app.buttons["wallet-receive-address"].waitForExistence(timeout: 10))
        snap("\(language)-w5-receive")
        back(app)

        app.buttons["wallet-send"].tap()
        XCTAssertTrue(app.textFields["wallet-send-to"].waitForExistence(timeout: 10))
        snap("\(language)-w6-send")
        back(app)

        app.buttons["wallet-see-all"].tap()
        snap("\(language)-w7-history")
        back(app)

        let wallets = app.buttons["wallet-wallets-row"]
        scroll(to: wallets, in: app)
        wallets.tap()
        snap("\(language)-w8-wallets")
        back(app)

        let node = app.buttons["wallet-node-row"]
        scroll(to: node, in: app)
        node.tap()
        XCTAssertTrue(app.textFields["wallet-node-url"].waitForExistence(timeout: 5))
        snap("\(language)-w9-node")
        back(app)

        app.swipeDown()
        app.buttons["wallet-coin-chip"].tap()
        tapWhenUp("wallet-add-coin", in: app)
        XCTAssertTrue(app.textFields["wallet-add-name"].waitForExistence(timeout: 5))
        snap("\(language)-w10-add-coin")
        back(app)
        app.terminate()
    }

    private func pick(_ id: String, _ app: XCUIApplication) {
        let chip = app.buttons["wallet-coin-chip"]
        XCTAssertTrue(chip.waitForExistence(timeout: 10))
        chip.tap()
        let coin = app.buttons["wallet-coin-\(id)"]
        XCTAssertTrue(coin.waitForExistence(timeout: 5))
        coin.tap()
    }

    private func back(_ app: XCUIApplication) {
        goBack(app)
    }

    private func snap(_ name: String) {
        // Let SwiftUI settle (a navigation push animates).
        usleep(700_000)
        let attachment = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
