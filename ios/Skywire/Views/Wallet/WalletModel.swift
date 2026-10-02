import Combine
import Foundation
import WalletCore

/// Where the send flow stands; the review sheet and the result screen read it.
struct SendState {
    var to = ""
    var amountText = ""
    var sendMax = false
    var feeRate = 12
    var presets: FeePresets?
    var planning = false
    var plan: TxPlan?
    var planError: String?
    var sending = false
    var sentTxid: String?
    var sentAmount: UInt64 = 0
    var sentTo = ""
}

/// The create/restore flow in progress — the phrase lives only here, in memory.
struct DraftState {
    var coin = CoinSpec.sky
    var seed: String?
    var quizPositions: [Int] = []
}

/// The wallet tab's screens, pushed on its navigation stack.
enum WalletRoute: Hashable {
    case create, verify, restore, receive, send, result, history, wallets, addCoin, node
    case tx(String)
    case reveal(String)
}

/// Drives the wallet tab (Android: ui/wallet/WalletViewModel.kt): the selected
/// coin and wallet, the cached and refreshed chain view, and every action the
/// screens take. All state is the store's; this keeps what the screens show
/// in step with it.
@MainActor
final class WalletModel: ObservableObject {
    let store: WalletStore

    @Published var path: [WalletRoute] = []
    @Published private(set) var ready = false
    @Published private(set) var coins: [CoinSpec] = []
    @Published private(set) var coin = CoinSpec.sky
    @Published private(set) var allWallets: [WalletMeta] = []
    /// Wallets of the selected coin.
    @Published private(set) var coinWallets: [WalletMeta] = []
    @Published private(set) var active: WalletMeta?
    @Published private(set) var snapshot: WalletSnapshot?
    /// The freshest fetch failed and the screen shows old numbers.
    @Published private(set) var stale = false
    @Published private(set) var refreshing = false
    @Published private(set) var restoring = false
    /// Index into the active wallet's receive addresses shown on Receive.
    @Published var receiveIndex = 0
    @Published var send = SendState()
    @Published private(set) var draft = DraftState()
    /// The node address the coin would use with nothing set — what the Node
    /// screen offers to go back to.
    @Published private(set) var defaultNodeUrl = ""
    /// A one-line outcome or refusal, shown briefly over the screen.
    @Published var message: String?

    /// How often the balance and history are read while the tab is up.
    static let refreshInterval: Duration = .seconds(30)

    private var refreshTask: Task<Void, Never>?
    private var actionTask: Task<Void, Never>?
    private var visible = false
    private var registryWatch: AnyCancellable?

    init(store: WalletStore) {
        self.store = store
        sync()
        // Every store change re-reads the lot, as Android's combine over the
        // DataStore flows does; on the next turn, when the value is in place.
        registryWatch = store.$registry.dropFirst().receive(on: DispatchQueue.main).sink { [weak self] _ in
            self?.sync()
        }
    }

    // MARK: State

    private func sync() {
        let coins = store.coins
        let coin = coins.first { $0.id == store.selectedCoinId } ?? .sky
        let wallets = store.wallets
        let coinWallets = wallets.filter { $0.coinId == coin.id }
        let activeId = store.activeWalletId(coin.id)
        let active = coinWallets.first { $0.id == activeId } ?? coinWallets.first
        let sameWallet = self.active?.id == active?.id

        self.coins = coins
        self.coin = coin
        defaultNodeUrl = store.defaultNodeUrls[coin.id] ?? coin.nodeUrl
        allWallets = wallets
        self.coinWallets = coinWallets
        self.active = active
        if let active {
            snapshot = store.cachedSnapshot(active.id) ?? (sameWallet ? snapshot : nil)
        } else {
            snapshot = sameWallet ? snapshot : nil
        }
        if sameWallet {
            receiveIndex = max(0, min(receiveIndex, (active?.receiveAddresses.count ?? 1) - 1))
        } else {
            stale = false
            receiveIndex = 0
        }
        ready = true
        if visible && (!sameWallet || refreshTask == nil) { startRefreshing() }
    }

    /// While the tab is on screen: refresh now and every 30 seconds after.
    func run() async {
        visible = true
        startRefreshing()
        while !Task.isCancelled {
            do { try await Task.sleep(for: .seconds(3600)) } catch { break }
        }
        visible = false
        refreshTask?.cancel()
        refreshTask = nil
    }

    func refreshNow() { startRefreshing() }

    private func startRefreshing() {
        refreshTask?.cancel()
        refreshTask = nil
        guard let walletId = active?.id else { return }
        refreshTask = Task { [weak self] in
            while !Task.isCancelled {
                guard let self else { return }
                self.refreshing = true
                do {
                    let fresh = try await self.store.refresh(walletId)
                    if Task.isCancelled { break }
                    if self.active?.id == walletId {
                        self.snapshot = fresh
                        self.stale = false
                    }
                } catch {
                    // A cancelled request is not a node that failed to answer.
                    if Task.isCancelled { break }
                    // The screen renders the cached numbers under the stale
                    // banner; with no cache at all it says "not synced yet".
                    if self.active?.id == walletId { self.stale = true }
                }
                self.refreshing = false
                do { try await Task.sleep(for: Self.refreshInterval) } catch { break }
            }
            self?.refreshing = false
        }
    }

    func messageShown() { message = nil }

    /// The error's own words — they come from the node or the core and are
    /// what gets pasted into an issue — cut to a readable length.
    static func report(_ error: Error) -> String {
        let text: String
        switch error {
        case let e as WalletError: text = e.errorDescription ?? L10n.text("wallet_error_rejected")
        case let e as NetworkError: text = e.message
        case let e as URLError: text = e.localizedDescription
        default: text = (error as? LocalizedError)?.errorDescription ?? String(describing: error)
        }
        return String(text.prefix(200))
    }

    private func action(_ block: @escaping @MainActor () async throws -> Void) {
        actionTask?.cancel()
        actionTask = Task {
            do {
                try await block()
            } catch is CancellationError {
            } catch {
                message = Self.report(error)
                restoring = false
            }
        }
    }

    // MARK: Coins

    func selectCoin(_ coinId: String) {
        store.setSelectedCoin(coinId)
        send = SendState()
        receiveIndex = 0
    }

    /// Point the selected coin at a different node, or hand it back to the
    /// shipped one with a blank `url`; the next refresh uses it.
    func setNodeUrl(_ url: String, onDone: @escaping () -> Void) {
        action { [self] in
            try store.setNodeUrl(coinId: coin.id, url: url)
            stale = false
            onDone()
            refreshNow()
        }
    }

    /// Remove a user-added coin. Refused for a built-in or while wallets
    /// still hold it — the store decides, and its words are what shows.
    func removeCoin(_ coinId: String) {
        action { [self] in
            guard let gone = try store.removeUserCoin(coinId) else { return }
            // The badge file was written by this layer; best-effort.
            if let icon = gone.icon { CoinIcons.delete(icon, in: store) }
            message = L10n.format("wallet_coin_removed", gone.name)
        }
    }

    func importCoinIcon(_ data: Data, onSaved: @escaping (String) -> Void) {
        action { [self] in
            let name = try CoinIcons.importIcon(data, into: store)
            onSaved(name)
        }
    }

    func addFiberCoin(name: String, ticker: String, nodeUrl: String, icon: String?, onDone: @escaping () -> Void) {
        action { [self] in
            guard !name.trimmingCharacters(in: .whitespaces).isEmpty else { throw WalletStoreError(L10n.text("wallet_add_coin_name_required")) }
            guard !ticker.trimmingCharacters(in: .whitespaces).isEmpty else { throw WalletStoreError(L10n.text("wallet_add_coin_ticker_required")) }
            let spec = try store.addFiberCoin(name: name, ticker: ticker, nodeUrl: nodeUrl, icon: icon)
            store.setSelectedCoin(spec.id)
            onDone()
        }
    }

    func addErc20Token(name: String, ticker: String, contract: String, decimals: String, icon: String?, onDone: @escaping () -> Void) {
        action { [self] in
            guard !name.trimmingCharacters(in: .whitespaces).isEmpty else { throw WalletStoreError(L10n.text("wallet_add_token_name_required")) }
            guard !ticker.trimmingCharacters(in: .whitespaces).isEmpty else { throw WalletStoreError(L10n.text("wallet_add_token_ticker_required")) }
            guard let parsed = Int(decimals.trimmingCharacters(in: .whitespaces)) else {
                throw WalletStoreError(L10n.text("wallet_add_token_decimals_invalid"))
            }
            let spec = try store.addErc20Token(name: name, ticker: ticker, contract: contract, decimals: parsed, icon: icon)
            store.setSelectedCoin(spec.id)
            onDone()
        }
    }

    // MARK: Create and restore

    /// Begin the create flow for the selected coin: a fresh phrase and the
    /// three positions the quiz asks for.
    func startCreate() {
        var rng = SystemRandomNumberGenerator()
        var pool = Array(1...12)
        let positions = (0..<3).map { _ in pool.remove(at: Int.random(in: 0..<pool.count, using: &rng)) }.sorted()
        draft = DraftState(coin: coin, seed: Bip39.newMnemonic(entropyBits: 128), quizPositions: positions)
    }

    func startRestore() {
        draft = DraftState(coin: coin, seed: nil, quizPositions: [])
    }

    /// The quiz answers checked; the positions answered wrong (empty: the
    /// wallet is being activated).
    func submitQuiz(_ answers: [Int: String], onActivated: @escaping () -> Void) -> Set<Int> {
        guard let seed = draft.seed else { return [] }
        let words = seed.split(separator: " ").map(String.init)
        let wrong = Set(draft.quizPositions.filter { pos in
            answers[pos]?.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() != words[pos - 1]
        })
        if wrong.isEmpty {
            let coin = draft.coin
            action { [self] in
                _ = try await store.addWallet(coin, name: defaultWalletName(coin), mnemonic: seed, restored: false)
                draft = DraftState()
                message = nil
                onActivated()
            }
        }
        return wrong
    }

    /// The phrase is validated by the restore screen before this is called.
    func restoreWallet(_ phrase: String, onDone: @escaping () -> Void) {
        let coin = draft.coin
        action { [self] in
            restoring = true
            _ = try await store.addWallet(coin, name: defaultWalletName(coin), mnemonic: phrase, restored: true)
            draft = DraftState()
            restoring = false
            onDone()
        }
    }

    private func defaultWalletName(_ coin: CoinSpec) -> String {
        let count = store.wallets.filter { $0.coinId == coin.id }.count
        return count == 0 ? coin.ticker : "\(coin.ticker) \(count + 1)"
    }

    // MARK: Receive

    func generateNewAddress() {
        guard let active else { return }
        action { [self] in
            _ = try await store.newReceiveAddress(walletId: active.id)
            receiveIndex = store.wallet(active.id).map { $0.receiveAddresses.count - 1 } ?? 0
        }
    }

    // MARK: Send

    func resetSend() { send = SendState() }

    /// The fee presets, on Bitcoin only (the other chains price themselves or
    /// burn hours).
    func loadFeePresets() async {
        guard coin.kind == .btc, let core = try? store.core(for: coin),
              let presets = try? await core.feePresets() else { return }
        send.presets = presets
        send.feeRate = max(send.feeRate, 1)
    }

    /// The Max amount, fee-adjusted on Bitcoin and ETH, full balance elsewhere.
    func sendMaxAmount() -> UInt64 {
        guard let snapshot, let core = try? store.core(for: coin) else { return 0 }
        return core.estimateMax(balance: snapshot.balance, feeRate: send.feeRate)
    }

    /// Build the plan, then hand over to the review sheet.
    func buildPlan(onReady: @escaping () -> Void) {
        guard let active else { return }
        let coin = coin
        let request = send
        let amount: UInt64
        if request.sendMax {
            amount = 0
        } else if let parsed = Amounts.parse(Self.amountInput(request.amountText), exponent: coin.exponent) {
            amount = parsed
        } else {
            send.planError = L10n.text("wallet_send_invalid_amount")
            return
        }
        send.planning = true
        send.planError = nil
        action { [self] in
            do {
                let plan = try await store.plan(
                    walletId: active.id,
                    toAddress: request.to,
                    amount: amount,
                    feeRate: coin.kind == .btc ? request.feeRate : nil,
                    sendMax: request.sendMax
                )
                send.planning = false
                send.plan = plan
                onReady()
            } catch let error as WalletError {
                send.planning = false
                send.planError = error.errorDescription
            } catch let error as NetworkError {
                send.planning = false
                send.planError = String(error.message.prefix(200))
            } catch let error as URLError {
                send.planning = false
                send.planError = String(error.localizedDescription.prefix(200))
            } catch {
                send.planning = false
                throw error
            }
        }
    }

    /// The amount as typed, with the phone's decimal separator read as the
    /// point the parser wants: a keyboard in a comma locale types "0,5".
    static func amountInput(_ text: String) -> String {
        let separator = Locale.current.decimalSeparator ?? "."
        return separator == "." ? text : text.replacingOccurrences(of: separator, with: ".")
    }

    /// After the device-owner check: sign locally, broadcast, and land on
    /// the result.
    func signAndSend(onSent: @escaping () -> Void) {
        guard let active, let plan = send.plan else { return }
        send.sending = true
        action { [self] in
            do {
                let txid = try await store.signAndBroadcast(walletId: active.id, plan: plan)
                send.sending = false
                send.sentTxid = txid
                send.sentAmount = plan.amount
                send.sentTo = plan.toAddress
                refreshNow()
                onSent()
            } catch {
                send.sending = false
                throw error
            }
        }
    }

    // MARK: Wallets

    func useWallet(_ walletId: String) {
        store.setActiveWallet(coinId: coin.id, walletId: walletId)
    }

    func renameWallet(_ walletId: String, name: String) {
        store.renameWallet(walletId, name: name)
    }

    func removeWallet(_ walletId: String) {
        let name = allWallets.first { $0.id == walletId }?.name ?? ""
        action { [self] in
            try store.removeWallet(walletId)
            message = L10n.format("wallet_removed", name)
        }
    }

    func revealSeed(_ walletId: String) -> String? {
        try? store.revealSeed(walletId)
    }
}
