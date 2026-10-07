import CoreClient
import Foundation

/// A market connected to before, offered again; the name is the operator's,
/// from the connect handshake (Android: SavedMarket).
struct SavedMarket: Codable, Equatable, Hashable {
    var pk: String
    var name = ""

    /// 8…6, the shortening the trading UI's own header uses.
    var label: String {
        let short = pk.count <= 20 ? pk : "\(pk.prefix(8))…\(pk.suffix(6))"
        return name.isEmpty ? short : "\(name) · \(short)"
    }
}

/// Drives SkyDEX (Android: DexViewModel): point skydex-client at a market,
/// start it, dial the market, and hand the screen the trading UI's address.
///
/// Two servers answer different questions. The visor (:8000) owns the app,
/// its argv and whether it runs; skydex-client's own API (:8051) owns the
/// market connection, because the engine dials only when asked. The market
/// state is polled, not remembered: the page keeps a Disconnect of its own,
/// and a header claiming "connected" over a page that is not would be worse
/// than none.
@MainActor
final class DexModel: ObservableObject {
    @Published private(set) var app: AppState?
    /// The trading UI answered here; nil while it has not.
    @Published private(set) var uiURL: URL?
    @Published private(set) var password: String?
    @Published private(set) var market: MarketStatus?
    @Published private(set) var recents: [SavedMarket]
    /// The market key field.
    @Published var entry = ""
    @Published private(set) var busy = false
    @Published var error: String?

    static let pollInterval: Duration = .seconds(2)
    /// ~15 s for the trading UI's listener, as skychat's screen waits.
    static let readyAttempts = 30
    static let recentsLimit = 5
    private static let recentsKey = "dex_recents"

    /// The field is filled from what the phone knows (the last market, or the
    /// one in the argv) only until the user has seen it once: refilling on a
    /// later poll would fight whoever is typing.
    private var prefilled = false
    private var action: Task<Void, Never>?

    init() {
        recents = UserDefaults.standard.data(forKey: Self.recentsKey)
            .flatMap { try? JSONDecoder().decode([SavedMarket].self, from: $0) } ?? []
        if let first = recents.first {
            entry = first.pk
            prefilled = true
        }
        password = try? SecretStore.app().password(.skydexPassword)
    }

    /// The page is worth showing exactly when there is a market behind it: the
    /// page's own screen before that is a second connect form, the one thing
    /// the native header exists to replace.
    var connected: Bool { uiURL != nil && market?.connected == true }
    var entryValid: Bool { SkydexProfile.isMarketPK(entry.trimmingCharacters(in: .whitespaces)) }

    func run(_ app: AppModel) async {
        guard app.connected else {
            self.app = nil
            uiURL = nil
            market = nil
            return
        }
        while !Task.isCancelled {
            do {
                let state = try await app.client.app(SkydexProfile.app)
                // One call answers both questions: an app that reports its
                // market is an app whose UI is up.
                let port = SkydexProfile.listenPort(state.args)
                let status = state.running ? await dex(port).status() : nil
                self.app = state
                market = status
                uiURL = status == nil ? nil : SkydexProfile.baseURL(port: port)
                if !prefilled, let configured = SkydexProfile.marketPK(state.args) {
                    prefilled = true
                    entry = configured
                }
            } catch is CancellationError {
                return
            } catch {
                if app.handle(error) { return }
                self.error = error.localizedDescription
            }
            do { try await Task.sleep(for: Self.pollInterval) } catch { return }
        }
    }

    func pick(_ recent: SavedMarket) {
        prefilled = true
        entry = recent.pk
        error = nil
    }

    /// Points skydex-client at the key in the field, makes sure it runs, and
    /// dials the market. The stop is unconditional and its failure ignored,
    /// SkySOCKS' rule: starting an app the visor counts as started is a 500,
    /// and the polled snapshot can be a poll behind.
    func connect(_ app: AppModel) {
        perform(app) { [self] in
            let pk = entry.trimmingCharacters(in: .whitespaces)
            guard SkydexProfile.isMarketPK(pk) else { throw DexError(L10n.text("dex_error_invalid_key")) }
            let current = try await app.client.app(SkydexProfile.app)
            _ = try? await app.client.updateApp(SkydexProfile.app, status: CoreClient.appStop)
            let started = try await app.client.updateApp(
                SkydexProfile.app,
                args: SkydexProfile.args(current.args, withMarketPK: pk),
                status: CoreClient.appStart
            )
            self.app = started
            let port = SkydexProfile.listenPort(started.args)
            let dex = dex(port)
            var up = false
            for _ in 0..<Self.readyAttempts {
                if await dex.probe() {
                    up = true
                    break
                }
                try await Task.sleep(for: .milliseconds(500))
            }
            guard up else { throw DexError(L10n.format("dex_error_no_answer", SkydexProfile.baseURL(port: port).absoluteString)) }
            let status: MarketStatus
            do {
                status = try await dex.connect(marketPK: pk)
            } catch let CoreClientError.http(_, _, code, message) {
                // The engine's own words (an unreachable market, a rejected
                // key), not the request that carried them.
                throw DexError(message.isEmpty || message == "(no body)" ? L10n.format("dex_error_connect", code) : message)
            }
            remember(SavedMarket(pk: pk, name: status.marketName))
            market = status
            uiURL = SkydexProfile.baseURL(port: port)
        }
    }

    /// Drops the market and stops the app: SkyDEX has no background duty, and
    /// a trading UI left listening would be a surface kept open for nothing.
    func disconnect(_ app: AppModel) {
        perform(app) { [self] in
            if let app = self.app {
                await dex(SkydexProfile.listenPort(app.args)).disconnect()
            }
            if let stopped = try? await app.client.updateApp(SkydexProfile.app, status: CoreClient.appStop) {
                self.app = stopped
            }
            uiURL = nil
            market = nil
        }
    }

    private func dex(_ port: Int) -> SkydexClient {
        let secret = password
        return SkydexClient(transport: LoopbackTransport(origin: SkydexProfile.origin(port: port))) {
            guard let secret else { throw DexError(L10n.text("dex_error_password")) }
            return secret
        }
    }

    private func remember(_ market: SavedMarket) {
        recents = Array(([market] + recents.filter { $0.pk != market.pk }).prefix(Self.recentsLimit))
        UserDefaults.standard.set(try? JSONEncoder().encode(recents), forKey: Self.recentsKey)
    }

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

/// An error in the screen's own words.
struct DexError: LocalizedError {
    let message: String
    init(_ message: String) { self.message = message }
    var errorDescription: String? { message }
}
