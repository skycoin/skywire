import XCTest

/// Fleet on the live network (M4 item 4.3), in the SkywireTour scheme. Needs
/// Fleet on in the app and a visor elsewhere whose config lists this phone's
/// key under `hypervisors` (playbook §5's desktop peer), named to the runner:
///
///   TEST_RUNNER_SKYWIRE_FLEET_PEER=<pk> xcodebuild … -scheme SkywireTour test \
///     -only-testing:SkywireUITests/FleetChecks
@MainActor
final class FleetChecks: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    /// The peer shows in the Fleet list once it has connected in.
    func testFleetListsThePeer() throws {
        guard let peer = ProcessInfo.processInfo.environment["SKYWIRE_FLEET_PEER"] else {
            throw XCTSkip("no peer: set TEST_RUNNER_SKYWIRE_FLEET_PEER")
        }
        let app = connectedApp()
        openTab(.apps, in: app)
        let fleet = app.buttons["hub-fleet"]
        XCTAssertTrue(fleet.waitForExistence(timeout: 10))
        fleet.tap()
        XCTAssertTrue(app.switches["fleet-screen-toggle"].waitForExistence(timeout: 10))
        XCTAssertEqual(app.switches["fleet-screen-toggle"].value as? String, "1", "Fleet is off in the app")
        // Rows name a visor by its short key (ten characters, an ellipsis,
        // the last eight) until the user names it.
        let row = app.descendants(matching: .any).matching(identifier: "fleet-visor")
            .matching(NSPredicate(format: "label CONTAINS %@", String(peer.prefix(10)))).firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 240), "the peer never appeared in Fleet")
        usleep(700_000)
        let attachment = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        attachment.name = "fleet-peer"
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
