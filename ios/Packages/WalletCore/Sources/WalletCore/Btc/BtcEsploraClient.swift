import Foundation

/// Esplora-compatible Bitcoin API (mempool.space, blockstream.info, or a
/// self-hosted electrs). Balance, history and broadcast only — keys never
/// appear on this wire.
public struct BtcEsploraClient: Sendable {

    private let base: URL
    private let http: HttpClient

    public init(baseURL: String, session: URLSession) throws {
        guard let url = parseBaseURL(baseURL) else { throw WalletCoreError("invalid esplora URL: \(baseURL)") }
        base = url
        http = HttpClient(session: session)
    }

    public struct TxoStats: Decodable, Sendable {
        public var fundedSum: UInt64 = 0
        public var spentSum: UInt64 = 0
        public var txCount: Int64 = 0

        enum CodingKeys: String, CodingKey { case fundedSum = "funded_txo_sum", spentSum = "spent_txo_sum", txCount = "tx_count" }

        public init() {}

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            fundedSum = try c.value(.fundedSum, default: 0)
            spentSum = try c.value(.spentSum, default: 0)
            txCount = try c.value(.txCount, default: 0)
        }
    }

    public struct AddressInfo: Decodable, Sendable {
        public var chain = TxoStats()
        public var mempool = TxoStats()

        enum CodingKeys: String, CodingKey { case chain = "chain_stats", mempool = "mempool_stats" }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            chain = try c.value(.chain, default: TxoStats())
            mempool = try c.value(.mempool, default: TxoStats())
        }
    }

    public struct UtxoStatus: Decodable, Sendable {
        public var confirmed = false
        public var blockHeight: Int64 = 0
        public var blockTime: Int64 = 0

        enum CodingKeys: String, CodingKey { case confirmed, blockHeight = "block_height", blockTime = "block_time" }

        public init() {}

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            confirmed = try c.value(.confirmed, default: false)
            blockHeight = try c.value(.blockHeight, default: 0)
            blockTime = try c.value(.blockTime, default: 0)
        }
    }

    public struct Utxo: Decodable, Sendable {
        public let txid: String
        public let vout: UInt32
        public let value: UInt64
        public var status = UtxoStatus()

        enum CodingKeys: String, CodingKey { case txid, vout, value, status }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            txid = try c.decode(String.self, forKey: .txid)
            vout = try c.decode(UInt32.self, forKey: .vout)
            value = try c.decode(UInt64.self, forKey: .value)
            status = try c.value(.status, default: UtxoStatus())
        }
    }

    public struct Prevout: Decodable, Sendable {
        public var address: String?
        public var value: UInt64 = 0

        enum CodingKeys: String, CodingKey { case address = "scriptpubkey_address", value }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            address = try c.decodeIfPresent(String.self, forKey: .address)
            value = try c.value(.value, default: 0)
        }
    }

    public struct Vin: Decodable, Sendable {
        public var prevout: Prevout?

        enum CodingKeys: String, CodingKey { case prevout }

        public init(from decoder: Decoder) throws {
            prevout = try decoder.container(keyedBy: CodingKeys.self).decodeIfPresent(Prevout.self, forKey: .prevout)
        }
    }

    public struct Vout: Decodable, Sendable {
        public var address: String?
        public var value: UInt64 = 0

        enum CodingKeys: String, CodingKey { case address = "scriptpubkey_address", value }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            address = try c.decodeIfPresent(String.self, forKey: .address)
            value = try c.value(.value, default: 0)
        }
    }

    public struct Tx: Decodable, Sendable {
        public let txid: String
        public var fee: UInt64 = 0
        public var status = UtxoStatus()
        public var vin: [Vin] = []
        public var vout: [Vout] = []

        enum CodingKeys: String, CodingKey { case txid, fee, status, vin, vout }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            txid = try c.decode(String.self, forKey: .txid)
            fee = try c.value(.fee, default: 0)
            status = try c.value(.status, default: UtxoStatus())
            vin = try c.value(.vin, default: [])
            vout = try c.value(.vout, default: [])
        }
    }

    public func addressInfo(_ address: String) async throws -> AddressInfo {
        try await decode(get("api/address/\(address)"))
    }

    public func utxos(_ address: String) async throws -> [Utxo] {
        try await decode(get("api/address/\(address)/utxo"))
    }

    public func transactions(_ address: String) async throws -> [Tx] {
        try await decode(get("api/address/\(address)/txs"))
    }

    public func tipHeight() async throws -> Int64 {
        let body = try await get("api/blocks/tip/height")
        guard let h = Int64(body.trimmingCharacters(in: .whitespacesAndNewlines)) else {
            throw NetworkError("server answered a non-numeric tip height")
        }
        return h
    }

    /// sat/vB presets. mempool.space serves /api/v1/fees/recommended; plain
    /// esplora serves /api/fee-estimates keyed by confirmation target.
    public func feeRates() async throws -> (economy: Int, normal: Int, priority: Int) {
        if let body = try? await get("api/v1/fees/recommended"),
           let o = try? JSONSerialization.jsonObject(with: Data(body.utf8)) as? [String: Any] {
            func f(_ k: String) -> Int { (o[k] as? NSNumber).map { saturatingInt($0.doubleValue) } ?? 1 }
            return (max(f("economyFee"), 1), max(f("halfHourFee"), 1), max(f("fastestFee"), 1))
        }
        let body = try await get("api/fee-estimates")
        guard let o = try JSONSerialization.jsonObject(with: Data(body.utf8)) as? [String: Any] else {
            throw NetworkError("server answered fee estimates in an unexpected shape")
        }
        func target(_ k: String) -> Double? { (o[k] as? NSNumber)?.doubleValue }
        let economy = target("144") ?? target("25") ?? 1.0
        let normal = target("6") ?? target("3") ?? economy
        let priority = target("1") ?? target("2") ?? normal
        return (max(saturatingInt(economy), 1), max(saturatingInt(normal), 1), max(saturatingInt(priority), 1))
    }

    /// Broadcast raw hex; the server's rejection text is the error message.
    public func broadcast(rawHex: String) async throws -> String {
        let resp = try await http.post(endpoint(base, "api/tx"), body: Data(rawHex.utf8), contentType: "text/plain")
        if !resp.isSuccessful {
            throw WalletError.nodeRejected(serverMessage(resp.body, status: resp.status, who: "server"))
        }
        return resp.body.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    private func get(_ path: String) async throws -> String {
        let resp = try await http.get(endpoint(base, path))
        if !resp.isSuccessful { throw NetworkError(serverMessage(resp.body, status: resp.status, who: "server")) }
        return resp.body
    }

    private func decode<T: Decodable>(_ body: String) throws -> T {
        do {
            return try JSONDecoder().decode(T.self, from: Data(body.utf8))
        } catch {
            throw NetworkError("server answered in an unexpected shape: \(error)")
        }
    }
}

/// Kotlin's Double.toInt(): toward zero, saturating at the Int range, NaN 0.
func saturatingInt(_ d: Double) -> Int {
    if d.isNaN { return 0 }
    if d >= Double(Int.max) { return .max }
    if d <= Double(Int.min) { return .min }
    return Int(d)
}
