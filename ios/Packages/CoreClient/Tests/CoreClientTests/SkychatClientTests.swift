@testable import CoreClient
import XCTest

/// SkychatClient against a stub that enforces skychat's gate the way
/// cmd/apps/skychat/commands/auth.go does: Basic auth, password only,
/// `WWW-Authenticate: Basic realm="skychat"` on a 401.
final class SkychatClientTests: XCTestCase {
    private var server: StubServer?
    private let secret = "s3cret:with:colons"

    override func tearDown() async throws {
        server?.stop()
    }

    private func client(password: String? = nil, routes: [String: String] = [:]) async throws -> SkychatClient {
        let expected = SkychatClient.authorization(password: secret)
        let server = try await StubServer.start { request in
            guard request.header("Authorization") == expected else {
                return .response(
                    status: 401,
                    headers: ["WWW-Authenticate": "Basic realm=\"skychat\""],
                    body: Data("skychat: authentication required\n".utf8)
                )
            }
            guard let body = routes[request.path] else {
                return .response(status: 404, headers: [:], body: Data())
            }
            return .response(status: 200, headers: ["Content-Type": "application/json"], body: Data(body.utf8))
        }
        self.server = server
        let password = password ?? secret
        return SkychatClient(transport: LoopbackTransport(origin: server.origin)) { password }
    }

    /// The header is RFC 7617's: base64 of "user:password", split at the
    /// first colon by the server, so a password with colons survives.
    func testAuthorizationIsBasicWithTheProfileUser() throws {
        let value = SkychatClient.authorization(password: secret)
        XCTAssertTrue(value.hasPrefix("Basic "))
        let decoded = try XCTUnwrap(Data(base64Encoded: String(value.dropFirst(6))))
        XCTAssertEqual(String(decoding: decoded, as: UTF8.self), "skywire:s3cret:with:colons")
    }

    func testProbeReportsTheStatus() async throws {
        let client = try await client(routes: ["/": "<!doctype html>"])
        let status = await client.probe()
        XCTAssertEqual(status, 200)
        XCTAssertEqual(server?.requests.map(\.target), ["/"])
    }

    /// A rotated secret: the running skychat holds an older password file.
    func testProbeReportsARejectedPassword() async throws {
        let client = try await client(password: "stale")
        let status = await client.probe()
        XCTAssertEqual(status, 401)
    }

    /// Nothing listening yet: nil, not an error.
    func testProbeIsNilWhenNothingAnswers() async throws {
        let client = SkychatClient(transport: LoopbackTransport(origin: URL(string: "http://127.0.0.1:9")!)) { "x" }
        let status = await client.probe()
        XCTAssertNil(status)
    }

    /// The Keychain failed: no request is sent without a credential.
    func testNoRequestWithoutAPassword() async throws {
        struct NoKeychain: Error {}
        let server = try await StubServer.start { _ in .response(status: 200, headers: [:], body: Data()) }
        self.server = server
        let client = SkychatClient(transport: LoopbackTransport(origin: server.origin)) { throw NoKeychain() }
        let status = await client.probe()
        XCTAssertNil(status)
        XCTAssertTrue(server.requests.isEmpty)
    }

    func testContactsDecodeTheAddressBook() async throws {
        let client = try await client(routes: ["/contacts": #"{"02aa":"Alice","03bb":"Bob"}"#])
        let names = await client.contacts()
        XCTAssertEqual(names, ["02aa": "Alice", "03bb": "Bob"])
    }

    func testContactsAreEmptyOnFailure() async throws {
        let rejected = try await client(password: "stale", routes: ["/contacts": #"{"02aa":"Alice"}"#])
        let none = await rejected.contacts()
        XCTAssertEqual(none, [:])
        server?.stop()
        let garbled = try await client(routes: ["/contacts": "not json"])
        let unreadable = await garbled.contacts()
        XCTAssertEqual(unreadable, [:])
        let missing = try await client(routes: [:]).contacts()
        XCTAssertEqual(missing, [:])
    }

    func testUnreadDecodesTheCount() async throws {
        let client = try await client(routes: ["/unread": "{\"unread\":7}\n"])
        let count = await client.unread()
        XCTAssertEqual(count, 7)
    }

    func testUnreadIsNilOnFailure() async throws {
        let client = try await client(routes: ["/unread": "{}"])
        let count = await client.unread()
        XCTAssertNil(count)
    }

    /// Leaving the foreground clears the page's "on screen" report, the way
    /// the page itself would (the body skychat's notifyFocusHandler reads).
    func testClearFocusPostsAnEmptyFocus() async throws {
        let client = try await client(routes: [:])
        await client.clearFocus()
        let request = try XCTUnwrap(server?.requests.first)
        XCTAssertEqual(request.method, "POST")
        XCTAssertEqual(request.path, "/notify-focus")
        XCTAssertEqual(request.jsonObject?["key"] as? String, "")
        XCTAssertEqual(request.jsonObject?["focused"] as? Bool, false)
    }
}
