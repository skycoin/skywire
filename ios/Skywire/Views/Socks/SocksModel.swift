import CoreClient
import Foundation

/// Drives SkySOCKS (Android: SocksViewModel): list the public proxies from
/// service discovery, point skysocks-client at one, start and stop it, and
/// watch what it reports. Everything hangs off the core: the local API exists
/// only while it runs, so polling runs while the screen is up and the core is
/// connected.
@MainActor
final class SocksModel: ObservableObject {
    @Published private(set) var app: AppState?
    @Published private(set) var connection: AppConnection?
    @Published private(set) var servers: [ServiceEntry]
    @Published private(set) var serversLoading = false
    @Published private(set) var serversError: String?
    @Published var query = ""
    @Published private(set) var listenPort = SocksProfile.defaultPort
    @Published private(set) var lastServer: SavedServer?
    @Published private(set) var favorites: [SavedServer]
    /// A start, stop or port change is in flight.
    @Published private(set) var busy = false
    @Published var error: String?

    /// Service discovery's type for proxies ("proxy" and "skysocks" are one
    /// family).
    static let proxyType = "proxy"
    static let pollInterval: Duration = .seconds(2)
    /// Six tries over about a minute (2, 4, 8, 16, 30 s): the list rides dmsg,
    /// which can take that long to come up after the core does.
    static let initialLoadAttempts = 6

    private let store = ServerStore(type: SocksModel.proxyType)
    private var action: Task<Void, Never>?

    init() {
        servers = store.cachedList ?? []
        favorites = store.favorites
        lastServer = store.lastServer
    }

    // MARK: What the screen shows

    /// The server the app is configured for, whether or not it runs.
    var selectedPK: String? {
        app.flatMap { SocksProfile.serverPK($0.args) } ?? lastServer?.pk
    }

    /// The selected server's country, from the list or from the saved server,
    /// but only for the same key, so a flag never belongs to another server.
    var selectedCountry: String? {
        guard let pk = selectedPK else { return nil }
        let country = servers.first { $0.pk == pk }?.geo?.country ?? (lastServer?.pk == pk ? lastServer?.country : nil)
        return country.flatMap { $0.isEmpty ? nil : $0 }
    }

    /// The selected server as the status card's star would save it.
    var selectedServer: SavedServer? {
        guard let pk = selectedPK else { return nil }
        if let entry = servers.first(where: { $0.pk == pk }) { return SavedServer(entry) }
        if let last = lastServer, last.pk == pk { return last }
        return SavedServer(pk: pk)
    }

    var running: Bool { app?.status == AppState.statusRunning }
    var starting: Bool { app?.status == AppState.statusStarting }
    var errored: Bool { app?.status == AppState.statusErrored }
    var listenAddress: String { "\(AppArgs.loopbackHost):\(listenPort)" }
    var favoriteRows: [FavoriteRow] { FavoriteRow.rows(favorites, servers, query: query) }
    var filteredServers: [ServiceEntry] { servers.filter { $0.matches(query) } }

    func isFavorite(_ pk: String) -> Bool {
        favorites.contains { $0.pk == pk }
    }

    // MARK: Loops

    /// Runs for as long as the screen is up and its key holds
    /// (`.task(id: app.connected)`).
    func run(_ app: AppModel) async {
        guard app.connected else {
            self.app = nil
            connection = nil
            return
        }
        async let list: Void = loadServers(app, attempts: Self.initialLoadAttempts)
        async let poll: Void = pollState(app)
        _ = await (list, poll)
    }

    /// The list: `attempts` tries with a growing pause, since the first tries
    /// after the core comes up routinely find no dmsg session yet. Only the
    /// last failure is shown, and a list already on screen (the cached one,
    /// or the last fetch) stays: a failed refresh is no reason to take it away.
    func loadServers(_ app: AppModel, attempts: Int = 1) async {
        serversLoading = true
        serversError = nil
        defer { serversLoading = false }
        var pause: Duration = .seconds(2)
        for attempt in 1...max(1, attempts) {
            do {
                let fetched = try await app.client.services(type: Self.proxyType)
                servers = fetched
                store.cachedList = fetched
                return
            } catch is CancellationError {
                return
            } catch {
                if app.handle(error) { return }
                if attempt == attempts {
                    serversError = error.localizedDescription
                    return
                }
                do { try await Task.sleep(for: pause) } catch { return }
                pause = min(pause * 2, .seconds(30))
            }
        }
    }

    private func pollState(_ app: AppModel) async {
        while !Task.isCancelled {
            do {
                let state = try await app.client.app(SocksProfile.app)
                // Only a running app has connections, and that summary is
                // best-effort: it must never hide the app's own state.
                let connection = state.running ? (try? await app.client.appConnections(SocksProfile.app))?.first : nil
                self.app = state
                self.connection = connection
                listenPort = SocksProfile.listenPort(state.args)
                error = nil
            } catch is CancellationError {
                return
            } catch {
                if app.handle(error) { return }
                self.error = error.localizedDescription
            }
            do { try await Task.sleep(for: Self.pollInterval) } catch { return }
        }
    }

    // MARK: Actions

    func toggleFavorite(_ server: SavedServer) {
        store.toggleFavorite(server)
        favorites = store.favorites
    }

    /// Point the app at `server` and make sure it runs.
    func connect(_ server: SavedServer, _ app: AppModel) {
        perform(app) { [self] in try await startWith(server, app) }
    }

    /// Reconnect to whatever the app is pointed at: the saved server, or the
    /// key still in its argv (a reinstall that kept the config).
    func reconnect(_ app: AppModel) {
        guard let pk = selectedPK else { return }
        connect(lastServer.flatMap { $0.pk == pk ? $0 : nil } ?? SavedServer(pk: pk), app)
    }

    func disconnect(_ app: AppModel) {
        perform(app) { [self] in
            self.app = try await app.client.updateApp(SocksProfile.app, status: CoreClient.appStop)
            connection = nil
        }
    }

    /// Moves the loopback listener. The whole argv is rewritten (the API's
    /// only shape for it), read fresh rather than from the polled snapshot.
    /// A running app is stopped around the change rather than left to the
    /// server's restart-on-args-change: that restart races the old process,
    /// whose blocked accept returns "use of closed network connection" as the
    /// app's error and leaves it stuck in Errored. Stop, rewrite, start lands
    /// on the new port every time.
    func setListenPort(_ port: Int, _ app: AppModel) {
        perform(app) { [self] in
            let current = try await app.client.app(SocksProfile.app)
            if current.running {
                _ = try? await app.client.updateApp(SocksProfile.app, status: CoreClient.appStop)
            }
            var updated = try await app.client.updateApp(SocksProfile.app, args: SocksProfile.args(current.args, withPort: port))
            if current.running {
                updated = try await app.client.updateApp(SocksProfile.app, status: CoreClient.appStart)
            }
            self.app = updated
            listenPort = SocksProfile.listenPort(updated.args)
        }
    }

    /// The transport type tried first changed (Settings' choice, reached from
    /// this screen too). It only steers route setup, so a running client is
    /// re-dialled to build its route over the new primary.
    func transportChanged(_ app: AppModel) {
        guard app.connected, running || starting, let server = selectedServer else { return }
        connect(server, app)
    }

    /// Point the app at `server` and start it. The stop is unconditional and
    /// its failure ignored: stopping a stopped app is harmless, while starting
    /// a running one is a server-side error and the polled snapshot can be a
    /// poll behind. With the app stopped, one PUT is valid in every case: the
    /// key rewrites the argv, then the status starts it with that key.
    private func startWith(_ server: SavedServer, _ app: AppModel) async throws {
        _ = try? await app.client.updateApp(SocksProfile.app, status: CoreClient.appStop)
        self.app = try await app.client.updateApp(SocksProfile.app, pk: server.pk, status: CoreClient.appStart)
        lastServer = server
        store.lastServer = server
    }

    /// One user action at a time, its failure on the screen.
    private func perform(_ app: AppModel, _ operation: @escaping @MainActor () async throws -> Void) {
        action?.cancel()
        action = Task {
            busy = true
            error = nil
            defer { busy = false }
            do {
                try await operation()
            } catch is CancellationError {
                return
            } catch {
                if !app.handle(error) {
                    self.error = error.localizedDescription
                }
            }
        }
    }
}
