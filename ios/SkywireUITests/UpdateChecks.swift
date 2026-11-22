import XCTest

/// The one-time update banner, with a stand-in for the store, in the SkywireTour scheme. A
/// Simulator build is no store install, so debug launch arguments say which store it came from
/// and where that store answers. Serve the two answers first:
///
///   mkdir -p /tmp/upd && cd /tmp/upd
///   echo '[{"tag_name":"mobile-v0.0.10","draft":false,"prerelease":false}]' > releases
///   echo '{"resultCount":1,"results":[{"version":"0.0.10","trackViewUrl":"https://apps.apple.com/app/id1"}]}' > lookup
///   python3 -m http.server 18777 --bind 127.0.0.1
@MainActor
final class UpdateChecks: XCTestCase {
    static let stub = "http://127.0.0.1:18777"

    override func setUp() {
        continueAfterFailure = false
    }

    private func launch(_ source: String, seen: String? = "none") -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = MailChecks.english + [
            "-UpdateNoticeSource", source,
            "-UpdateNoticeInstalled", "0.0.9",
            "-UpdateNoticeReleases", "\(Self.stub)/releases",
            "-UpdateNoticeLookup", "\(Self.stub)/lookup",
        ] + (seen.map { ["-updates_notice_seen", $0] } ?? [])
        app.launch()
        return app
    }

    /// Later hides it, and the version it named is not offered again.
    func testLaterIsOnceForAVersion() {
        let app = launch("testflight")
        let banner = app.descendants(matching: .any)["update-banner"]
        XCTAssertTrue(banner.waitForExistence(timeout: 20), "no banner for a newer release")
        XCTAssertTrue(app.staticTexts["Skywire 0.0.10 is available on TestFlight. You have 0.0.9."].exists, "the banner names the wrong release")
        snap("update-banner")
        app.buttons["update-later"].tap()
        let gone = XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: banner)
        XCTAssertEqual(XCTWaiter().wait(for: [gone], timeout: 5), .completed, "Later left the banner up")

        app.terminate()
        let again = launch("testflight", seen: nil)
        XCTAssertFalse(again.descendants(matching: .any)["update-banner"].waitForExistence(timeout: 10), "the answered version came back")
    }

    /// Update hands the store's page to the system and leaves the app.
    func testUpdateOpensTheStore() {
        let app = launch("appstore")
        let banner = app.descendants(matching: .any)["update-banner"]
        XCTAssertTrue(banner.waitForExistence(timeout: 20), "no banner for a newer App Store version")
        XCTAssertTrue(app.staticTexts["Skywire 0.0.10 is available on App Store. You have 0.0.9."].exists, "the banner names the wrong store")
        app.buttons["update-now"].tap()
        XCTAssertTrue(app.wait(for: .runningBackground, timeout: 10), "Update opened nothing")
        app.activate()
        XCTAssertFalse(banner.exists, "Update left the banner up")
    }

    private func snap(_ name: String) {
        usleep(700_000)
        let attachment = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
