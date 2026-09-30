@testable import CoreClient
import XCTest

/// CoreClient against the recorded API (FakeVisor): the session flows first,
/// then each route of playbook appendix B that M2 implements.
final class CoreClientTests: XCTestCase {
    static let password = "Rec0rd!ng-Pass"

    private var visor: FakeVisor!
    private var server: StubServer!
    private var client: CoreClient!

    /// A visor whose account exists (the usual launch) unless a test starts
    /// its own.
    override func setUp() async throws {
        try await start(FakeVisor(accountPassword: Self.password))
    }

    override func tearDown() async throws {
        server?.stop()
    }

    private func start(_ visor: FakeVisor, password: String = CoreClientTests.password) async throws {
        server?.stop()
        self.visor = visor
        server = try await visor.start()
        client = CoreClient(
            transport: LoopbackTransport(origin: server.origin),
            password: { password },
            restartRetryDelay: .milliseconds(10)
        )
    }

    /// Request lines the server saw, API paths only.
    private var requestLines: [String] {
        server.requests.map { "\($0.method) \($0.path)" }
    }

    // MARK: Session

    func testPingNeedsNoSession() async {
        let alive = await client.ping()
        XCTAssertTrue(alive)
        XCTAssertEqual(visor.served, ["ping"])
    }

    func testPingIsFalseWhenNothingListens() async {
        server.stop()
        let dead = CoreClient(transport: LoopbackTransport(origin: URL(string: "http://127.0.0.1:9")!), password: { "" })
        let alive = await dead.ping()
        XCTAssertFalse(alive)
    }

    func testFirstRunCreatesTheAccountThenLogsIn() async throws {
        try await start(FakeVisor(accountPassword: nil))
        try await client.ensureSession()
        XCTAssertEqual(visor.served, ["user-unauthorized", "user-exists-false", "create-account", "login"])
        let created = try XCTUnwrap(server.requests.first { $0.path == "/api/create-account" }?.jsonObject)
        XCTAssertEqual(created["username"] as? String, "admin")
        XCTAssertEqual(created["password"] as? String, Self.password)
        // The session holds: the next call needs no login.
        _ = try await client.about()
        XCTAssertEqual(visor.served.last, "about")
        XCTAssertEqual(visor.served.filter { $0 == "login" }.count, 1)
    }

    func testLaterRunLogsInWithoutCreating() async throws {
        try await client.ensureSession()
        XCTAssertEqual(visor.served, ["user-unauthorized", "user-exists-true", "login"])
    }

    func testLiveSessionSkipsTheLogin() async throws {
        try await client.ensureSession()
        let before = visor.served.count
        try await client.ensureSession()
        XCTAssertEqual(Array(visor.served[before...]), ["user"])
    }

    func testRejectedPasswordIsAuthFailed() async throws {
        try await start(FakeVisor(accountPassword: Self.password), password: "An0ther!pass")
        do {
            try await client.ensureSession()
            XCTFail("login with the wrong password succeeded")
        } catch let CoreClientError.authFailed(message) {
            XCTAssertTrue(message.contains("incorrect username or password"), message)
        }
    }

    /// A core restart drops the session: the next call gets 401, logs in
    /// again once and is retried, with no error reaching the caller.
    func testUnauthorizedTriggersOneReloginAndRetry() async throws {
        _ = try await client.about()
        visor.dropSessions()
        let before = visor.served.count
        let about = try await client.about()
        XCTAssertEqual(about.publicKey, recordedPK)
        XCTAssertEqual(
            Array(visor.served[before...]),
            ["about-unauthorized", "user-unauthorized", "user-exists-true", "login", "about"]
        )
    }

    func testConcurrentCallersShareOneLogin() async throws {
        let client = try XCTUnwrap(self.client)
        async let first: Void = client.ensureSession()
        async let second: Void = client.ensureSession()
        async let third: Void = client.ensureSession()
        _ = try await (first, second, third)
        XCTAssertEqual(visor.served.filter { $0 == "login" }.count, 1, "\(visor.served)")
    }

    /// Mutations carry a CSRF token fresh from /api/csrf; reads carry none.
    func testMutationsCarryAFreshCSRFToken() async throws {
        _ = try await client.updateApp("skysocks-client", args: "--addr 127.0.0.1:1080 --reconnect")
        let put = try XCTUnwrap(server.requests.last { $0.method == "PUT" })
        XCTAssertNotNil(put.header("X-CSRF-Token"))
        let csrfBeforePut = server.requests.prefix { $0.method != "PUT" }.last
        XCTAssertEqual(csrfBeforePut?.path, "/api/csrf")
        XCTAssertTrue(server.requests.filter { $0.method == "GET" }.allSatisfy { $0.header("X-CSRF-Token") == nil })
    }

    // MARK: The visor

    func testAbout() async throws {
        let about = try await client.about()
        XCTAssertEqual(about.publicKey, recordedPK)
        XCTAssertEqual(about.build?.version.hasPrefix("v1."), true)
    }

    func testLocalPKIsFetchedOnceAndForgotten() async throws {
        let first = try await client.localPK()
        let second = try await client.localPK()
        XCTAssertEqual(first, recordedPK)
        XCTAssertEqual(second, recordedPK)
        XCTAssertEqual(visor.served.filter { $0 == "about" }.count, 1)
        await client.forgetIdentity()
        _ = try await client.localPK()
        XCTAssertEqual(visor.served.filter { $0 == "about" }.count, 2)
    }

    func testSummary() async throws {
        let summary = try await client.summary()
        XCTAssertEqual(summary.overview.localPK, recordedPK)
        XCTAssertEqual(summary.overview.apps.map(\.name).sorted(), ["skychat", "skydex-client", "skysocks-client", "vpn-client"])
        XCTAssertEqual(summary.overview.publicIPOrNil, "203.0.113.10")
        XCTAssertFalse(summary.dmsgServers.isEmpty)
        XCTAssertEqual(summary.health?.servicesHealth, "healthy")
        XCTAssertGreaterThan(summary.uptime, 0)
        XCTAssertTrue(requestLines.contains("GET /api/visors/\(recordedPK)/summary"))
    }

    func testPublicIPOrNilRejectsTheNATWord() throws {
        func overview(_ ip: String) throws -> Overview {
            try JSONDecoder().decode(Overview.self, from: Data(#"{"public_ip": "\#(ip)"}"#.utf8))
        }
        XCTAssertNil(try overview("Symmetric NAT").publicIPOrNil)
        XCTAssertNil(try overview("").publicIPOrNil)
        XCTAssertEqual(try overview("2001:db8::1").publicIPOrNil, "2001:db8::1")
    }

    func testServiceHealth() async throws {
        let health = try await client.serviceHealth()
        XCTAssertFalse(health.isEmpty)
        XCTAssertTrue(health.allSatisfy { !$0.name.isEmpty })
    }

    func testRuntimeLogs() async throws {
        let page = try await client.runtimeLogs(since: 0)
        XCTAssertFalse(page.entries.isEmpty)
        XCTAssertEqual(page.latest, Int64(page.entries.count))
        XCTAssertEqual(page.dropped, 0)
    }

    /// Caught up, the server sends `entries: null`: an empty page.
    func testRuntimeLogsCaughtUpIsEmpty() async throws {
        let target = Fixture.named("runtime-logs-caught-up").path
        let since = Int64(target.split(separator: "=").last!)!
        let page = try await client.runtimeLogs(since: since)
        XCTAssertEqual(page.entries, [])
        XCTAssertGreaterThan(page.latest, 0)
    }

    func testVisorsSummaryListsThisVisorFirst() async throws {
        let visors = try await client.visorsSummary()
        XCTAssertEqual(visors.first?.overview.localPK, recordedPK)
        XCTAssertEqual(visors.first?.isHypervisor, true)
        XCTAssertEqual(visors.first?.online, true)
    }

    func testRestartVisor() async throws {
        try await client.restartVisor(pk: recordedPK)
        XCTAssertEqual(visor.served.last, "restart")
        XCTAssertEqual(server.requests.last?.body, Data("{}".utf8))
    }

    /// 503 is "reconnecting, retry": the call is repeated until it lands.
    func testRestartVisorRetriesWhileReconnecting() async throws {
        let unavailable = StubServer.Reply.response(
            status: 503, headers: [:], body: Data(#"{"error":"visor is reconnecting"}"#.utf8)
        )
        try await client.ensureSession()
        visor.queue("POST", "/api/visors/\(recordedPK)/restart", unavailable, unavailable)
        try await client.restartVisor(pk: recordedPK)
        XCTAssertEqual(server.requests.filter { $0.path.hasSuffix("/restart") }.count, 3)
    }

    func testRestartVisorGivesUpAfterFourAttempts() async throws {
        let unavailable = StubServer.Reply.response(status: 503, headers: [:], body: Data(#"{"error":"reconnecting"}"#.utf8))
        try await client.ensureSession()
        visor.queue("POST", "/api/visors/\(recordedPK)/restart", unavailable, unavailable, unavailable, unavailable)
        do {
            try await client.restartVisor(pk: recordedPK)
            XCTFail("restart succeeded through four 503s")
        } catch let CoreClientError.http(_, _, status, message) {
            XCTAssertEqual(status, 503)
            XCTAssertEqual(message, "reconnecting")
        }
        XCTAssertEqual(server.requests.filter { $0.path.hasSuffix("/restart") }.count, 4)
    }

    func testDmsgReconnect() async throws {
        let closed = try await client.dmsgReconnect()
        XCTAssertEqual(closed, 2)
    }

    // MARK: Apps

    func testApp() async throws {
        let app = try await client.app("skysocks-client")
        XCTAssertEqual(app.name, "skysocks-client")
        XCTAssertFalse(app.running)
        XCTAssertFalse(app.args.isEmpty)
    }

    func testAppConnections() async throws {
        let connections = try await client.appConnections("skychat")
        XCTAssertFalse(connections.isEmpty)
    }

    /// No running process: the server's 500 is an empty list.
    func testAppConnectionsOfAStoppedAppAreEmpty() async throws {
        let connections = try await client.appConnections("skysocks-client")
        XCTAssertEqual(connections, [])
        XCTAssertEqual(visor.served.last, "app-connections-not-running")
    }

    func testAppStats() async throws {
        let stats = try await client.appStats("skychat")
        XCTAssertNotNil(stats.startTime)
        XCTAssertNotNil(stats.connections)
    }

    func testAppStatsOfAStoppedAppAreEmpty() async throws {
        let stats = try await client.appStats("skysocks-client")
        XCTAssertEqual(stats, AppStats())
    }

    /// Only the given fields travel.
    func testUpdateAppSendsOnlyTheGivenFields() async throws {
        let app = try await client.updateApp("skysocks-client", args: "--addr 127.0.0.1:1080 --reconnect")
        XCTAssertEqual(app.name, "skysocks-client")
        let body = try XCTUnwrap(server.requests.last { $0.method == "PUT" }?.jsonObject)
        XCTAssertEqual(body.keys.sorted(), ["args"])
        XCTAssertEqual(body["args"] as? String, "--addr 127.0.0.1:1080 --reconnect")
    }

    func testAppLogs() async throws {
        let page = try await client.appLogs("skychat", since: nil)
        XCTAssertFalse(page.logs.isEmpty)
        XCTAssertFalse(page.lastLogTimestamp.isEmpty)
    }

    /// 500 "no new available logs" is an empty page that keeps the cursor.
    func testAppLogsWithNothingNewAreEmpty() async throws {
        let target = Fixture.named("app-logs-no-new").path
        let since = try XCTUnwrap(target.split(separator: "=").last?.removingPercentEncoding)
        let page = try await client.appLogs("skychat", since: since)
        XCTAssertEqual(page, AppLogs(lastLogTimestamp: since))
        XCTAssertEqual(visor.served.last, "app-logs-no-new")
    }

    /// 500 "proc … is not found": the app is not running, also an empty page.
    func testAppLogsOfAStoppedAppAreEmpty() async throws {
        let page = try await client.appLogs("skysocks-client", since: nil)
        XCTAssertEqual(page, AppLogs(lastLogTimestamp: ""))
        XCTAssertEqual(visor.served.last, "app-logs-not-running")
    }

    // MARK: Service discovery

    func testServices() async throws {
        let servers = try await client.services(type: "proxy")
        XCTAssertFalse(servers.isEmpty)
        XCTAssertTrue(servers.allSatisfy { $0.pk.count == 66 && $0.type == "skysocks" })
        // The upstream path travels encoded, as one query value.
        XCTAssertEqual(server.requests.last?.target, Fixture.named("svc-fetch-proxy").path)
    }

    /// SD answers a bare `null` for no results.
    func testServicesNullIsEmpty() async throws {
        visor.queue(
            "GET", Fixture.named("svc-fetch-proxy").path,
            .response(status: 200, headers: ["Content-Type": "application/json"], body: Data("null".utf8))
        )
        _ = try await client.localPK()
        let servers = try await client.services(type: "proxy")
        XCTAssertEqual(servers, [])
    }

    // MARK: Router settings

    func testRouterSettings() async throws {
        let settings = try await client.routerSettings()
        XCTAssertEqual(settings.minHops, 1)
        XCTAssertEqual(settings.transportPreference.count, 8)
    }

    /// Exactly the four fields go back, the others as the GET had them: the
    /// GET's knobs would be pinned into the config if they travelled.
    func testSetMinHopsSendsOnlyTheFourFields() async throws {
        let settings = try await client.setMinHops(2)
        XCTAssertEqual(settings.minHops, 2)
        let body = try XCTUnwrap(server.requests.last { $0.method == "PUT" }?.jsonObject)
        XCTAssertEqual(body.keys.sorted(), ["existing_tp_only", "force_local_routes", "min_hops", "transport_preference"])
        XCTAssertEqual(body["min_hops"] as? Int, 2)
        let before = Fixture.named("router-settings").decode(RouterSettings.self)
        XCTAssertEqual(body["transport_preference"] as? [String], before.transportPreference)
    }

    func testSetTransportPreferenceKeepsMinHops() async throws {
        let order = ["dmsg", "stcpr", "squicr", "sudph", "stcp", "webrtc", "swsr", "swtr"]
        _ = try await client.setTransportPreference(order)
        let body = try XCTUnwrap(server.requests.last { $0.method == "PUT" }?.jsonObject)
        XCTAssertEqual(body["transport_preference"] as? [String], order)
        XCTAssertEqual(body["min_hops"] as? Int, 1)
    }

    // MARK: Errors

    func testErrorsCarryTheServersMessage() async throws {
        visor.queue(
            "GET", "/api/about",
            .response(status: 502, headers: ["Content-Type": "application/json"], body: Data(#"{"error":"upstream gone"}"#.utf8))
        )
        do {
            _ = try await client.about()
            XCTFail("a 502 decoded")
        } catch let CoreClientError.http(method, path, status, message) {
            XCTAssertEqual([method, path, message], ["GET", "/api/about", "upstream gone"])
            XCTAssertEqual(status, 502)
        }
    }

    /// Every recording is served by the flow a phone runs, in the order a
    /// first launch would reach it: the proof that each route in appendix B
    /// that M2 implements works against what a real core answers.
    func testEveryRecordedRouteIsExercised() async throws {
        try await start(FakeVisor(accountPassword: nil))
        _ = await client.ping()
        _ = try await client.csrfToken()
        try await client.ensureSession()                                  // user-unauthorized, user-exists-false, create-account, login
        try await client.ensureSession()                                  // user
        _ = try await client.about()
        let pk = try await client.localPK()
        _ = try await client.summary()
        _ = try await client.serviceHealth()
        let logs = try await client.runtimeLogs(since: 0)
        _ = try await client.runtimeLogs(since: logs.latest + 1_000_000)
        _ = try await client.visorsSummary()
        _ = try await client.app("skysocks-client")
        _ = try await client.appConnections("skysocks-client")
        _ = try await client.appStats("skysocks-client")
        _ = try await client.appConnections("skychat")
        _ = try await client.appStats("skychat")
        _ = try await client.appLogs("skysocks-client", since: nil)
        let chat = try await client.appLogs("skychat", since: nil)
        _ = try await client.appLogs("skychat", since: chat.lastLogTimestamp)
        _ = try await client.updateApp("skysocks-client", args: "--addr 127.0.0.1:1080 --reconnect")
        _ = try await client.setMinHops(2)                                // router-settings, put-router-settings
        _ = try await client.services(type: "proxy")
        _ = try await client.dmsgReconnect()
        try await client.restartVisor(pk: pk)

        // The recordings only the rules produce: a second login on a live
        // session, a wrong password, a mutation without CSRF, a 401 on a data
        // route.
        let rules = FakeVisor(accountPassword: Self.password)
        let ruleServer = try await rules.start()
        defer { ruleServer.stop() }
        let raw = LoopbackTransport(origin: ruleServer.origin)
        let json = ["Content-Type": "application/json"]
        let good = Data(#"{"username":"admin","password":"\#(Self.password)"}"#.utf8)
        _ = try await raw.send(HTTPRequest(path: "/api/user-exists"))
        _ = try await raw.send(HTTPRequest(method: .post, path: "/api/login", headers: json, body: Data(#"{"username":"admin","password":"x"}"#.utf8)))
        _ = try await raw.send(HTTPRequest(path: "/api/about"))
        let login = try await raw.send(HTTPRequest(method: .post, path: "/api/login", headers: json, body: good))
        let cookie = try XCTUnwrap(login.header("Set-Cookie")?.split(separator: ";").first).description
        _ = try await raw.send(HTTPRequest(method: .post, path: "/api/login", headers: json.merging(["Cookie": cookie]) { $1 }, body: good))
        _ = try await raw.send(HTTPRequest(method: .put, path: "/api/visors/\(pk)/apps/skysocks-client", headers: json.merging(["Cookie": cookie]) { $1 }, body: Data("{}".utf8)))

        let served = Set(visor.served + rules.served)
        let recorded = Set(Fixture.all.keys)
        XCTAssertEqual(recorded.subtracting(served).sorted(), [], "recordings no flow reached")
        let log = recorded.sorted().map { name in
            let fixture = Fixture.named(name)
            return "\(fixture.status) \(fixture.method) \(fixture.path.replacingOccurrences(of: pk, with: "{pk}"))  [\(name)]"
        }
        print("Routes exercised against the recordings (\(log.count)):\n" + log.joined(separator: "\n"))
    }
}
