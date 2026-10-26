@testable import CoreClient
import XCTest

/// The mail routes against what a real lite core answered
/// (ios/scripts/record-api-fixtures.sh mails itself, so every route has a recording).
final class MailClientTests: XCTestCase {
    private var visor: FakeVisor!
    private var server: StubServer!
    private var client: CoreClient!

    override func setUp() async throws {
        visor = FakeVisor(accountPassword: CoreClientTests.password)
        server = try await visor.start()
        client = CoreClient(transport: LoopbackTransport(origin: server.origin)) { CoreClientTests.password }
    }

    override func tearDown() async throws {
        server?.stop()
    }

    /// The id of the one message the recording delivered.
    private var recordedID: String {
        Fixture.named("mail-inbox").decode([MailSummary].self)[0].id
    }

    func testStatus() async throws {
        let status = try await client.mailStatus()
        XCTAssertTrue(status.enabled)
        XCTAssertTrue(status.running)
        XCTAssertEqual(status.limits.maxMessageSize, 1 << 20)
        XCTAssertEqual(status.limits.maxAgeDays, 7)
        XCTAssertTrue(status.addressDmsg.hasSuffix(SkymailAddress.dmsg))
        // The address names this visor's key, in both forms.
        XCTAssertEqual(SkymailAddress.pk(ofAddress: status.addressDmsg), recordedPK)
        XCTAssertEqual(SkymailAddress.pk(ofAddress: status.address), recordedPK)
        XCTAssertEqual(status.myAddress, status.addressDmsg)
    }

    /// Off, the visor says why and lists no usage.
    func testStatusWhileOff() async throws {
        visor.serveVariant("mail-status-off")
        let status = try await client.mailStatus()
        XCTAssertFalse(status.enabled)
        XCTAssertFalse(status.running)
        XCTAssertEqual(status.reason, "disabled")
        XCTAssertEqual(status.myAddress, nil)
    }

    func testListsAndReadsTheMessage() async throws {
        let inbox = try await client.mailList(MailFolder.inbox)
        XCTAssertEqual(inbox.count, 1)
        XCTAssertEqual(inbox[0].subject, "Fixture")
        XCTAssertTrue(inbox[0].fromVerified)
        XCTAssertEqual(inbox[0].peerPK, recordedPK)
        XCTAssertTrue(inbox[0].date.hasPrefix("20"))
        let sent = try await client.mailList(MailFolder.sent)
        XCTAssertEqual(sent.map(\.subject), ["Fixture"])

        let message = try await client.mailRead(MailFolder.inbox, id: recordedID)
        XCTAssertEqual(message.subject, "Fixture")
        XCTAssertTrue(message.text.hasPrefix("Hello from the recorder."))
        XCTAssertTrue(message.fromVerified)
        XCTAssertFalse(message.messageID.isEmpty)
        XCTAssertEqual(message.attachments.map(\.name), ["note.txt"])
        XCTAssertEqual(message.attachments.first?.size, 6)

        let bytes = try await client.mailAttachment(MailFolder.inbox, id: recordedID, n: 0)
        XCTAssertEqual(String(decoding: bytes, as: UTF8.self), "hello\n")
    }

    /// Delivered to this visor's own key: "local", per recipient.
    func testSendSaysHowEachRecipientWent() async throws {
        let to = try XCTUnwrap(Fixture.named("mail-status").decode(MailStatus.self).myAddress)
        let result = try await client.mailSend(MailOutgoing(
            to: [to], subject: "Fixture", body: "Hello from the recorder.\n",
            attachments: [MailOutgoingAttachment(name: "note.txt", contentType: "text/plain", data: Data("hello\n".utf8))]
        ))
        XCTAssertEqual(result.delivered, 1)
        XCTAssertEqual(result.recipients.map(\.via), ["local"])
        XCTAssertTrue(result.error.isEmpty)

        // The attachment travels as Go's []byte: standard base64.
        let body = try XCTUnwrap(server.requests.last { $0.method == "POST" }?.jsonObject)
        let attachment = try XCTUnwrap((body["attachments"] as? [[String: Any]])?.first)
        XCTAssertEqual(attachment["data"] as? String, "aGVsbG8K")
        XCTAssertEqual(attachment["content_type"] as? String, "text/plain")
        XCTAssertEqual(body["to"] as? [String], [to])
    }

    /// A send waits as long as the visor may take to deliver: five minutes, not the
    /// usual request budget, because the visor finishes what this side gives up on.
    func testASendWaitsFiveMinutes() async throws {
        let recorder = TimeoutRecorder(LoopbackTransport(origin: server.origin))
        client = CoreClient(transport: recorder) { CoreClientTests.password }
        let to = try XCTUnwrap(Fixture.named("mail-status").decode(MailStatus.self).myAddress)
        _ = try await client.mailSend(MailOutgoing(to: [to], subject: "Fixture", body: "x"))
        let sends = recorder.timeouts.filter { $0.path.hasSuffix("/mail/send") }
        XCTAssertEqual(sends.map(\.timeout), [5 * 60])
    }

    /// Nobody got it (502): a result naming the reason, not a throw.
    func testARefusedSendIsAResult() async throws {
        visor.serveVariant("mail-send-refused")
        let result = try await client.mailSend(MailOutgoing(to: ["bob@example.com"], subject: "Nowhere", body: "x"))
        XCTAssertEqual(result.delivered, 0)
        XCTAssertFalse(result.error.isEmpty)
        XCTAssertEqual(result.recipients.map(\.rcpt), ["bob@example.com"])
        XCTAssertFalse(result.recipients[0].error.isEmpty)
    }

    /// A send failure without a result body is still an error.
    func testASendThatAnswersNothingThrows() async throws {
        let path = "/api/visors/\(recordedPK)/mail/send"
        visor.queue("POST", path, .response(status: 500, headers: [:], body: Data("boom".utf8)))
        do {
            _ = try await client.mailSend(MailOutgoing(to: ["x"]))
            XCTFail("a 500 with no result decoded")
        } catch let CoreClientError.http(_, failedPath, status, _) {
            XCTAssertEqual(failedPath, path)
            XCTAssertEqual(status, 500)
        }
    }

    func testSettingsAndWhitelistSendTheirOneField() async throws {
        try await client.mailSetWhitelist([recordedPK])
        var body = try XCTUnwrap(server.requests.last { $0.method == "PUT" }?.jsonObject)
        XCTAssertEqual(body["pks"] as? [String], [recordedPK])
        try await client.mailSetEnabled(false)
        body = try XCTUnwrap(server.requests.last { $0.method == "PUT" }?.jsonObject)
        XCTAssertEqual(body.keys.sorted(), ["enable"])
        XCTAssertEqual(body["enable"] as? Bool, false)
    }

    func testDelete() async throws {
        try await client.mailDelete(MailFolder.inbox, id: recordedID)
        XCTAssertEqual(visor.served.last, "mail-delete")
    }
}

/// A transport that notes each request's own timeout and passes it on.
private final class TimeoutRecorder: RequestTransport, @unchecked Sendable {
    private let inner: any RequestTransport
    private let lock = NSLock()
    private var recorded: [(path: String, timeout: TimeInterval?)] = []

    init(_ inner: any RequestTransport) { self.inner = inner }

    var timeouts: [(path: String, timeout: TimeInterval?)] { lock.withLock { recorded } }

    func send(_ request: HTTPRequest) async throws -> HTTPResponse {
        lock.withLock { recorded.append((request.path, request.timeout)) }
        return try await inner.send(request)
    }

    func stream(_ request: HTTPRequest) async throws -> HTTPStream { try await inner.stream(request) }

    func upload(_ request: HTTPRequest, producing: @escaping @Sendable (OutputStream) -> Void) async throws -> HTTPStream {
        try await inner.upload(request, producing: producing)
    }
}
