import XCTest

/// Flows the G2 gate walks through by hand, driven here so the author can
/// check them before the reviewer does. Part of the SkywireTour scheme (they
/// need the live network), not of CI. `testFleetOn` and `testFleetOff` are
/// run one at a time with -only-testing, with the config read in between:
///
///   jq .hypervisor.dmsg_ingest "$(xcrun simctl get_app_container booted \
///     com.skycoin.skywire data)/Library/Application Support/skywire/skywire-config.json"
@MainActor
final class FlowChecks: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    override func tearDown() {
        // A failed assertion ends the test inside answeringFaceID, past its
        // own cleanup; a flag left behind would answer the next test's prompts.
        try? FileManager.default.removeItem(atPath: Self.faceIDFlag)
    }

    /// Restart from Home: the core goes down and comes back, and the visor
    /// card fills again, which needs a new session (the old one died with the
    /// core), with nothing asked of the user.
    func testRestartFromHomeComesBackLoggedIn() {
        let app = connectedApp()
        app.buttons["home-actions"].tap()
        app.buttons["home-restart"].tap()
        wait(for: state(app), anyOf: ["stopping", "stopped", "starting", "running"], timeout: 30)
        wait(for: state(app), anyOf: ["connected"], timeout: 180)
        XCTAssertTrue(app.descendants(matching: .any)["visor-card"].waitForExistence(timeout: 60))
    }

    /// The core log level: debug asks, restarts the core, and the Process
    /// source then shows DEBUG lines (the Core source, the visor's runtime
    /// ring, stops at info by design: logstore.DefaultHookLevel); set back to
    /// info the same way.
    func testLogLevelReachesTheCore() {
        let app = connectedApp()
        setLogLevel("debug", app)
        openTab(1, in: app)
        let diagnostics = app.buttons["diagnostics-link"]
        scroll(to: diagnostics, in: app)
        diagnostics.tap()
        app.buttons["logs-source-process"].tap()
        let debugLine = app.staticTexts["DEBUG"].firstMatch
        XCTAssertTrue(debugLine.waitForExistence(timeout: 30), "no DEBUG line in the process log")
        app.navigationBars.buttons.element(boundBy: 0).tap()
        app.navigationBars.buttons.element(boundBy: 0).tap()
        openTab(0, in: app)
        setLogLevel("info", app)
    }

    /// Settings > Logs & diagnostics > Core log level, confirmed, and back on
    /// Home until the restarted core is connected again.
    private func setLogLevel(_ level: String, _ app: XCUIApplication) {
        openTab(1, in: app)
        let diagnostics = app.buttons["diagnostics-link"]
        scroll(to: diagnostics, in: app)
        diagnostics.tap()
        let picker = app.buttons["log-level-picker"]
        scroll(to: picker, in: app)
        picker.tap()
        app.buttons[level].firstMatch.tap()
        // A change asks before it restarts the core (FlowChecks runs in
        // English). No question means the level was already this one (an
        // earlier run): nothing restarts.
        let confirm = app.buttons["Restart"].firstMatch
        let changed = confirm.waitForExistence(timeout: 5)
        if changed { confirm.tap() }
        app.navigationBars.buttons.element(boundBy: 0).tap()
        openTab(0, in: app)
        if changed {
            wait(for: state(app), anyOf: ["stopping", "stopped", "starting", "running"], timeout: 30)
        }
        wait(for: state(app), anyOf: ["connected"], timeout: 180)
    }

    func testFleetOn() { toggleFleet(to: true) }

    func testFleetOff() { toggleFleet(to: false) }

    /// Fleet is pinned in the config and read once at start, so the switch
    /// restarts a running core.
    private func toggleFleet(to on: Bool) {
        let app = connectedApp()
        openTab(1, in: app)
        let toggle = app.switches["fleet-toggle"]
        scroll(to: toggle, in: app)
        let isOn = (toggle.value as? String) == "1"
        guard isOn != on else { return }
        toggle.switches.firstMatch.tap()
        XCTAssertEqual(toggle.value as? String, on ? "1" : "0")
        // The core's state is on Home.
        openTab(0, in: app)
        wait(for: state(app), anyOf: ["stopping", "stopped", "starting", "running"], timeout: 30)
        wait(for: state(app), anyOf: ["connected"], timeout: 180)
    }

    /// The app lock, round trip: turned on (behind a Face ID check), still
    /// unlocked after less than the 30 s grace away, locked again after more,
    /// unlocked, turned off. Needs Face ID enrolled on the Simulator, and the
    /// Mac to answer prompts with a match while this test asks (it keeps a
    /// flag file in its tmp dir):
    ///
    ///   xcrun simctl spawn booted notifyutil -s com.apple.BiometricKit.enrollmentChanged 1
    ///   xcrun simctl spawn booted notifyutil -p com.apple.BiometricKit.enrollmentChanged
    ///   ios/scripts/faceid-matcher.sh &
    func testAppLockRoundTrip() {
        let app = XCUIApplication()
        app.launch()
        // Left on by an earlier run: a cold start is locked.
        let unlock = app.buttons["unlock-button"]
        if unlock.waitForExistence(timeout: 5) {
            answeringFaceID { waitGone(unlock) }
        }
        openTab(1, in: app)
        let toggle = app.switches["app-lock-toggle"]
        scroll(to: toggle, in: app)
        XCTAssertTrue(toggle.isEnabled, "no Face ID or passcode on this Simulator")
        if toggle.value as? String == "1" {
            setLockSwitch(toggle, to: "0")
        }
        setLockSwitch(toggle, to: "1")

        // Away for less than the grace, leaving straight after the check's
        // sheet closed (the leg G2 asked for): back unlocked, on the screen
        // it left.
        XCUIDevice.shared.press(.home)
        sleep(10)
        app.activate()
        XCTAssertFalse(unlock.waitForExistence(timeout: 5), "locked after only 10 s away")
        XCTAssertTrue(toggle.isHittable, "the app's own screen did not come back")

        XCUIDevice.shared.press(.home)
        sleep(35)
        app.activate()
        // Locked, and its prompt waits: nothing answers it yet.
        XCTAssertTrue(unlock.waitForExistence(timeout: 10), "not locked after 35 s away")
        sleep(3)
        XCTAssertTrue(unlock.exists, "unlocked with no Face ID match")
        answeringFaceID { waitGone(unlock) }

        scroll(to: toggle, in: app)
        setLockSwitch(toggle, to: "0")
    }

    /// Turns the lock switch to `value` past its Face ID check. A tap made
    /// while the last prompt's sheet is still leaving can come to nothing (no
    /// prompt comes up), so it taps again when the switch has not moved in
    /// the time the matcher takes to answer a prompt that did come up.
    private func setLockSwitch(_ toggle: XCUIElement, to value: String) {
        answeringFaceID {
            for _ in 0..<3 where toggle.value as? String != value {
                toggle.switches.firstMatch.tap()
                let moved = XCTNSPredicateExpectation(predicate: NSPredicate(format: "value == %@", value), object: toggle)
                _ = XCTWaiter().wait(for: [moved], timeout: 8)
            }
            XCTAssertEqual(toggle.value as? String, value, "the app lock switch did not move to \(value)")
        }
    }

    private func waitGone(_ element: XCUIElement) {
        let gone = XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: element)
        XCTAssertEqual(XCTWaiter().wait(for: [gone], timeout: 30), .completed, "still locked after a match")
    }

    /// Has faceid-matcher.sh (on the Mac) answer every Face ID prompt with a
    /// match while `step` runs; a prompt raised after it returns waits. The
    /// flag file stays for the whole step because a match sent while no
    /// prompt is up is dropped: asking for one match per prompt lost it in
    /// the gap between one sheet closing and the next opening.
    private func answeringFaceID(_ step: () -> Void) {
        FileManager.default.createFile(atPath: Self.faceIDFlag, contents: nil)
        defer { try? FileManager.default.removeItem(atPath: Self.faceIDFlag) }
        step()
    }

    private static let faceIDFlag = NSTemporaryDirectory() + "faceid-match"

    private func connectedApp() -> XCUIApplication {
        let app = XCUIApplication()
        app.launch()
        XCTAssertTrue(state(app).waitForExistence(timeout: 15))
        if state(app).value as? String == "stopped" {
            app.buttons["connect-button"].tap()
        }
        wait(for: state(app), anyOf: ["connected"], timeout: 180)
        return app
    }

    /// Selects a tab and waits until it is the selected one.
    private func openTab(_ index: Int, in app: XCUIApplication) {
        let tab = app.tabBars.buttons.element(boundBy: index)
        XCTAssertTrue(tab.waitForExistence(timeout: 10))
        // A tap can land while a system sheet (Face ID) is still leaving.
        for _ in 0..<3 where !tab.isSelected {
            tab.tap()
            let selected = XCTNSPredicateExpectation(predicate: NSPredicate(format: "isSelected == true"), object: tab)
            _ = XCTWaiter().wait(for: [selected], timeout: 3)
        }
        XCTAssertTrue(tab.isSelected, "tab \(index) not selected")
    }

    /// Swipes up until `element` can be tapped, a few times at most.
    private func scroll(to element: XCUIElement, in app: XCUIApplication) {
        for _ in 0..<6 where !element.isHittable {
            app.swipeUp()
        }
        XCTAssertTrue(element.isHittable, "\(element) not reachable by scrolling")
    }

    private func state(_ app: XCUIApplication) -> XCUIElement {
        app.descendants(matching: .any)["core-state"]
    }

    private func wait(for element: XCUIElement, anyOf values: [String], timeout: TimeInterval) {
        let expectation = XCTNSPredicateExpectation(predicate: NSPredicate(format: "value IN %@", values), object: element)
        XCTAssertEqual(XCTWaiter().wait(for: [expectation], timeout: timeout), .completed,
                       "core-state stayed \(element.value ?? "nil"), not one of \(values)")
    }
}
