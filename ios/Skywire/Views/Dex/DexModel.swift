import CoreClient
import Foundation
import WebKit

/// A market connected to before, offered again; the name is the operator's,
/// from the connect handshake (Android: SavedMarket).
struct SavedMarket: Codable, Equatable, Hashable {
    var pk: String
    var name = ""
    var ts: Int64 = 0

    init(pk: String, name: String = "", ts: Int64 = 0) {
        self.pk = pk
        self.name = name
        self.ts = ts
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        pk = try c.decode(String.self, forKey: .pk)
        name = (try? c.decodeIfPresent(String.self, forKey: .name)) ?? ""
        ts = (try? c.decodeIfPresent(Int64.self, forKey: .ts)) ?? 0
    }

    /// 8…6, the shortening the trading UI's own header uses.
    static func short(_ pk: String) -> String {
        pk.count <= 20 ? pk : "\(pk.prefix(8))…\(pk.suffix(6))"
    }
}

/// What SkyDEX keeps on this phone (Android: dex/DexStore.kt): the addresses to
/// pre-fill, and finished trades the market deletes after its cleanup window.
struct DexLocal: Codable, Equatable {
    /// "SKY" plus one payout address per payment coin, as last registered.
    var wallets: [String: String] = [:]
    var history: [DexOrder] = []
    var legacyImported = false

    var registered: Bool { !(wallets[DexModel.sky] ?? "").trimmingCharacters(in: .whitespaces).isEmpty }

    init() {}

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        wallets = (try? c.decodeIfPresent([String: String].self, forKey: .wallets)) ?? [:]
        history = (try? c.decodeIfPresent([DexOrder].self, forKey: .history)) ?? []
        legacyImported = (try? c.decodeIfPresent(Bool.self, forKey: .legacyImported)) ?? false
    }
}

enum DexTab: CaseIterable {
    case market, orders, listings, history, settings
}

/// The outcome under the Settings form: saved, or what went wrong.
struct WalletsMessage: Equatable {
    let ok: Bool
    let text: String
}

/// Drives SkyDEX (Android: ui/dex/DexViewModel.kt): point skydex-client at a
/// market, start it, dial the market, then trade through skydex-client's
/// control API, which passes every call to the market over dmsg.
///
/// Two servers answer different questions. The visor (:8000) owns the app, its
/// argv and whether it runs; skydex-client's own API (:8051) owns the market
/// connection. The market is the source of truth for orders; what the web page
/// kept in localStorage lives in DexLocal.
@MainActor
final class DexModel: ObservableObject {
    @Published private(set) var app: AppState?
    /// skydex-client's control API answered here; nil while it has not.
    @Published private(set) var uiURL: URL?
    @Published private(set) var market: MarketStatus?
    @Published private(set) var recents: [SavedMarket]
    /// The market key field.
    @Published var entry = ""
    @Published private(set) var busy = false
    @Published var error: String?

    @Published private(set) var tab = DexTab.market
    @Published private(set) var currencies: [String] = []
    @Published private(set) var sellCoins: [String] = []
    /// Whether the market has a sell coin enabled; nil until it has answered.
    @Published private(set) var ready: Bool?
    /// Nil until the first answer, so "nothing yet" is not "nothing at all".
    @Published private(set) var products: [DexProduct]?
    @Published private(set) var orders: [DexOrder]?
    /// Live get_order_status answers for in-flight buys, by order id.
    @Published private(set) var statuses: [String: DexOrderStatus] = [:]
    @Published private(set) var listings: [DexListing]?
    /// The last poll of the open tab that failed; the next good one clears it.
    @Published private(set) var tradeError: String?
    /// Why the last buy, listing or cancel was refused; kept until the next action or tab.
    @Published private(set) var actionError: String?
    @Published private(set) var deposit: DexCreatedListing?
    @Published private(set) var payment: DexPurchase?
    /// A buy or a new listing is in flight.
    @Published private(set) var working = false
    /// The order or listing whose cancel is in flight.
    @Published private(set) var busyId: String?
    @Published private(set) var savingWallets = false
    @Published private(set) var walletsMessage: WalletsMessage?
    @Published private(set) var local: DexLocal
    @Published var notice: String?

    nonisolated static let sky = "SKY"
    static let pollInterval: Duration = .seconds(2)
    /// ~15 s for the control API's listener, as skychat's screen waits.
    static let readyAttempts = 30
    static let recentsLimit = 12
    nonisolated static let tradeInterval: Duration = .seconds(5)
    nonisolated static let historyInterval: Duration = .seconds(8)
    nonisolated static let readinessInterval: Duration = .seconds(8)
    static let redialGap: TimeInterval = 20
    private static let recentsKey = "dex_recents"

    private var prefilled = false
    private var action: Task<Void, Never>?
    private var lastRedial = Date.distantPast
    private let password: String?
    private let storeFile: URL

    init() {
        recents = UserDefaults.standard.data(forKey: Self.recentsKey)
            .flatMap { try? JSONDecoder().decode([SavedMarket].self, from: $0) } ?? []
        password = try? SecretStore.app().password(.skydexPassword)
        storeFile = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("skydex", isDirectory: true).appendingPathComponent("state.json")
        local = (try? JSONDecoder().decode(DexLocal.self, from: Data(contentsOf: storeFile))) ?? DexLocal()
        if let first = recents.first {
            entry = first.pk
            prefilled = true
        }
    }

    /// The trading screens show exactly when there is a market behind them.
    var connected: Bool { uiURL != nil && market?.connected == true }
    var entryValid: Bool { SkydexProfile.isMarketPK(entry.trimmingCharacters(in: .whitespaces)) }

    // MARK: The connection

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
                // One call answers both questions: an app that reports its market is an app whose API is up.
                let port = SkydexProfile.listenPort(state.args)
                let status = state.running ? await dex(port).status() : nil
                self.app = state
                if let status, status.marketName.isEmpty, status.marketPK == market?.marketPK {
                    var merged = status
                    merged.marketName = market?.marketName ?? ""
                    market = merged
                } else {
                    market = status
                }
                uiURL = status == nil ? nil : SkydexProfile.origin(port: port)
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

    func setEntry(_ value: String) {
        prefilled = true
        entry = value
        error = nil
    }

    func selectTab(_ next: DexTab) {
        tab = next
        tradeError = nil
        actionError = nil
        walletsMessage = nil
    }

    func connectTo(_ saved: SavedMarket, _ app: AppModel) {
        setEntry(saved.pk)
        connect(app)
    }

    func removeRecent(_ saved: SavedMarket) {
        writeRecents(recents.filter { $0.pk != saved.pk })
    }

    func clearRecents() {
        writeRecents([])
    }

    /// Points skydex-client at the key in the field, makes sure it runs, and
    /// dials the market. The stop is unconditional and its failure ignored,
    /// SkySOCKS' rule: starting an app the visor counts as started is a 500.
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
            await importLegacy(SkydexProfile.origin(port: port))
            let status: MarketStatus
            do {
                status = try await dex.connect(marketPK: pk)
            } catch let CoreClientError.http(_, _, code, message) {
                // The engine's own words (an unreachable market, a rejected key).
                throw DexError(message.isEmpty || message == "(no body)" ? L10n.format("dex_error_connect", code) : message)
            }
            saveRecent(SavedMarket(pk: pk, name: status.marketName, ts: Self.nowMs()))
            market = status
            uiURL = SkydexProfile.origin(port: port)
            tab = .market
        }
    }

    /// Drops the market and stops the app: SkyDEX has no background duty, and
    /// a trading API left listening would be a surface kept open for nothing.
    func disconnect(_ app: AppModel) {
        perform(app) { [self] in
            if let state = self.app {
                await dex(SkydexProfile.listenPort(state.args)).disconnect()
            }
            if let stopped = try? await app.client.updateApp(SkydexProfile.app, status: CoreClient.appStop) {
                self.app = stopped
            }
            uiURL = nil
            market = nil
        }
    }

    // MARK: Trading

    func buy(_ product: DexProduct) {
        trade { [self] in
            guard local.registered else { return needRegister() }
            payment = try await client().buy(product.id)
            try await refreshProducts()
        }
    }

    func createListing(sellCoin: String, amount: Double, price: Double, paymentCurrency: String, onDone: @escaping () -> Void) {
        trade { [self] in
            guard local.registered else { return needRegister() }
            deposit = try await client().createListing(sellCoin: sellCoin, amount: amount, price: price, paymentCurrency: paymentCurrency)
            onDone()
        }
    }

    func cancelOrder(_ order: DexOrder) {
        cancel(order.id) { [self] in
            let reply = try await client().cancelOrder(order.id)
            notice = reply.isEmpty ? L10n.text("dex_order_canceled") : reply
            try await refreshOrders()
        }
    }

    func cancelListing(_ listing: DexListing) {
        cancel(listing.id) { [self] in
            let reply = try await client().cancelListing(listing.id)
            notice = reply.isEmpty ? L10n.text("dex_offer_canceled") : reply
            listings = try await client().listings()
        }
    }

    func dismissDeposit() { deposit = nil }

    func dismissPayment() { payment = nil }

    /// Registers the addresses with the market, then remembers them here (Settings.jsx).
    func saveWallets(sky: String, payouts: [String: String]) {
        let skyAddress = sky.trimmingCharacters(in: .whitespaces)
        guard !skyAddress.isEmpty else {
            walletsMessage = WalletsMessage(ok: false, text: L10n.text("dex_sky_required"))
            return
        }
        let wallets = payouts.mapValues { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.value.isEmpty }
        Task {
            savingWallets = true
            walletsMessage = nil
            defer { savingWallets = false }
            do {
                try await client().register(walletSky: skyAddress, wallets: wallets)
                updateLocal { $0.wallets = wallets.merging([Self.sky: skyAddress]) { _, new in new } }
                walletsMessage = WalletsMessage(ok: true, text: L10n.text("dex_wallets_saved"))
            } catch {
                walletsMessage = WalletsMessage(ok: false, text: error.localizedDescription)
            }
        }
    }

    func removeHistory(_ order: DexOrder) {
        updateLocal { $0.history.removeAll { $0.id == order.id && $0.type == order.type } }
    }

    func clearHistory() {
        updateLocal { $0.history = [] }
    }

    /// Polls what the open tab shows, plus the market's readiness, for as long
    /// as the screen shows this market (the page's timers ran only while it was open).
    func tradeLoop() async {
        products = nil
        orders = nil
        listings = nil
        statuses = [:]
        ready = nil
        tradeError = nil
        await withTaskGroup(of: Void.self) { group in
            group.addTask {
                while !Task.isCancelled {
                    await self.refreshReadiness()
                    try? await Task.sleep(for: Self.readinessInterval)
                }
            }
            group.addTask {
                while !Task.isCancelled {
                    let tab = await self.tab
                    await self.refreshTab(tab)
                    // Sleeps in slices so a tab switch refreshes at once.
                    let interval = tab == .history ? DexModel.historyInterval : DexModel.tradeInterval
                    var waited: Duration = .zero
                    while waited < interval, !Task.isCancelled, await self.tab == tab {
                        try? await Task.sleep(for: .milliseconds(250))
                        waited += .milliseconds(250)
                    }
                }
            }
        }
    }

    private func refreshTab(_ tab: DexTab) async {
        do {
            switch tab {
            case .market: try await refreshProducts()
            case .orders, .history: try await refreshOrders()
            case .listings:
                listings = try await client().listings()
                tradeError = nil
            case .settings: break
            }
        } catch is CancellationError {
        } catch {
            tradeError = await redial(error) ? nil : error.localizedDescription
        }
    }

    private func refreshReadiness() async {
        guard let c = try? await client().currencies() else { return }
        sellCoins = c.sellCoins
        currencies = c.currencies
        ready = !c.sellCoins.isEmpty
        let pk = market?.marketPK ?? ""
        if !c.marketName.isEmpty, !pk.isEmpty {
            market?.marketName = c.marketName
            let known = recents.first { $0.pk == pk }
            if known?.name != c.marketName { saveRecent(SavedMarket(pk: pk, name: c.marketName, ts: known?.ts ?? Self.nowMs())) }
        }
    }

    private func refreshProducts() async throws {
        products = try await client().products()
        tradeError = nil
    }

    /// Orders, the live status of in-flight buys, and finished ones folded into History.
    private func refreshOrders() async throws {
        let dex = try client()
        var seen = Set<String>()
        // A self-trade comes back twice under one id, once as the buy and once as the sell.
        let list = try await dex.orders().filter { seen.insert($0.type + ":" + $0.id).inserted }
        let tracked = list.filter { $0.type == "buy" && DexFormat.inFlight.contains($0.status) }
        var live: [String: DexOrderStatus] = [:]
        await withTaskGroup(of: DexOrderStatus?.self) { group in
            for o in tracked { group.addTask { try? await dex.orderStatus(o.id) } }
            for await status in group {
                if let status { live[status.orderId] = status }
            }
        }
        // A live status only while its order is in flight; after that the list is the truth.
        let trackedIds = Set(tracked.map(\.id))
        orders = list
        statuses = statuses.filter { trackedIds.contains($0.key) }.merging(live) { _, new in new }
        tradeError = nil
        let pk = market?.marketPK ?? ""
        let name = market?.marketName ?? ""
        updateLocal { $0.history = DexFormat.mergeHistory($0.history, list, marketPk: pk, marketName: name, now: Self.nowMs()) }
    }

    /// skydex-client keeps a market connection whose dmsg stream has closed, and
    /// every call then fails. Dial the same market again, at most every 20 s.
    private func redial(_ error: any Error) async -> Bool {
        let text = error.localizedDescription
        guard text.range(of: #"stream closed|session shutdown|closed pipe|broken pipe|connection reset|\bEOF\b"#, options: [.regularExpression, .caseInsensitive]) != nil,
              let state = app, let pk = market?.marketPK, !pk.isEmpty,
              Date().timeIntervalSince(lastRedial) >= Self.redialGap
        else { return false }
        lastRedial = Date()
        guard let status = try? await dex(SkydexProfile.listenPort(state.args)).connect(marketPK: pk) else { return false }
        var merged = status
        if merged.marketName.isEmpty { merged.marketName = market?.marketName ?? "" }
        market = merged
        return true
    }

    private func client() throws -> SkydexClient {
        guard uiURL != nil, let state = app else { throw DexError(L10n.text("dex_error_not_connected")) }
        return dex(SkydexProfile.listenPort(state.args))
    }

    private func needRegister() {
        tab = .settings
        walletsMessage = WalletsMessage(ok: false, text: L10n.text("dex_banner_register_title"))
    }

    /// A buy or a listing: one at a time, its failure shown on the tab. "Register first" opens Settings.
    private func trade(_ block: @escaping @MainActor () async throws -> Void) {
        Task {
            working = true
            actionError = nil
            defer { working = false }
            do {
                try await block()
            } catch {
                let message = error.localizedDescription
                actionError = message
                if message.range(of: "register", options: .caseInsensitive) != nil { tab = .settings }
                if await redial(error) { notice = L10n.text("dex_reconnected") }
            }
        }
    }

    private func cancel(_ id: String, _ block: @escaping @MainActor () async throws -> Void) {
        Task {
            busyId = id
            actionError = nil
            defer { busyId = nil }
            do {
                try await block()
            } catch {
                actionError = error.localizedDescription
                if await redial(error) { notice = L10n.text("dex_reconnected") }
            }
        }
    }

    private func dex(_ port: Int) -> SkydexClient {
        let secret = password
        return SkydexClient(transport: LoopbackTransport(origin: SkydexProfile.origin(port: port))) {
            guard let secret else { throw DexError(L10n.text("dex_error_password")) }
            return secret
        }
    }

    // MARK: What the phone keeps

    private func updateLocal(_ change: (inout DexLocal) -> Void) {
        var next = local
        change(&next)
        guard next != local else { return }
        local = next
        let file = storeFile
        try? FileManager.default.createDirectory(at: file.deletingLastPathComponent(), withIntermediateDirectories: true)
        if let data = try? JSONEncoder().encode(next) { try? data.write(to: file, options: .atomic) }
    }

    /// Most recent first, deduplicated by key, a previously learned name kept.
    private func saveRecent(_ saved: SavedMarket) {
        var entry = saved
        if entry.name.isEmpty { entry.name = recents.first { $0.pk == saved.pk }?.name ?? "" }
        writeRecents([entry] + recents.filter { $0.pk != saved.pk })
    }

    private func writeRecents(_ list: [SavedMarket]) {
        recents = Array(list.prefix(Self.recentsLimit))
        UserDefaults.standard.set(try? JSONEncoder().encode(recents), forKey: Self.recentsKey)
    }

    /// Once, the first time skydex-client is up: what the old trading page kept in WebKit storage.
    private func importLegacy(_ origin: URL) async {
        guard !local.legacyImported else { return }
        let webKit = FileManager.default.urls(for: .libraryDirectory, in: .userDomainMask)[0].appendingPathComponent("WebKit", isDirectory: true)
        guard FileManager.default.fileExists(atPath: webKit.path), let password else {
            updateLocal { $0.legacyImported = true }
            return
        }
        guard let storage = await LocalStorageReader.read(origin.appendingPathComponent("api/config"), user: SkydexProfile.user, password: password) else { return }
        let legacy = DexFormat.parseLegacy(storage)
        updateLocal { s in
            if s.wallets.isEmpty { s.wallets = legacy.wallets }
            s.history += legacy.history.filter { old in !s.history.contains { $0.id == old.id && $0.type == old.type } }
            s.legacyImported = true
        }
        // Markets already listed here take the page's name and last-used time when they lack them.
        let known = recents.map { k -> SavedMarket in
            guard let old = legacy.markets.first(where: { $0.pk == k.pk }) else { return k }
            return SavedMarket(pk: k.pk, name: k.name.isEmpty ? old.name : k.name, ts: k.ts > 0 ? k.ts : old.ts)
        }
        let imported = legacy.markets.filter { m in !known.contains { $0.pk == m.pk } }.map { SavedMarket(pk: $0.pk, name: $0.name, ts: $0.ts) }
        if !imported.isEmpty || known != recents { writeRecents((known + imported).sorted { $0.ts > $1.ts }) }
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

    static func nowMs() -> Int64 { Int64(Date().timeIntervalSince1970 * 1000) }
}

/// An error in the screen's own words.
struct DexError: LocalizedError {
    let message: String
    init(_ message: String) { self.message = message }
    var errorDescription: String? { message }
}
