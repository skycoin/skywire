import Foundation

/// The local API over `NETunnelProviderSession.sendProviderMessage`, the
/// fallback for a device whose app cannot reach the packet-tunnel extension's
/// 127.0.0.1 (proposal §5.2). Each request travels as one message and its
/// response comes back as the reply: `ProviderMessageTransport` is the app's
/// end, `ProviderMessageHandler` the extension's, which makes the request on
/// its own loopback. Nothing above `RequestTransport` changes.
public enum ProviderMessage {
    /// Bumped when the shape changes; an extension refuses another version.
    static let version = 1

    struct Request: Codable {
        var v: Int
        var method: String
        var path: String
        var headers: [String: String]
        var body: Data?
        var timeout: TimeInterval?
    }

    struct Reply: Codable {
        var v: Int
        var status: Int?
        var headers: [String: String]?
        var body: Data?
        /// Set when no response arrived; `urlError` keeps its code for the caller.
        var error: String?
        var urlError: Int?
    }
}

public enum ProviderMessageError: Error, LocalizedError, Equatable, Sendable {
    /// A streamed response or upload: one message carries one whole response.
    case streamingUnsupported
    case malformedReply
    /// The extension made no request, and says why.
    case refused(String)

    public var errorDescription: String? {
        switch self {
        case .streamingUnsupported: "a streamed request cannot travel as a provider message"
        case .malformedReply: "the packet-tunnel extension answered something unreadable"
        case let .refused(reason): "the packet-tunnel extension refused the request: \(reason)"
        }
    }
}

/// The app's end: requests as provider messages.
public struct ProviderMessageTransport: RequestTransport {
    public typealias Send = @Sendable (Data) async throws -> Data

    private let sendMessage: Send

    /// - Parameter send: delivers one message to the extension and returns its
    ///   reply (`NETunnelProviderSession.sendProviderMessage`).
    public init(send: @escaping Send) {
        sendMessage = send
    }

    public func send(_ request: HTTPRequest) async throws -> HTTPResponse {
        let message = try JSONEncoder().encode(ProviderMessage.Request(
            v: ProviderMessage.version, method: request.method.rawValue, path: request.path,
            headers: request.headers, body: request.body, timeout: request.timeout
        ))
        let data = try await sendMessage(message)
        guard let reply = try? JSONDecoder().decode(ProviderMessage.Reply.self, from: data) else {
            throw ProviderMessageError.malformedReply
        }
        if let code = reply.urlError {
            throw URLError(URLError.Code(rawValue: code))
        }
        if let error = reply.error {
            throw ProviderMessageError.refused(error)
        }
        guard let status = reply.status else { throw ProviderMessageError.malformedReply }
        return HTTPResponse(status: status, headers: reply.headers ?? [:], body: reply.body ?? Data())
    }

    public func stream(_ request: HTTPRequest) async throws -> HTTPStream {
        throw ProviderMessageError.streamingUnsupported
    }

    public func upload(_ request: HTTPRequest, producing: @escaping @Sendable (OutputStream) -> Void) async throws -> HTTPStream {
        throw ProviderMessageError.streamingUnsupported
    }
}

/// The extension's end: each message one request over `transport`, its loopback.
public struct ProviderMessageHandler: Sendable {
    private let transport: any RequestTransport

    public init(transport: any RequestTransport) {
        self.transport = transport
    }

    /// The reply to one message. Never throws: a failure is a reply too.
    public func handle(_ message: Data) async -> Data {
        guard let request = try? JSONDecoder().decode(ProviderMessage.Request.self, from: message) else {
            return Self.encode(ProviderMessage.Reply(v: ProviderMessage.version, error: "not a request"))
        }
        guard request.v == ProviderMessage.version else {
            return Self.encode(ProviderMessage.Reply(v: ProviderMessage.version, error: "version \(request.v), this extension speaks \(ProviderMessage.version)"))
        }
        guard let method = HTTPMethod(rawValue: request.method) else {
            return Self.encode(ProviderMessage.Reply(v: ProviderMessage.version, error: "method \(request.method)"))
        }
        do {
            let response = try await transport.send(HTTPRequest(
                method: method, path: request.path, headers: request.headers, body: request.body, timeout: request.timeout
            ))
            return Self.encode(ProviderMessage.Reply(
                v: ProviderMessage.version, status: response.status, headers: response.headers, body: response.body
            ))
        } catch let error as URLError {
            return Self.encode(ProviderMessage.Reply(v: ProviderMessage.version, error: error.localizedDescription, urlError: error.code.rawValue))
        } catch {
            return Self.encode(ProviderMessage.Reply(v: ProviderMessage.version, error: error.localizedDescription))
        }
    }

    private static func encode(_ reply: ProviderMessage.Reply) -> Data {
        (try? JSONEncoder().encode(reply)) ?? Data()
    }
}
