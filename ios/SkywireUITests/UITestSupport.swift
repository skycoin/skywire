import XCTest

/// What the flow checks share: a connected app, the tabs, the core's state.
@MainActor
extension XCTestCase {
    func connectedApp(arguments: [String] = []) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = arguments
        app.launch()
        XCTAssertTrue(state(app).waitForExistence(timeout: 15))
        if state(app).value as? String == "stopped" {
            app.buttons["connect-button"].tap()
            allowNotificationsIfAsked()
        }
        wait(for: state(app), anyOf: ["connected"], timeout: 180)
        return app
    }

    /// The first Connect asks whether the app may notify (once per install);
    /// the system's alert would otherwise sit over every later step.
    @discardableResult
    func allowNotificationsIfAsked(within timeout: TimeInterval = 3) -> Bool {
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let allow = springboard.alerts.buttons["Allow"]
        guard allow.waitForExistence(timeout: timeout) else { return false }
        allow.tap()
        return true
    }

    /// Taps a bar slot and waits until it is the selected one (the cloud has no selected state).
    func openTab(_ which: Tab, in app: XCUIApplication) {
        let tab = app.buttons[which.rawValue]
        XCTAssertTrue(tab.waitForExistence(timeout: 10))
        // A tap can land while a system sheet (Face ID) is still leaving.
        for _ in 0..<3 where which == .apps || !tab.isSelected {
            tab.tap()
            if which == .apps { return }
            let selected = XCTNSPredicateExpectation(predicate: NSPredicate(format: "isSelected == true"), object: tab)
            _ = XCTWaiter().wait(for: [selected], timeout: 3)
        }
        XCTAssertTrue(tab.isSelected, "tab \(which) not selected")
    }

    /// The top bar's back arrow on the screen in front (a screen kept under it has one too).
    func goBack(_ app: XCUIApplication) {
        let backs = app.buttons.matching(identifier: "top-back")
        XCTAssertTrue(backs.firstMatch.waitForExistence(timeout: 5), "no back arrow")
        for index in 0..<backs.count where backs.element(boundBy: index).isHittable {
            backs.element(boundBy: index).tap()
            return
        }
        XCTFail("no back arrow in front")
    }

    /// Taps a dialog's or sheet's button once it is up: they animate in, and a tap made while
    /// one slides lands where the button is not yet.
    func tapWhenUp(_ label: String, in app: XCUIApplication, timeout: TimeInterval = 5) {
        let button = app.buttons[label].firstMatch
        XCTAssertTrue(button.waitForExistence(timeout: timeout), "no \(label) button")
        usleep(400_000)
        button.tap()
    }

    /// Swipes up on `start`, the screen by default, until `element` can be tapped, a few times at
    /// most. A swipe that starts in a focused text box does not move the page.
    func scroll(to element: XCUIElement, in app: XCUIApplication, from start: XCUIElement? = nil) {
        for _ in 0..<6 where !element.isHittable {
            (start ?? app).swipeUp()
        }
        XCTAssertTrue(element.isHittable, "\(element) not reachable by scrolling")
    }

    /// Launches the app in `language` (Xcode's App Language option) and checks it took: the bar's
    /// Home reads in that language. A language chosen in Settings must not outrank it.
    func launch(_ app: XCUIApplication, language: String, locale: String) {
        app.launchArguments = ["-AppleLanguages", "(\(language))", "-AppleLocale", locale]
        app.launch()
        let home = ["en": "Home", "zh-Hans": "首页", "es": "Inicio"][language]
        let tab = app.buttons["tab-home"]
        XCTAssertTrue(tab.waitForExistence(timeout: 15))
        XCTAssertEqual(tab.label, home, "the app is not drawn in \(language)")
    }

    /// Settings > Logs & diagnostics > Core log level, confirmed, and back on
    /// Home until the restarted core is connected again.
    func setLogLevel(_ level: String, _ app: XCUIApplication) {
        openTab(.settings, in: app)
        let diagnostics = app.buttons["diagnostics-link"]
        scroll(to: diagnostics, in: app)
        diagnostics.tap()
        let chip = app.buttons["log-level-\(level)"]
        scroll(to: chip, in: app)
        chip.tap()
        // A change asks before it restarts the core (FlowChecks runs in
        // English). No question means the level was already this one (an
        // earlier run): nothing restarts.
        let confirm = app.buttons["Restart core"].firstMatch
        let changed = confirm.waitForExistence(timeout: 5)
        if changed { confirm.tap() }
        goBack(app)
        openTab(.home, in: app)
        if changed {
            wait(for: state(app), anyOf: ["stopping", "stopped", "starting", "running"], timeout: 30)
        }
        wait(for: state(app), anyOf: ["connected"], timeout: 180)
    }

    func openSocks(_ app: XCUIApplication) {
        openTab(.apps, in: app)
        let row = app.buttons["hub-socks"]
        XCTAssertTrue(row.waitForExistence(timeout: 10))
        row.tap()
        XCTAssertTrue(app.staticTexts["socks-state"].waitForExistence(timeout: 10))
    }

    /// Taps proxies from service discovery, first to last (at most four),
    /// until one connects: a public proxy that answered yesterday may not
    /// today, so the last one picked is not relied on.
    func connectFromTheList(_ app: XCUIApplication) {
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

    func wait(for element: XCUIElement, label: String, timeout: TimeInterval) -> Bool {
        let expectation = XCTNSPredicateExpectation(predicate: NSPredicate(format: "label == %@", label), object: element)
        return XCTWaiter().wait(for: [expectation], timeout: timeout) == .completed
    }

    func state(_ app: XCUIApplication) -> XCUIElement {
        app.descendants(matching: .any)["core-state"]
    }

    func wait(for element: XCUIElement, anyOf values: [String], timeout: TimeInterval) {
        let expectation = XCTNSPredicateExpectation(predicate: NSPredicate(format: "value IN %@", values), object: element)
        XCTAssertEqual(XCTWaiter().wait(for: [expectation], timeout: timeout), .completed,
                       "core-state stayed \(element.value ?? "nil"), not one of \(values)")
    }
}
