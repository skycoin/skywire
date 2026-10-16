import CoreClient
import Foundation

/// A server the user picked or starred, as it was then: its key, and where it
/// was and what it ran, so a favorite still says where it is when discovery
/// does not list it at the moment (Android: SavedServer).
struct SavedServer: Codable, Equatable, Sendable {
    var pk: String
    var country = ""
    var version = ""

    init(pk: String, country: String = "", version: String = "") {
        self.pk = pk
        self.country = country
        self.version = version
    }

    init(_ entry: ServiceEntry) {
        self.init(pk: entry.pk, country: entry.geo?.country ?? "", version: entry.version)
    }

    /// The entry a list row draws, for a favorite discovery does not list.
    var entry: ServiceEntry {
        ServiceEntry(address: pk, geo: GeoInfo(country: country), version: version)
    }
}

/// The phone's memory of public servers, one set per service-discovery type
/// (SkySOCKS' proxies now, SkyVPN's exits in M7), kept across launches
/// (Android: FavoriteServers, ServerListCache, the SOCKS screen's last
/// server).
///
/// - Favorites: finding a fast server is the slow part, and nothing in the
///   list says which one is quick from here; a star keeps one at the top.
/// - The last list: it rides dmsg and takes seconds to arrive, so the screen
///   opens on the last one and the fetch only refreshes it.
/// - The last server: what Reconnect dials.
@MainActor
struct ServerStore {
    let type: String
    var defaults: UserDefaults = .standard

    var favorites: [SavedServer] {
        get { read([SavedServer].self, "favorites_\(type)") ?? [] }
        nonmutating set { write(newValue, "favorites_\(type)") }
    }

    /// Stars `server` if it is not a favorite, unstars it if it is; returns
    /// whether it now is.
    @discardableResult
    func toggleFavorite(_ server: SavedServer) -> Bool {
        let current = favorites
        let starring = !current.contains { $0.pk == server.pk }
        favorites = starring ? current + [server] : current.filter { $0.pk != server.pk }
        return starring
    }

    var cachedList: [ServiceEntry]? {
        get { read([ServiceEntry].self, "servers_cache_\(type)") }
        nonmutating set { write(newValue, "servers_cache_\(type)") }
    }

    var lastServer: SavedServer? {
        get { read(SavedServer.self, "last_server_\(type)") }
        nonmutating set { write(newValue, "last_server_\(type)") }
    }

    private func read<T: Decodable>(_ type: T.Type, _ key: String) -> T? {
        guard let data = defaults.data(forKey: key) else { return nil }
        return try? JSONDecoder().decode(T.self, from: data)
    }

    private func write<T: Encodable>(_ value: T?, _ key: String) {
        guard let value, let data = try? JSONEncoder().encode(value) else {
            defaults.removeObject(forKey: key)
            return
        }
        defaults.set(data, forKey: key)
    }
}

/// One row of the favorites section: the server as discovery lists it now
/// (its current location and version), or as it was saved when discovery
/// does not list it (`listed` false).
struct FavoriteRow: Equatable {
    let entry: ServiceEntry
    let listed: Bool

    /// The favorites in the order they were starred, filtered by the search.
    static func rows(_ favorites: [SavedServer], _ servers: [ServiceEntry], query: String) -> [FavoriteRow] {
        favorites
            .map { saved in
                servers.first { $0.pk == saved.pk }.map { FavoriteRow(entry: $0, listed: true) }
                    ?? FavoriteRow(entry: saved.entry, listed: false)
            }
            .filter { $0.entry.matches(query) }
    }
}

extension ServiceEntry {
    /// The search box's rule, shared by the list and the favorites: the key,
    /// the country, the region or the version contains the query.
    func matches(_ query: String) -> Bool {
        let query = query.trimmingCharacters(in: .whitespaces)
        guard !query.isEmpty else { return true }
        return [pk, geo?.country ?? "", geo?.region ?? "", version].contains { $0.localizedCaseInsensitiveContains(query) }
    }
}
