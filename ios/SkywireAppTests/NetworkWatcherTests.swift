import CoreClient
@testable import Skywire
import XCTest

/// What the network watcher tells the core as paths come and go (Android: NetworkWatcher),
/// with the paths given by the test and a stand-in for the core's POST /api/network.
@MainActor
final class NetworkWatcherTests: XCTestCase {
    private var sent: [NetworkEvent] = []
    private var failing = 0
    private let wifi = NetworkPath(interface: "en0", metered: false, addresses: ["192.168.1.20"])
    private let cellular = NetworkPath(interface: "pdp_ip0", metered: true, addresses: ["10.20.30.40"])

    private func watcher() -> NetworkWatcher {
        let timing = NetworkWatcher.Timing(settle: .milliseconds(50), retry: .milliseconds(20), pendingRetry: .milliseconds(150))
        let watcher = NetworkWatcher(timing: timing) { [unowned self] event in
            if failing > 0 {
                failing -= 1
                throw URLError(.cannotConnectToHost)
            }
            sent.append(event)
            return try JSONDecoder().decode(NetworkReply.self, from: Data(#"{"mode":"normal","metered":false,"moves":0}"#.utf8))
        }
        watcher.start(monitoring: false)
        return watcher
    }

    private func waitForReports(_ count: Int, file: StaticString = #filePath, line: UInt = #line) async {
        let deadline = ContinuousClock.now + .seconds(3)
        while sent.count < count, ContinuousClock.now < deadline {
            try? await Task.sleep(for: .milliseconds(10))
        }
        XCTAssertEqual(sent.count, count, file: file, line: line)
    }

    func testTheNetworkTheCoreStartedOnIsReportedOnceAsChanged() async throws {
        let watcher = watcher()
        watcher.note(wifi)
        watcher.note(wifi)
        await waitForReports(1)
        try await Task.sleep(for: .milliseconds(200))
        XCTAssertEqual(sent, [NetworkEvent(event: "changed", metered: false, ifname: "en0")])
    }

    func testSwitchingToCellularIsOneMoveForTheWholeBurst() async throws {
        let watcher = watcher()
        watcher.note(wifi)
        await waitForReports(1)
        watcher.note(cellular)
        watcher.note(NetworkPath(interface: "pdp_ip0", metered: true, addresses: ["10.20.30.40", "2001:db8::40"]))
        await waitForReports(2)
        try await Task.sleep(for: .milliseconds(200))
        XCTAssertEqual(sent.last, NetworkEvent(event: "moved", metered: true, ifname: "pdp_ip0"))
        XCTAssertEqual(sent.count, 2)
    }

    func testLowDataModeAloneIsReportedAsChanged() async {
        let watcher = watcher()
        watcher.note(wifi)
        await waitForReports(1)
        watcher.note(NetworkPath(interface: "en0", metered: true, addresses: wifi.addresses))
        await waitForReports(2)
        XCTAssertEqual(sent.last, NetworkEvent(event: "changed", metered: true, ifname: "en0"))
    }

    func testComingBackAfterNoNetworkIsAMove() async {
        let watcher = watcher()
        watcher.note(wifi)
        await waitForReports(1)
        watcher.note(NetworkPath(interface: nil, metered: false, addresses: []))
        watcher.note(wifi)
        await waitForReports(2)
        XCTAssertEqual(sent.last?.event, "moved")
    }

    func testAMoveTheCoreMissedIsOfferedAgain() async {
        let watcher = watcher()
        watcher.note(wifi)
        await waitForReports(1)
        failing = 2
        watcher.note(cellular)
        await waitForReports(2)
        XCTAssertEqual(failing, 0, "both attempts were not made")
        XCTAssertEqual(sent.last, NetworkEvent(event: "moved", metered: true, ifname: "pdp_ip0"))
    }

    func testStopDropsAReportNotYetSent() async throws {
        let watcher = watcher()
        watcher.note(wifi)
        watcher.stop()
        try await Task.sleep(for: .milliseconds(200))
        XCTAssertEqual(sent, [])
    }
}
