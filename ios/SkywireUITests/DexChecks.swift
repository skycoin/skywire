import XCTest

/// SkyDEX on the Simulator (M4 item 4.2), in the SkywireTour scheme. Without
/// a market to trade on this proves everything up to the dial: the field's
/// check, skydex-client started with `--market-pk`, its gated API answering,
/// and the market's failure shown in the engine's own words. With a market
/// (TEST_RUNNER_SKYWIRE_DEX_MARKET=<pk>, the desktop peer's with PEER_MARKET=1)
/// it connects, registers a Skycoin address in Settings, walks the native
/// tabs, and disconnects. When the market sells a coin it also creates a sell
/// order and checks the deposit instructions and their QR code.
@MainActor
final class DexChecks: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    func testMarketFlow() {
        let app = connectedApp()
        openTab(.apps, in: app)
        let dex = app.buttons["hub-dex"]
        XCTAssertTrue(dex.waitForExistence(timeout: 10))
        dex.tap()
        let field = app.textFields["dex-market-field"]
        XCTAssertTrue(field.waitForExistence(timeout: 10))
        let connect = app.buttons["dex-connect"]

        // A key of the wrong shape is caught in the field.
        replace(field, with: "02abc")
        XCTAssertFalse(connect.isEnabled, "Connect took a malformed key")

        let market = ProcessInfo.processInfo.environment["SKYWIRE_DEX_MARKET"]
            // A real curve point where no market runs (secp256k1's
            // generator): the engine accepts the key and the dial fails. A
            // made-up key is refused before any dial ("invalid market public
            // key").
            ?? "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
        replace(field, with: market)
        XCTAssertTrue(connect.isEnabled)
        connect.tap()
        if ProcessInfo.processInfo.environment["SKYWIRE_DEX_MARKET"] == nil {
            let error = app.staticTexts["dex-error"]
            XCTAssertTrue(error.waitForExistence(timeout: 150), "no answer from the dial")
            snap("dex-unreachable")
            XCTAssertFalse(error.label.contains("did not answer"), "skydex-client's UI never came up: \(error.label)")
        } else {
            XCTAssertTrue(app.buttons["dex-disconnect"].waitForExistence(timeout: 150), "the market never connected")
            snap("dex-connected")
            trade(app)
            app.buttons["dex-disconnect"].tap()
            XCTAssertTrue(field.waitForExistence(timeout: 30), "Disconnect did not return to the form")
            snap("dex-disconnected")
        }
    }

    /// A product on the market (the bench puts one in its database, as the market has no
    /// self-buy check): Buy shows what to pay and to whom, My Orders lists the order awaiting
    /// payment, and cancelling it takes it off. Run after testMarketFlow, which registers.
    func testBuyFlow() throws {
        guard let market = ProcessInfo.processInfo.environment["SKYWIRE_DEX_MARKET"],
              let product = ProcessInfo.processInfo.environment["SKYWIRE_DEX_PRODUCT"]
        else { throw XCTSkip("no market product: set TEST_RUNNER_SKYWIRE_DEX_MARKET and _DEX_PRODUCT") }
        let app = connectedApp()
        openTab(.apps, in: app)
        app.buttons["hub-dex"].tap()
        let field = app.textFields["dex-market-field"]
        XCTAssertTrue(field.waitForExistence(timeout: 10))
        replace(field, with: market)
        app.buttons["dex-connect"].tap()
        let buy = app.buttons["dex-buy-\(product)"]
        XCTAssertTrue(buy.waitForExistence(timeout: 150), "the product is not on the market")
        buy.tap()
        XCTAssertTrue(app.descendants(matching: .any)["dex-payment"].waitForExistence(timeout: 60), "no payment instructions")
        snap("dex-payment")
        app.buttons["dex-tab-orders"].tap()
        XCTAssertTrue(app.staticTexts["Pending payment"].waitForExistence(timeout: 30), "the order is not under My Orders")
        snap("dex-order")
        app.buttons["Cancel buy"].firstMatch.tap()
        let keep = app.buttons["Keep"]
        XCTAssertTrue(keep.waitForExistence(timeout: 5), "no confirmation")
        usleep(400_000)
        let confirm = app.buttons.matching(NSPredicate(format: "label == 'Cancel buy'")).allElementsBoundByIndex.first { abs($0.frame.midY - keep.frame.midY) < 4 }
        XCTAssertNotNil(confirm, "the dialog has no Cancel buy")
        confirm?.tap()
        let gone = XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: app.staticTexts["Pending payment"])
        XCTAssertEqual(XCTWaiter().wait(for: [gone], timeout: 60), .completed, "the order was not canceled")
        snap("dex-order-canceled")
        app.buttons["dex-disconnect"].tap()
    }

    /// Settings first (the market wants an address before any trade), then every tab.
    private func trade(_ app: XCUIApplication) {
        app.buttons["dex-tab-settings"].tap()
        let sky = app.textFields["dex-sky-address"]
        XCTAssertTrue(sky.waitForExistence(timeout: 10), "Settings has no Skycoin address field")
        replace(sky, with: "29uMZ7vhsv8tRqjocLPMjuUaJVLyu91iNwW")
        // A seller paid in BTC needs a BTC payout address (the market says so otherwise).
        let btc = app.textFields["dex-payout-BTC"]
        if btc.waitForExistence(timeout: 10) { replace(btc, with: "bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq") }
        app.buttons["dex-save-addresses"].tap()
        let saved = app.staticTexts["dex-wallets-message"]
        XCTAssertTrue(saved.waitForExistence(timeout: 60), "saving the addresses said nothing")
        XCTAssertTrue(saved.label.hasPrefix("Addresses saved"), "the market refused the address: \(saved.label)")
        snap("dex-settings")
        for tab in ["orders", "listings", "history"] {
            app.buttons["dex-tab-\(tab)"].tap()
            usleep(1_500_000)
            snap("dex-\(tab)")
        }
        app.buttons["dex-tab-market"].tap()
        let sell = app.buttons["dex-new-sell"]
        XCTAssertTrue(sell.waitForExistence(timeout: 10))
        let ready = XCTNSPredicateExpectation(predicate: NSPredicate(format: "isEnabled == true"), object: sell)
        guard XCTWaiter().wait(for: [ready], timeout: 20) == .completed else {
            // A market with no sell coin says so instead.
            XCTAssertTrue(app.descendants(matching: .any)["dex-banner-warn"].exists, "no sell coin and no banner saying so")
            snap("dex-market-not-ready")
            return
        }
        sell.tap()
        let amount = app.textFields["dex-sell-amount"]
        XCTAssertTrue(amount.waitForExistence(timeout: 5))
        replace(amount, with: "1")
        replace(app.textFields["dex-sell-price"], with: "0.0001")
        app.buttons["dex-create-sell"].tap()
        let deposit = app.descendants(matching: .any)["dex-deposit"]
        XCTAssertTrue(deposit.waitForExistence(timeout: 60), "no deposit instructions: \(app.staticTexts["dex-action-error"].exists ? app.staticTexts["dex-action-error"].label : "nothing said")")
        app.buttons["dex-show-qr"].firstMatch.tap()
        XCTAssertTrue(app.descendants(matching: .any)["dex-qr"].waitForExistence(timeout: 5), "no QR code")
        snap("dex-deposit-qr")
        app.buttons["dex-qr-close"].tap()
        app.buttons["dex-tab-listings"].tap()
        XCTAssertTrue(app.staticTexts["Pending deposit"].waitForExistence(timeout: 20), "the new listing is not under My Listings")
        snap("dex-listing")

        // Cancel it: the market allows one pending listing per seller.
        let cancel = app.buttons["Cancel offer"].firstMatch
        XCTAssertTrue(cancel.waitForExistence(timeout: 5), "the pending listing offers no cancel")
        cancel.tap()
        // The dialog's own Cancel offer, drawn over the card's.
        let keep = app.buttons["Keep"]
        XCTAssertTrue(keep.waitForExistence(timeout: 5), "no confirmation")
        usleep(400_000)
        let both = app.buttons.matching(NSPredicate(format: "label == 'Cancel offer'")).allElementsBoundByIndex
        let confirm = both.first { abs($0.frame.midY - keep.frame.midY) < 4 }
        XCTAssertNotNil(confirm, "the dialog has no Cancel offer")
        confirm?.tap()
        let gone = XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: app.staticTexts["Pending deposit"])
        XCTAssertEqual(XCTWaiter().wait(for: [gone], timeout: 60), .completed, "the listing was not canceled")
    }

    private func replace(_ field: XCUIElement, with text: String) {
        field.tap()
        if let current = field.value as? String, !current.isEmpty, current != field.placeholderValue {
            field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: current.count + 2))
        }
        field.typeText(text)
    }

    private func snap(_ name: String) {
        usleep(700_000)
        let attachment = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
