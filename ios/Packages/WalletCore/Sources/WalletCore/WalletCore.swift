import Foundation

/// The one seam the app sees. Three implementations: Skycoin/fiber nodes
/// (parameterized by node URL — every fiber chain speaks the same API),
/// Bitcoin over an esplora-compatible server, and Ethereum (+ one ERC-20 per
/// core) over JSON-RPC and an etherscan-style indexer.
///
/// Amounts are integers in the chain's base unit: droplets (10⁻⁶) for
/// Skycoin-family coins, satoshis (10⁻⁸) for Bitcoin, gwei (10⁻⁹) for ETH.
/// Fees mean different things per chain — burned coin hours vs satoshis vs
/// gwei — and the plan carries whichever applies.
///
/// Kotlin's interface methods with default bodies are requirements here with
/// the same defaults in the extension below.
public protocol WalletCore: Sendable {

    /// A fresh 12-word BIP 39 phrase.
    func newSeed() -> String

    /// Wordlist + checksum validation of a phrase being restored.
    func validateSeed(_ mnemonic: String) -> Bool

    /// A phrase restored although its checksum fails, after the user
    /// confirmed it. The older Skycoin wallets wrote 24 list words with no
    /// checksum.
    func acceptsLegacySeed(_ mnemonic: String) -> Bool

    /// Whether any text, taken exactly as written, can be a seed. The classic
    /// fiber wallet hashes the seed's bytes, so skycoin-web lets a user
    /// restore one from text that is not a BIP 39 phrase at all, after a
    /// warning.
    func takesAnySeed() -> Bool

    /// Deterministic addresses for this seed. Skycoin-family wallets have a
    /// single chain (change returns to the first address, change list stays
    /// empty); Bitcoin derives a separate change chain the UI never shows.
    func deriveAddresses(seed: KeySource, receiveCount: Int, changeCount: Int) throws -> AddressBook

    func validateAddress(_ address: String) -> Bool

    /// On restore: probe the network for used addresses. (receive, change)
    /// counts, each ≥ the minimum.
    func scanUsed(seed: KeySource) async throws -> (receive: Int, change: Int)

    func balance(_ book: AddressBook) async throws -> WalletBalance

    /// The wallet's unspent outputs; empty where a chain has no coin control.
    func utxos(_ book: AddressBook) async throws -> [UtxoRecord]

    func history(_ book: AddressBook) async throws -> [TxRecord]

    /// What the node says about itself, so it can be checked before it is
    /// used. Throws NetworkError or URLError when it cannot be reached.
    func nodeInfo() async throws -> NodeInfo

    /// Bitcoin fee-rate presets in sat/vB; nil on chains that burn hours or
    /// price themselves.
    func feePresets() async throws -> FeePresets?

    /// Largest sendable amount given the current balance (fee-adjusted on
    /// Bitcoin and ETH).
    func estimateMax(balance: WalletBalance, feeRate: Int?) -> UInt64

    /// Build and fully price an unsigned transaction. Throws WalletError on
    /// validation problems, NetworkError or URLError when the node cannot
    /// be reached or answers badly.
    ///
    /// - fromAddress: spends only outputs held by this one address; nil
    ///   spends from the whole wallet. Ignored where a wallet is one address.
    /// - inputs: hashes of the unspent outputs to spend; nil chooses them
    ///   automatically. Fiber chains only.
    /// - hoursTo: hours the destination receives; nil splits them
    ///   automatically. Fiber chains only.
    /// - more: destinations after the first, in output order. Fiber chains
    ///   only; hours are given for every destination or for none.
    /// - changeAddress: where change goes; nil keeps the chain's own rule.
    ///   Fiber chains only.
    /// - shareFactor: the share of post-burn hours automatic hours send
    ///   onward, 0 to 1; nil is the default half. Fiber chains only.
    /// - feeLevel: how much tip to offer; nil is normal. Ethereum family only.
    ///
    /// The extension below adds the six-argument form, which leaves all of
    /// these at their defaults.
    func buildTx(
        seed: KeySource,
        book: AddressBook,
        toAddress: String,
        amount: UInt64,
        feeRate: Int?,
        sendMax: Bool,
        fromAddress: String?,
        inputs: Set<String>?,
        hoursTo: UInt64?,
        more: [Recipient],
        changeAddress: String?,
        shareFactor: Decimal?,
        feeLevel: FeeLevel?
    ) async throws -> TxPlan

    /// Local signing — the only step that touches keys, and it never
    /// suspends.
    func signTx(seed: KeySource, plan: TxPlan) throws -> SignedTx

    /// Hand the signed transaction to the node; returns the txid it reports.
    func broadcast(_ tx: SignedTx) async throws -> String

    /// The wallet's extended public keys, for a watch-only view; nil where a
    /// wallet has none.
    func extendedPublicKeys(seed: KeySource) throws -> ExtendedPublicKeys?

    /// The plan's transaction with no signatures, to be signed elsewhere.
    /// Nil where a chain has no such form here.
    func unsignedHex(_ plan: TxPlan) -> String?

    /// Signs a raw transaction made elsewhere. Every input must be an
    /// unspent output of this wallet. Fiber chains only.
    func signRaw(seed: KeySource, book: AddressBook, rawHex: String) async throws -> SignedTx

    /// Broadcasts a raw signed transaction; returns the txid the node
    /// reports. Fiber chains only.
    func broadcastRaw(_ rawHex: String) async throws -> String
}

extension WalletCore {
    public func acceptsLegacySeed(_ mnemonic: String) -> Bool { false }

    public func takesAnySeed() -> Bool { false }

    public func utxos(_ book: AddressBook) async throws -> [UtxoRecord] { [] }

    public func extendedPublicKeys(seed: KeySource) throws -> ExtendedPublicKeys? { nil }

    public func unsignedHex(_ plan: TxPlan) -> String? { nil }

    public func signRaw(seed: KeySource, book: AddressBook, rawHex: String) async throws -> SignedTx {
        throw WalletError.badTransaction("this chain does not sign raw transactions here")
    }

    public func broadcastRaw(_ rawHex: String) async throws -> String {
        throw WalletError.badTransaction("this chain does not broadcast raw transactions here")
    }

    /// A plain send: one destination, the chain's own input choice, hours
    /// split and change address, and the normal fee level.
    public func buildTx(
        seed: KeySource,
        book: AddressBook,
        toAddress: String,
        amount: UInt64,
        feeRate: Int?,
        sendMax: Bool
    ) async throws -> TxPlan {
        try await buildTx(
            seed: seed, book: book, toAddress: toAddress, amount: amount, feeRate: feeRate, sendMax: sendMax,
            fromAddress: nil, inputs: nil, hoursTo: nil, more: [], changeAddress: nil, shareFactor: nil, feeLevel: nil
        )
    }
}

/// A BIP 44 account's xpub and its receive and change chain xpubs.
public struct ExtendedPublicKeys: Equatable, Sendable {
    public let account: String
    public let receive: String
    public let change: String

    public init(account: String, receive: String, change: String) {
        self.account = account
        self.receive = receive
        self.change = change
    }
}

/// One destination of a send. `hours` is fiber only; nil splits hours
/// automatically.
public struct Recipient: Equatable, Sendable {
    public let address: String
    public let amount: UInt64
    public let hours: UInt64?

    public init(address: String, amount: UInt64, hours: UInt64? = nil) {
        self.address = address
        self.amount = amount
        self.hours = hours
    }
}

/// What a wallet's keys come from: its phrase, the BIP 39 passphrase that
/// goes with it, and the way keys are derived from the two.
public struct KeySource: Equatable, Sendable {
    public let mnemonic: String
    public let passphrase: String
    public let derivation: Derivation

    public init(mnemonic: String, passphrase: String = "", derivation: Derivation = .standard) {
        self.mnemonic = mnemonic
        self.passphrase = passphrase
        self.derivation = derivation
    }
}

/// How keys come from a phrase. `standard` is each chain's usual way:
/// Skycoin's deterministic chain over the phrase bytes for fiber coins,
/// BIP 84 for Bitcoin, BIP 44 for Ethereum. `bip44` is the alternative a
/// chain offers: BIP 44 coin 8000 for fiber coins, legacy BIP 44 for
/// Bitcoin. The raw values are the Kotlin enum's names.
public enum Derivation: String, Codable, CaseIterable, Sendable {
    case standard = "STANDARD"
    case bip44 = "BIP44"
}

public struct AddressBook: Equatable, Sendable {
    public let receive: [String]
    public let change: [String]

    public init(receive: [String], change: [String]) {
        self.receive = receive
        self.change = change
    }

    public func all() -> [String] { receive + change }
}

/// One unspent output, as a coin picker shows it. Fiber chains only.
public struct UtxoRecord: Equatable, Sendable {
    public let uxid: String
    public let address: String
    public let coins: UInt64
    /// Calculated hours at the chain head.
    public let hours: UInt64

    public init(uxid: String, address: String, coins: UInt64, hours: UInt64) {
        self.uxid = uxid
        self.address = address
        self.coins = coins
        self.hours = hours
    }
}

public struct WalletBalance: Equatable, Sendable {
    /// Spendable now (confirmed, minus outputs already spent by pending txns).
    public let confirmed: UInt64
    /// Balance once pending transactions settle.
    public let predicted: UInt64
    /// Calculated coin hours held — nil off the Skycoin family.
    public let hours: UInt64?
    /// Confirmed spendable outputs backing the balance (funded addresses on ETH).
    public let spendableOutputs: Int
    /// Confirmed coins held by each single address — the addresses sheet's
    /// numbers. Empty when a chain reports only the total.
    public let perAddress: [String: UInt64]

    public init(
        confirmed: UInt64, predicted: UInt64, hours: UInt64?, spendableOutputs: Int,
        perAddress: [String: UInt64] = [:]
    ) {
        self.confirmed = confirmed
        self.predicted = predicted
        self.hours = hours
        self.spendableOutputs = spendableOutputs
        self.perAddress = perAddress
    }
}

public struct TxRecord: Equatable, Sendable {
    public let txid: String
    public let incoming: Bool
    /// Net effect on this wallet, in base units, always ≥ 0.
    public let amount: UInt64
    /// Counterparty address when one exists (self-sends have none).
    public let party: String?
    /// Unix seconds; block time when confirmed, first-seen time while pending.
    public let timestamp: Int64
    public let confirmed: Bool
    public let confirmations: Int64
    /// Burned hours (Skycoin family), satoshis (Bitcoin) or gwei (ETH); nil
    /// when unknown.
    public let fee: UInt64?
    /// This wallet's addresses the coins left from (a send) or arrived at
    /// (a receive).
    public let own: [String]

    public init(
        txid: String, incoming: Bool, amount: UInt64, party: String?, timestamp: Int64,
        confirmed: Bool, confirmations: Int64, fee: UInt64?, own: [String] = []
    ) {
        self.txid = txid
        self.incoming = incoming
        self.amount = amount
        self.party = party
        self.timestamp = timestamp
        self.confirmed = confirmed
        self.confirmations = confirmations
        self.fee = fee
        self.own = own
    }
}

public struct FeePresets: Equatable, Sendable {
    public let economy: Int
    public let normal: Int
    public let priority: Int

    public init(economy: Int, normal: Int, priority: Int) {
        self.economy = economy
        self.normal = normal
        self.priority = priority
    }
}

/// A node's own account of itself. Fields a chain does not report are nil.
public struct NodeInfo: Equatable, Sendable {
    public let coinName: String?
    public let ticker: String?
    public let version: String?
    public let height: Int64?
    /// Fiber chains: one hour in this many is burned per transaction.
    public let burnFactor: Int?
    public let pubkey: String?
    /// Fiber chains: the coin's block explorer.
    public let explorerURL: String?
    /// Why the wallet should not use this node; nil when it can.
    public let problem: NodeProblem?
    /// Fiber chains: the node's peer connections.
    public let connections: Int?
    /// Fiber chains: false while the node is still catching up with its peers.
    public let synced: Bool?

    public init(
        coinName: String? = nil, ticker: String? = nil, version: String? = nil, height: Int64? = nil,
        burnFactor: Int? = nil, pubkey: String? = nil, explorerURL: String? = nil, problem: NodeProblem? = nil,
        connections: Int? = nil, synced: Bool? = nil
    ) {
        self.coinName = coinName
        self.ticker = ticker
        self.version = version
        self.height = height
        self.burnFactor = burnFactor
        self.pubkey = pubkey
        self.explorerURL = explorerURL
        self.problem = problem
        self.connections = connections
        self.synced = synced
    }
}

public enum NodeProblem: String, Codable, CaseIterable, Sendable {
    /// A Skycoin-family node older than 0.24, which the wallet's calls do not fit.
    case tooOld = "TOO_OLD"
    /// An Ethereum server on a chain other than mainnet.
    case wrongChain = "WRONG_CHAIN"
}

/// An Ethereum fee choice: the least tip, the node's suggested tip, or twice it.
public enum FeeLevel: String, Codable, CaseIterable, Sendable {
    case slow = "SLOW"
    case normal = "NORMAL"
    case fast = "FAST"
}

public struct TxPlan: Sendable {
    /// The first destination.
    public let toAddress: String
    /// Coins leaving for every destination together.
    public let amount: UInt64
    /// Burned hours on Skycoin-family chains, satoshis on Bitcoin, gwei on ETH.
    public let fee: UInt64
    public let changeAmount: UInt64
    /// Hours arriving at the destinations — Skycoin family only.
    public let hoursToRecipient: UInt64?
    /// Hours coming back with change — Skycoin family only.
    public let hoursChange: UInt64?
    /// Virtual size in vbytes (Bitcoin), gas limit (ETH).
    public let vsize: Int?
    public let feeRate: Int?
    /// What signTx needs, per chain.
    let payload: TxPayload
    /// Every destination with the hours it receives, in output order.
    public let recipients: [Recipient]
    /// Where the change goes, when the chain lets a send say.
    public let changeAddress: String?

    init(
        toAddress: String, amount: UInt64, fee: UInt64, changeAmount: UInt64, hoursToRecipient: UInt64?,
        hoursChange: UInt64?, vsize: Int?, feeRate: Int?, payload: TxPayload,
        recipients: [Recipient]? = nil, changeAddress: String? = nil
    ) {
        self.toAddress = toAddress
        self.amount = amount
        self.fee = fee
        self.changeAmount = changeAmount
        self.hoursToRecipient = hoursToRecipient
        self.hoursChange = hoursChange
        self.vsize = vsize
        self.feeRate = feeRate
        self.payload = payload
        self.recipients = recipients ?? [Recipient(address: toAddress, amount: amount, hours: hoursToRecipient)]
        self.changeAddress = changeAddress
    }
}

/// The implementation half of a plan; the app never sees inside it.
enum TxPayload: Sendable {
    case sky(SkyFiberWalletCore.Payload)
    case btc(BtcWalletCore.Payload)
    case eth(EthWalletCore.Payload)
}

public struct SignedTx: Sendable {
    public let rawHex: String
    public let txid: String
    /// Bitcoin: the change-chain index the plan consumed.
    let changeIndex: Int?

    init(rawHex: String, txid: String, changeIndex: Int? = nil) {
        self.rawHex = rawHex
        self.txid = txid
        self.changeIndex = changeIndex
    }
}
