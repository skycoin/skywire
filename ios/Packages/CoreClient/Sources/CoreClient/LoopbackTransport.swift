import Foundation
import os

/// The API over plain HTTP to the visor's loopback listener, 127.0.0.1:8000
/// (`ConfigProfile.apiAddress` in the app). The only file in CoreClient that
/// uses URLSession.
///
/// The sessions keep no cookies and no cache: CoreClient owns the session
/// cookie (another transport has no cookie store to lean on), and nothing the
/// API answers is worth reusing.
public final class LoopbackTransport: RequestTransport {
    /// `http://127.0.0.1:8000`: scheme, host and port, no path.
    public let origin: URL

    private let session: URLSession
    private let streamSession: URLSession

    public init(origin: URL) {
        self.origin = origin
        session = URLSession(configuration: Self.configuration(resourceTimeout: Self.exchangeLimit))
        streamSession = URLSession(configuration: Self.configuration(resourceTimeout: Self.streamLimit))
    }

    public func send(_ request: HTTPRequest) async throws -> HTTPResponse {
        let urlRequest = try makeURLRequest(request)
        do {
            return try await exchange(urlRequest)
        } catch let error as URLError where error.code == .networkConnectionLost && request.method == .get {
            // A connection that died before it answered: the core restarted
            // under a request in flight. A read can be asked again; a write is
            // left to the caller, which knows whether repeating it is safe.
            return try await exchange(urlRequest)
        }
    }

    public func stream(_ request: HTTPRequest) async throws -> HTTPStream {
        let task = streamSession.dataTask(with: try makeURLRequest(request))
        let relay = StreamRelay(task: task)
        task.delegate = relay
        return try await withTaskCancellationHandler {
            try await relay.head()
        } onCancel: {
            task.cancel()
        }
    }

    private func exchange(_ request: URLRequest) async throws -> HTTPResponse {
        let (data, response) = try await session.data(for: request)
        guard let http = response as? HTTPURLResponse else { throw URLError(.badServerResponse) }
        return HTTPResponse(status: http.statusCode, headers: Self.headers(of: http), body: data)
    }

    private func makeURLRequest(_ request: HTTPRequest) throws -> URLRequest {
        guard let url = URL(string: origin.absoluteString + request.path) else { throw URLError(.badURL) }
        var urlRequest = URLRequest(url: url)
        urlRequest.httpMethod = request.method.rawValue
        urlRequest.httpBody = request.body
        urlRequest.timeoutInterval = request.timeout ?? Self.streamLimit
        for (name, value) in request.headers {
            urlRequest.setValue(value, forHTTPHeaderField: name)
        }
        // A fresh connection per request. A kept-alive one outlives a core
        // restart, and the first request after it fails on the dead socket
        // (seen in M1). On loopback a new connection costs nothing.
        urlRequest.setValue("close", forHTTPHeaderField: "Connection")
        return urlRequest
    }

    static func headers(of response: HTTPURLResponse) -> [String: String] {
        var headers: [String: String] = [:]
        for (name, value) in response.allHeaderFields {
            if let name = name as? String {
                headers[name] = "\(value)"
            }
        }
        return headers
    }

    private static func configuration(resourceTimeout: TimeInterval) -> URLSessionConfiguration {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.httpCookieStorage = nil
        configuration.httpShouldSetCookies = false
        configuration.httpCookieAcceptPolicy = .never
        configuration.urlCache = nil
        configuration.requestCachePolicy = .reloadIgnoringLocalCacheData
        configuration.waitsForConnectivity = false
        configuration.timeoutIntervalForResource = resourceTimeout
        return configuration
    }

    /// Longer than any request's own timeout (svc-fetch's is the longest).
    private static let exchangeLimit: TimeInterval = 120
    /// A stream ends when its reader ends it, not on a clock.
    private static let streamLimit: TimeInterval = 60 * 60 * 24 * 365
}

/// Carries one streaming task's callbacks into `HTTPStream`: the response
/// head resumes `head()`, each chunk is yielded to the body stream, and the
/// task's end finishes it. The task holds its delegate until it completes.
private final class StreamRelay: NSObject, URLSessionDataDelegate, @unchecked Sendable {
    private struct State: Sendable {
        var head: CheckedContinuation<HTTPStream, any Error>?
        var body: AsyncThrowingStream<Data, any Error>.Continuation?
    }

    // @unchecked Sendable above: `task` is immutable and URLSessionTask is
    // thread-safe; the rest of the state is behind this lock.
    private let state = OSAllocatedUnfairLock(initialState: State())
    private let task: URLSessionDataTask

    init(task: URLSessionDataTask) {
        self.task = task
    }

    func head() async throws -> HTTPStream {
        try await withCheckedThrowingContinuation { continuation in
            state.withLock { $0.head = continuation }
            task.resume()
        }
    }

    func urlSession(
        _ session: URLSession,
        dataTask: URLSessionDataTask,
        didReceive response: URLResponse,
        completionHandler: @escaping @Sendable (URLSession.ResponseDisposition) -> Void
    ) {
        guard let http = response as? HTTPURLResponse else {
            // didCompleteWithError reports it to head().
            completionHandler(.cancel)
            return
        }
        let (body, continuation) = AsyncThrowingStream<Data, any Error>.makeStream()
        let task = self.task
        continuation.onTermination = { termination in
            // The reader went away (cancelled, or dropped the stream): close
            // the connection, or a server that keeps it open never lets go.
            if case .cancelled = termination {
                task.cancel()
            }
        }
        let head = state.withLock { state in
            state.body = continuation
            defer { state.head = nil }
            return state.head
        }
        completionHandler(.allow)
        head?.resume(returning: HTTPStream(
            status: http.statusCode,
            headers: LoopbackTransport.headers(of: http),
            body: body
        ))
    }

    func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
        _ = state.withLock { $0.body }?.yield(data)
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: (any Error)?) {
        let (head, body) = state.withLock { state in
            defer {
                state.head = nil
                state.body = nil
            }
            return (state.head, state.body)
        }
        head?.resume(throwing: error ?? URLError(.badServerResponse))
        if let error {
            body?.finish(throwing: error)
        } else {
            body?.finish()
        }
    }
}
