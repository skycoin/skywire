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
}
