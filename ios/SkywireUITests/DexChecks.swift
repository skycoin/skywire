import XCTest

/// SkyDEX on the Simulator (M4 item 4.2), in the SkywireTour scheme. Without
/// a market to trade on this proves everything up to the dial: the field's
/// check, skydex-client started with `--market-pk`, its gated UI answering,
/// and the market's failure shown in the engine's own words. With a market
/// (TEST_RUNNER_SKYWIRE_DEX_MARKET=<pk>) it connects, shows the trading UI
/// under the native header, and disconnects.
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
            XCTAssertTrue(app.webViews["dex-webview"].waitForExistence(timeout: 150), "no trading UI")
            snap("dex-connected")
            app.buttons["dex-disconnect"].tap()
            XCTAssertTrue(field.waitForExistence(timeout: 30), "Disconnect did not return to the form")
            snap("dex-disconnected")
        }
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
