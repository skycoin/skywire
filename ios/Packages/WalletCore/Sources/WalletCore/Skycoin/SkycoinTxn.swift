import Foundation

/// The Skycoin transaction and its exact wire encoding (skyencoder output,
/// little-endian, u32 length prefixes on slices):
///
///     u32 Length | u8 Type | 32B InnerHash
///     | u32 nSigs | 65B × sig | u32 nIn | 32B × uxid
///     | u32 nOut | (u8 version ‖ 20B keyHash ‖ u64 coins ‖ u64 hours) × out
///
/// InnerHash = sha256 over the In and Out sections alone; the hash signed for
/// input i is sha256(InnerHash ‖ In[i]); the txid is sha256 of the whole
/// serialization.
public struct SkycoinTxn: Sendable {
    public var length: UInt32 = 0
    public var type: UInt8 = 0
    public var innerHash = [UInt8](repeating: 0, count: 32)
    /// 65 bytes each; zero-filled when unsigned.
    public var sigs: [[UInt8]] = []
    /// 32-byte uxids.
    public var inputs: [[UInt8]] = []
    public var outputs: [Output] = []

    public struct Output: Sendable {
        public let addressKey: [UInt8]
        public let addressVersion: UInt8
        public let coins: UInt64
        public let hours: UInt64
    }

    public init() {}

    public mutating func pushInput(_ uxid: [UInt8]) throws {
        try require(uxid.count == 32, "uxid must be 32 bytes")
        inputs.append(uxid)
    }

    public mutating func pushOutput(address: String, coins: UInt64, hours: UInt64) throws {
        guard let key = SkycoinCrypto.decodeAddress(address) else {
            throw WalletCoreError("invalid address \(address)")
        }
        outputs.append(Output(addressKey: key, addressVersion: 0, coins: coins, hours: hours))
    }

    private func writeInOut(_ w: inout ByteWriter) {
        w.u32(UInt32(inputs.count))
        for i in inputs { w.append(i) }
        w.u32(UInt32(outputs.count))
        for o in outputs {
            w.u8(o.addressVersion)
            w.append(o.addressKey)
            w.u64(o.coins)
            w.u64(o.hours)
        }
    }

    public func hashInner() -> [UInt8] {
        var w = ByteWriter()
        writeInOut(&w)
        return Hashes.sha256(w.bytes)
    }

    public func serialize() -> [UInt8] {
        var w = ByteWriter()
        w.u32(length)
        w.u8(type)
        w.append(innerHash)
        w.u32(UInt32(sigs.count))
        for s in sigs { w.append(s) }
        writeInOut(&w)
        return w.bytes
    }

    /// Sets Length, Type and InnerHash — the Go UpdateHeader. Length is part
    /// of the serialization but has a fixed width, so one pass settles it.
    public mutating func updateHeader() {
        type = 0
        innerHash = hashInner()
        length = UInt32(serialize().count)
    }

    /// Sign input i with the key owning its output.
    public mutating func signInputs(keys: [[UInt8]]) throws {
        try require(keys.count == inputs.count, "one key per input")
        innerHash = hashInner()
        var signed = [[UInt8]]()
        for (i, key) in keys.enumerated() {
            let h = Hashes.sha256(innerHash, inputs[i])
            signed.append(try Secp256k1.signCompact(hash: h, sec: key))
        }
        sigs = signed
        updateHeader()
    }

    public func txidHex() -> String { Hashes.sha256(serialize()).hex }

    public func serializeHex() -> String { serialize().hex }

    /// The same transaction with every signature zeroed, as an unsigned
    /// transaction is written.
    public func unsignedHex() -> String {
        var copy = SkycoinTxn()
        copy.inputs = inputs
        copy.outputs = outputs
        copy.sigs = Array(repeating: [UInt8](repeating: 0, count: 65), count: inputs.count)
        copy.updateHeader()
        return copy.serializeHex()
    }

    /// True when every input carries a signature.
    public func isSigned() -> Bool {
        sigs.count == inputs.count && !sigs.contains { s in s.allSatisfy { $0 == 0 } }
    }

    /// Reads the wire encoding back. Refuses trailing bytes, a length field
    /// that disagrees, and an inner hash that does not match the inputs and
    /// outputs, so a mangled paste fails here and not at a node. Throws
    /// WalletCoreError with Kotlin's messages.
    public static func decode(_ hex: String) throws -> SkycoinTxn {
        guard let bytes = [UInt8](hex: hex.trimmingCharacters(in: .whitespacesAndNewlines)) else {
            throw WalletCoreError("not a hex transaction")
        }
        var r = ByteReader(bytes)
        func count(_ size: Int) throws -> Int {
            let n = Int(Int32(bitPattern: try r.u32()))
            try require(n >= 0, "transaction ends early")
            try r.need(n * size)
            return n
        }
        var txn = SkycoinTxn()
        try r.need(4 + 1)
        txn.length = try r.u32()
        txn.type = try r.u8()
        txn.innerHash = try r.take(32)
        for _ in 0..<(try count(65)) { txn.sigs.append(try r.take(65)) }
        for _ in 0..<(try count(32)) { txn.inputs.append(try r.take(32)) }
        for _ in 0..<(try count(1 + 20 + 8 + 8)) {
            let version = try r.u8()
            let key = try r.take(20)
            txn.outputs.append(Output(addressKey: key, addressVersion: version, coins: try r.u64(), hours: try r.u64()))
        }
        try require(r.remaining == 0, "bytes after the transaction")
        try require(Int(txn.length) == bytes.count, "length field does not match")
        try require(txn.innerHash == txn.hashInner(), "inner hash does not match")
        return txn
    }
}

/// Little-endian reader for SkycoinTxn.decode; every read past the end is
/// "transaction ends early".
private struct ByteReader {
    private let bytes: [UInt8]
    private var pos = 0

    init(_ bytes: [UInt8]) { self.bytes = bytes }

    var remaining: Int { bytes.count - pos }

    func need(_ n: Int) throws {
        try require(n <= remaining, "transaction ends early")
    }

    mutating func take(_ n: Int) throws -> [UInt8] {
        try need(n)
        defer { pos += n }
        return Array(bytes[pos..<(pos + n)])
    }

    mutating func u8() throws -> UInt8 { try take(1)[0] }

    mutating func u32() throws -> UInt32 {
        try take(4).reversed().reduce(0) { $0 << 8 | UInt32($1) }
    }

    mutating func u64() throws -> UInt64 {
        try take(8).reversed().reduce(0) { $0 << 8 | UInt64($1) }
    }
}
