import XCTest

/// The footprint table (playbook 8.2), run by ios/scripts/footprint-table.py,
/// which samples the app with the Mac's footprint(1) while each test holds
/// one state. Every test launches the app afresh, so a state carries nothing
/// from the one before. The state on screen goes into the file
/// SKYWIRE_FOOTPRINT_STATE names (a path on the Mac: the Simulator shares its
/// file system), "-" between holds; the script samples only inside a hold.
@MainActor
final class FootprintTour: XCTestCase {
    private var stateFile = ""
    private var hold: TimeInterval = 240

    override func setUpWithError() throws {
        continueAfterFailure = false
        let env = ProcessInfo.processInfo.environment
        guard let file = env["SKYWIRE_FOOTPRINT_STATE"] else {
            throw XCTSkip("run by ios/scripts/footprint-table.py")
        }
        stateFile = file
        hold = env["SKYWIRE_FOOTPRINT_HOLD"].flatMap(TimeInterval.init) ?? hold
    }

    /// The app up, the core never started.
    func test1Idle() {
        let app = XCUIApplication()
        app.launchArguments = ["-core.wantsConnected", "NO"]
        app.launch()
        XCTAssertTrue(state(app).waitForExistence(timeout: 15))
        XCTAssertEqual(state(app).value as? String, "stopped")
        keep("idle", for: hold)
    }

    /// Connected on Home, for the 10 minutes G1 measured, so the two compare.
    func test2Connected() {
        _ = connectedApp()
        keep("connected", for: max(hold, 600))
    }

    func test3ChatOpen() {
        let app = connectedApp()
        openTab(.chat, in: app)
        XCTAssertTrue(app.buttons["chat-row-saved"].waitForExistence(timeout: 90), "the chat list never came up")
        keep("chat", for: hold)
    }

    /// A public proxy connected; the script sends requests through it while this holds.
    func test4SocksActive() {
        let app = connectedApp()
        openSocks(app)
        connectFromTheList(app)
        keep("socks", for: hold)
    }

    /// skydex-client started on a market: the trading screens with
    /// SKYWIRE_DEX_MARKET, else the screen with the dial's failure (secp256k1's
    /// generator point, where no market runs).
    func test5DexOpen() {
        let market = ProcessInfo.processInfo.environment["SKYWIRE_DEX_MARKET"]
        let app = connectedApp()
        openTab(.apps, in: app)
        let dex = app.buttons["hub-dex"]
        XCTAssertTrue(dex.waitForExistence(timeout: 10))
        dex.tap()
        let field = app.textFields["dex-market-field"]
        XCTAssertTrue(field.waitForExistence(timeout: 10))
        field.tap()
        if let current = field.value as? String, !current.isEmpty, current != field.placeholderValue {
            field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: current.count + 2))
        }
        field.typeText(market ?? "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798")
        app.buttons["dex-connect"].tap()
        if market != nil {
            XCTAssertTrue(app.buttons["dex-disconnect"].waitForExistence(timeout: 150), "no trading screens")
            keep("dex", for: hold)
        } else {
            XCTAssertTrue(app.staticTexts["dex-error"].waitForExistence(timeout: 150), "no answer from the dial")
            keep("dex-no-market", for: hold)
        }
    }

    /// The script asks its desktop peer to call this phone once the state
    /// reads "call-ring"; this answers on the call screen and holds the call.
    func test6InACall() throws {
        guard ProcessInfo.processInfo.environment["SKYWIRE_FOOTPRINT_CALL"] == "1" else {
            throw XCTSkip("no call peer: footprint-table.py --call-peer")
        }
        let app = connectedApp()
        mark("call-ring")
        let answer = app.buttons["call-answer"]
        // A new identity's first dmsg session to the peer can take minutes.
        XCTAssertTrue(answer.waitForExistence(timeout: 300), "the peer's call never rang")
        answer.tap()
        XCTAssertTrue(app.descendants(matching: .any)["call-elapsed"].waitForExistence(timeout: 60), "the call did not connect")
        keep("call", for: hold)
        app.buttons["call-hang-up"].tap()
    }

    private func keep(_ name: String, for seconds: TimeInterval) {
        mark(name)
        snap(name)
        sleep(UInt32(seconds))
        mark("-")
    }

    private func mark(_ name: String) {
        XCTAssertNoThrow(try Data((name + "\n").utf8).write(to: URL(fileURLWithPath: stateFile)))
    }

    private func snap(_ name: String) {
        usleep(700_000)
        let attachment = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
