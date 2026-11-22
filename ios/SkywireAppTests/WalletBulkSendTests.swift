@testable import Skywire
import XCTest

/// Bulk send text: one `address,amount[,hours]` per line, as skycoin-web takes it (Android: WalletBulkSendTest).
final class WalletBulkSendTests: XCTestCase {
    func testLinesBecomeDestinationsAndBlankLinesAreSkipped() {
        XCTAssertEqual(parseBulk(" a1, 1.5 \n\n a2,2\r\n"),
                       .ok([DestDraft(address: "a1", amountText: "1.5"), DestDraft(address: "a2", amountText: "2")]))
    }

    func testHoursOnEveryLine() {
        guard case let .ok(destinations) = parseBulk("a1,1,10\na2,2,20") else { return XCTFail("not read") }
        XCTAssertEqual(destinations.map(\.hoursText), ["10", "20"])
    }

    func testErrorsNameTheLine() {
        XCTAssertEqual(parseBulk(" \n "), .empty)
        XCTAssertEqual(parseBulk("a1,1\na2"), .badLine(2))
        XCTAssertEqual(parseBulk("a1,,1"), .badLine(1))
        // The blank line still counts, so the number matches what the user sees.
        XCTAssertEqual(parseBulk("a1,1,5\n\na2,2"), .mixedHours(3))
        XCTAssertEqual(parseBulk("a1,1,5\r\n\r\na2,2"), .mixedHours(3))
    }
}
