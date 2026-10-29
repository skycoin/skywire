import CoreClient
@testable import Skywire
import XCTest

/// SkyMail's side of the visor's contact book (Android: MailContactsTest, the same cases).
final class MailContactsTests: XCTestCase {
    private let pcLabel = "akn3cloqt2fcnaq42wbiy5ye7uymhyogbmyuvegosv7xj3q4ynf5e"
    private let phoneLabel = "anyq2ho3fh467piyhz5ed33pndpspexznab2gckoulqs5h4hr2xse"
    private var pc: String { SkymailAddress.pk(ofLabel: pcLabel)! }
    private var phone: String { SkymailAddress.pk(ofLabel: phoneLabel)! }
    private var dir: URL!

    override func setUpWithError() throws {
        dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: dir)
    }

    func testOneAddressPerMailboxFromMailAndKeys() {
        let book = [
            BookContact(uid: "a", name: "Alice", keys: [pc, phone], mail: ["bob@\(pcLabel).skynet"]),
            BookContact(uid: "b", name: "Bank", mail: ["bank@example.com"]),
            BookContact(uid: "c", name: "Coins", payments: [BookPayment(coin: "skycoin", address: "2jBbGx")]),
        ]
        let list = MailContacts.of(book)
        XCTAssertEqual(list.map(\.uid), ["a"])
        XCTAssertEqual(list[0].addresses, ["bob@\(pcLabel).skynet", "mail@\(phoneLabel).dmsg"])
    }

    func testANewNameForAKnownMailboxRenamesThatContact() {
        let pay = [BookPayment(coin: "skycoin", address: "2jBbGx")]
        let book = [BookContact(uid: "a", name: "PC", keys: [pc], payments: pay, notes: "desk")]
        XCTAssertEqual(
            MailContacts.saved(book, name: " Desktop ", address: "mail@\(pcLabel).dmsg"),
            BookContact(uid: "a", name: "Desktop", keys: [pc], mail: ["mail@\(pcLabel).dmsg"], payments: pay, notes: "desk")
        )
        XCTAssertEqual(
            MailContacts.saved(book, name: "Phone", address: "mail@\(phoneLabel).dmsg"),
            BookContact(name: "Phone", keys: [phone], mail: ["mail@\(phoneLabel).dmsg"])
        )
    }

    func testEditingCanChangeTheAddress() {
        let book = [BookContact(uid: "a", name: "PC", keys: [pc], mail: ["mail@\(pcLabel).skynet"])]
        XCTAssertEqual(
            MailContacts.saved(book, name: "Phone", address: "mail@\(phoneLabel).dmsg", uid: "a", replacing: "mail@\(pcLabel).skynet"),
            BookContact(uid: "a", name: "Phone", keys: [phone], mail: ["mail@\(phoneLabel).dmsg"])
        )
    }

    func testImportSkipsMailboxesAlreadyInTheBook() throws {
        let file = dir.appendingPathComponent(MailContacts.legacyFileName)
        try Data((#"[{"name":"PC","address":"mail@\#(pcLabel).dmsg"},{"name":"Phone","address":"mail@\#(phoneLabel).dmsg"},"# +
            #"{"name":"Phone again","address":"x@\#(phoneLabel).skynet"},{"name":"Typo","address":"mail@nope.dmsg"}]"#).utf8).write(to: file)
        let book = [BookContact(uid: "a", name: "Desktop", keys: [pc])]
        XCTAssertEqual(MailContacts.imports(file, book), [BookContact(name: "Phone", keys: [phone], mail: ["mail@\(phoneLabel).dmsg"])])
        try Data("not json".utf8).write(to: file)
        XCTAssertEqual(MailContacts.imports(file, book), [])
    }
}
