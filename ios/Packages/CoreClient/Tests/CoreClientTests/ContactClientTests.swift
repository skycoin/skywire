@testable import CoreClient
import XCTest

/// The contact book routes, with the bodies a desktop visor answered
/// (pkg/visor/hypervisor_handlers_contacts.go).
final class ContactClientTests: XCTestCase {
    private var visor: FakeVisor!
    private var server: StubServer!
    private var client: CoreClient!

    private static let stored = #"{"uid":"b9b0956b-e3b1-430f-88ae-91b8413056e5","name":"Ann","keys":["022a7a031e369a1c9c64f9e0a806b22c4141904cc6d8362c96c97b964e63e19d59"],"mail":["mail@example.org"]}"#
    private var path: String { "/api/visors/\(recordedPK)/contacts" }

    override func setUp() async throws {
        visor = FakeVisor(accountPassword: CoreClientTests.password)
        server = try await visor.start()
        client = CoreClient(transport: LoopbackTransport(origin: server.origin)) { CoreClientTests.password }
    }

    override func tearDown() async throws {
        server?.stop()
    }

    private func reply(_ status: Int, _ body: String) -> StubServer.Reply {
        .response(status: status, headers: ["Content-Type": "application/json"], body: Data(body.utf8))
    }

    func testListsTheBook() async throws {
        visor.queue("GET", path, reply(200, "[\(Self.stored)]"))
        let book = try await client.contacts()
        XCTAssertEqual(book, [BookContact(uid: "b9b0956b-e3b1-430f-88ae-91b8413056e5", name: "Ann", keys: ["022a7a031e369a1c9c64f9e0a806b22c4141904cc6d8362c96c97b964e63e19d59"], mail: ["mail@example.org"])])
    }

    func testAnEmptyBookIsNoContacts() async throws {
        visor.queue("GET", path, reply(200, "[]"))
        let book = try await client.contacts()
        XCTAssertTrue(book.isEmpty)
    }

    /// A new contact goes without a uid and the empty lists Go would drop.
    func testPutSendsTheContactAndReadsItBack() async throws {
        visor.queue("POST", path, reply(200, Self.stored))
        let saved = try await client.contactPut(BookContact(name: "Ann", keys: ["022a7a031e369a1c9c64f9e0a806b22c4141904cc6d8362c96c97b964e63e19d59"], mail: ["mail@example.org"]))
        XCTAssertEqual(saved.uid, "b9b0956b-e3b1-430f-88ae-91b8413056e5")
        let body = try XCTUnwrap(server.requests.last { $0.method == "POST" && $0.path == path }?.jsonObject)
        XCTAssertEqual(body.keys.sorted(), ["keys", "mail", "name", "uid"])
        XCTAssertEqual(body["uid"] as? String, "")
    }

    /// A malformed contact is refused the same way every time, so it says so.
    func testAMalformedContactIsRejected() async throws {
        visor.queue("POST", path, reply(400, #"{"error":"malformed contact: Invalid public key"}"#))
        do {
            _ = try await client.contactPut(BookContact(name: "Bad", keys: ["zz"]))
            XCTFail("a malformed contact was taken")
        } catch let rejected as ContactRejected {
            XCTAssertEqual(rejected.message, "malformed contact: Invalid public key")
        }
    }

    func testDeletingAGoneContactIsNoError() async throws {
        visor.queue("DELETE", "\(path)/gone", reply(404, #"{"error":"contact not found"}"#))
        try await client.contactDelete("gone")
        visor.queue("DELETE", "\(path)/b9b0956b", reply(200, "true"))
        try await client.contactDelete("b9b0956b")
        visor.queue("DELETE", "\(path)/x", reply(500, #"{"error":"disk full"}"#))
        do {
            try await client.contactDelete("x")
            XCTFail("a failed delete passed")
        } catch let CoreClientError.http(method, _, status, _) {
            XCTAssertEqual(method, "DELETE")
            XCTAssertEqual(status, 500)
        }
    }
}
