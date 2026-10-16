@testable import CoreClient
import XCTest

/// Pairs printed by `skywire cli mail` for two real visors, as in Android's SkymailAddressTest.
final class SkymailAddressTests: XCTestCase {
    private let phone = "03710d1ddb29f9efbd183e7a41ef6f68df2792f96803a3094ea2e12e9f878eaf22"
    private let phoneLabel = "anyq2ho3fh467piyhz5ed33pndpspexznab2gckoulqs5h4hr2xse"
    private let pc = "029bb12dd09e8a26821cd5828c7704fd30c3e1c60b314a90ce957f74ee1cc34bd2"
    private let pcLabel = "akn3cloqt2fcnaq42wbiy5ye7uymhyogbmyuvegosv7xj3q4ynf5e"

    func testLabelMatchesTheVisor() {
        XCTAssertEqual(SkymailAddress.label(phone), phoneLabel)
        XCTAssertEqual(SkymailAddress.label(pc.uppercased()), pcLabel)
        XCTAssertEqual(SkymailAddress.of(pc), "mail@\(pcLabel).dmsg")
    }

    func testLabelDecodesBack() {
        XCTAssertEqual(SkymailAddress.pk(ofLabel: phoneLabel), phone)
        XCTAssertEqual(SkymailAddress.pk(ofAddress: "Bob@\(pcLabel.uppercased()).SKYNET"), pc)
        XCTAssertEqual(SkymailAddress.pk(ofAddress: "bob@host.\(pcLabel).skynet"), pc)
    }

    func testRecipientsAcceptKeysAndAddresses() {
        XCTAssertEqual(SkymailAddress.recipient(pc), "mail@\(pcLabel).dmsg")
        XCTAssertEqual(SkymailAddress.recipient("skychat://\(pc)/"), "mail@\(pcLabel).dmsg")
        XCTAssertEqual(SkymailAddress.recipient(" bob@\(pcLabel).skynet "), "bob@\(pcLabel).skynet")
        XCTAssertNil(SkymailAddress.recipient("bob@example.com"))
        XCTAssertNil(SkymailAddress.recipient("not a key"))
        XCTAssertNil(SkymailAddress.label(String(pc.dropLast(2))))
    }

    func testNotHexIsNotAKey() {
        XCTAssertNil(SkymailAddress.label("+" + String(pc.dropFirst())))
        XCTAssertNil(SkymailAddress.pk(ofLabel: String(phoneLabel.dropLast()) + "1"))
    }

    /// Settings' allow-list entry: a key, a link or an address all name the key.
    func testInputNamesTheKey() {
        XCTAssertEqual(SkymailAddress.pk(ofInput: pc.uppercased()), pc)
        XCTAssertEqual(SkymailAddress.pk(ofInput: "skychat://\(pc)/"), pc)
        XCTAssertEqual(SkymailAddress.pk(ofInput: "anyone@\(pcLabel).dmsg"), pc)
        XCTAssertNil(SkymailAddress.pk(ofInput: "bob@example.com"))
    }
}
