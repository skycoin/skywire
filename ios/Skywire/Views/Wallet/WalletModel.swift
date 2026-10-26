import Combine
import Foundation
import WalletCore

/// Where the send flow stands; the review sheet and the result screen read it.
struct SendState {
    var to = ""
    var amountText = ""
    var sendMax = false
    /// The one address the send spends from; nil = the whole wallet.
    var fromAddress: String?
    /// Unspent outputs picked to fund the send; empty = automatic.
    var inputs: Set<String> = []
    /// The unspent outputs as last loaded, for the picker and one address's outputs.
    var utxos: [UtxoRecord] = []
    /// Hours for the recipient as typed; blank = the automatic split. Fiber only.
    var hoursText = ""
    /// Destinations after the first. Fiber only.
    var extra: [DestDraft] = []
    /// Where change goes; blank keeps the automatic choice. Fiber only.
    var changeAddress = ""
    /// Percent of the hours left after the burn that automatic hours send on, 10 to 100. Fiber only.
    var sharePercent = 50
    /// The tip offered. Ethereum family only.
    var ethLevel = FeeLevel.normal
    /// The amount field holds US dollars, converted at the current price.
    var usdMode = false
    var feeRate = 12
    var presets: FeePresets?
    var planning = false
    var plan: TxPlan?
    var planError: String?
    var sending = false
    var sentTxid: String?
    var sentAmount: UInt64 = 0
    var sentTo = ""
    var sentCount = 1
}

/// The create/restore flow in progress — the phrase lives only here, in memory.
struct DraftState {
    var coin = CoinSpec.sky
    var seed: String?
    var quizPositions: [Int] = []
    /// The kind of wallet being made: fiber coins offer BIP 44 beside their classic wallet,
    /// Bitcoin legacy beside native segwit.
    var derivation = Derivation.standard
    /// The optional BIP 39 passphrase, held in memory only until the wallet is saved.
    var passphrase = ""
}

/// The wallet tab's screens, pushed on its navigation stack.
enum WalletRoute: Hashable {
    case create, verify, restore, receive, send, result, history, wallets, addCoin, node, nodeInfo
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
    /// History shows only transactions this address of the wallet took part in; nil shows all.
    @Published var historyAddress: String?
    @Published var send = SendState()
    @Published private(set) var draft = DraftState()
    /// The node address the coin would use with nothing set — what the Node
    /// screen offers to go back to.
    @Published private(set) var defaultNodeUrl = ""
    /// The same for the Ethereum family's history indexer; nil for coins without one.
    @Published private(set) var defaultIndexerUrl: String?
    /// The user's node addresses for this coin, in priority order, without the shipped fallback.
    @Published private(set) var nodeUrls: [String] = []
    /// The coin's node as it last answered; nil before it has, or when it did not.
    @Published private(set) var node: NodeInfo?
    /// False when the last node check got no answer.
    @Published private(set) var nodeReachable = true
    /// One of the coin in US dollars, when the user turned prices on and the coin has one.
    @Published private(set) var usdPrice: Double?
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
        // Another coin, however it was chosen (adding one selects it): the last coin's node and
        // price are not this one's.
        if coin.id != self.coin.id {
            node = nil
            nodeReachable = true
            usdPrice = nil
        }
        self.coin = coin
        let shipped = store.defaultCoins[coin.id]
        defaultNodeUrl = shipped?.nodeUrl ?? coin.nodeUrl
        defaultIndexerUrl = shipped?.indexerUrl ?? coin.indexerUrl
        nodeUrls = coin.nodeUrls.filter { $0 != defaultNodeUrl }
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
            historyAddress = nil
        }
        ready = true
        if visible && (!sameWallet || refreshTask == nil) { startRefreshing() }
    }

    /// While the tab is on screen: refresh now and every 30 seconds after.
    func run() async {
        visible = true
        // Turned off in Settings while the tab was away: no price stays on screen.
        if !WalletPrices.enabled { usdPrice = nil }
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
                await self.checkNodeStatus()
                await self.refreshPrice()
                do { try await Task.sleep(for: Self.refreshInterval) } catch { break }
            }
            self?.refreshing = false
        }
    }

    /// The node status line: asked with each refresh, as skycoin-web polls its node's health.
    private func checkNodeStatus() async {
        let coin = coin
        let info = try? await store.nodeStatus(coin)
        if Task.isCancelled || self.coin.id != coin.id { return }
        node = info
        nodeReachable = info != nil
    }

    /// The coin's USD price, when the user turned prices on; a failed fetch keeps the last one.
    private func refreshPrice() async {
        guard WalletPrices.enabled, let id = coin.priceId else {
            usdPrice = nil
            return
        }
        let price = try? await WalletPrices.usd(id)
        if Task.isCancelled || coin.priceId != id || !WalletPrices.enabled { return }
        usdPrice = price ?? usdPrice
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

    // MARK: Node details (fiber)

    func chainInfo() async throws -> ChainInfo { try await store.chainInfo(coin) }

    func peers() async throws -> [PeerInfo] { try await store.peers(coin) }

    func pending() async throws -> [PendingInfo] {
        guard let active else { throw WalletStoreError("no wallet") }
        return try await store.pending(walletId: active.id)
    }

    func deletePending(_ txid: String) async throws { try await store.deletePending(coin, txid: txid) }

    /// Checks a node before it joins the selected coin's list: what it reports, or why it will not do.
    func checkNode(_ url: String) async -> Result<NodeInfo, WalletStoreError> {
        do {
            return .success(try await store.checkNode(coin, url: url))
        } catch let refused as WalletStoreError {
            return .failure(refused)
        } catch {
            // A .dmsg node is reached through the Skywire core, which may simply not be running.
            return .failure(WalletStoreError(WalletMesh.isMeshURL(url) ? L10n.text("wallet_node_unreachable_mesh")
                                                                     : L10n.text("wallet_node_unreachable")))
        }
    }

    /// Save the selected coin's node list: every add, remove and move lands here at once, so the
    /// order on screen is the order the wallet asks in. An empty list hands the coin back to its
    /// shipped address; `indexerUrl` moves the Ethereum family's indexer, nil leaving it as it is.
    func saveNodeUrls(_ urls: [String], indexerUrl: String? = nil) {
        action { [self] in
            try store.setNodeUrls(coinId: coin.id, urls: urls, indexerUrl: indexerUrl)
            stale = false
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

    /// Adds a fiber coin once its node answers; a blank name or ticker comes from the node.
    func addFiberCoin(name: String, ticker: String, nodeUrl: String, icon: String?, onDone: @escaping () -> Void) {
        action { [self] in
            let spec = try await store.addFiberCoin(name: name, ticker: ticker, nodeUrl: nodeUrl, icon: icon)
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

    /// Begin the create flow for the selected coin: a fresh phrase of `words` words and the three
    /// positions the quiz asks for. A new length keeps the wallet type and passphrase chosen.
    func startCreate(words: Int = 12, keepChoices: Bool = false) {
        let derivation = keepChoices ? draft.derivation : .standard
        let passphrase = keepChoices ? draft.passphrase : ""
        var rng = SystemRandomNumberGenerator()
        let seed = Bip39.newMnemonic(entropyBits: words == 24 ? 256 : 128)
        var pool = Array(1...seed.split(separator: " ").count)
        let positions = (0..<3).map { _ in pool.remove(at: Int.random(in: 0..<pool.count, using: &rng)) }.sorted()
        draft = DraftState(coin: coin, seed: seed, quizPositions: positions, derivation: derivation, passphrase: passphrase)
    }

    func setDraftDerivation(_ derivation: Derivation) { draft.derivation = derivation }

    func setDraftPassphrase(_ passphrase: String) { draft.passphrase = passphrase }

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
            let derivation = draft.derivation, passphrase = draft.passphrase
            action { [self] in
                _ = try await store.addWallet(coin, name: defaultWalletName(coin), mnemonic: seed, restored: false,
                                              derivation: derivation, passphrase: passphrase)
                draft = DraftState()
                message = nil
                onActivated()
            }
        }
        return wrong
    }

    /// The phrase is validated by the restore screen before this is called. `legacy` is a
    /// checksum-less phrase the user chose to restore anyway; `exact` a non-standard seed, as typed.
    func restoreWallet(_ phrase: String, legacy: Bool = false, exact: Bool = false, onDone: @escaping () -> Void) {
        let coin = draft.coin
        let derivation = draft.derivation, passphrase = draft.passphrase
        action { [self] in
            restoring = true
            _ = try await store.addWallet(coin, name: defaultWalletName(coin), mnemonic: phrase, restored: true, legacy: legacy,
                                          derivation: derivation, passphrase: passphrase, exact: exact)
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

    /// Loads the unspent outputs, keeping the last list when the node does not answer. Fiber only.
    func loadUtxos() {
        guard let active, coin.kind == .skyFiber else { return }
        Task { [self] in
            if let list = try? await store.utxos(walletId: active.id), self.active?.id == active.id {
                send.utxos = list
            }
        }
    }

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
        // Picked outputs cap the send at what they hold; a fiber fee burns hours, not coins.
        if !send.inputs.isEmpty {
            return send.utxos.filter { send.inputs.contains($0.uxid) }.reduce(0) { $0 &+ $1.coins }
        }
        // With a from-address chosen, Max is what that address holds. The output count is an
        // estimate on Bitcoin: the plan recomputes the true fee from the real inputs either way.
        let balance: WalletBalance
        if let from = send.fromAddress {
            let held = snapshot.perAddress[from] ?? 0
            balance = WalletBalance(confirmed: held, predicted: held, hours: snapshot.hours, spendableOutputs: 1)
        } else {
            balance = snapshot.balance
        }
        return core.estimateMax(balance: balance, feeRate: send.feeRate)
    }

    /// What the send can spend: the chosen address's coins, or the wallet's.
    var sendAvailable: UInt64 {
        send.fromAddress.map { snapshot?.perAddress[$0] ?? 0 } ?? snapshot?.confirmed ?? 0
    }

    // MARK: More destinations (fiber)

    func addDestination() {
        send.extra.append(DestDraft())
        send.sendMax = false
        send.plan = nil
    }

    func updateDestination(_ index: Int, _ change: (inout DestDraft) -> Void) {
        guard send.extra.indices.contains(index) else { return }
        change(&send.extra[index])
        send.plan = nil
        send.planError = nil
    }

    func removeDestination(_ index: Int) {
        guard send.extra.indices.contains(index) else { return }
        send.extra.remove(at: index)
        send.plan = nil
        send.planError = nil
    }

    /// Bulk send replaces every destination, the first one included.
    func applyBulk(_ destinations: [DestDraft]) {
        guard let first = destinations.first else { return }
        send.to = first.address
        send.amountText = first.amountText
        send.hoursText = first.hoursText
        send.extra = Array(destinations.dropFirst())
        send.sendMax = false
        send.plan = nil
        send.planError = nil
    }

    func toggleInput(_ uxid: String) {
        if send.inputs.contains(uxid) { send.inputs.remove(uxid) } else { send.inputs.insert(uxid) }
        send.plan = nil
    }

    func clearInputs() {
        send.inputs = []
        send.plan = nil
    }

    /// Spend from one address, or (nil) the whole wallet. The picked outputs belonged to the
    /// old choice, so they go.
    func sendFrom(_ address: String?) {
        send.fromAddress = address
        send.inputs = []
        send.sendMax = false
        send.planError = nil
        send.plan = nil
    }

    /// Build the plan, then hand over to the review sheet.
    func buildPlan(onReady: @escaping () -> Void) {
        guard let active else { return }
        let coin = coin
        let request = send
        let amount: UInt64
        // In USD mode the field holds dollars; the plan is always in the coin.
        let coinText = request.usdMode
            ? usdPrice.flatMap { coin.coinTextFromUsd(Self.amountInput(request.amountText), price: $0) } ?? ""
            : Self.amountInput(request.amountText)
        if request.sendMax {
            amount = 0
        } else if let parsed = Amounts.parse(coinText, exponent: coin.exponent) {
            amount = parsed
        } else {
            send.planError = L10n.text("wallet_send_invalid_amount")
            return
        }
        let typedHours = request.hoursText.trimmingCharacters(in: .whitespacesAndNewlines)
        var hoursTo: UInt64?
        if coin.kind == .skyFiber, !typedHours.isEmpty {
            guard let hours = UInt64(typedHours) else {
                send.planError = L10n.text("wallet_send_invalid_hours")
                return
            }
            hoursTo = hours
        }
        var more: [Recipient] = []
        for (index, draft) in request.extra.enumerated() where coin.kind == .skyFiber {
            let coins = Amounts.parse(Self.amountInput(draft.amountText), exponent: coin.exponent).flatMap { $0 > 0 ? $0 : nil }
            let typed = draft.hoursText.trimmingCharacters(in: .whitespacesAndNewlines)
            let hours = typed.isEmpty ? nil : UInt64(typed)
            let problem: String? = if coins == nil {
                L10n.format("wallet_send_dest_invalid_amount", index + 2)
            } else if !typed.isEmpty && hours == nil {
                L10n.text("wallet_send_invalid_hours")
            } else if (hours == nil) != (hoursTo == nil) {
                L10n.text("wallet_send_hours_mixed")
            } else {
                nil
            }
            if let problem {
                send.planError = problem
                return
            }
            more.append(Recipient(address: draft.address.trimmingCharacters(in: .whitespacesAndNewlines), amount: coins ?? 0, hours: hours))
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
                    sendMax: request.sendMax,
                    fromAddress: coin.isEthFamily ? nil : request.fromAddress,
                    inputs: coin.kind == .skyFiber ? request.inputs : [],
                    hoursTo: hoursTo,
                    more: more,
                    changeAddress: coin.kind == .skyFiber ? request.changeAddress : nil,
                    shareFactor: coin.kind == .skyFiber ? Decimal(request.sharePercent) / 100 : nil,
                    feeLevel: coin.isEthFamily ? request.ethLevel : nil
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
                send.sentCount = plan.recipients.count
                refreshNow()
                onSent()
            } catch {
                send.sending = false
                throw error
            }
        }
    }

    // MARK: Offline transactions (fiber)

    /// The planned send unsigned, or nil when there is no plan.
    func unsignedHex() -> String? {
        guard let active, let plan = send.plan else { return nil }
        return try? store.unsignedHex(walletId: active.id, plan: plan)
    }

    func signRaw(_ rawHex: String) async throws -> String {
        guard let active else { throw WalletStoreError("unknown wallet") }
        return try await store.signRaw(walletId: active.id, rawHex: rawHex)
    }

    func broadcastRaw(_ rawHex: String) async throws -> String {
        guard let active else { throw WalletStoreError("unknown wallet") }
        let txid = try await store.broadcastRaw(walletId: active.id, rawHex: rawHex)
        refreshNow()
        return txid
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

    func revealPassphrase(_ walletId: String) -> String {
        (try? store.revealPassphrase(walletId)) ?? ""
    }

    func extendedPublicKeys(_ walletId: String) -> ExtendedPublicKeys? {
        try? store.extendedPublicKeys(walletId: walletId)
    }
}
