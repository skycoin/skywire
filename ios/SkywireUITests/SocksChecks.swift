import XCTest

/// SkySOCKS on the live network (M4 item 4.1), in the SkywireTour scheme. The
/// proxy itself is checked from the Mac between the tests, since a
/// Simulator app's 127.0.0.1 is the Mac's:
///
///   -only-testing:SkywireUITests/SocksChecks/testConnectsToAProxy
///   curl -s --socks5-hostname 127.0.0.1:1080 https://api.ipify.org   # the exit's IP
///   curl -s https://api.ipify.org                                    # the Mac's
///   -only-testing:SkywireUITests/SocksChecks/testPortMoves           # 1090, then back
///   -only-testing:SkywireUITests/SocksChecks/testRestartWithTheProxyConnected
///   -only-testing:SkywireUITests/SocksChecks/testDisconnects
@MainActor
final class SocksChecks: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    /// Picks proxies from service discovery, first to last, until one
    /// connects, and holds the connection 40 s for the Mac's curl (the app,
    /// and the proxy with it, ends with the test).
    func testConnectsToAProxy() {
        let app = connectedApp()
        openSocks(app)
        connectFromTheList(app)
        snap("socks-connected")
        // The app (and the proxy with it) ends with the test: held here for
        // the Mac's curl through 127.0.0.1:1080.
        sleep(40)
        snap("socks-held")
    }

    /// The listener moves to 1090 and the app comes back connected there,
    /// then moves back to 1080.
    func testPortMoves() {
        let app = connectedApp()
        openSocks(app)
        // Each test launches the app afresh, the proxy off: Reconnect dials
        // the server the last test picked.
        let state = app.staticTexts["socks-state"]
        if state.label != "Connected" {
            app.buttons["socks-connect"].tap()
            XCTAssertTrue(wait(for: state, label: "Connected", timeout: 90), "Reconnect did not connect")
        }
        for port in ["1090", "1080"] {
            app.buttons["socks-change-port"].tap()
            // The port sheet's field, filled with the current port.
            let field = app.textFields["socks-port-field"]
            XCTAssertTrue(field.waitForExistence(timeout: 5), "no port field")
            field.tap()
            field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: 6) + port)
            tapWhenUp("Save", in: app)
            let address = app.buttons["socks-address"]
            let moved = XCTNSPredicateExpectation(predicate: NSPredicate(format: "label CONTAINS %@", "127.0.0.1:\(port)"), object: address)
            XCTAssertEqual(XCTWaiter().wait(for: [moved], timeout: 30), .completed, "the address never showed \(port)")
            XCTAssertTrue(wait(for: app.staticTexts["socks-state"], label: "Connected", timeout: 90), "not connected on \(port)")
            snap("socks-port-\(port)")
        }
    }

    /// A core restart with the proxy connected, the path that crashed the app
    /// at G4: the transport manager overran its close timeout, the stcpr
    /// accept loop spun on the shared port closed under it, and its warnings,
    /// each one a synchronous call into Swift, took the app down. The app
    /// must live through it and the core come back connected. The restart is
    /// the one a log level change asks for: Home's own Restart went with its
    /// menu in M6.5, as Android has none.
    func testRestartWithTheProxyConnected() {
        let app = connectedApp()
        openSocks(app)
        connectFromTheList(app)
        setLogLevel("debug", app)
        XCTAssertEqual(app.state, .runningForeground, "the app did not live through the restart")
        snap("socks-restarted")
        setLogLevel("info", app)
    }

    func testDisconnects() {
        let app = connectedApp()
        openSocks(app)
        let state = app.staticTexts["socks-state"]
        if state.label == "Connected" {
            app.buttons["socks-connect"].tap()
        }
        XCTAssertTrue(wait(for: state, label: "Disconnected", timeout: 30))
    }

    /// Taps proxies from service discovery, first to last (at most four),
    /// until one connects: a public proxy that answered yesterday may not
    /// today, so the last one picked is not relied on.
    private func connectFromTheList(_ app: XCUIApplication) {
        // A row holds the server and its star, so it is a container, not a button.
        let rows = app.descendants(matching: .any).matching(identifier: "socks-server-row")
        XCTAssertTrue(rows.firstMatch.waitForExistence(timeout: 120), "service discovery listed no proxy")
        let state = app.staticTexts["socks-state"]
        for index in 0..<min(rows.count, 4) {
            rows.element(boundBy: index).tap()
            if wait(for: state, label: "Connected", timeout: 90) {
                return
            }
        }
        XCTFail("no proxy connected; state \(state.label)")
    }

    private func openSocks(_ app: XCUIApplication) {
        openTab(.apps, in: app)
        let row = app.buttons["hub-socks"]
        XCTAssertTrue(row.waitForExistence(timeout: 10))
        row.tap()
        XCTAssertTrue(app.staticTexts["socks-state"].waitForExistence(timeout: 10))
    }

    private func wait(for element: XCUIElement, label: String, timeout: TimeInterval) -> Bool {
        let expectation = XCTNSPredicateExpectation(predicate: NSPredicate(format: "label == %@", label), object: element)
        return XCTWaiter().wait(for: [expectation], timeout: timeout) == .completed
    }

    private func snap(_ name: String) {
        usleep(700_000)
        let attachment = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
