@testable import CoreClient
import XCTest

/// SkydexClient against a stub gated like skydex-client
/// (cmd/apps/skydex-client/commands/auth.go: Basic auth, password only), and
/// the profile's argv helpers for `--market-pk`.
final class SkydexClientTests: XCTestCase {
    private var server: StubServer?
    private let secret = "dex-secret"
    private let market = "02" + String(repeating: "a1", count: 32)

    override func tearDown() async throws {
        server?.stop()
    }

    private func client(connectReply: StubServer.Reply? = nil) async throws -> SkydexClient {
        let expected = BasicAuth.header(user: SkydexProfile.user, password: secret)
        let market = market
        let server = try await StubServer.start { request in
            guard request.header("Authorization") == expected else {
                return .response(status: 401, headers: ["WWW-Authenticate": "Basic realm=\"skydex\""], body: Data())
            }
            switch (request.method, request.path) {
            case ("GET", "/"):
                return .response(status: 200, headers: [:], body: Data("<!doctype html>".utf8))
            case ("GET", "/api/status"):
                return .response(status: 200, headers: [:], body: Data(#"{"connected":false}"#.utf8))
            case ("POST", "/api/connect"):
                if let connectReply { return connectReply }
                let body = request.jsonObject?["market_pk"] as? String ?? ""
                return .response(status: 200, headers: [:], body: Data(#"{"connected":true,"market_pk":"\#(body)","market_name":"Test market"}"#.utf8))
            case ("POST", "/api/disconnect"):
                return .response(status: 204, headers: [:], body: Data())
            default:
                _ = market
                return .response(status: 404, headers: [:], body: Data())
            }
        }
        self.server = server
        let secret = secret
        return SkydexClient(transport: LoopbackTransport(origin: server.origin)) { secret }
    }

    func testProbeStatusConnectDisconnect() async throws {
        let client = try await client()
        let up = await client.probe()
        XCTAssertTrue(up)
        let idle = await client.status()
        XCTAssertEqual(idle, MarketStatus(connected: false))
        let connected = try await client.connect(marketPK: market)
        XCTAssertEqual(connected, MarketStatus(connected: true, marketPK: market, marketName: "Test market"))
        await client.disconnect()
        let connect = try XCTUnwrap(server?.requests.first { $0.path == "/api/connect" })
        XCTAssertEqual(connect.header("Content-Type"), "application/json")
        XCTAssertEqual(server?.requests.last?.path, "/api/disconnect")
    }

    /// The engine's own words reach the user.
    func testAFailedDialCarriesTheEnginesError() async throws {
        let client = try await client(connectReply: .response(
            status: 502, headers: ["Content-Type": "application/json"], body: Data(#"{"error":"market unreachable"}"#.utf8)
        ))
        do {
            _ = try await client.connect(marketPK: market)
            XCTFail("a 502 connected")
        } catch let CoreClientError.http(_, _, status, message) {
            XCTAssertEqual(status, 502)
            XCTAssertEqual(message, "market unreachable")
        }
    }

    func testMarketArgs() {
        let args = ["--addr", "127.0.0.1:8051", "--password-file", "/data/skydex-password"]
        XCTAssertNil(SkydexProfile.marketPK(args))
        let written = SkydexProfile.args(args, withMarketPK: market)
        XCTAssertEqual(AppArgs.split(written), args + ["--market-pk", market])
        XCTAssertEqual(SkydexProfile.marketPK(AppArgs.split(written) ?? []), market)
        XCTAssertEqual(SkydexProfile.args(["--market-pk=03ff"], withMarketPK: market), "--market-pk=\(market)")
    }

    func testMarketKeyShape() {
        XCTAssertTrue(SkydexProfile.isMarketPK(market))
        XCTAssertTrue(SkydexProfile.isMarketPK("03" + String(repeating: "AB", count: 32)))
        XCTAssertFalse(SkydexProfile.isMarketPK("04" + String(repeating: "ab", count: 32)))
        XCTAssertFalse(SkydexProfile.isMarketPK(String(market.dropLast())))
        XCTAssertFalse(SkydexProfile.isMarketPK(String(market.dropLast()) + "g"))
    }
}
