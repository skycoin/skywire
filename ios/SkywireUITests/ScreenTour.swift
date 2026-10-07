import XCTest

/// Walks the app's screens in each of its languages, connected to the real
/// network, and keeps a screenshot of each: the screenshots a gate's exit
/// evidence asks for, taken the same way every time. Not part of the Skywire
/// scheme's tests (CI): it dials the live network and takes minutes. Run it
/// with the SkywireTour scheme, then export the pictures:
///
///   xcodebuild -project ios/Skywire.xcodeproj -scheme SkywireTour \
///     -destination 'platform=iOS Simulator,name=iPhone 18 Pro' \
///     -resultBundlePath ios/build/Tour.xcresult test
///   xcrun xcresulttool export attachments --path ios/build/Tour.xcresult \
///     --output-path ios/gates/evidence/G2/screens
@MainActor
final class ScreenTour: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    func testTourEnglish() throws { try tour(language: "en", locale: "en_US") }

    func testTourSimplifiedChinese() throws { try tour(language: "zh-Hans", locale: "zh_CN") }

    func testTourSpanish() throws { try tour(language: "es", locale: "es_ES") }

    private func tour(language: String, locale: String) throws {
        let app = XCUIApplication()
        launch(app, language: language, locale: locale)

        // Disconnect first if the app came back connected, so every language
        // shows both states and exercises both directions of the button.
        let state = app.descendants(matching: .any)["core-state"]
        XCTAssertTrue(state.waitForExistence(timeout: 15))
        let connect = app.buttons["connect-button"]
        if state.value as? String != "stopped" {
            wait(for: state, anyOf: ["connected", "starting", "running"], timeout: 180)
            connect.tap()
        }
        wait(for: state, anyOf: ["stopped"], timeout: 60)
        snap("\(language)-1-home-disconnected")

        connect.tap()
        allowNotificationsIfAsked()
        // A start dials dmsg discovery and can take a minute.
        wait(for: state, anyOf: ["connected"], timeout: 180)
        let card = app.descendants(matching: .any)["visor-card"]
        XCTAssertTrue(card.waitForExistence(timeout: 60), "the visor card never filled")
        app.buttons["visor-expand"].tap()
        snap("\(language)-2-home-connected")

        // The chat page, once skychat answered and the page drew its list.
        openTab(.chat, in: app)
        let page = app.webViews["chat-webview"]
        XCTAssertTrue(page.waitForExistence(timeout: 60), "the chat page never came up")
        XCTAssertTrue(page.buttons.firstMatch.waitForExistence(timeout: 30), "the chat page drew nothing")
        snap("\(language)-3-chat")

        // The apps hub and its screens (M4).
        openTab(.apps, in: app)
        XCTAssertTrue(app.buttons["hub-socks"].waitForExistence(timeout: 10))
        snap("\(language)-3a-apps")
        app.buttons["hub-socks"].tap()
        XCTAssertTrue(app.staticTexts["socks-state"].waitForExistence(timeout: 10))
        // The proxy list rides dmsg; a row or the list's own message, then the shot.
        _ = app.descendants(matching: .any).matching(identifier: "socks-server-row").firstMatch.waitForExistence(timeout: 60)
        snap("\(language)-3b-socks")
        // The transport chooser lives here now, as on Android: a sheet.
        app.buttons["transport-link"].tap()
        usleep(500_000)
        snap("\(language)-3b2-transport")
        app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.1)).tap()
        usleep(500_000)
        goBack(app)
        app.buttons["hub-dex"].tap()
        XCTAssertTrue(app.textFields["dex-market-field"].waitForExistence(timeout: 10))
        snap("\(language)-3c-dex")
        goBack(app)
        app.buttons["hub-fleet"].tap()
        XCTAssertTrue(app.switches["fleet-screen-toggle"].waitForExistence(timeout: 10))
        snap("\(language)-3d-fleet")
        goBack(app)

        openTab(.settings, in: app)
        snap("\(language)-4-settings")
        app.swipeUp()
        snap("\(language)-6-settings-lower")
        let diagnostics = app.buttons["diagnostics-link"]
        scroll(to: diagnostics, in: app)
        diagnostics.tap()
        snap("\(language)-7-diagnostics")

        app.buttons["logs-source-core"].tap()
        XCTAssertTrue(app.buttons["top-back"].waitForExistence(timeout: 5))
        // A few polls' worth of the runtime log.
        sleep(4)
        snap("\(language)-8-logs-core")
        app.terminate()
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
