import Foundation

/// skydex-client's answer about its one market connection.
public struct MarketStatus: Decodable, Sendable, Equatable {
    public var connected: Bool
    public var marketPK: String
    /// The operator's display name; often empty.
    public var marketName: String

    enum CodingKeys: String, CodingKey {
        case connected, marketPK = "market_pk", marketName = "market_name"
    }

    public init(connected: Bool, marketPK: String = "", marketName: String = "") {
        self.connected = connected
        self.marketPK = marketPK
        self.marketName = marketName
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        connected = c.lenient(.connected, false)
        marketPK = c.lenient(.marketPK, "")
        marketName = c.lenient(.marketName, "")
    }
}

/// skydex-client's own control API (127.0.0.1:8051 by default), apart from
/// the visor's (Android: api/SkydexApi.kt). The seam that lets the market key
/// be entered natively while the trading UI stays the page the desktop
/// serves: the engine never dials a market by itself (`--market-pk` only
/// pre-fills the page's form), so something has to POST `/api/connect`.
/// Every request answers the phone's gate (`SkydexProfile`) with Basic auth.
public struct SkydexClient: Sendable {
    private let transport: any RequestTransport
    private let password: @Sendable () throws -> String

    public init(transport: any RequestTransport, password: @escaping @Sendable () throws -> String) {
        self.transport = transport
        self.password = password
    }

    /// Loopback, and a server that answers at once or is not up yet.
    static let timeout: TimeInterval = 5
    /// `/api/connect` returns only once a dmsg route to the market is built
    /// and its first handshake answered: seconds on a good network, minutes
    /// on a bad one.
    static let dialTimeout: TimeInterval = 95

    /// True once the trading UI answers: the gate for showing the page.
    public func probe() async -> Bool {
        (try? await send(.get, "/"))?.isSuccess ?? false
    }

    /// The market connection, or nil while the app is not answering (the
    /// ordinary case while it comes up).
    public func status() async -> MarketStatus? {
        guard let response = try? await send(.get, "/api/status"), response.isSuccess else { return nil }
        return try? JSONDecoder().decode(MarketStatus.self, from: response.body)
    }

    /// Dials `marketPK` and handshakes with it. The failure is the one the
    /// user most needs to read (an unreachable market, a rejected key), so it
    /// is thrown with the engine's own words.
    public func connect(marketPK: String) async throws -> MarketStatus {
        let body = try JSONSerialization.data(withJSONObject: ["market_pk": marketPK])
        let response = try await send(.post, "/api/connect", body: body, timeout: Self.dialTimeout)
        guard response.isSuccess else {
            throw CoreClientError.http(method: "POST", path: "/api/connect", status: response.status, message: CoreClient.errorMessage(response))
        }
        do {
            return try JSONDecoder().decode(MarketStatus.self, from: response.body)
        } catch {
            throw CoreClientError.decoding(path: "/api/connect", message: "\(error)")
        }
    }

    /// Drops the market connection; best-effort, there is nothing to retry.
    public func disconnect() async {
        _ = try? await send(.post, "/api/disconnect", body: Data("{}".utf8))
    }

    private func send(_ method: HTTPMethod, _ path: String, body: Data? = nil, timeout: TimeInterval = SkydexClient.timeout) async throws -> HTTPResponse {
        var headers = ["Authorization": BasicAuth.header(user: SkydexProfile.user, password: try password())]
        if body != nil {
            headers["Content-Type"] = "application/json"
        }
        return try await transport.send(HTTPRequest(method: method, path: path, headers: headers, body: body, timeout: timeout))
    }
}
