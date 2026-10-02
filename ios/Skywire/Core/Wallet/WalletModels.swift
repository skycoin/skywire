import Foundation
import WalletCore

/// Which family a coin belongs to — the protocols this wallet speaks
/// (Android: wallet/WalletModels.kt, as are the rest of this file's types).
enum CoinKind: String, Codable, Sendable {
    case skyFiber = "SKY_FIBER"
    case btc = "BTC"
    case eth = "ETH"
    case erc20 = "ERC20"
}

/// A coin the wallet can hold. SKY, BTC, ETH and USDT ship built in; fiber
/// coins and ERC-20 tokens are added by the user — every fiber chain runs the
/// same daemon and differs only in where it lives, and every ERC-20 speaks
/// the same contract surface and differs only in address and decimals.
struct CoinSpec: Codable, Equatable, Identifiable, Sendable {
    var id: String
    var name: String
    var ticker: String
    var kind: CoinKind
    var nodeUrl: String
    /// %s is the txid; nil hides the explorer button.
    var explorerTxUrl: String?
    var builtIn = false
    /// ERC-20 only: the token's contract address.
    var contract: String?
    /// ERC-20 only: the token's on-chain decimals.
    var tokenDecimals: Int?
    /// ETH family: etherscan-style history API base (Blockscout, keyless).
    var indexerUrl: String?
    /// User-added coins: the file name of the badge picked at creation
    /// (CoinIcons); nil falls back to ticker letters.
    var icon: String?

    init(
        id: String, name: String, ticker: String, kind: CoinKind, nodeUrl: String,
        explorerTxUrl: String? = nil, builtIn: Bool = false, contract: String? = nil,
        tokenDecimals: Int? = nil, indexerUrl: String? = nil, icon: String? = nil
    ) {
        self.id = id
        self.name = name
        self.ticker = ticker
        self.kind = kind
        self.nodeUrl = nodeUrl
        self.explorerTxUrl = explorerTxUrl
        self.builtIn = builtIn
        self.contract = contract
        self.tokenDecimals = tokenDecimals
        self.indexerUrl = indexerUrl
        self.icon = icon
    }

    enum CodingKeys: String, CodingKey {
        case id, name, ticker, kind, nodeUrl, explorerTxUrl, builtIn, contract, tokenDecimals, indexerUrl, icon
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decode(String.self, forKey: .name)
        ticker = try c.decode(String.self, forKey: .ticker)
        kind = try c.decode(CoinKind.self, forKey: .kind)
        nodeUrl = try c.decode(String.self, forKey: .nodeUrl)
        explorerTxUrl = try c.decodeIfPresent(String.self, forKey: .explorerTxUrl)
        builtIn = try c.decodeIfPresent(Bool.self, forKey: .builtIn) ?? false
        contract = try c.decodeIfPresent(String.self, forKey: .contract)
        tokenDecimals = try c.decodeIfPresent(Int.self, forKey: .tokenDecimals)
        indexerUrl = try c.decodeIfPresent(String.self, forKey: .indexerUrl)
        icon = try c.decodeIfPresent(String.self, forKey: .icon)
    }

    /// Base-unit exponent: droplets 10⁻⁶, satoshis 10⁻⁸ — and for the ETH
    /// family, whatever fits the app's 64-bit amounts: gwei (10⁻⁹) for the
    /// native coin because wei overflows 64 bits at ~18 ETH, and a token's own
    /// decimals capped at nine for the same reason.
    var exponent: Int {
        switch kind {
        case .btc: 8
        case .eth: 9
        case .erc20: min(tokenDecimals ?? Self.defaultTokenDecimals, 9)
        case .skyFiber: 6
        }
    }

    /// True for the native coin and every ERC-20 — one account on one chain,
    /// one address, differing only in which asset is being looked at. A
    /// wallet for any of them is a wallet for all of them, which is why
    /// creating one mirrors across the rest (WalletStore.mirrorIntoEthFamily).
    ///
    /// Fiber coins are deliberately not a family: they derive alike but each
    /// is a separate chain with its own ledger, so sharing a wallet between
    /// two of them would claim a balance that is not there.
    var isEthFamily: Bool { kind == .eth || kind == .erc20 }

    /// Decimals shown in balances and amount fields.
    var displayDecimals: Int {
        switch kind {
        case .btc: 8
        case .eth: 6
        case .erc20: min(exponent, 6)
        case .skyFiber: 3
        }
    }

    /// Whether this coin's node address is one the user may set: Skycoin and
    /// every fiber chain, where one daemon answers balances, history and
    /// broadcast alike. The Ethereum family reads balances from an RPC and
    /// history from a separate indexer, so one field there would move half of
    /// it and quietly leave the rest.
    var nodeUrlEditable: Bool { kind == .skyFiber }

    /// This coin as it is actually reached, once the user's own node address
    /// is applied.
    func withNodeOverride(_ overrides: [String: String]) -> CoinSpec {
        guard let url = overrides[id]?.trimmingCharacters(in: .whitespacesAndNewlines), !url.isEmpty, url != nodeUrl
        else { return self }
        var copy = self
        copy.nodeUrl = url
        return copy
    }

    /// Base units as the screens show them.
    func amountText(_ units: UInt64) -> String {
        Amounts.format(units, exponent: exponent, minDecimals: displayDecimals)
    }

    static let sky = CoinSpec(
        id: "SKY",
        name: "Skycoin",
        ticker: "SKY",
        kind: .skyFiber,
        // https, like every other endpoint here including Skycoin's own
        // explorer on the next line. Over plain http the query string of
        // every balance and history call carries the whole address book in
        // the clear, which hands anyone on the path the one thing a wallet
        // most wants kept apart: which addresses belong together.
        nodeUrl: "https://node.skycoin.com",
        explorerTxUrl: "https://explorer.skycoin.com/app/transaction/%s",
        builtIn: true
    )
    static let btc = CoinSpec(
        id: "BTC",
        name: "Bitcoin",
        ticker: "BTC",
        kind: .btc,
        nodeUrl: "https://mempool.space",
        explorerTxUrl: "https://mempool.space/tx/%s",
        builtIn: true
    )
    static let eth = CoinSpec(
        id: "ETH",
        name: "Ethereum",
        ticker: "ETH",
        kind: .eth,
        nodeUrl: ethNode,
        explorerTxUrl: "\(ethIndexer)/tx/%s",
        builtIn: true,
        indexerUrl: ethIndexer
    )
    static let usdt = CoinSpec(
        id: "USDT",
        name: "Tether USD",
        ticker: "USDT",
        kind: .erc20,
        nodeUrl: ethNode,
        explorerTxUrl: "\(ethIndexer)/tx/%s",
        builtIn: true,
        contract: "0xdAC17F958D2ee523a2206206994597C13D831ec7",
        tokenDecimals: 6,
        indexerUrl: ethIndexer
    )

    /// Keyless public endpoints; both are user-replaceable per token.
    static let ethNode = "https://ethereum-rpc.publicnode.com"
    static let ethIndexer = "https://eth.blockscout.com"
    static let defaultTokenDecimals = 18
}

/// A wallet: one seed, one coin, its derived addresses. Addresses are public
/// and kept here so opening the app never needs the sealed seed.
struct WalletMeta: Codable, Equatable, Identifiable, Sendable {
    var id: String
    var coinId: String
    var name: String
    var createdAtMs: Int64
    var receiveAddresses: [String]
    var changeAddresses: [String] = []
    /// When this wallet's addresses were last discovered from the chain, or 0
    /// while that has never succeeded.
    ///
    /// A restore asks the node which of the seed's addresses have been used;
    /// a fresh phrase has nothing to ask about and is scanned by definition.
    /// When the question cannot be put — the node is slow, the link is bad —
    /// the wallet is still created, holding only the first address, and the
    /// coins on the rest would be invisible and unspendable if nothing asked
    /// again. 0 means the question is still open: refresh asks it again until
    /// it is answered, and the screen says so meanwhile.
    var addressScanAtMs: Int64 = 0

    init(
        id: String, coinId: String, name: String, createdAtMs: Int64,
        receiveAddresses: [String], changeAddresses: [String] = [], addressScanAtMs: Int64 = 0
    ) {
        self.id = id
        self.coinId = coinId
        self.name = name
        self.createdAtMs = createdAtMs
        self.receiveAddresses = receiveAddresses
        self.changeAddresses = changeAddresses
        self.addressScanAtMs = addressScanAtMs
    }

    enum CodingKeys: String, CodingKey {
        case id, coinId, name, createdAtMs, receiveAddresses, changeAddresses, addressScanAtMs
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        coinId = try c.decode(String.self, forKey: .coinId)
        name = try c.decode(String.self, forKey: .name)
        createdAtMs = try c.decode(Int64.self, forKey: .createdAtMs)
        receiveAddresses = try c.decode([String].self, forKey: .receiveAddresses)
        changeAddresses = try c.decodeIfPresent([String].self, forKey: .changeAddresses) ?? []
        addressScanAtMs = try c.decodeIfPresent(Int64.self, forKey: .addressScanAtMs) ?? 0
    }

    /// True while this wallet's address list has never been confirmed
    /// against the chain.
    var addressScanPending: Bool { addressScanAtMs <= 0 }
}

/// Fold a completed address scan into a wallet, and mark the question
/// closed.
///
/// The lists only ever grow. A scan reports how many addresses the CHAIN has
/// seen; that is not how many the wallet HOLDS, because a user can ask for
/// further ones locally and may already have handed one out. Taking an
/// address away because nobody has paid it yet would be the same class of
/// mistake as never finding it: coins arriving somewhere the wallet no
/// longer watches.
func settledAddresses(_ meta: WalletMeta, scanned: AddressBook, nowMs: Int64) -> WalletMeta {
    var settled = meta
    if scanned.receive.count >= meta.receiveAddresses.count { settled.receiveAddresses = scanned.receive }
    if scanned.change.count >= meta.changeAddresses.count { settled.changeAddresses = scanned.change }
    settled.addressScanAtMs = nowMs
    return settled
}

/// One remembered transaction — TxRecord flattened for the cache file.
struct CachedTx: Codable, Equatable, Identifiable, Sendable {
    var txid: String
    var incoming: Bool
    var amount: UInt64
    var party: String?
    var timestamp: Int64
    var confirmed: Bool
    var confirmations: Int64
    var fee: UInt64?

    var id: String { txid }

    init(_ record: TxRecord) {
        txid = record.txid
        incoming = record.incoming
        amount = record.amount
        party = record.party
        timestamp = record.timestamp
        confirmed = record.confirmed
        confirmations = record.confirmations
        fee = record.fee
    }

    init(
        txid: String, incoming: Bool, amount: UInt64, party: String?, timestamp: Int64,
        confirmed: Bool, confirmations: Int64, fee: UInt64?
    ) {
        self.txid = txid
        self.incoming = incoming
        self.amount = amount
        self.party = party
        self.timestamp = timestamp
        self.confirmed = confirmed
        self.confirmations = confirmations
        self.fee = fee
    }
}

/// The last successful view of a wallet, kept on disk so the tab renders
/// instantly and honestly when the node is unreachable — the screen marks it
/// stale rather than blank.
struct WalletSnapshot: Codable, Equatable, Sendable {
    var confirmed: UInt64 = 0
    var predicted: UInt64 = 0
    var hours: UInt64?
    var spendableOutputs = 0
    var txs: [CachedTx] = []
    var fetchedAtMs: Int64 = 0
    /// When `txs` was last actually fetched, which can lag `fetchedAtMs`: the
    /// balance and the history are fetched separately and the history is the
    /// one that can be too big to arrive (see WalletStore.refresh). 0 when it
    /// has never landed — "do not claim this list is complete".
    var historyFetchedAtMs: Int64 = 0

    /// True when the tx list is older than the balance beside it, or never
    /// arrived.
    var historyBehind: Bool { historyFetchedAtMs < fetchedAtMs }

    var balance: WalletBalance {
        WalletBalance(confirmed: confirmed, predicted: predicted, hours: hours, spendableOutputs: spendableOutputs)
    }
}

/// Fold one refresh's results into the snapshot that gets cached.
///
/// `history` is nil when that fetch failed, which is a normal outcome rather
/// than an error: the balance is a few hundred bytes and the transaction list
/// is unbounded, so on a slow link the second can miss while the first lands.
/// When it misses, the last list we did get is carried forward unchanged and
/// its timestamp with it — so the snapshot goes on saying, truthfully, how
/// old that list is, and never passes an empty one off as a fetched one.
func mergeSnapshot(balance: WalletBalance, history: [TxRecord]?, previous: WalletSnapshot?, nowMs: Int64) -> WalletSnapshot {
    WalletSnapshot(
        confirmed: balance.confirmed,
        predicted: balance.predicted,
        hours: balance.hours,
        spendableOutputs: balance.spendableOutputs,
        txs: history.map { $0.map(CachedTx.init) } ?? previous?.txs ?? [],
        fetchedAtMs: nowMs,
        historyFetchedAtMs: history != nil ? nowMs : previous?.historyFetchedAtMs ?? 0
    )
}

/// Milliseconds since 1970, the unit the stored timestamps use.
func nowMs() -> Int64 { Int64(Date().timeIntervalSince1970 * 1000) }
