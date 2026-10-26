import Foundation
import os

/// Walks a coin's node addresses in their stored order (Android: NodeFailover in
/// WalletRepository.kt). Earlier is higher priority, and a transport failure, a 429 or a 5xx
/// hands the request to the next node. A node that just failed is skipped for `skipSeconds`, so a
/// dead first choice costs one connect timeout and not one per request; a success puts it back.
///
/// Only requests to the first node's host move: the Ethereum family's indexer rides the same
/// client at its own address and must stay there.
public struct NodeFailover: Sendable {
    public static let skipSeconds: TimeInterval = 30

    let bases: [URL]

    public init(_ bases: [String]) {
        self.bases = bases.compactMap(parseBaseURL)
    }

    /// Shared across instances: the app builds a core per call, and a node's failure has to outlive it.
    private static let failures = OSAllocatedUnfairLock<[String: Date]>(initialState: [:])

    func send(_ request: URLRequest, _ attempt: (URLRequest) async throws -> HttpClient.Response) async throws -> HttpClient.Response {
        guard bases.count > 1, let primary = bases.first, let url = request.url, url.host == primary.host,
              let parts = URLComponents(url: url, resolvingAgainstBaseURL: false)
        else { return try await attempt(request) }
        // The request was built on the first node's base path; each node gets its own.
        let segments = Self.segments(parts.percentEncodedPath)
        let prefix = Self.baseSegments(primary)
        let tail = segments.starts(with: prefix) ? Array(segments.dropFirst(prefix.count)) : segments
        let now = Date()
        let recent = Self.failures.withLock { $0 }
        let live = bases.filter { recent[$0.absoluteString].map { now.timeIntervalSince($0) > Self.skipSeconds } ?? true }
        let order = live.isEmpty ? bases : live
        var last: Error?
        for (i, base) in order.enumerated() {
            var shifted = request
            shifted.url = Self.rebase(base, tail: tail, query: parts.percentEncodedQuery)
            do {
                let response = try await attempt(shifted)
                // A throttled or failing node is as unusable as a dead one, unless it is the last.
                if (response.status == 429 || response.status >= 500) && i < order.count - 1 {
                    Self.failures.withLock { $0[base.absoluteString] = Date() }
                    continue
                }
                _ = Self.failures.withLock { $0.removeValue(forKey: base.absoluteString) }
                return response
            } catch let error as URLError where error.code != .cancelled {
                Self.failures.withLock { $0[base.absoluteString] = Date() }
                last = error
            }
        }
        throw last ?? URLError(.cannotConnectToHost)
    }

    /// The path's segments as OkHttp's encodedPathSegments gives them: a trailing slash is an empty one.
    private static func segments(_ path: String) -> [String] {
        Array(path.split(separator: "/", omittingEmptySubsequences: false).dropFirst().map(String.init))
    }

    private static func baseSegments(_ base: URL) -> [String] {
        segments(URLComponents(url: base, resolvingAgainstBaseURL: false)?.percentEncodedPath ?? "").filter { !$0.isEmpty }
    }

    private static func rebase(_ base: URL, tail: [String], query: String?) -> URL? {
        guard var parts = URLComponents(url: base, resolvingAgainstBaseURL: false) else { return nil }
        parts.percentEncodedPath = "/" + (baseSegments(base) + tail).joined(separator: "/")
        parts.percentEncodedQuery = query
        return parts.url
    }
}
