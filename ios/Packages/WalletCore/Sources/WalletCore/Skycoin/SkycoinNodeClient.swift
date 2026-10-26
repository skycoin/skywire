import Foundation

/// REST client for a Skycoin-family node. Every fiber chain runs the same
/// daemon, so one client serves Skycoin and any user-added fiber coin — only
/// the base URL differs.
public struct SkycoinNodeClient: Sendable {

    private let base: URL
    private let http: HttpClient

    public init(baseURL: String, session: URLSession, failover: NodeFailover? = nil) throws {
        guard let url = parseBaseURL(baseURL) else { throw WalletCoreError("invalid node URL: \(baseURL)") }
        base = url
        http = HttpClient(session: session, failover: failover)
    }

    public struct VerifyTxnParams: Decodable, Sendable {
        public var burnFactor: UInt32 = 10
        public var maxTransactionSize: UInt32 = 32768
        public var maxDecimals: Int = 3

        enum CodingKeys: String, CodingKey {
            case burnFactor = "burn_factor", maxTransactionSize = "max_transaction_size", maxDecimals = "max_decimals"
        }

        public init() {}

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            burnFactor = try c.value(.burnFactor, default: 10)
            maxTransactionSize = try c.value(.maxTransactionSize, default: 32768)
            maxDecimals = try c.value(.maxDecimals, default: 3)
        }
    }

    public struct BlockHeader: Decodable, Sendable {
        public var seq: UInt64 = 0
        public var timestamp: UInt64 = 0

        enum CodingKeys: String, CodingKey { case seq, timestamp }

        public init() {}

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            seq = try c.value(.seq, default: 0)
            timestamp = try c.value(.timestamp, default: 0)
        }
    }

    public struct BlockchainInfo: Decodable, Sendable {
        public var head = BlockHeader()

        enum CodingKeys: String, CodingKey { case head }

        public init() {}

        public init(from decoder: Decoder) throws {
            head = try decoder.container(keyedBy: CodingKeys.self).value(.head, default: BlockHeader())
        }
    }

    public struct BuildInfo: Decodable, Sendable {
        public var version = ""

        enum CodingKeys: String, CodingKey { case version }

        public init() {}

        public init(from decoder: Decoder) throws {
            version = try decoder.container(keyedBy: CodingKeys.self).value(.version, default: "")
        }
    }

    /// The fiber.toml settings a node reports: how the coin names and shows
    /// itself.
    public struct FiberInfo: Decodable, Sendable {
        public var name = ""
        public var displayName = ""
        public var ticker = ""
        public var explorerURL = ""

        enum CodingKeys: String, CodingKey {
            case name, displayName = "display_name", ticker, explorerURL = "explorer_url"
        }

        public init() {}

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            name = try c.value(.name, default: "")
            displayName = try c.value(.displayName, default: "")
            ticker = try c.value(.ticker, default: "")
            explorerURL = try c.value(.explorerURL, default: "")
        }
    }

    public struct Health: Decodable, Sendable {
        public var blockchain = BlockchainInfo()
        public var userVerifyTxn = VerifyTxnParams()
        public var coin = ""
        public var version = BuildInfo()
        public var blockchainPubkey = ""
        public var fiber = FiberInfo()
        public var openConnections = -1

        enum CodingKeys: String, CodingKey {
            case blockchain, userVerifyTxn = "user_verify_transaction", coin, version
            case blockchainPubkey = "blockchain_pubkey", fiber, openConnections = "open_connections"
        }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            blockchain = try c.value(.blockchain, default: BlockchainInfo())
            userVerifyTxn = try c.value(.userVerifyTxn, default: VerifyTxnParams())
            coin = try c.value(.coin, default: "")
            version = try c.value(.version, default: BuildInfo())
            blockchainPubkey = try c.value(.blockchainPubkey, default: "")
            fiber = try c.value(.fiber, default: FiberInfo())
            openConnections = try c.value(.openConnections, default: -1)
        }
    }

    public struct BlockHead: Decodable, Sendable {
        public var seq: UInt64 = 0
        public var timestamp: UInt64 = 0
        public var blockHash = ""

        enum CodingKeys: String, CodingKey { case seq, timestamp, blockHash = "block_hash" }

        public init() {}

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            seq = try c.value(.seq, default: 0)
            timestamp = try c.value(.timestamp, default: 0)
            blockHash = try c.value(.blockHash, default: "")
        }
    }

    public struct Metadata: Decodable, Sendable {
        public var head = BlockHead()

        enum CodingKeys: String, CodingKey { case head }

        public init(from decoder: Decoder) throws {
            head = try decoder.container(keyedBy: CodingKeys.self).value(.head, default: BlockHead())
        }
    }

    public struct Supply: Decodable, Sendable {
        public var currentSupply = ""
        public var totalSupply = ""

        enum CodingKeys: String, CodingKey { case currentSupply = "current_supply", totalSupply = "total_supply" }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            currentSupply = try c.lenientString(.currentSupply, default: "")
            totalSupply = try c.lenientString(.totalSupply, default: "")
        }
    }

    public struct Connection: Decodable, Sendable {
        public var address = ""
        public var height: UInt64 = 0
        public var outgoing = false
        public var trusted = false
        public var userAgent = ""
        public var lastSent: Int64 = 0
        public var lastReceived: Int64 = 0

        enum CodingKeys: String, CodingKey {
            case address, height, outgoing, trusted = "is_trusted_peer", userAgent = "user_agent"
            case lastSent = "last_sent", lastReceived = "last_received"
        }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            address = try c.value(.address, default: "")
            height = try c.value(.height, default: 0)
            outgoing = try c.value(.outgoing, default: false)
            trusted = try c.value(.trusted, default: false)
            userAgent = try c.value(.userAgent, default: "")
            lastSent = try c.value(.lastSent, default: 0)
            lastReceived = try c.value(.lastReceived, default: 0)
        }
    }

    public struct Connections: Decodable, Sendable {
        public var connections: [Connection] = []

        enum CodingKeys: String, CodingKey { case connections }

        public init(from decoder: Decoder) throws {
            connections = try decoder.container(keyedBy: CodingKeys.self).value(.connections, default: [])
        }
    }

    public struct PendingEntry: Decodable, Sendable {
        public let transaction: VerboseTxn
        public var received = ""

        enum CodingKeys: String, CodingKey { case transaction, received }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            transaction = try c.decode(VerboseTxn.self, forKey: .transaction)
            received = try c.value(.received, default: "")
        }
    }

    /// How far the node has synced: its own height and the highest its
    /// peers report.
    public struct Progress: Decodable, Sendable {
        public var current: UInt64 = 0
        public var highest: UInt64 = 0

        enum CodingKeys: String, CodingKey { case current, highest }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            current = try c.value(.current, default: 0)
            highest = try c.value(.highest, default: 0)
        }
    }

    public struct NodeOutput: Decodable, Sendable {
        public let hash: String
        public var time: UInt64 = 0
        public var blockSeq: UInt64 = 0
        public var srcTx = ""
        public let address: String
        public let coins: String
        public var hours: UInt64 = 0
        public var calculatedHours: UInt64 = 0

        enum CodingKeys: String, CodingKey {
            case hash, time, blockSeq = "block_seq", srcTx = "src_tx", address, coins, hours
            case calculatedHours = "calculated_hours"
        }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            hash = try c.decode(String.self, forKey: .hash)
            time = try c.value(.time, default: 0)
            blockSeq = try c.value(.blockSeq, default: 0)
            srcTx = try c.value(.srcTx, default: "")
            address = try c.decode(String.self, forKey: .address)
            coins = try c.decode(String.self, forKey: .coins)
            hours = try c.value(.hours, default: 0)
            calculatedHours = try c.value(.calculatedHours, default: 0)
        }
    }

    public struct Outputs: Decodable, Sendable {
        public var headOutputs: [NodeOutput] = []
        public var outgoingOutputs: [NodeOutput] = []
        public var incomingOutputs: [NodeOutput] = []

        enum CodingKeys: String, CodingKey {
            case headOutputs = "head_outputs", outgoingOutputs = "outgoing_outputs", incomingOutputs = "incoming_outputs"
        }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            headOutputs = try c.value(.headOutputs, default: [])
            outgoingOutputs = try c.value(.outgoingOutputs, default: [])
            incomingOutputs = try c.value(.incomingOutputs, default: [])
        }
    }

    public struct BalancePair: Decodable, Sendable {
        public var coins: UInt64 = 0
        public var hours: UInt64 = 0

        enum CodingKeys: String, CodingKey { case coins, hours }

        public init() {}

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            coins = try c.value(.coins, default: 0)
            hours = try c.value(.hours, default: 0)
        }
    }

    public struct Balance: Decodable, Sendable {
        public var confirmed = BalancePair()
        public var predicted = BalancePair()

        enum CodingKeys: String, CodingKey { case confirmed, predicted }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            confirmed = try c.value(.confirmed, default: BalancePair())
            predicted = try c.value(.predicted, default: BalancePair())
        }
    }

    public struct TxnStatus: Decodable, Sendable {
        public var confirmed = false
        public var unconfirmed = false
        /// When confirmed: how many blocks deep (1 = in the head block).
        public var height: UInt64 = 0
        public var blockSeq: UInt64 = 0

        enum CodingKeys: String, CodingKey { case confirmed, unconfirmed, height, blockSeq = "block_seq" }

        public init() {}

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            confirmed = try c.value(.confirmed, default: false)
            unconfirmed = try c.value(.unconfirmed, default: false)
            height = try c.value(.height, default: 0)
            blockSeq = try c.value(.blockSeq, default: 0)
        }
    }

    public struct VerboseInput: Decodable, Sendable {
        public let uxid: String
        public let owner: String
        public var coins = "0"
        public var hours: UInt64 = 0
        public var calculatedHours: UInt64 = 0

        enum CodingKeys: String, CodingKey { case uxid, owner, coins, hours, calculatedHours = "calculated_hours" }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            uxid = try c.decode(String.self, forKey: .uxid)
            owner = try c.decode(String.self, forKey: .owner)
            coins = try c.value(.coins, default: "0")
            hours = try c.value(.hours, default: 0)
            calculatedHours = try c.value(.calculatedHours, default: 0)
        }
    }

    public struct VerboseOutput: Decodable, Sendable {
        public var uxid = ""
        public let dst: String
        public var coins = "0"
        public var hours: UInt64 = 0

        enum CodingKeys: String, CodingKey { case uxid, dst, coins, hours }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            uxid = try c.value(.uxid, default: "")
            dst = try c.decode(String.self, forKey: .dst)
            coins = try c.value(.coins, default: "0")
            hours = try c.value(.hours, default: 0)
        }
    }

    public struct VerboseTxn: Decodable, Sendable {
        public let txid: String
        public var timestamp: UInt64 = 0
        public var innerHash = ""
        public var fee: UInt64 = 0
        public var inputs: [VerboseInput] = []
        public var outputs: [VerboseOutput] = []

        enum CodingKeys: String, CodingKey { case txid, timestamp, innerHash = "inner_hash", fee, inputs, outputs }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            txid = try c.decode(String.self, forKey: .txid)
            timestamp = try c.value(.timestamp, default: 0)
            innerHash = try c.value(.innerHash, default: "")
            fee = try c.value(.fee, default: 0)
            inputs = try c.value(.inputs, default: [])
            outputs = try c.value(.outputs, default: [])
        }
    }

    public struct TxnEntry: Decodable, Sendable {
        public var status = TxnStatus()
        public var time: UInt64 = 0
        public let txn: VerboseTxn

        enum CodingKeys: String, CodingKey { case status, time, txn }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            status = try c.value(.status, default: TxnStatus())
            time = try c.value(.time, default: 0)
            txn = try c.decode(VerboseTxn.self, forKey: .txn)
        }
    }

    public func health() async throws -> Health {
        try await get("api/v1/health")
    }

    public func outputs(_ addresses: [String]) async throws -> Outputs {
        try await get("api/v1/outputs", [("addrs", addresses.joined(separator: ","))])
    }

    public func balance(_ addresses: [String]) async throws -> Balance {
        try await get("api/v1/balance", [("addrs", addresses.joined(separator: ","))])
    }

    public func transactions(_ addresses: [String]) async throws -> [TxnEntry] {
        try await get("api/v1/transactions", [("addrs", addresses.joined(separator: ",")), ("verbose", "1")])
    }

    public func progress() async throws -> Progress {
        try await get("api/v1/blockchain/progress")
    }

    public func metadata() async throws -> Metadata {
        try await get("api/v1/blockchain/metadata")
    }

    public func coinSupply() async throws -> Supply {
        try await get("api/v1/coinSupply")
    }

    public func connections() async throws -> Connections {
        try await get("api/v1/network/connections")
    }

    public func pendingTxs() async throws -> [PendingEntry] {
        try await get("api/v1/pendingTxs", [("verbose", "1")])
    }

    /// Removes a transaction from the node's own unconfirmed pool. Peers
    /// that hold it are unaffected.
    public func deletePending(txid: String) async throws {
        let csrf = await fetchCsrf()
        let resp = try await http.delete(
            endpoint(base, "api/v1/pendingTxs", [("txid", txid)]),
            headers: csrf.map { ["X-CSRF-Token": $0] } ?? [:]
        )
        if !resp.isSuccessful {
            throw WalletError.nodeRejected(Self.errorMessage(resp.body, code: resp.status))
        }
    }

    /// Broadcast; returns the txid the node reports. Node-side rejections
    /// carry the node's own words.
    public func inject(rawTxHex: String) async throws -> String {
        let csrf = await fetchCsrf()
        let payload = Data("{\"rawtx\":\"\(rawTxHex)\"}".utf8)
        let resp = try await http.post(
            endpoint(base, "api/v1/injectTransaction"),
            body: payload,
            contentType: "application/json",
            headers: csrf.map { ["X-CSRF-Token": $0] } ?? [:]
        )
        if !resp.isSuccessful {
            throw WalletError.nodeRejected(Self.errorMessage(resp.body, code: resp.status))
        }
        return try JSONDecoder().decode(String.self, from: Data(resp.body.trimmingCharacters(in: .whitespacesAndNewlines).utf8))
    }

    /// CSRF tokens are optional server-side; absence is not an error.
    private func fetchCsrf() async -> String? {
        guard let resp = try? await http.get(endpoint(base, "api/v1/csrf")), resp.isSuccessful,
              let obj = try? JSONSerialization.jsonObject(with: Data(resp.body.utf8)) as? [String: Any]
        else { return nil }
        return obj["csrf_token"] as? String
    }

    private func get<T: Decodable>(_ path: String, _ query: [(String, String)] = []) async throws -> T {
        let resp = try await http.get(endpoint(base, path, query))
        if !resp.isSuccessful { throw NetworkError(Self.errorMessage(resp.body, code: resp.status)) }
        do {
            return try JSONDecoder().decode(T.self, from: Data(resp.body.utf8))
        } catch {
            throw NetworkError("node answered in an unexpected shape: \(error)")
        }
    }

    /// Prefer the node's own message: v2 wraps it in error.message, v1 is
    /// plain text.
    public static func errorMessage(_ body: String, code: Int) -> String {
        if let obj = try? JSONSerialization.jsonObject(with: Data(body.utf8)) as? [String: Any],
           let error = obj["error"] as? [String: Any],
           let message = error["message"] as? String {
            return String(message.prefix(300))
        }
        return serverMessage(body, status: code, who: "node")
    }
}
