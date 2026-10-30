@testable import CoreClient
import XCTest

/// CoreClient over provider messages, against the recorded API: the app's
/// ProviderMessageTransport hands each message straight to the extension's
/// ProviderMessageHandler (what sendProviderMessage does across processes),
/// which makes the request on its loopback to FakeVisor.
final class ProviderMessageTests: XCTestCase {
    private var server: StubServer?

    override func tearDown() async throws {
        server?.stop()
    }

    private func client(_ visor: FakeVisor) async throws -> CoreClient {
        let server = try await visor.start()
        self.server = server
        let handler = ProviderMessageHandler(transport: LoopbackTransport(origin: server.origin))
        let transport = ProviderMessageTransport { await handler.handle($0) }
        return CoreClient(transport: transport) { CoreClientTests.password }
    }

    /// A first run's session (account, login, the cookie it sets), a read, and
    /// mutations carrying CSRF tokens all make the round trip.
    func testTheAPIWorksOverProviderMessages() async throws {
        let visor = FakeVisor(accountPassword: nil)
        let client = try await client(visor)
        try await client.ensureSession()
        let about = try await client.about()
        XCTAssertEqual(about.publicKey, recordedPK)
        let app = try await client.updateApp("skysocks-client", args: "--addr 127.0.0.1:1080 --reconnect")
        XCTAssertEqual(app.name, "skysocks-client")
        XCTAssertEqual(Array(visor.served.prefix(4)), ["user-unauthorized", "user-exists-false", "create-account", "login"])
    }

    /// Bodies are bytes both ways: an attachment up, an attachment down.
    func testBinaryBodiesSurvive() async throws {
        let client = try await client(FakeVisor(accountPassword: CoreClientTests.password))
        let to = try XCTUnwrap(Fixture.named("mail-status").decode(MailStatus.self).myAddress)
        let sent = try await client.mailSend(MailOutgoing(
            to: [to], subject: "Fixture", body: "Hello from the recorder.\n",
            attachments: [MailOutgoingAttachment(name: "note.txt", contentType: "text/plain", data: Data("hello\n".utf8))]
        ))
        XCTAssertEqual(sent.delivered, 1)
        let id = Fixture.named("mail-inbox").decode([MailSummary].self)[0].id
        let bytes = try await client.mailAttachment(MailFolder.inbox, id: id, n: 0)
        XCTAssertEqual(bytes, Data("hello\n".utf8))
    }

    /// No visor behind the extension: the app sees the same URLError a loopback
    /// call would have thrown, so its "core not reachable" handling holds.
    func testAConnectionFailureKeepsItsCode() async throws {
        let handler = ProviderMessageHandler(transport: LoopbackTransport(origin: URL(string: "http://127.0.0.1:9")!))
        let transport = ProviderMessageTransport { await handler.handle($0) }
        do {
            _ = try await transport.send(HTTPRequest(path: "/api/ping", timeout: 3))
            XCTFail("a refused connection answered")
        } catch let error as URLError {
            XCTAssertEqual(error.code, .cannotConnectToHost)
        }
    }

    /// One message carries one whole response: the notification stream cannot.
    func testStreamsAreRefusedNotHung() async throws {
        let transport = ProviderMessageTransport { _ in XCTFail("nothing should be sent"); return Data() }
        do {
            _ = try await transport.stream(HTTPRequest(path: "/api/notifications/stream", timeout: nil))
            XCTFail("a stream was attempted")
        } catch let error as ProviderMessageError {
            XCTAssertEqual(error, .streamingUnsupported)
        }
    }

    func testTheExtensionRefusesAnotherVersion() async throws {
        let handler = ProviderMessageHandler(transport: LoopbackTransport(origin: URL(string: "http://127.0.0.1:9")!))
        let reply = await handler.handle(Data(#"{"v":2,"method":"GET","path":"/","headers":{}}"#.utf8))
        let transport = ProviderMessageTransport { _ in reply }
        do {
            _ = try await transport.send(HTTPRequest(path: "/"))
            XCTFail("a refused message answered")
        } catch let ProviderMessageError.refused(reason) {
            XCTAssertTrue(reason.contains("version 2"), reason)
        }
    }

    func testAnUnreadableReplyIsAnError() async throws {
        let transport = ProviderMessageTransport { _ in Data("not json".utf8) }
        do {
            _ = try await transport.send(HTTPRequest(path: "/"))
            XCTFail("garbage decoded")
        } catch let error as ProviderMessageError {
            XCTAssertEqual(error, .malformedReply)
        }
    }
}
