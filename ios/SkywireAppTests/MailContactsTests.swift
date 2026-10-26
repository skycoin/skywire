@testable import Skywire
import XCTest

/// SkyMail's own address book (Android: MailContactsTest).
final class MailContactsTests: XCTestCase {
    private let pcLabel = "akn3cloqt2fcnaq42wbiy5ye7uymhyogbmyuvegosv7xj3q4ynf5e"
    private let phoneLabel = "anyq2ho3fh467piyhz5ed33pndpspexznab2gckoulqs5h4hr2xse"
    private var dir: URL!

    override func setUpWithError() throws {
        dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: dir)
    }

    private func store() -> MailContacts {
        MailContacts(file: dir.appendingPathComponent("local").appendingPathComponent(MailContacts.fileName))
    }

    func testOneEntryPerMailboxWhateverTheNetwork() throws {
        let book = store()
        _ = try book.put(MailContact(name: "PC", address: "mail@\(pcLabel).dmsg"))
        _ = try book.put(MailContact(name: "Phone", address: "mail@\(phoneLabel).dmsg"))
        let list = try book.put(MailContact(name: "Desktop", address: "mail@\(pcLabel).skynet"))
        XCTAssertEqual(list.map(\.name), ["Desktop", "Phone"])
        XCTAssertEqual(store().load(), list)
    }

    func testEditingCanChangeTheAddress() throws {
        let book = store()
        _ = try book.put(MailContact(name: "PC", address: "mail@\(pcLabel).dmsg"))
        let list = try book.put(MailContact(name: "Phone", address: "mail@\(phoneLabel).dmsg"), replacing: "mail@\(pcLabel).dmsg")
        XCTAssertEqual(list, [MailContact(name: "Phone", address: "mail@\(phoneLabel).dmsg")])
    }

    func testRemoveAndUnreadable() throws {
        let book = store()
        _ = try book.put(MailContact(name: "PC", address: "mail@\(pcLabel).dmsg"))
        XCTAssertEqual(try book.remove("MAIL@\(pcLabel).SKYNET"), [])
        try Data("not json".utf8).write(to: book.file)
        XCTAssertEqual(store().load(), [])
    }
}
