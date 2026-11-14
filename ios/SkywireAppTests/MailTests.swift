@testable import Skywire
import XCTest

/// The mail screen's own rules (Android: MailSizeTest and friends).
@MainActor
final class MailTests: XCTestCase {
    /// A tester's 4.9 MB and 2.58 MB files were refused against a 10 MB limit (Android: MailSizeTest).
    func testTheLimitCountsTheFilesNotTheirBase64() {
        let files = [5_138_022, 2_580_000].map { DraftAttachment(name: "f.mp3", mime: "audio/mpeg", bytes: Data(count: $0)) }
        XCTAssertEqual(MailModel.filesSize(files), 7_718_022)
        XCTAssertLessThanOrEqual(MailModel.filesSize(files), MailModel.defaultMaxMessage)
        XCTAssertEqual(MailModel().messageLimit, 10 << 20)
    }

    private let pcLabel = "akn3cloqt2fcnaq42wbiy5ye7uymhyogbmyuvegosv7xj3q4ynf5e"
    private let pc = "029bb12dd09e8a26821cd5828c7704fd30c3e1c60b314a90ce957f74ee1cc34bd2"

    /// Flipping the network moves what is written in To and Cc; a bare key is read over it.
    func testTheNetworkChoiceMovesTheDraft() {
        let model = MailModel()
        model.draft = MailDraft(to: "mail@\(pcLabel).dmsg, bob@example.com", cc: "Bob <b@\(pcLabel).dmsg>")
        model.setViaSkynet(true)
        XCTAssertEqual(model.draft?.to, "mail@\(pcLabel).skynet, bob@example.com")
        XCTAssertEqual(model.draft?.cc, "Bob <b@\(pcLabel).skynet>")
        XCTAssertEqual(MailModel.parseRecipients(pc, suffix: model.draft!.suffix).first?.address, "mail@\(pcLabel).skynet")
        model.setViaSkynet(false)
        XCTAssertEqual(model.draft?.to, "mail@\(pcLabel).dmsg, bob@example.com")
    }

    func testAReplyStartsOnTheNetworkItAnswers() {
        XCTAssertTrue(MailModel.overSkynet("Bob <bob@\(pcLabel).skynet> "))
        XCTAssertTrue(MailModel.overSkynet("bob@\(pcLabel).SKYNET"))
        XCTAssertFalse(MailModel.overSkynet("bob@\(pcLabel).dmsg"))
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
