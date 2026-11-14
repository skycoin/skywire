import CoreClient
@testable import Skywire
import XCTest

/// What counts as metered, and what Home says about it (Android: MobileDataTest.kt).
@MainActor
final class MobileDataTests: XCTestCase {
    private let running = NetworkStanding(coreOn: true, connected: true, metered: true, useMobileData: true)

    func testACoreOnMobileDataSaysSo() {
        XCTAssertTrue(running.onMobileData)
        XCTAssertFalse(running.waitingForWifi)
        var wifi = running
        wifi.metered = false
        XCTAssertFalse(wifi.onMobileData)
    }

    func testWithMobileDataOffTheCoreWaitsForWifi() {
        var off = running
        off.useMobileData = false
        XCTAssertTrue(off.waitingForWifi)
        XCTAssertFalse(off.onMobileData)
        var starting = off
        starting.connected = false
        XCTAssertTrue(starting.waitingForWifi)
        var wifi = off
        wifi.metered = false
        XCTAssertFalse(wifi.waitingForWifi)
        var stopped = off
        stopped.coreOn = false
        XCTAssertFalse(stopped.waitingForWifi)
    }

    func testMobileDataIsOffUntilTheUserAllowsIt() {
        XCTAssertFalse(MobileData.defaultAllowed)
        XCTAssertFalse(NetworkStanding().useMobileData)
        let defaults = UserDefaults(suiteName: "mobile-data-\(UUID().uuidString)")!
        XCTAssertFalse(AppSettings(defaults: defaults).useMobileData)
        XCTAssertFalse(AppSettings(defaults: defaults).profileSettings.useMobileData)
    }

    func testTheQuestionIsAskedOnceWhileHeldOffMobileData() {
        var held = running
        held.useMobileData = false
        XCTAssertTrue(held.offerMobileData)
        var asked = held
        asked.asked = true
        XCTAssertFalse(asked.offerMobileData)
        var wifi = held
        wifi.metered = false
        XCTAssertFalse(wifi.offerMobileData)
        XCTAssertFalse(running.offerMobileData)
        var stopped = held
        stopped.coreOn = false
        XCTAssertFalse(stopped.offerMobileData)
    }

    func testNoInternetIsNotAWaitForWifi() {
        var offline = running
        offline.useMobileData = false
        offline.online = false
        XCTAssertTrue(offline.waitingForInternet)
        XCTAssertFalse(offline.waitingForWifi)
    }

    /// The core starts on mobile data only once allowed, and not at all while no network is known.
    func testAStartWaitsForAnUnmeteredNetwork() {
        XCTAssertTrue(NetworkStanding.startAllowed(metered: false, useMobileData: false))
        XCTAssertFalse(NetworkStanding.startAllowed(metered: true, useMobileData: false))
        XCTAssertFalse(NetworkStanding.startAllowed(metered: nil, useMobileData: false))
        XCTAssertTrue(NetworkStanding.startAllowed(metered: true, useMobileData: true))
        XCTAssertTrue(NetworkStanding.startAllowed(metered: nil, useMobileData: true))
    }

    /// A lost network is offline at once and has no metered answer (#6179's fix to #6170).
    func testALostNetworkIsOfflineWithNoMeteredAnswer() {
        let internet = Internet()
        XCTAssertTrue(internet.online)
        XCTAssertNil(internet.metered)
        internet.note(NetworkPath(interface: "pdp_ip0", metered: true, addresses: ["10.0.0.2"]))
        XCTAssertTrue(internet.online)
        XCTAssertEqual(internet.metered, true)
        internet.note(NetworkPath(interface: nil, metered: true, addresses: []))
        XCTAssertFalse(internet.online)
        XCTAssertNil(internet.metered)
        internet.note(NetworkPath(interface: "en0", metered: false, addresses: ["192.168.1.2"]))
        XCTAssertTrue(internet.online)
        XCTAssertEqual(internet.metered, false)
    }
}
