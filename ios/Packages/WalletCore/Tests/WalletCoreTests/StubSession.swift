import Foundation
import os

/// A fake node: the Swift side of the Kotlin tests' OkHttp interceptors.
/// Every request on the session it makes is answered by `handler`, which
/// sees the request and its body and returns a status and a body.
enum StubSession {
    typealias Handler = @Sendable (_ request: URLRequest, _ body: Data) throws -> (status: Int, body: String)

    fileprivate static let handlers = OSAllocatedUnfairLock<[String: Handler]>(initialState: [:])
    fileprivate static let header = "X-Stub-Session"

    /// A session answering 200 with `handler`'s body.
    static func ok(_ handler: @escaping @Sendable (_ request: URLRequest, _ body: Data) throws -> String) -> URLSession {
        make { request, body in (200, try handler(request, body)) }
    }

    static func make(_ handler: @escaping Handler) -> URLSession {
        let id = UUID().uuidString
        handlers.withLock { $0[id] = handler }
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [StubURLProtocol.self]
        config.httpAdditionalHeaders = [header: id]
        return URLSession(configuration: config)
    }
}

/// Thrown by a handler for a request the test did not expect.
struct UnexpectedRequest: Error, CustomStringConvertible {
    let description: String
    init(_ request: URLRequest) { description = "unexpected request \(request.httpMethod ?? "GET") \(request.url?.absoluteString ?? "")" }
}

private final class StubURLProtocol: URLProtocol, @unchecked Sendable {
    override class func canInit(with request: URLRequest) -> Bool { true }

    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let id = request.value(forHTTPHeaderField: StubSession.header) ?? ""
        guard let handler = StubSession.handlers.withLock({ $0[id] }), let url = request.url else {
            client?.urlProtocol(self, didFailWithError: URLError(.unsupportedURL))
            return
        }
        do {
            let (status, body) = try handler(request, Self.body(of: request))
            let response = HTTPURLResponse(url: url, statusCode: status, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "application/json"])!
            client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: Data(body.utf8))
            client?.urlProtocolDidFinishLoading(self)
        } catch {
            client?.urlProtocol(self, didFailWithError: error)
        }
    }

    override func stopLoading() {}

    /// A body set on the request reaches a protocol as a stream.
    private static func body(of request: URLRequest) -> Data {
        if let data = request.httpBody { return data }
        guard let stream = request.httpBodyStream else { return Data() }
        stream.open()
        defer { stream.close() }
        var data = Data()
        var buffer = [UInt8](repeating: 0, count: 4096)
        while stream.hasBytesAvailable {
            let n = stream.read(&buffer, maxLength: buffer.count)
            if n <= 0 { break }
            data.append(buffer, count: n)
        }
        return data
    }
}
