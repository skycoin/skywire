@testable import Skywire
import XCTest

/// When a failed core starts again, as Android's core service restarts an exited visor.
final class CoreRestartsTests: XCTestCase {
    private let t0 = ContinuousClock.now

    func testShortRunsDoubleTheWaitUpToThirtySeconds() {
        var restarts = CoreRestarts()
        restarts.connected(at: t0)
        XCTAssertEqual(restarts.next, 2, "Home names the start after the first")
        var waits: [Duration] = []
        var now = t0
        for _ in 0..<6 {
            now += .seconds(5)
            waits.append(restarts.failed(at: now))
            restarts.starting(at: now)
        }
        XCTAssertEqual(waits, [.seconds(2), .seconds(4), .seconds(8), .seconds(16), .seconds(30), .seconds(30)])
        XCTAssertEqual(restarts.next, 8)
    }

    func testARunOfOverAMinuteStartsTheWaitAgain() {
        var restarts = CoreRestarts()
        restarts.connected(at: t0)
        _ = restarts.failed(at: t0 + .seconds(1))
        restarts.starting(at: t0 + .seconds(3))
        XCTAssertEqual(restarts.failed(at: t0 + .seconds(3) + .seconds(61)), .seconds(1))
    }

    func testAConnectCountsAgain() {
        var restarts = CoreRestarts()
        restarts.connected(at: t0)
        _ = restarts.failed(at: t0 + .seconds(1))
        restarts.starting(at: t0 + .seconds(3))
        restarts.connected(at: t0 + .seconds(10))
        XCTAssertEqual(restarts.next, 2)
        XCTAssertEqual(restarts.failed(at: t0 + .seconds(11)), .seconds(2))
    }
}
