import Foundation

/// A failure CoreClient reports. Transport failures (connection refused,
/// timeout) arrive as the transport threw them, usually a URLError.
public enum CoreClientError: Error, LocalizedError, Sendable, Equatable {
    /// The stored password was refused. The account in users.db was created
    /// with another password (a Keychain that lost its item under a kept data
    /// dir); the way out is to delete users.db with the core stopped, so the
    /// next start creates the account again.
    case authFailed(String)
    /// The API answered with an error status. `message` is its `error` field,
    /// or the body when it has none.
    case http(method: String, path: String, status: Int, message: String)
    /// The API answered 2xx with a body that is not the route's shape.
    case decoding(path: String, message: String)

    public var errorDescription: String? {
        switch self {
        case let .authFailed(message): message
        case let .http(method, path, status, message): "\(method) \(path) failed (\(status)): \(message)"
        case let .decoding(path, message): "\(path) answered something unreadable: \(message)"
        }
    }
}

/// The visor's local API, as the app uses it: the Swift port of Android's
/// api/VisorApi.kt, route for route (playbook appendix B).
///
/// The auth model, as the server implements it:
///  - one account, username `admin`, whose password is a device-local secret
///    (`password` below, the app's Keychain);
///  - an `swm-session` cookie held in the visor's memory, so every core
///    restart invalidates it: any 401 triggers one re-login and one retry;
///  - a CSRF token (`X-CSRF-Token`, 30 s) on every mutating call, fetched
///    fresh per attempt.
///
/// Every request goes through `transport`; nothing here opens a connection.
public actor CoreClient {
    /// `status` values for `updateApp`.
    public static let appStop = 0
    public static let appStart = 1

    /// The account the app owns. The server accepts any name for the first
    /// account; "admin" is what the Android app and the desktop UI use.
    public static let username = "admin"

    /// How long a normal call may wait for its answer.
    static let standardTimeout: TimeInterval = 15
    /// For the routes the visor answers by going out over the network itself.
    /// svc-fetch dials a deployment service over dmsg with a 15 s budget per
    /// hop, so the loopback call has to outlast it (Android: 45 s read, 50 s
    /// call).
    static let relayTimeout: TimeInterval = 50
    /// /api/ping answers at once or not at all.
    static let pingTimeout: TimeInterval = 3

    /// A visor restart answered 503 ("known but reconnecting") is retried:
    /// enough to outlast a hypervisor-client redial (measured ~4 s).
    static let restartAttempts = 4

    private let transport: any RequestTransport
    private let password: @Sendable () async throws -> String
    private let restartRetryDelay: Duration

    private var cookies: [String: SessionCookie] = [:]
    private var cachedPK: String?
    private var sessionTask: Task<Void, any Error>?

    /// - Parameters:
    ///   - transport: how requests reach the API.
    ///   - password: the account's password; asked for only when a login is
    ///     needed.
    public init(transport: any RequestTransport, password: @escaping @Sendable () async throws -> String) {
        self.init(transport: transport, password: password, restartRetryDelay: .seconds(3))
    }

    init(
        transport: any RequestTransport,
        password: @escaping @Sendable () async throws -> String,
        restartRetryDelay: Duration
    ) {
        self.transport = transport
        self.password = password
        self.restartRetryDelay = restartRetryDelay
    }

    // MARK: Liveness and session

    /// True once the API answers at all; needs no session.
    public func ping() async -> Bool {
        let response = try? await exchange(.get, "/api/ping", timeout: Self.pingTimeout)
        return response?.isSuccess ?? false
    }

    /// Makes sure a session exists: the first run creates the account with
    /// the device's password, later runs log in. Concurrent callers share one
    /// attempt. Throws `authFailed` when the password is refused.
    public func ensureSession() async throws {
        if let running = sessionTask {
            return try await running.value
        }
        let task = Task { try await establishSession() }
        sessionTask = task
        defer { sessionTask = nil }
        try await task.value
    }

    private func establishSession() async throws {
        if try await exchange(.get, "/api/user").isSuccess { return }
        let credentials = try JSONEncoder().encode(Credentials(username: Self.username, password: try await password()))
        let existsResponse = try await exchange(.get, "/api/user-exists")
        let exists = existsResponse.isSuccess && (try? JSONDecoder().decode(UserExists.self, from: existsResponse.body))?.exists == true
        if !exists {
            let created = try await exchange(.post, "/api/create-account", body: credentials)
            // 500 "user exists": lost a race or lost state. The login below is
            // the real test.
            if !created.isSuccess && created.status != 500 {
                throw CoreClientError.authFailed("create-account failed: \(Self.errorMessage(created))")
            }
        }
        let login = try await exchange(.post, "/api/login", body: credentials)
        switch login.status {
        case 200..<300:
            return
        case 403:
            // "not logged out": a live session cookie already exists.
            return
        case 401:
            throw CoreClientError.authFailed("stored password rejected: \(Self.errorMessage(login))")
        default:
            throw CoreClientError.http(method: "POST", path: "/api/login", status: login.status, message: Self.errorMessage(login))
        }
    }

    /// A fresh CSRF token for one mutating call; it lives 30 seconds.
    public func csrfToken() async throws -> String {
        let response = try await exchange(.get, "/api/csrf")
        try Self.check(response, "GET", "/api/csrf")
        return try Self.decode(CSRFToken.self, response, "/api/csrf").token
    }

    // MARK: The visor

    public func about() async throws -> About {
        try await get("/api/about")
    }

    /// The visor's public key, fetched once and then remembered.
    public func localPK() async throws -> String {
        if let cachedPK { return cachedPK }
        let pk = try await about().publicKey
        cachedPK = pk
        return pk
    }

    /// Forgets the remembered key. Every per-visor route is addressed by it,
    /// so after an identity change the old one would 404 every call; Settings
    /// calls this the moment it replaces the identity.
    public func forgetIdentity() {
        cachedPK = nil
    }

    public func summary() async throws -> VisorSummary {
        try await get("/api/visors/\(try await localPK())/summary")
    }

    public func serviceHealth() async throws -> [ServiceHealthEntry] {
        try await getList("/api/service-health")
    }

    /// The runtime-log lines after cursor `since`, of `pk` (default: this
    /// visor). Fleet passes a remote key: the same route answers from that
    /// visor's ring buffer.
    public func runtimeLogs(since: Int64, pk: String? = nil) async throws -> RuntimeLogsDelta {
        let visor = if let pk { pk } else { try await localPK() }
        return try await get("/api/visors/\(visor)/runtime-logs?since=\(since)")
    }

    /// Every visor this one knows: itself first, then each remote that dialed
    /// in over dmsg (Fleet), offline ones from their last snapshot.
    public func visorsSummary() async throws -> [VisorSummary] {
        try await getList("/api/visors-summary")
    }

    /// Restarts visor `pk`: it closes its module stack, re-reads its config
    /// and runs again. The server answers 202 without waiting (the restart
    /// tears down the connection carrying the request); what happened shows
    /// in the next summary.
    public func restartVisor(pk: String) async throws {
        let path = "/api/visors/\(pk)/restart"
        for attempt in 1...Self.restartAttempts {
            let response = try await authed(.post, path, body: Data("{}".utf8))
            if response.isSuccess { return }
            // 503 is "known but not connected right now": a managed visor's
            // RPC connection idle-closes after about two minutes and redials
            // within seconds. The server expects the caller to retry.
            if response.status != 503 || attempt == Self.restartAttempts {
                throw Self.httpError(response, "POST", path)
            }
            try await Task.sleep(for: restartRetryDelay)
        }
    }

    /// Drops the visor's dmsg sessions so it re-dials now: for when the
    /// phone's network changed under it and the old sockets are dead but
    /// nobody knows yet. Returns how many sessions were closed.
    public func dmsgReconnect() async throws -> Int {
        let path = "/api/dmsg/reconnect"
        let response = try await authed(.post, path, body: Data("{}".utf8))
        try Self.check(response, "POST", path)
        return try Self.decode(DmsgReconnectResult.self, response, path).sessionsClosed
    }

    // MARK: Apps

    public func app(_ name: String) async throws -> AppState {
        try await get("/api/visors/\(try await localPK())/apps/\(name)")
    }

    /// The app's live connections. 500 means it has no running process and a
    /// JSON null that it holds no connection yet: both are an empty list.
    public func appConnections(_ name: String) async throws -> [AppConnection] {
        let path = "/api/visors/\(try await localPK())/apps/\(name)/connections"
        let response = try await authed(.get, path)
        if response.status == 500 { return [] }
        try Self.check(response, "GET", path)
        return try Self.decodeList(response, path)
    }

    /// The app's runtime stats, empty when it has no running process (500).
    public func appStats(_ name: String) async throws -> AppStats {
        let path = "/api/visors/\(try await localPK())/apps/\(name)/stats"
        let response = try await authed(.get, path)
        if response.status == 500 { return AppStats() }
        try Self.check(response, "GET", path)
        return try Self.decode(AppStats.self, response, path)
    }

    /// One PUT on an app, carrying only the fields given: `pk` sets the
    /// server it dials, `args` replaces the whole argv (one shell-quoted
    /// string), `killswitch` toggles vpn-client's flag, `status` starts
    /// (`appStart`) or stops (`appStop`) it. The first three restart a running
    /// app; on a stopped one they only rewrite the config, so
    /// configure-then-start is safe in one call.
    public func updateApp(
        _ name: String,
        pk: String? = nil,
        args: String? = nil,
        killswitch: Bool? = nil,
        status: Int? = nil
    ) async throws -> AppState {
        var fields: [String: Any] = [:]
        if let pk { fields["pk"] = pk }
        if let args { fields["args"] = args }
        if let killswitch { fields["killswitch"] = killswitch }
        if let status { fields["status"] = status }
        let path = "/api/visors/\(try await localPK())/apps/\(name)"
        let response = try await authed(.put, path, body: try JSONSerialization.data(withJSONObject: fields, options: .sortedKeys))
        try Self.check(response, "PUT", path)
        return try Self.decode(AppState.self, response, path)
    }

    /// A page of the app's log after `since` (RFC 3339 nanoseconds; nil for
    /// the start). The server answers 500 "no new available logs" when
    /// nothing is new and 500 "proc … is not found" when the app is not
    /// running: both are an empty page, not an error.
    public func appLogs(_ app: String, since: String?) async throws -> AppLogs {
        var path = "/api/visors/\(try await localPK())/apps/\(app)/logs"
        if let since, !since.isEmpty {
            path += "?since=" + Self.queryEncode(since)
        }
        let response = try await authed(.get, path)
        if response.isSuccess {
            return try Self.decode(AppLogs.self, response, path)
        }
        let message = Self.errorMessage(response)
        if response.status == 500, message.contains("no new available logs") || message.contains("is not found") {
            return AppLogs(lastLogTimestamp: since ?? "")
        }
        throw CoreClientError.http(method: "GET", path: path, status: response.status, message: message)
    }

    // MARK: Service discovery

    /// Public servers of one service type (`proxy` for SkySOCKS, `vpn` for
    /// SkyVPN), straight from service discovery. There is no dedicated route:
    /// /api/svc-fetch is the visor's generic deployment-service proxy, and SD's
    /// own /api/services takes the type filter.
    public func services(type: String) async throws -> [ServiceEntry] {
        let upstream = Self.queryEncode("/api/services?type=\(type)")
        let path = "/api/svc-fetch?service=sd&path=\(upstream)"
        let response = try await authed(.get, path, timeout: Self.relayTimeout)
        try Self.check(response, "GET", path)
        // The upstream's payload verbatim: a bare null for an empty result.
        return try Self.decodeList(response, path)
    }

    // MARK: Router settings

    public func routerSettings() async throws -> RouterSettings {
        try await get("/api/visors/\(try await localPK())/router-settings")
    }

    /// Installs `order` as the transport-type priority order, live: the visor
    /// applies it without a restart and persists it to its config.
    public func setTransportPreference(_ order: [String]) async throws -> RouterSettings {
        try await updateRouterSettings { $0.transportPreference = order }
    }

    /// The minimum number of hops a route takes. 1 allows a direct route; 2 or
    /// more forces intermediaries (sender privacy, at the cost of latency).
    public func setMinHops(_ hops: Int) async throws -> RouterSettings {
        try await updateRouterSettings { $0.minHops = hops }
    }

    /// Read-modify-write of the four fields the phone owns, and exactly those
    /// four go back. The PUT applies force_local_routes, existing_tp_only and
    /// min_hops unconditionally, so a field sent alone would also send
    /// `min_hops: 0`, which the router reads as routing disabled. And the GET
    /// carries more than it takes back safely: its `knobs` are the router's
    /// tuning values, and a PUT that includes them applies each as an explicit
    /// override and writes all of them into the config
    /// (Visor.SetRouterSettings → persistRouterKnobs), pinning today's
    /// defaults for good. Every other field the PUT reads is "zero or absent
    /// means leave it", so leaving them out changes nothing.
    private func updateRouterSettings(_ edit: (inout RouterSettingsWrite) -> Void) async throws -> RouterSettings {
        let path = "/api/visors/\(try await localPK())/router-settings"
        let current = try await authed(.get, path)
        try Self.check(current, "GET", path)
        var write = RouterSettingsWrite(try Self.decode(RouterSettings.self, current, path))
        edit(&write)
        let response = try await authed(.put, path, body: try JSONEncoder().encode(write))
        try Self.check(response, "PUT", path)
        return try Self.decode(RouterSettings.self, response, path)
    }

    // MARK: Plumbing

    private func get<T: Decodable>(_ path: String) async throws -> T {
        let response = try await authed(.get, path)
        try Self.check(response, "GET", path)
        return try Self.decode(T.self, response, path)
    }

    private func getList<T: Decodable>(_ path: String) async throws -> [T] {
        let response = try await authed(.get, path)
        try Self.check(response, "GET", path)
        return try Self.decodeList(response, path)
    }

    /// A call that needs the session: on 401, one re-login and one retry.
    /// A mutation gets a fresh CSRF token per attempt; the retry comes after a
    /// full login round trip, and the token lives 30 seconds.
    private func authed(
        _ method: HTTPMethod,
        _ path: String,
        body: Data? = nil,
        timeout: TimeInterval = CoreClient.standardTimeout
    ) async throws -> HTTPResponse {
        let first = try await exchange(method, path, body: body, csrf: method != .get, timeout: timeout)
        guard first.status == 401 else { return first }
        try await ensureSession()
        return try await exchange(method, path, body: body, csrf: method != .get, timeout: timeout)
    }

    /// One request with the session cookie attached, and the cookies the
    /// response sets remembered.
    private func exchange(
        _ method: HTTPMethod,
        _ path: String,
        body: Data? = nil,
        csrf: Bool = false,
        timeout: TimeInterval = CoreClient.standardTimeout
    ) async throws -> HTTPResponse {
        var headers = ["Accept": "application/json"]
        if body != nil {
            headers["Content-Type"] = "application/json"
        }
        if csrf {
            headers["X-CSRF-Token"] = try await csrfToken()
        }
        if let cookie = cookieHeader() {
            headers["Cookie"] = cookie
        }
        let response = try await transport.send(
            HTTPRequest(method: method, path: path, headers: headers, body: body, timeout: timeout)
        )
        remember(response)
        return response
    }

    // MARK: Cookies

    /// A cookie the API set: the session, in practice. Kept here, not in the
    /// transport, because a transport other than the loopback one has no cookie
    /// store. One origin only, so no domain or path matching.
    private struct SessionCookie {
        let value: String
        let expires: Date?
    }

    private static let cookieURL = URL(string: "http://127.0.0.1/")!

    private func remember(_ response: HTTPResponse) {
        guard let setCookie = response.header("Set-Cookie") else { return }
        let now = Date()
        for cookie in HTTPCookie.cookies(withResponseHeaderFields: ["Set-Cookie": setCookie], for: Self.cookieURL) {
            if let expires = cookie.expiresDate, expires <= now {
                // The logout form: an expired cookie deletes it.
                cookies[cookie.name] = nil
            } else {
                cookies[cookie.name] = SessionCookie(value: cookie.value, expires: cookie.expiresDate)
            }
        }
    }

    private func cookieHeader() -> String? {
        let now = Date()
        cookies = cookies.filter { $0.value.expires.map { $0 > now } ?? true }
        guard !cookies.isEmpty else { return nil }
        return cookies.keys.sorted().map { "\($0)=\(cookies[$0]!.value)" }.joined(separator: "; ")
    }

    // MARK: Decoding

    private static func check(_ response: HTTPResponse, _ method: String, _ path: String) throws {
        if !response.isSuccess {
            throw httpError(response, method, path)
        }
    }

    private static func httpError(_ response: HTTPResponse, _ method: String, _ path: String) -> CoreClientError {
        .http(method: method, path: path, status: response.status, message: errorMessage(response))
    }

    private static func decode<T: Decodable>(_ type: T.Type, _ response: HTTPResponse, _ path: String) throws -> T {
        do {
            return try JSONDecoder().decode(T.self, from: response.body)
        } catch {
            throw CoreClientError.decoding(path: path, message: "\(error)")
        }
    }

    /// A list route's body, where an empty body or a JSON null means none.
    private static func decodeList<T: Decodable>(_ response: HTTPResponse, _ path: String) throws -> [T] {
        let text = String(decoding: response.body, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
        if text.isEmpty || text == "null" { return [] }
        return try decode([T].self, response, path)
    }

    /// The API's `{"error": "..."}`, or the body as text, at most 500
    /// characters.
    static func errorMessage(_ response: HTTPResponse) -> String {
        if let error = try? JSONDecoder().decode(APIError.self, from: response.body) {
            return String(error.error.prefix(500))
        }
        let text = String(decoding: response.body, as: UTF8.self)
        return text.isEmpty ? "(no body)" : String(text.prefix(500))
    }

    /// A query value, percent-encoded so that `/`, `?`, `=` and `&` inside it
    /// stay part of the value.
    static func queryEncode(_ value: String) -> String {
        value.addingPercentEncoding(withAllowedCharacters: queryValueAllowed) ?? value
    }

    private static let queryValueAllowed = CharacterSet(
        charactersIn: "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"
    )
}
