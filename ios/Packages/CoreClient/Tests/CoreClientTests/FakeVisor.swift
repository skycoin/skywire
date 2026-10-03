import Foundation
import os

/// One exchange recorded from a real lite core (Tests/Fixtures/routes, see
/// the README there): the request line it answered and the response.
struct Fixture: Decodable, Sendable {
    let method: String
    let path: String
    let status: Int
    let headers: [String: String]
    let body: String

    /// Every fixture, by file name without the extension.
    static let all: [String: Fixture] = {
        let urls = Bundle.module.urls(forResourcesWithExtension: "json", subdirectory: "Fixtures/routes") ?? []
        precondition(!urls.isEmpty, "no fixtures in the test bundle")
        return Dictionary(uniqueKeysWithValues: urls.map { url in
            let fixture = try! JSONDecoder().decode(Fixture.self, from: Data(contentsOf: url))
            return (url.deletingPathExtension().lastPathComponent, fixture)
        })
    }()

    static func named(_ name: String) -> Fixture {
        guard let fixture = all[name] else { preconditionFailure("no fixture \(name)") }
        return fixture
    }

    var reply: StubServer.Reply {
        .response(status: status, headers: headers, body: Data(body.utf8))
    }

    /// The fixture's JSON body, decoded as `T`.
    func decode<T: Decodable>(_ type: T.Type) -> T {
        try! JSONDecoder().decode(T.self, from: Data(body.utf8))
    }
}

/// The recording's identity: every per-visor path in the fixtures carries it.
let recordedPK: String = {
    struct About: Decodable { let public_key: String }
    return Fixture.named("about").decode(About.self).public_key
}()

/// The visor's local API as the tests see it: the recorded answers, served
/// under the real server's rules for the session. A request must carry the
/// session cookie that /api/login set (else the recorded 401), a mutation a
/// CSRF token that /api/csrf issued (else the recorded 403); the account
/// exists only once /api/create-account made it, and /api/login checks the
/// password it was made with. Everything else is looked up by its exact
/// request line in the recordings.
final class FakeVisor: Sendable {
    private struct State: Sendable {
        var accountPassword: String?
        var liveSession: String?
        /// Fixture names served, in order.
        var served: [String] = []
        /// Replies queued for a request line, served before its fixture.
        var queued: [String: [StubServer.Reply]] = [:]
    }

    private let state: OSAllocatedUnfairLock<State>

    /// The session cookie's value in the login recording.
    private static let sessionValue: String = {
        let setCookie = Fixture.named("login").headers["set-cookie"]!
        let pair = setCookie.split(separator: ";")[0]
        return String(pair.split(separator: "=", maxSplits: 1)[1])
    }()

    private static let csrfToken: String = {
        struct Token: Decodable { let csrf_token: String }
        return Fixture.named("csrf").decode(Token.self).csrf_token
    }()

    /// Recordings answered by the session rules below, not by lookup.
    private static let sessionFixtures: Set<String> = [
        "user", "user-unauthorized", "user-exists-true", "user-exists-false", "create-account",
        "login", "login-already", "login-wrong-password", "about-unauthorized", "put-app-without-csrf",
    ]

    private static let byRequestLine: [String: String] = {
        var table: [String: String] = [:]
        for (name, fixture) in Fixture.all where !sessionFixtures.contains(name) {
            let line = "\(fixture.method) \(fixture.path)"
            precondition(table[line] == nil, "two fixtures for \(line)")
            table[line] = name
        }
        return table
    }()

    /// - Parameter accountPassword: the password of an account that already
    ///   exists, or nil for a first run.
    init(accountPassword: String? = nil) {
        state = OSAllocatedUnfairLock(initialState: State(accountPassword: accountPassword))
    }

    var served: [String] { state.withLock { $0.served } }

    /// What a core restart does to the API: the sessions it held are gone.
    func dropSessions() {
        state.withLock { $0.liveSession = nil }
    }

    /// Answers `method target` with `replies`, one per request that passes the
    /// session rules, before the recording takes over again.
    func queue(_ method: String, _ target: String, _ replies: StubServer.Reply...) {
        state.withLock { $0.queued["\(method) \(target)", default: []].append(contentsOf: replies) }
    }

    func start() async throws -> StubServer {
        try await StubServer.start { [self] request in handle(request) }
    }

    func handle(_ request: StubServer.Request) -> StubServer.Reply {
        state.withLock { state in
            let line = "\(request.method) \(request.target)"
            let hasSession = state.liveSession != nil && Self.cookie(of: request) == state.liveSession
            func serve(_ name: String) -> StubServer.Reply {
                state.served.append(name)
                if name == "login" {
                    state.liveSession = Self.sessionValue
                    return Self.freshLoginReply()
                }
                return Fixture.named(name).reply
            }
            switch (request.method, request.path) {
            case ("GET", "/api/user"):
                return serve(hasSession ? "user" : "user-unauthorized")
            case ("GET", "/api/user-exists"):
                return serve(state.accountPassword == nil ? "user-exists-false" : "user-exists-true")
            case ("POST", "/api/create-account"):
                state.accountPassword = request.jsonObject?["password"] as? String
                return serve("create-account")
            case ("POST", "/api/login"):
                if hasSession { return serve("login-already") }
                let password = request.jsonObject?["password"] as? String
                return serve(password != nil && password == state.accountPassword ? "login" : "login-wrong-password")
            case ("GET", "/api/ping"), ("GET", "/api/csrf"):
                break
            default:
                guard hasSession else { return serve("about-unauthorized") }
                if request.method != "GET", request.header("X-CSRF-Token") != Self.csrfToken {
                    return serve("put-app-without-csrf")
                }
            }
            // Queued replies stand in for the recording, behind the same
            // session rules.
            if var replies = state.queued[line], !replies.isEmpty {
                let reply = replies.removeFirst()
                state.queued[line] = replies
                return reply
            }
            guard let name = Self.byRequestLine[line] else {
                return .response(status: 404, headers: ["Content-Type": "application/json"], body: Data(#"{"error":"not recorded"}"#.utf8))
            }
            return serve(name)
        }
    }

    private static func cookie(of request: StubServer.Request) -> String? {
        guard let header = request.header("Cookie") else { return nil }
        for pair in header.split(separator: ";") {
            let parts = pair.trimmingCharacters(in: .whitespaces).split(separator: "=", maxSplits: 1)
            if parts.count == 2, parts[0] == "swm-session" {
                return String(parts[1])
            }
        }
        return nil
    }

    /// The login recording with its cookie's Expires moved a day ahead: the
    /// recorded one lapsed four hours after the recording, and a client that
    /// honours Expires (this one does) would drop it on arrival.
    private static func freshLoginReply() -> StubServer.Reply {
        let fixture = Fixture.named("login")
        var headers = fixture.headers
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.timeZone = TimeZone(identifier: "GMT")
        formatter.dateFormat = "EEE, dd MMM yyyy HH:mm:ss 'GMT'"
        let expires = formatter.string(from: Date().addingTimeInterval(86_400))
        headers["set-cookie"] = headers["set-cookie"]!.replacingOccurrences(
            of: #"Expires=[^;]+"#, with: "Expires=\(expires)", options: .regularExpression
        )
        return .response(status: fixture.status, headers: headers, body: Data(fixture.body.utf8))
    }
}
