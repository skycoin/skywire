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
public protocol WalletCore: Sendable {

    /// A fresh 12-word BIP 39 phrase.
    func newSeed() -> String

    /// Wordlist + checksum validation of a phrase being restored.
    func validateSeed(_ mnemonic: String) -> Bool

    /// Deterministic addresses for this seed. Skycoin-family wallets have a
    /// single chain (change returns to the first address, change list stays
    /// empty); Bitcoin derives a separate change chain the UI never shows.
    func deriveAddresses(seed: String, receiveCount: Int, changeCount: Int) throws -> AddressBook

    func validateAddress(_ address: String) -> Bool

    /// On restore: probe the network for used addresses. (receive, change)
    /// counts, each ≥ the minimum.
    func scanUsed(seed: String) async throws -> (receive: Int, change: Int)

    func balance(_ book: AddressBook) async throws -> WalletBalance

    func history(_ book: AddressBook) async throws -> [TxRecord]

    /// Bitcoin fee-rate presets in sat/vB; nil on chains that burn hours or
    /// price themselves.
    func feePresets() async throws -> FeePresets?

    /// Largest sendable amount given the current balance (fee-adjusted on
    /// Bitcoin and ETH).
    func estimateMax(balance: WalletBalance, feeRate: Int?) -> UInt64

    /// Build and fully price an unsigned transaction. Throws WalletError on
    /// validation problems, NetworkError or URLError when the node cannot
    /// be reached or answers badly.
    func buildTx(
        seed: String,
        book: AddressBook,
        toAddress: String,
        amount: UInt64,
        feeRate: Int?,
        sendMax: Bool
    ) async throws -> TxPlan

    /// Local signing — the only step that touches keys, and it never
    /// suspends.
    func signTx(seed: String, plan: TxPlan) throws -> SignedTx

    /// Hand the signed transaction to the node; returns the txid it reports.
    func broadcast(_ tx: SignedTx) async throws -> String
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

public struct WalletBalance: Equatable, Sendable {
    /// Spendable now (confirmed, minus outputs already spent by pending txns).
    public let confirmed: UInt64
    /// Balance once pending transactions settle.
    public let predicted: UInt64
    /// Calculated coin hours held — nil off the Skycoin family.
    public let hours: UInt64?
    /// Confirmed spendable outputs backing the balance (funded addresses on ETH).
    public let spendableOutputs: Int

    public init(confirmed: UInt64, predicted: UInt64, hours: UInt64?, spendableOutputs: Int) {
        self.confirmed = confirmed
        self.predicted = predicted
        self.hours = hours
        self.spendableOutputs = spendableOutputs
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

    public init(
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

public struct TxPlan: Sendable {
    public let toAddress: String
    public let amount: UInt64
    /// Burned hours on Skycoin-family chains, satoshis on Bitcoin, gwei on ETH.
    public let fee: UInt64
    public let changeAmount: UInt64
    /// Hours arriving at the destination — Skycoin family only.
    public let hoursToRecipient: UInt64?
    /// Hours coming back with change — Skycoin family only.
    public let hoursChange: UInt64?
    /// Virtual size in vbytes (Bitcoin), gas limit (ETH).
    public let vsize: Int?
    public let feeRate: Int?
    /// What signTx needs, per chain.
    let payload: TxPayload
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
