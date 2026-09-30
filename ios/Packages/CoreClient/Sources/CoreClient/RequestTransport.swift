import Foundation

/// How a request reaches the visor's local API. CoreClient never opens a
/// connection itself; it hands every request to one of these.
///
/// The seam exists for the device. In the app process on the Simulator, and
/// hopefully on a device, the API is plain HTTP on 127.0.0.1
/// (`LoopbackTransport`). If a device's app cannot reach the packet-tunnel
/// extension's loopback, the same requests travel as provider messages
/// instead (M7's ProviderMessageTransport), and nothing above this protocol
/// changes. So an implementation carries bytes and nothing else: cookies,
/// CSRF tokens and re-login are CoreClient's, not the transport's.
public protocol RequestTransport: Sendable {
    /// One request, one complete response. Throws only when no response
    /// arrived (connection refused, timeout); any HTTP status is a response.
    func send(_ request: HTTPRequest) async throws -> HTTPResponse

    /// One request whose response body is read as it arrives: server-sent
    /// events, and the call audio later. Returns with the response head, which
    /// over `LoopbackTransport` means with the first body bytes too: the
    /// system's HTTP stack surfaces a response only once some of its body has
    /// arrived (measured, see LoopbackTransportTests). A caller
    /// must not read "returned" as "connected" on a route that stays silent
    /// after its head (the visor's SSE routes write nothing until their first
    /// ping).
    func stream(_ request: HTTPRequest) async throws -> HTTPStream
}

public enum HTTPMethod: String, Sendable {
    case get = "GET"
    case post = "POST"
    case put = "PUT"
    case delete = "DELETE"
}

/// A request to the local API, independent of how it travels.
public struct HTTPRequest: Sendable {
    public var method: HTTPMethod
    /// Path and query from the origin, already percent-encoded:
    /// `/api/visors/<pk>/summary`.
    public var path: String
    public var headers: [String: String]
    public var body: Data?
    /// How long the response may keep the caller waiting with nothing
    /// arriving; nil for a stream, which lasts as long as it is read.
    public var timeout: TimeInterval?

    public init(
        method: HTTPMethod = .get,
        path: String,
        headers: [String: String] = [:],
        body: Data? = nil,
        timeout: TimeInterval? = 15
    ) {
        self.method = method
        self.path = path
        self.headers = headers
        self.body = body
        self.timeout = timeout
    }
}

/// A complete response.
public struct HTTPResponse: Sendable {
    public let status: Int
    /// Header names lowercased. A header sent several times (Set-Cookie) is
    /// one value, joined with ", ", as HTTP allows and Foundation delivers it.
    public let headers: [String: String]
    public let body: Data

    public init(status: Int, headers: [String: String], body: Data) {
        self.status = status
        self.headers = Dictionary(headers.map { ($0.key.lowercased(), $0.value) }, uniquingKeysWith: { "\($0), \($1)" })
        self.body = body
    }

    public func header(_ name: String) -> String? { headers[name.lowercased()] }

    public var isSuccess: Bool { (200..<300).contains(status) }
}

/// A response whose body is still arriving.
public struct HTTPStream: Sendable {
    public let status: Int
    /// As in `HTTPResponse`: names lowercased.
    public let headers: [String: String]
    /// The body in the chunks it arrives in. It finishes when the server ends
    /// the response and throws when the connection fails. Cancelling the task
    /// that iterates it, or dropping the stream, closes the connection: that is
    /// the only way to end a stream the server keeps open.
    public let body: AsyncThrowingStream<Data, any Error>

    public init(status: Int, headers: [String: String], body: AsyncThrowingStream<Data, any Error>) {
        self.status = status
        self.headers = Dictionary(headers.map { ($0.key.lowercased(), $0.value) }, uniquingKeysWith: { "\($0), \($1)" })
        self.body = body
    }

    public func header(_ name: String) -> String? { headers[name.lowercased()] }

    public var isSuccess: Bool { (200..<300).contains(status) }
}
