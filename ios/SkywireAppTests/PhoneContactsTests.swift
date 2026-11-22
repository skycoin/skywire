import CoreClient
@testable import Skywire
import XCTest

/// What a copied contact holds (Android: PhoneContactsTest). iOS has no custom rows, so a key
/// and a Skymail address are links that open Skywire.
final class PhoneContactsTests: XCTestCase {
    private let pcLabel = "akn3cloqt2fcnaq42wbiy5ye7uymhyogbmyuvegosv7xj3q4ynf5e"
    private var pc: String { SkymailAddress.pk(ofLabel: pcLabel)! }

    func testSkymailIsNeverAnEmailRow() {
        let c = BookContact(
            uid: "a", name: "PC", keys: [pc.uppercased()], mail: ["me@\(pcLabel).skynet", "pc@example.com"],
            payments: [BookPayment(coin: "skycoin", address: "2jBbGx")], notes: "desk"
        )
        XCTAssertEqual(PhoneContacts.rows(c), PhoneContacts.Rows(
            name: "PC",
            urls: ["skychat://\(pc)", "skymail:me@\(pcLabel).skynet"],
            emails: ["pc@example.com"]
        ))
    }

    /// Only what is written decides whether an entry is rewritten.
    func testRowsFollowWhatIsWritten() {
        let c = BookContact(uid: "a", name: "PC", keys: [pc])
        var grouped = c
        grouped.groups = ["work"]
        XCTAssertEqual(PhoneContacts.rows(c), PhoneContacts.rows(grouped))
        var renamed = c
        renamed.name = "Desk"
        XCTAssertNotEqual(PhoneContacts.rows(c), PhoneContacts.rows(renamed))
    }

    /// The rows' links come back to the routers that claim them.
    @MainActor
    func testTheLinksOpenSkywire() throws {
        let chat = try XCTUnwrap(URL(string: PhoneContacts.chatLink(pc)))
        XCTAssertTrue(ChatRouter.shared.offer(chat))
        if let request = ChatRouter.shared.pending { ChatRouter.shared.handled(request) }

        let mail = try XCTUnwrap(URL(string: PhoneContacts.mailLink("me@\(pcLabel).skynet")))
        XCTAssertTrue(MailRouter.shared.offer(mail))
        let link = try XCTUnwrap(MailRouter.shared.pending)
        XCTAssertEqual(link.to, "me@\(pcLabel).skynet")
        MailRouter.shared.handled(link)
        XCTAssertFalse(MailRouter.shared.offer(try XCTUnwrap(URL(string: "skymail:nobody@example"))))
    }
}
