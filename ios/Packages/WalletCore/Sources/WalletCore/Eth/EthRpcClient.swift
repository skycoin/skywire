import Foundation

/// Ethereum chain access, split the same way Bitcoin's is: a node for state
/// and broadcast (JSON-RPC), an indexer for history — plain JSON-RPC cannot
/// list an address's past transactions. The indexer speaks the
/// etherscan-style `?module=account` API, which Blockscout instances serve
/// without a key. Keys never appear on either wire.
public actor EthRpcClient {

    /// Most recent rows per address — a phone screen, not an archive.
    private static let historyPage = "50"

    private let rpc: URL
    private let indexer: URL?
    private let http: HttpClient
    private var requestId = 0

    public init(rpcURL: String, indexerURL: String?, session: URLSession) throws {
        guard let url = parseBaseURL(rpcURL) else { throw WalletCoreError("invalid RPC URL: \(rpcURL)") }
        rpc = url
        indexer = indexerURL.flatMap(parseBaseURL)
        http = HttpClient(session: session)
    }

    // MARK: JSON-RPC

    public func balanceWei(_ address: String) async throws -> BigUInt {
        try quantity(await call("eth_getBalance", address, "latest"))
    }

    /// Pending-tag nonce, so back-to-back sends chain instead of colliding.
    public func nonce(_ address: String) async throws -> BigUInt {
        try quantity(await call("eth_getTransactionCount", address, "pending"))
    }

    public func chainId() async throws -> BigUInt {
        try quantity(await call("eth_chainId"))
    }

    /// The node's tip suggestion, or 1 gwei where the method is not served.
    public func maxPriorityFeePerGas() async -> BigUInt {
        (try? quantity(await call("eth_maxPriorityFeePerGas"))) ?? BigUInt(1_000_000_000)
    }

    public func baseFeePerGas() async throws -> BigUInt {
        let block = try await call("eth_getBlockByNumber", "latest", false)
        guard let fee = (block as? [String: Any])?["baseFeePerGas"], !(fee is NSNull) else { return .zero }
        return try quantity(fee)
    }

    /// The node's gas estimate, or nil when it refuses (the caller falls back).
    public func estimateGas(from: String, to: String, valueWei: BigUInt, data: [UInt8]?) async -> BigUInt? {
        var tx: [String: Any] = ["from": from, "to": to]
        if !valueWei.isZero { tx["value"] = "0x" + valueWei.hex }
        if let data, !data.isEmpty { tx["data"] = "0x" + data.hex }
        return try? quantity(await call("eth_estimateGas", tx))
    }

    /// A read-only contract call; returns the raw return bytes.
    public func ethCall(to: String, data: [UInt8]) async throws -> [UInt8] {
        let result = try await call("eth_call", ["to": to, "data": "0x" + data.hex], "latest")
        return try hexBytes(result)
    }

    /// Broadcast; returns the tx hash the node reports.
    public func sendRaw(_ raw: [UInt8]) async throws -> String {
        let result = try await call("eth_sendRawTransaction", "0x" + raw.hex)
        guard let hash = result as? String else { throw NetworkError("node answered a non-string tx hash") }
        return hash
    }

    private func call(_ method: String, _ params: Any...) async throws -> Any {
        requestId += 1
        let envelope: [String: Any] = ["jsonrpc": "2.0", "id": requestId, "method": method, "params": params]
        let body = try JSONSerialization.data(withJSONObject: envelope)
        let resp = try await http.post(rpc, body: body, contentType: "application/json")
        if !resp.isSuccessful { throw NetworkError("node answered HTTP \(resp.status)") }
        guard let obj = try? JSONSerialization.jsonObject(with: Data(resp.body.utf8)) as? [String: Any] else {
            throw NetworkError("node answered in an unexpected shape")
        }
        if let err = obj["error"], !(err is NSNull) {
            // The node's own words: an estimateGas revert reason or a
            // rejected broadcast is a message the user can act on.
            let message = ((err as? [String: Any])?["message"] as? String) ?? String(describing: err)
            throw WalletError.nodeRejected(message)
        }
        guard let result = obj["result"], !(result is NSNull) else {
            throw NetworkError("node answered without a result")
        }
        return result
    }

    private func quantity(_ value: Any) throws -> BigUInt {
        guard var text = value as? String else { throw NetworkError("node answered a non-string quantity") }
        if text.hasPrefix("0x") { text.removeFirst(2) }
        if text.isEmpty { return .zero }
        guard let v = BigUInt(hex: text) else { throw NetworkError("node answered a malformed quantity") }
        return v
    }

    private func hexBytes(_ value: Any) throws -> [UInt8] {
        guard var text = value as? String else { throw NetworkError("node answered non-string call data") }
        if text.hasPrefix("0x") { text.removeFirst(2) }
        if text.utf8.count % 2 == 1 { text = "0" + text }
        guard let bytes = [UInt8](hex: text) else { throw NetworkError("node answered malformed call data") }
        return bytes
    }

    // MARK: indexer (history)

    /// One row of `action=txlist` / `action=tokentx`. Every number arrives
    /// as a decimal string; both actions share the fields this wallet reads.
    public struct IndexedTx: Decodable, Sendable {
        public var hash = ""
        public var from = ""
        public var to = ""
        public var value = "0"
        public var timestamp = "0"
        public var confirmations = "0"
        public var isError = "0"
        public var gasUsed = "0"
        public var gasPrice = "0"

        enum CodingKeys: String, CodingKey {
            case hash, from, to, value, timestamp = "timeStamp", confirmations, isError, gasUsed, gasPrice
        }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            hash = try c.lenientString(.hash, default: "")
            from = try c.lenientString(.from, default: "")
            to = try c.lenientString(.to, default: "")
            value = try c.lenientString(.value, default: "0")
            timestamp = try c.lenientString(.timestamp, default: "0")
            confirmations = try c.lenientString(.confirmations, default: "0")
            isError = try c.lenientString(.isError, default: "0")
            gasUsed = try c.lenientString(.gasUsed, default: "0")
            gasPrice = try c.lenientString(.gasPrice, default: "0")
        }
    }

    public func transactions(_ address: String) async throws -> [IndexedTx] {
        try await indexed("txlist", address: address, contract: nil)
    }

    public func tokenTransfers(_ address: String, contract: String) async throws -> [IndexedTx] {
        try await indexed("tokentx", address: address, contract: contract)
    }

    private func indexed(_ action: String, address: String, contract: String?) async throws -> [IndexedTx] {
        guard let base = indexer else { return [] }
        var query = [
            ("module", "account"), ("action", action), ("address", address),
            ("sort", "desc"), ("page", "1"), ("offset", Self.historyPage),
        ]
        if let contract { query.append(("contractaddress", contract)) }
        let resp = try await http.get(endpoint(base, "api", query))
        if !resp.isSuccessful { throw NetworkError("indexer answered HTTP \(resp.status)") }
        guard let obj = try? JSONSerialization.jsonObject(with: Data(resp.body.utf8)) as? [String: Any] else {
            throw NetworkError("indexer answered in an unexpected shape")
        }
        // status "0" covers both "No transactions found" (an empty wallet,
        // not an error) and real failures, which come with a non-list result.
        guard let rows = obj["result"] as? [Any] else { return [] }
        let data = try JSONSerialization.data(withJSONObject: rows)
        do {
            return try JSONDecoder().decode([IndexedTx].self, from: data)
        } catch {
            throw NetworkError("indexer answered rows in an unexpected shape: \(error)")
        }
    }
}
