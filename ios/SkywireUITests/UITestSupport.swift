import XCTest

/// What the flow checks share: a connected app, the tabs, the core's state.
@MainActor
extension XCTestCase {
    func connectedApp() -> XCUIApplication {
        let app = XCUIApplication()
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
    func allowNotificationsIfAsked() {
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let allow = springboard.alerts.buttons["Allow"]
        if allow.waitForExistence(timeout: 3) {
            allow.tap()
        }
    }

    /// Selects a tab and waits until it is the selected one.
    func openTab(_ which: Tab, in app: XCUIApplication) {
        let tab = app.tabBars.buttons.element(boundBy: which.rawValue)
        XCTAssertTrue(tab.waitForExistence(timeout: 10))
        // A tap can land while a system sheet (Face ID) is still leaving.
        for _ in 0..<3 where !tab.isSelected {
            tab.tap()
            let selected = XCTNSPredicateExpectation(predicate: NSPredicate(format: "isSelected == true"), object: tab)
            _ = XCTWaiter().wait(for: [selected], timeout: 3)
        }
        XCTAssertTrue(tab.isSelected, "tab \(which) not selected")
    }

    /// Swipes up until `element` can be tapped, a few times at most.
    func scroll(to element: XCUIElement, in app: XCUIApplication) {
        for _ in 0..<6 where !element.isHittable {
            app.swipeUp()
        }
        XCTAssertTrue(element.isHittable, "\(element) not reachable by scrolling")
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
