import Foundation

/// The HTTP path the node clients share: URLSession where Kotlin has OkHttp.
/// Keys never travel on it; the bodies are balances, histories and signed
/// transactions.
struct HttpClient: Sendable {
    let session: URLSession

    struct Response {
        let status: Int
        let body: String
        var isSuccessful: Bool { (200..<300).contains(status) }
    }

    func get(_ url: URL) async throws -> Response {
        try await send(URLRequest(url: url))
    }

    func post(_ url: URL, body: Data, contentType: String, headers: [String: String] = [:]) async throws -> Response {
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.httpBody = body
        request.setValue(contentType, forHTTPHeaderField: "Content-Type")
        for (name, value) in headers { request.setValue(value, forHTTPHeaderField: name) }
        return try await send(request)
    }

    private func send(_ request: URLRequest) async throws -> Response {
        let (data, response) = try await session.data(for: request)
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        return Response(status: status, body: String(decoding: data, as: UTF8.self))
    }
}

/// A node's base URL as the Kotlin clients take it: trimmed, trailing
/// slashes dropped, http or https with a host — else nil.
func parseBaseURL(_ text: String) -> URL? {
    var t = text.trimmingCharacters(in: .whitespacesAndNewlines)
    while t.hasSuffix("/") { t.removeLast() }
    guard let url = URL(string: t),
          let scheme = url.scheme?.lowercased(), scheme == "http" || scheme == "https",
          let host = url.host, !host.isEmpty
    else { return nil }
    return url
}

/// base + "/" + path segments, with query parameters — OkHttp's
/// newBuilder().addPathSegments(path).addQueryParameter(…).
func endpoint(_ base: URL, _ path: String, _ query: [(String, String)] = []) -> URL {
    var components = URLComponents(url: base, resolvingAgainstBaseURL: false)!
    var basePath = components.path
    while basePath.hasSuffix("/") { basePath.removeLast() }
    components.path = basePath + "/" + path
    if !query.isEmpty {
        components.queryItems = query.map { URLQueryItem(name: $0.0, value: $0.1) }
    }
    return components.url!
}

/// The server's words, cut to 300 characters, or "<who> answered HTTP <code>".
func serverMessage(_ body: String, status: Int, who: String) -> String {
    let t = body.trimmingCharacters(in: .whitespacesAndNewlines)
    return t.isEmpty ? "\(who) answered HTTP \(status)" : String(t.prefix(300))
}

/// Decoding helpers for the Kotlin models' defaults (a missing key takes the
/// declared default, as kotlinx.serialization does).
extension KeyedDecodingContainer {
    func value<T: Decodable>(_ key: Key, default value: T) throws -> T {
        try decodeIfPresent(T.self, forKey: key) ?? value
    }

    /// A string field that some servers send as a number (kotlinx's lenient
    /// mode takes either).
    func lenientString(_ key: Key, default value: String) throws -> String {
        if let s = try? decodeIfPresent(String.self, forKey: key) { return s }
        if let n = try? decodeIfPresent(UInt64.self, forKey: key) { return String(n) }
        if let n = try? decodeIfPresent(Int64.self, forKey: key) { return String(n) }
        if let n = try? decodeIfPresent(Double.self, forKey: key) { return String(n) }
        return value
    }
}
