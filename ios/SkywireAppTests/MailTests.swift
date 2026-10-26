@testable import Skywire
import XCTest

/// The mail screen's own rules (Android: MailSizeTest and friends).
@MainActor
final class MailTests: XCTestCase {
    /// The visor writes an attachment as MIME base64 in 76-column lines and ends the last one
    /// with CRLF too, which Foundation's encoder leaves off.
    private func visorWrites(_ count: Int) -> Int64 {
        Int64(Data(count: count).base64EncodedString(options: [.lineLength76Characters, .endLineWithCarriageReturn, .endLineWithLineFeed]).utf8.count) + 2
    }

    func testAttachmentWireSizeMatchesTheMimeEncoding() {
        for count in [0, 1, 2, 3, 56, 57, 58, 114, 300 * 1024, 770_000, 1 << 20] {
            XCTAssertEqual(MailModel.attachmentWireSize(Int64(count)), visorWrites(count), "count \(count)")
        }
    }

    /// Both sides of the warning in whole KB: in MB a mail just over the limit and the limit read the same.
    func testKilobytesRoundUp() {
        let saved = L10n.locale
        defer { L10n.locale = saved }
        L10n.locale = Locale(identifier: "en")
        XCTAssertEqual(MailModel.kilobytes(1), "1 KB")
        XCTAssertEqual(MailModel.kilobytes(1024), "1 KB")
        XCTAssertEqual(MailModel.kilobytes(1025), "2 KB")
        XCTAssertEqual(MailModel.kilobytes(10 << 20), "10,240 KB")
    }
}
