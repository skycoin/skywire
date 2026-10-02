import Foundation
import WalletCore

/// What the wallet keeps beside the sealed phrases: the user's coins and node
/// addresses, the wallets (addresses only), which coin and which wallet are
/// selected. Android keeps the same entries in its "wallet" DataStore.
struct WalletRegistry: Codable, Equatable, Sendable {
    var wallets: [WalletMeta] = []
    /// Every user-added coin, fiber chains and tokens alike (Android's
    /// `fiber_coins`, a key that predates tokens).
    var userCoins: [CoinSpec] = []
    /// Coin id → the node address the user set.
    var nodeUrls: [String: String] = [:]
    var selectedCoin: String?
    /// Coin id → the wallet in use for it.
    var activeWallets: [String: String] = [:]

    init() {}

    enum CodingKeys: String, CodingKey { case wallets, userCoins, nodeUrls, selectedCoin, activeWallets }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        wallets = try c.decodeIfPresent([WalletMeta].self, forKey: .wallets) ?? []
        userCoins = try c.decodeIfPresent([CoinSpec].self, forKey: .userCoins) ?? []
        nodeUrls = try c.decodeIfPresent([String: String].self, forKey: .nodeUrls) ?? [:]
        selectedCoin = try c.decodeIfPresent(String.self, forKey: .selectedCoin)
        activeWallets = try c.decodeIfPresent([String: String].self, forKey: .activeWallets) ?? [:]
    }
}

/// A refusal the user can read: the store's preconditions, in the
/// catalogue's words (Android's require(…) { getString(…) }).
struct WalletStoreError: Error, LocalizedError, Equatable {
    let message: String
    init(_ message: String) { self.message = message }
    var errorDescription: String? { message }
}

/// Everything the wallet screens need, behind one door: the coin list, the
/// wallets and their sealed seeds, cached chain views, and the send path
/// (Android: wallet/WalletRepository.kt). Network work delegates to WalletCore
/// implementations; nothing above this class ever touches key material or
/// node URLs.
///
/// The registry and the snapshots live in Application Support/wallet,
/// excluded from backups (Android: allowBackup=false): the phrases are
/// Keychain items that never leave this phone, so a backup restored
/// elsewhere would otherwise bring wallets without their keys.
@MainActor
final class WalletStore: ObservableObject {
    @Published private(set) var registry: WalletRegistry

    let directory: URL
    let seeds: WalletSeedStore
    private let session: URLSession
    /// Wallet id → when a failed address scan may be tried again (ms).
    private var scanRetryAfter: [String: Int64] = [:]

    /// Long enough that a scan the link cannot carry is not retried in a loop.
    static let scanRetryBackoffMs: Int64 = 5 * 60 * 1000

    init(directory: URL, seeds: WalletSeedStore, session: URLSession = WalletStore.makeSession()) {
        self.directory = directory
        self.seeds = seeds
        self.session = session
        registry = Self.read(directory.appendingPathComponent("registry.json")) ?? WalletRegistry()
    }

    static func app() -> WalletStore {
        let support = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        return WalletStore(directory: support.appendingPathComponent("wallet", isDirectory: true), seeds: .app())
    }

    /// The session every node client shares. The request timeout is an IDLE
    /// timeout (time between bytes), so a slow but progressing download
    /// survives and a dead socket still fails; the resource timeout is only
    /// a backstop. A Skycoin node's /api/v1/transactions has no pagination
    /// and one ordinary address's history measured 19 MB: the backstop is
    /// sized so that still arrives over a genuinely slow link (Android: the
    /// same two numbers on its OkHttp client). Ephemeral: nothing about which
    /// addresses were asked about lands in a cache on disk.
    nonisolated static func makeSession() -> URLSession {
        let config = URLSessionConfiguration.ephemeral
        config.timeoutIntervalForRequest = 20
        config.timeoutIntervalForResource = 5 * 60
        config.waitsForConnectivity = false
        return URLSession(configuration: config)
    }

    // MARK: Coins

    /// Every coin, as it is actually reached — the user's own node address
    /// applied on top of the shipped one where they have set it. SKY first,
    /// user fiber coins in the order added, then the other built-ins, then
    /// user tokens in the order added.
    var coins: [CoinSpec] {
        shippedCoins.map { $0.withNodeOverride(registry.nodeUrls) }
    }

    /// The coin list before any node override — what "use the default"
    /// restores.
    var defaultNodeUrls: [String: String] {
        Dictionary(uniqueKeysWithValues: shippedCoins.map { ($0.id, $0.nodeUrl) })
    }

    private var shippedCoins: [CoinSpec] {
        [CoinSpec.sky] + registry.userCoins.filter { $0.kind == .skyFiber }
            + [CoinSpec.btc, CoinSpec.eth, CoinSpec.usdt] + registry.userCoins.filter { $0.kind == .erc20 }
    }

    func coin(_ id: String) -> CoinSpec? { coins.first { $0.id == id } }

    /// Point a coin at a different node, or hand it back to the shipped one
    /// with a blank `url`. Any address scan waiting on a back-off is
    /// released: the whole point of changing the node is that the old one
    /// was not answering.
    func setNodeUrl(coinId: String, url: String) throws {
        var trimmed = url.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.hasSuffix("/") { trimmed.removeLast() }
        guard trimmed.isEmpty || Self.isHTTPURL(trimmed) else { throw WalletStoreError(L10n.text("wallet_add_coin_node_invalid")) }
        mutate { reg in
            if trimmed.isEmpty { reg.nodeUrls[coinId] = nil } else { reg.nodeUrls[coinId] = trimmed }
        }
        for w in registry.wallets where w.coinId == coinId { scanRetryAfter[w.id] = nil }
    }

    func addFiberCoin(name: String, ticker: String, nodeUrl: String, icon: String?) throws -> CoinSpec {
        var url = nodeUrl.trimmingCharacters(in: .whitespacesAndNewlines)
        if url.hasSuffix("/") { url.removeLast() }
        guard Self.isHTTPURL(url) else { throw WalletStoreError(L10n.text("wallet_add_coin_node_invalid")) }
        let spec = CoinSpec(
            id: "fiber-\(Self.shortID(8))",
            name: name.trimmingCharacters(in: .whitespacesAndNewlines),
            ticker: ticker.trimmingCharacters(in: .whitespacesAndNewlines).uppercased(),
            kind: .skyFiber,
            nodeUrl: url,
            icon: icon
        )
        mutate { $0.userCoins.append(spec) }
        return spec
    }

    /// Add an ERC-20 token on Ethereum mainnet: same chain plumbing as USDT,
    /// different contract and decimals. The contract must be checksum-valid;
    /// decimals must match the contract's own or amounts will be off by
    /// powers of ten.
    func addErc20Token(name: String, ticker: String, contract: String, decimals: Int, icon: String?) throws -> CoinSpec {
        let address = contract.trimmingCharacters(in: .whitespacesAndNewlines)
        guard EthCrypto.isValidAddress(address) else { throw WalletStoreError(L10n.text("wallet_add_token_contract_invalid")) }
        guard (0...36).contains(decimals) else { throw WalletStoreError(L10n.text("wallet_add_token_decimals_range")) }
        let spec = CoinSpec(
            id: "erc20-\(Self.shortID(8))",
            name: name.trimmingCharacters(in: .whitespacesAndNewlines),
            ticker: ticker.trimmingCharacters(in: .whitespacesAndNewlines).uppercased(),
            kind: .erc20,
            nodeUrl: CoinSpec.ethNode,
            explorerTxUrl: "\(CoinSpec.ethIndexer)/tx/%s",
            contract: address,
            tokenDecimals: decimals,
            indexerUrl: CoinSpec.ethIndexer,
            icon: icon
        )
        mutate { $0.userCoins.append(spec) }
        // The account this token lives in may already be set up under ETH or
        // another token; if so it arrives holding it, rather than asking for
        // a phrase the user has already given.
        adoptEthFamilyWallets(spec)
        return spec
    }

    /// Drop a user-added coin, returning the spec that went so the caller can
    /// name it and clear up its icon file. Refuses while any wallet still
    /// holds the coin rather than cascading into removeWallet, which erases a
    /// phrase that may exist nowhere else; built-ins are not removable.
    func removeUserCoin(_ coinId: String) throws -> CoinSpec? {
        guard let spec = coin(coinId) else { return nil }
        guard !spec.builtIn else { throw WalletStoreError(L10n.format("wallet_coin_remove_builtin", spec.name)) }
        let held = registry.wallets.filter { $0.coinId == coinId }.count
        guard held == 0 else { throw WalletStoreError(L10n.format("wallet_coin_remove_blocked_count", held)) }
        mutate { reg in
            reg.userCoins.removeAll { $0.id == coinId }
            reg.activeWallets[coinId] = nil
            // Selection has to move or the tab opens on a coin that is no
            // longer in its own list.
            if reg.selectedCoin == coinId { reg.selectedCoin = CoinSpec.sky.id }
        }
        return spec
    }

    func core(for spec: CoinSpec) throws -> any WalletCore {
        switch spec.kind {
        case .skyFiber:
            return try SkyFiberWalletCore(nodeURL: spec.nodeUrl, session: session)
        case .btc:
            return try BtcWalletCore(esploraURL: spec.nodeUrl, session: session)
        case .eth:
            return try EthWalletCore(rpcURL: spec.nodeUrl, indexerURL: spec.indexerUrl, session: session)
        case .erc20:
            guard let contract = spec.contract else { throw WalletStoreError("token \(spec.id) has no contract address") }
            return try EthWalletCore(
                rpcURL: spec.nodeUrl,
                indexerURL: spec.indexerUrl,
                session: session,
                token: EthWalletCore.Erc20Token(contract: contract, decimals: spec.tokenDecimals ?? CoinSpec.defaultTokenDecimals)
            )
        }
    }

    // MARK: Selection

    var selectedCoinId: String { registry.selectedCoin ?? CoinSpec.sky.id }

    func setSelectedCoin(_ coinId: String) { mutate { $0.selectedCoin = coinId } }

    func activeWalletId(_ coinId: String) -> String? { registry.activeWallets[coinId] }

    func setActiveWallet(coinId: String, walletId: String) { mutate { $0.activeWallets[coinId] = walletId } }

    // MARK: Wallets

    var wallets: [WalletMeta] { registry.wallets }

    func wallet(_ id: String) -> WalletMeta? { registry.wallets.first { $0.id == id } }

    /// Create (fresh phrase, already quiz-verified) or restore. Restores
    /// probe the network for used addresses; a dead node degrades to one
    /// address rather than failing the restore — but says so, in
    /// `addressScanAtMs`, so the probe is retried until it lands.
    func addWallet(_ spec: CoinSpec, name: String, mnemonic: String, restored: Bool) async throws -> WalletMeta {
        let core = try core(for: spec)
        let seed = Self.normalizeSeed(mnemonic)
        guard core.validateSeed(seed) else { throw WalletStoreError(L10n.text("wallet_seed_invalid")) }

        // nil means the question could not be put, which is different from
        // "the answer is one address" — a fresh phrase genuinely has one and
        // is settled, a restore that could not reach the node is not.
        let scan: (receive: Int, change: Int)? = restored ? (try? await core.scanUsed(seed: seed)) : (1, 0)
        let counts = scan ?? (1, 0)
        let scannedAt: Int64 = scan != nil ? nowMs() : 0
        let book = try await offMain { try core.deriveAddresses(seed: seed, receiveCount: counts.receive, changeCount: counts.change) }

        // This coin may already hold this exact account — most likely because
        // mirroring put it there and the user restored the phrase under the
        // sibling anyway. Two entries for one address would show the same
        // balance twice, so the existing one is adopted instead of duplicated.
        let head = book.receive.first
        if let already = registry.wallets.first(where: { $0.coinId == spec.id && head != nil && $0.receiveAddresses.first == head }) {
            // A restore that scanned further than the mirror knew about is the
            // one thing worth carrying over.
            let longer = book.receive.count > already.receiveAddresses.count
            let settles = scannedAt > 0 && already.addressScanPending
            var grown = already
            if longer || settles {
                if longer {
                    grown.receiveAddresses = book.receive
                    grown.changeAddresses = book.change
                }
                grown.addressScanAtMs = max(already.addressScanAtMs, scannedAt)
                let updated = grown
                mutate { reg in reg.wallets = reg.wallets.map { $0.id == updated.id ? updated : $0 } }
            }
            setActiveWallet(coinId: spec.id, walletId: grown.id)
            return grown
        }

        let trimmedName = name.trimmingCharacters(in: .whitespacesAndNewlines)
        let meta = WalletMeta(
            id: "w-\(Self.shortID(12))",
            coinId: spec.id,
            name: trimmedName.isEmpty ? spec.ticker : trimmedName,
            createdAtMs: nowMs(),
            receiveAddresses: book.receive,
            changeAddresses: book.change,
            addressScanAtMs: scannedAt
        )
        try seeds.putSeed(meta.id, seed)
        mutate { reg in
            reg.wallets.append(meta)
            reg.activeWallets[spec.id] = meta.id
        }
        if spec.isEthFamily { try mirrorIntoEthFamily(meta, seed: seed, book: book, exceptCoin: spec.id) }
        return meta
    }

    /// Give every other Ethereum-family coin the same wallet. ETH and every
    /// ERC-20 are one account on one chain — the same key, the same address,
    /// differing only in which asset is being looked at; without this a user
    /// who has entered their phrase for ETH is asked for it again for each
    /// token. `book` is passed rather than re-derived, so "the mirror has
    /// identical addresses" is true by construction.
    private func mirrorIntoEthFamily(_ source: WalletMeta, seed: String, book: AddressBook, exceptCoin: String) throws {
        for sibling in coins where sibling.isEthFamily && sibling.id != exceptCoin {
            try mirrorWallet(sibling, name: source.name, createdAtMs: source.createdAtMs, seed: seed,
                             book: book, addressScanAtMs: source.addressScanAtMs)
        }
    }

    /// One mirrored wallet, or nothing if `coin` already has this account
    /// (matched on the first receive address: an address IS the account
    /// here). The mirror carries the source's scan answer, the same
    /// addresses' answer.
    @discardableResult
    private func mirrorWallet(
        _ coin: CoinSpec, name: String, createdAtMs: Int64, seed: String, book: AddressBook, addressScanAtMs: Int64
    ) throws -> WalletMeta? {
        guard let head = book.receive.first else { return nil }
        if registry.wallets.contains(where: { $0.coinId == coin.id && $0.receiveAddresses.first == head }) { return nil }
        let meta = WalletMeta(
            id: "w-\(Self.shortID(12))",
            coinId: coin.id,
            name: name,
            createdAtMs: createdAtMs,
            receiveAddresses: book.receive,
            changeAddresses: book.change,
            addressScanAtMs: addressScanAtMs
        )
        // A second sealed copy of a phrase already on this phone, under the
        // same protection — no new exposure, and the alternative (one seed
        // shared by reference) would let deleting any one wallet strand the
        // others.
        try seeds.putSeed(meta.id, seed)
        mutate { reg in
            reg.wallets.append(meta)
            // Only if that coin has nothing selected yet: arriving at a coin
            // the user was already using must not move them off their choice.
            if reg.activeWallets[coin.id] == nil { reg.activeWallets[coin.id] = meta.id }
        }
        return meta
    }

    /// Give a newly added ERC-20 the Ethereum wallets that already exist, one
    /// per distinct account (several ETH-family coins already hold a copy
    /// each, and they must not become several copies here).
    private func adoptEthFamilyWallets(_ token: CoinSpec) {
        let ethCoinIds = Set(coins.filter { $0.isEthFamily && $0.id != token.id }.map(\.id))
        var seen = Set<String>()
        for existing in registry.wallets where ethCoinIds.contains(existing.coinId) {
            guard let head = existing.receiveAddresses.first, seen.insert(head).inserted else { continue }
            // Unreadable seed: nothing to copy, and the user can still restore
            // the token's wallet by hand.
            guard let seed = try? seeds.seed(existing.id) else { continue }
            _ = try? mirrorWallet(
                token, name: existing.name, createdAtMs: existing.createdAtMs, seed: seed,
                book: AddressBook(receive: existing.receiveAddresses, change: existing.changeAddresses),
                addressScanAtMs: existing.addressScanAtMs
            )
        }
    }

    /// Trimmed, lower-cased, single-spaced (Android: normalizeSeed).
    static func normalizeSeed(_ mnemonic: String) -> String {
        mnemonic.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
            .split(whereSeparator: { $0.isWhitespace }).joined(separator: " ")
    }

    func renameWallet(_ id: String, name: String) {
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        mutate { reg in
            reg.wallets = reg.wallets.map { w in
                guard w.id == id else { return w }
                var renamed = w
                renamed.name = trimmed
                return renamed
            }
        }
    }

    /// Deletes the sealed seed, the metadata and the cache. Irreversible.
    func removeWallet(_ id: String) throws {
        guard let gone = wallet(id) else { return }
        try seeds.deleteSeed(id)
        try? FileManager.default.removeItem(at: cacheFile(id))
        mutate { reg in
            reg.wallets.removeAll { $0.id == id }
            if reg.activeWallets[gone.coinId] == id {
                reg.activeWallets[gone.coinId] = reg.wallets.first { $0.coinId == gone.coinId }?.id
            }
        }
    }

    /// The phrase in the clear — callers gate this behind the device-owner
    /// check (WalletAuth).
    func revealSeed(_ id: String) throws -> String? { try seeds.seed(id) }

    func newReceiveAddress(walletId: String) async throws -> String {
        guard let meta = wallet(walletId), let spec = coin(meta.coinId) else { throw WalletStoreError("unknown wallet") }
        guard let seed = try seeds.seed(walletId) else { throw WalletStoreError(L10n.text("wallet_seed_unavailable")) }
        let core = try core(for: spec)
        let receiveCount = meta.receiveAddresses.count + 1
        let changeCount = meta.changeAddresses.count
        let book = try await offMain { try core.deriveAddresses(seed: seed, receiveCount: receiveCount, changeCount: changeCount) }
        mutate { reg in
            reg.wallets = reg.wallets.map { w in
                guard w.id == walletId else { return w }
                var grown = w
                grown.receiveAddresses = book.receive
                return grown
            }
        }
        return book.receive.last ?? ""
    }

    // MARK: Chain view and cache

    private var cacheDir: URL { directory.appendingPathComponent("cache", isDirectory: true) }
    private func cacheFile(_ walletId: String) -> URL { cacheDir.appendingPathComponent("\(walletId).json") }

    func cachedSnapshot(_ walletId: String) -> WalletSnapshot? { Self.read(cacheFile(walletId)) }

    /// Fetch balance and history; persist and return the fresh snapshot.
    ///
    /// The two halves fail independently, because they cost wildly different
    /// amounts: the balance is one small fixed-size answer and the half
    /// everything depends on (the numbers, whether Send is allowed); the
    /// history is unbounded and routinely megabytes. So the history is allowed
    /// to miss: the balance lands, the last history we did get is kept, and
    /// the snapshot records when that was. A balance that will not fetch is
    /// still fatal — that is the network being down.
    func refresh(_ walletId: String) async throws -> WalletSnapshot {
        guard let found = wallet(walletId), let spec = coin(found.coinId) else { throw WalletStoreError("unknown wallet") }
        let core = try core(for: spec)
        // Ask again, if the question is still open: reading a balance from
        // half a wallet's addresses is a wrong number, not a stale one.
        let meta = await settleAddressScan(found, core: core)
        let book = AddressBook(receive: meta.receiveAddresses, change: meta.changeAddresses)
        let balance = try await core.balance(book)
        let previous = cachedSnapshot(walletId)
        let history = try? await core.history(book)
        let snapshot = mergeSnapshot(balance: balance, history: history, previous: previous, nowMs: nowMs())
        // The wallet may have been removed while this was in flight.
        if wallet(walletId) != nil { Self.write(snapshot, to: cacheFile(walletId), in: cacheDir) }
        return snapshot
    }

    /// Finish a restore's address discovery when it could not be done at the
    /// time, and record that it is done. A failure backs off for five
    /// minutes rather than retrying on the next 30-second tick (a scan costs
    /// as much as a history fetch, on what is usually metered data); the
    /// back-off is in memory only, so a relaunch is a fresh try. The address
    /// lists only ever grow here (settledAddresses).
    private func settleAddressScan(_ meta: WalletMeta, core: any WalletCore) async -> WalletMeta {
        guard meta.addressScanPending else { return meta }
        let now = nowMs()
        if now < scanRetryAfter[meta.id] ?? 0 { return meta }
        guard let seed = try? seeds.seed(meta.id) else { return meta }
        guard let scanned = try? await core.scanUsed(seed: seed) else {
            scanRetryAfter[meta.id] = now + Self.scanRetryBackoffMs
            return meta
        }
        scanRetryAfter[meta.id] = nil
        let receiveCount = max(scanned.receive, meta.receiveAddresses.count)
        let changeCount = max(scanned.change, meta.changeAddresses.count)
        guard let book = try? await offMain({ try core.deriveAddresses(seed: seed, receiveCount: receiveCount, changeCount: changeCount) })
        else { return meta }
        let settled = settledAddresses(meta, scanned: book, nowMs: nowMs())
        mutate { reg in reg.wallets = reg.wallets.map { $0.id == settled.id ? settled : $0 } }
        return settled
    }

    // MARK: Send

    func plan(walletId: String, toAddress: String, amount: UInt64, feeRate: Int?, sendMax: Bool) async throws -> TxPlan {
        guard let meta = wallet(walletId), let spec = coin(meta.coinId) else { throw WalletStoreError("unknown wallet") }
        guard let seed = try seeds.seed(walletId) else { throw WalletStoreError(L10n.text("wallet_seed_unavailable")) }
        return try await core(for: spec).buildTx(
            seed: seed,
            book: AddressBook(receive: meta.receiveAddresses, change: meta.changeAddresses),
            toAddress: toAddress.trimmingCharacters(in: .whitespacesAndNewlines),
            amount: amount,
            feeRate: feeRate,
            sendMax: sendMax
        )
    }

    /// Sign locally and broadcast; returns the network's txid.
    func signAndBroadcast(walletId: String, plan: TxPlan) async throws -> String {
        guard let meta = wallet(walletId), let spec = coin(meta.coinId) else { throw WalletStoreError("unknown wallet") }
        guard let seed = try seeds.seed(walletId) else { throw WalletStoreError(L10n.text("wallet_seed_unavailable")) }
        let core = try core(for: spec)
        let signed = try await offMain { try core.signTx(seed: seed, plan: plan) }
        let txid = try await core.broadcast(signed)

        // A Bitcoin plan that paid change to a fresh chain address makes that
        // address part of the wallet the moment the transaction exists.
        if let btc = core as? BtcWalletCore {
            let index = btc.consumedChangeIndex(signed)
            if index >= 0, index == meta.changeAddresses.count {
                let receiveCount = meta.receiveAddresses.count
                let book = try await offMain { try btc.deriveAddresses(seed: seed, receiveCount: receiveCount, changeCount: index + 1) }
                mutate { reg in
                    reg.wallets = reg.wallets.map { w in
                        guard w.id == walletId else { return w }
                        var grown = w
                        grown.changeAddresses = book.change
                        return grown
                    }
                }
            }
        }
        return txid
    }

    // MARK: Storage

    private func mutate(_ change: (inout WalletRegistry) -> Void) {
        var next = registry
        change(&next)
        guard next != registry else { return }
        registry = next
        Self.write(next, to: directory.appendingPathComponent("registry.json"), in: directory)
    }

    private static func read<T: Decodable>(_ url: URL) -> T? {
        guard let data = try? Data(contentsOf: url) else { return nil }
        return try? JSONDecoder().decode(T.self, from: data)
    }

    /// Atomic, in a directory kept out of backups.
    private static func write<T: Encodable>(_ value: T, to url: URL, in dir: URL) {
        let fm = FileManager.default
        if !fm.fileExists(atPath: dir.path) {
            try? fm.createDirectory(at: dir, withIntermediateDirectories: true)
            var excluded = dir
            var values = URLResourceValues()
            values.isExcludedFromBackup = true
            try? excluded.setResourceValues(values)
        }
        guard let data = try? JSONEncoder().encode(value) else { return }
        try? data.write(to: url, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
    }

    private static func isHTTPURL(_ text: String) -> Bool {
        guard let url = URL(string: text), let scheme = url.scheme?.lowercased(),
              scheme == "http" || scheme == "https", let host = url.host, !host.isEmpty
        else { return false }
        return true
    }

    /// The first `count` characters of a fresh UUID, as Android's ids take
    /// them (`UUID.randomUUID().toString().take(n)`).
    private static func shortID(_ count: Int) -> String {
        String(UUID().uuidString.lowercased().prefix(count))
    }
}

/// Runs CPU-bound key work (derivation, signing) away from the main actor:
/// a Skycoin address chain or a BIP 39 stretch is milliseconds per address,
/// and a restore derives a hundred.
func offMain<T: Sendable>(_ work: @escaping @Sendable () throws -> T) async throws -> T {
    try await Task.detached(priority: .userInitiated) { try work() }.value
}
