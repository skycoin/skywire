@testable import WalletCore
import XCTest

/// Port of SkycoinCreateManualHoursTest.kt. Manual hours selection: the
/// destination receives exactly the hours the caller named, the half share
/// and the full-share retry stay out of it, and every hour that burns is in
/// the fee the review screen shows.
final class SkycoinCreateManualHoursTests: XCTestCase {

    // Real addresses from the golden fixtures; pushOutput checks the checksum.
    private let holder = "cfb24jWcms9pZtvEx3UjUuLPdT6rbx6r9M"
    private let dest = "nJUPS5CrLV2iBdMvdMzMiP7iA2dzumaWGc"
    private let change = "UxnG3PoFvKtRuLu5nyFdb6eu6pu1SZadkR"

    private func ux(_ coins: UInt64, _ hours: UInt64, id: Int = 0) -> SkycoinCreate.UxBalance {
        SkycoinCreate.UxBalance(
            hash: (0..<32).map { UInt8(truncatingIfNeeded: $0 + id) },
            hashHex: "00",
            bkSeq: 1,
            address: holder,
            coins: coins,
            initialHours: 0,
            hours: hours
        )
    }

    func testManualHoursGoExactlyToTheDestination() throws {
        let created = try SkycoinCreate.create(
            unspents: [ux(10_000_000, 100)],
            to: [SkycoinCreate.Destination(address: dest, coins: 4_000_000, hours: 30)],
            changeAddress: change,
            burnFactor: 2
        )
        XCTAssertEqual(50, created.feeHours) // ceil(100 / 2)
        XCTAssertEqual(30, created.hoursToDestinations)
        XCTAssertEqual(20, created.changeHours) // 100 - 50 - 30
        XCTAssertEqual(6_000_000, created.changeCoins)
    }

    func testHoursBeyondWhatTheInputsHoldAreRefused() {
        // 100 input hours burn 50, so 60 to the destination cannot be met.
        XCTAssertThrowsError(try SkycoinCreate.create(
            unspents: [ux(10_000_000, 100)],
            to: [SkycoinCreate.Destination(address: dest, coins: 1_000_000, hours: 60)],
            changeAddress: change,
            burnFactor: 2
        )) { XCTAssertEqual(SkycoinCreate.CreateError.insufficientHours, $0 as? SkycoinCreate.CreateError) }
    }

    func testZeroManualHoursSendsCoinsWithoutHours() throws {
        let created = try SkycoinCreate.create(
            unspents: [ux(10_000_000, 100)],
            to: [SkycoinCreate.Destination(address: dest, coins: 4_000_000, hours: 0)],
            changeAddress: change,
            burnFactor: 2
        )
        XCTAssertEqual(0, created.hoursToDestinations)
        XCTAssertEqual(50, created.changeHours)
    }

    func testHoursWithNoChangeToCarryThemShowAsBurned() throws {
        // Everything leaves as coins, 1 hour goes along, and the other 999 burn.
        let created = try SkycoinCreate.create(
            unspents: [ux(10_000_000, 1000)],
            to: [SkycoinCreate.Destination(address: dest, coins: 10_000_000, hours: 1)],
            changeAddress: change,
            burnFactor: 10
        )
        XCTAssertEqual(999, created.feeHours)
        XCTAssertEqual(0, created.changeCoins)
        XCTAssertEqual(0, created.changeHours)
    }

    func testAnotherOutputRescuesTheHoursAsInGo() throws {
        // The extra input costs 1 hour of fee and carries 899 change hours home.
        let created = try SkycoinCreate.create(
            unspents: [ux(10_000_000, 1000, id: 0), ux(1_000_000, 10, id: 1)],
            to: [SkycoinCreate.Destination(address: dest, coins: 10_000_000, hours: 1)],
            changeAddress: change,
            burnFactor: 10
        )
        XCTAssertEqual(2, created.spends.count)
        XCTAssertEqual(101, created.feeHours) // ceil(1010 / 10)
        XCTAssertEqual(1_000_000, created.changeCoins)
        XCTAssertEqual(908, created.changeHours) // 1010 - 101 - 1
    }
}
