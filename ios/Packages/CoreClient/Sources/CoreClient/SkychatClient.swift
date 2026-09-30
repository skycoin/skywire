import Foundation

/// skychat's own HTTP surface (127.0.0.1:8001 by default), apart from the
/// visor's API that `CoreClient` talks to. The port of Android's
/// api/SkychatApi.kt: a readiness probe the Chat tab waits on before it shows
/// the page, and the two pieces of the page's state native screens need, the
/// address book and the unread count. Everything else is the page's business.
///
/// Every request answers skychat's password gate (`SkychatProfile`) with
/// Basic auth. Failures come back as nil or empty rather than as errors, as
/// on Android: a missing name costs a shortened key on screen, a missing count
/// costs a badge, and neither is worth an error on a screen that has other
/// work to do.
public struct SkychatClient: Sendable {
    private let transport: any RequestTransport
    private let password: @Sendable () throws -> String

    /// - Parameters:
    ///   - transport: reaches skychat's origin (`SkychatProfile.baseURL`).
    ///   - password: the gate's device-local secret, read per request.
    public init(transport: any RequestTransport, password: @escaping @Sendable () throws -> String) {
        self.transport = transport
        self.password = password
    }

    /// The `Authorization` value for `password`: what the page's challenge is
    /// answered with, and what a download started outside the page carries.
    /// skychat checks the password alone; the user name is the profile's.
    public static func authorization(password: String) -> String {
        "Basic " + Data("\(SkychatProfile.user):\(password)".utf8).base64EncodedString()
    }

    /// The status of a GET on the page, or nil when nothing answered, the
    /// ordinary case while the app is still coming up. A 401 is reported, not
    /// swallowed: the running skychat loaded an older password file, which the
    /// caller fixes by restarting the app.
    public func probe() async -> Int? {
        await get("/")?.status
    }

    /// The operator's names for public keys, from skychat's address book;
    /// empty on any failure.
    public func contacts() async -> [String: String] {
        guard let response = await get("/contacts"), response.isSuccess,
              let names = try? JSONDecoder().decode([String: String].self, from: response.body)
        else { return [:] }
        return names
    }

    /// The unread estimate skychat keeps for surfaces that are not the page
    /// (the tab's badge); nil on any failure, since no badge beats a wrong one.
    public func unread() async -> Int? {
        struct Count: Decodable { let unread: Int }
        guard let response = await get("/unread"), response.isSuccess,
              let count = try? JSONDecoder().decode(Count.self, from: response.body)
        else { return nil }
        return count.unread
    }

    /// Loopback and a server that either answers at once or is not up yet: a
    /// short limit keeps a readiness poll on its own schedule.
    private static let timeout: TimeInterval = 5

    private func get(_ path: String) async -> HTTPResponse? {
        guard let secret = try? password() else { return nil }
        let request = HTTPRequest(
            path: path,
            headers: ["Authorization": Self.authorization(password: secret)],
            timeout: Self.timeout
        )
        return try? await transport.send(request)
    }
}
