import Foundation
import Network
import os

/// A small HTTP/1.1 server on 127.0.0.1 (a free port), inside the test
/// process: one request per connection, answered by `handler`, then closed.
/// Enough of HTTP for the loopback transport and for the replies the visor's
/// API gives, including a streamed body that stays open until the client
/// leaves.
final class StubServer: Sendable {
    struct Request: Sendable {
        let method: String
        /// Path and query as sent.
        let target: String
        /// Names lowercased.
        let headers: [String: String]
        let body: Data

        var path: String { String(target.prefix { $0 != "?" }) }

        func header(_ name: String) -> String? { headers[name.lowercased()] }

        /// The body as a JSON object, or nil.
        var jsonObject: [String: Any]? {
            try? JSONSerialization.jsonObject(with: body) as? [String: Any]
        }
    }

    enum Reply: Sendable {
        case response(status: Int, headers: [String: String], body: Data)
        /// The head, then each chunk `interval` apart; with `close` the body
        /// ends after the last chunk, without it the connection stays open
        /// until the client closes it.
        case stream(status: Int, headers: [String: String], chunks: [Data], interval: Duration, close: Bool)
        /// Never answers; the client has to give up.
        case silence
    }

    typealias Handler = @Sendable (Request) async -> Reply

    private struct State: Sendable {
        var requests: [Request] = []
        var clientClosedStreams = 0
    }

    let port: UInt16
    private let listener: NWListener
    private let queue = DispatchQueue(label: "StubServer")
    private let handler: Handler
    private let state = OSAllocatedUnfairLock(initialState: State())

    var origin: URL { URL(string: "http://127.0.0.1:\(port)")! }

    /// Every request received, in order.
    var requests: [Request] { state.withLock { $0.requests } }

    /// Streams (and silences) whose client closed the connection.
    var clientClosedStreams: Int { state.withLock { $0.clientClosedStreams } }

    private init(listener: NWListener, port: UInt16, handler: @escaping Handler) {
        self.listener = listener
        self.port = port
        self.handler = handler
    }

    static func start(handler: @escaping Handler) async throws -> StubServer {
        let parameters = NWParameters.tcp
        parameters.requiredLocalEndpoint = .hostPort(host: "127.0.0.1", port: .any)
        let listener = try NWListener(using: parameters)
        let queue = DispatchQueue(label: "StubServer.listener")
        let port: UInt16 = try await withCheckedThrowingContinuation { continuation in
            let resumed = OSAllocatedUnfairLock(initialState: false)
            let claim: @Sendable () -> Bool = { resumed.withLock { done in
                defer { done = true }
                return !done
            } }
            listener.stateUpdateHandler = { state in
                switch state {
                case .ready:
                    if claim() { continuation.resume(returning: listener.port?.rawValue ?? 0) }
                case let .failed(error):
                    if claim() { continuation.resume(throwing: error) }
                default:
                    break
                }
            }
            // Accepts nothing until the server object exists (below).
            listener.newConnectionHandler = { $0.cancel() }
            listener.start(queue: queue)
        }
        let server = StubServer(listener: listener, port: port, handler: handler)
        listener.newConnectionHandler = { [server] connection in server.accept(connection) }
        return server
    }

    func stop() {
        listener.cancel()
    }

    private func accept(_ connection: NWConnection) {
        connection.start(queue: queue)
        receive(connection, buffer: Data())
    }

    private func receive(_ connection: NWConnection, buffer: Data) {
        connection.receive(minimumIncompleteLength: 1, maximumLength: 1 << 16) { [self] data, _, isComplete, error in
            var buffer = buffer
            if let data { buffer.append(data) }
            if let request = Self.parse(buffer) {
                state.withLock { $0.requests.append(request) }
                Task { await self.reply(to: request, on: connection) }
            } else if isComplete || error != nil {
                connection.cancel()
            } else {
                receive(connection, buffer: buffer)
            }
        }
    }

    private func reply(to request: Request, on connection: NWConnection) async {
        switch await handler(request) {
        case let .response(status, headers, body):
            var head = headers
            head["Content-Length"] = "\(body.count)"
            connection.send(
                content: Self.head(status, head) + body,
                contentContext: .finalMessage,
                isComplete: true,
                completion: .contentProcessed { _ in connection.cancel() }
            )
        case let .stream(status, headers, chunks, interval, close):
            // Chunked, as Go's net/http frames a flushed response of unknown
            // length (the visor's SSE and audio routes).
            watchForClientClose(connection)
            var head = headers
            head["Transfer-Encoding"] = "chunked"
            connection.send(content: Self.head(status, head), completion: .contentProcessed { _ in })
            for chunk in chunks {
                try? await Task.sleep(for: interval)
                connection.send(content: Self.chunk(chunk), completion: .contentProcessed { _ in })
            }
            if close {
                connection.send(content: Self.chunk(Data()), contentContext: .finalMessage, isComplete: true, completion: .contentProcessed { _ in
                    connection.cancel()
                })
            }
        case .silence:
            watchForClientClose(connection)
        }
    }

    /// Counts the client's close, the only way a kept-open reply ends.
    private func watchForClientClose(_ connection: NWConnection) {
        connection.receive(minimumIncompleteLength: 1, maximumLength: 1 << 16) { [self] _, _, isComplete, error in
            if isComplete || error != nil {
                state.withLock { $0.clientClosedStreams += 1 }
                connection.cancel()
            } else {
                watchForClientClose(connection)
            }
        }
    }

    /// One chunk of a chunked body; an empty one ends the body.
    private static func chunk(_ data: Data) -> Data {
        Data((String(data.count, radix: 16) + "\r\n").utf8) + data + Data("\r\n".utf8)
    }

    private static func head(_ status: Int, _ headers: [String: String]) -> Data {
        var lines = ["HTTP/1.1 \(status) \(HTTPURLResponse.localizedString(forStatusCode: status))"]
        for (name, value) in headers.sorted(by: { $0.key < $1.key }) {
            lines.append("\(name): \(value)")
        }
        lines.append("Connection: close")
        return Data((lines.joined(separator: "\r\n") + "\r\n\r\n").utf8)
    }

    /// A complete request in `buffer`, or nil while it is still arriving.
    private static func parse(_ buffer: Data) -> Request? {
        guard let end = buffer.range(of: Data("\r\n\r\n".utf8)) else { return nil }
        let lines = String(decoding: buffer[..<end.lowerBound], as: UTF8.self).components(separatedBy: "\r\n")
        let requestLine = lines[0].split(separator: " ")
        guard requestLine.count >= 2 else { return nil }
        var headers: [String: String] = [:]
        for line in lines.dropFirst() {
            guard let colon = line.firstIndex(of: ":") else { continue }
            headers[line[..<colon].lowercased()] = line[line.index(after: colon)...].trimmingCharacters(in: .whitespaces)
        }
        let length = Int(headers["content-length"] ?? "0") ?? 0
        let body = buffer[end.upperBound...]
        guard body.count >= length else { return nil }
        return Request(
            method: String(requestLine[0]),
            target: String(requestLine[1]),
            headers: headers,
            body: Data(body.prefix(length))
        )
    }
}
