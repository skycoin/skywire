/// Segwit transaction limited to what this wallet creates: P2WPKH inputs,
/// arbitrary standard outputs, BIP 143 sighash, RBF signaled.
public struct BtcTxn: Sendable {
    public static let sighashAll: UInt32 = 1

    public struct Input: Sendable {
        /// Display-order txid (as APIs show it); serialization reverses it.
        public let txid: String
        public let vout: UInt32
        public let valueSats: UInt64
        /// hash160 of the owning compressed pubkey — the P2WPKH program.
        public let pubKeyHash: [UInt8]
        /// Default opts into replacement (BIP 125) while still allowing locktime.
        public let sequence: UInt32
        public var witness: [[UInt8]] = []

        public init(txid: String, vout: UInt32, valueSats: UInt64, pubKeyHash: [UInt8], sequence: UInt32 = 0xFFFF_FFFD) {
            self.txid = txid
            self.vout = vout
            self.valueSats = valueSats
            self.pubKeyHash = pubKeyHash
            self.sequence = sequence
        }
    }

    public struct Output: Sendable {
        public let valueSats: UInt64
        public let scriptPubKey: [UInt8]

        public init(valueSats: UInt64, scriptPubKey: [UInt8]) {
            self.valueSats = valueSats
            self.scriptPubKey = scriptPubKey
        }
    }

    public var inputs: [Input]
    public let outputs: [Output]
    private let version: UInt32
    private let locktime: UInt32

    /// Throws when an input's txid is not 32 bytes of hex: it is written
    /// into every serialization and sighash.
    public init(inputs: [Input], outputs: [Output], version: UInt32 = 2, locktime: UInt32 = 0) throws {
        for i in inputs {
            guard let b = [UInt8](hex: i.txid), b.count == 32 else { throw WalletCoreError("invalid input txid \(i.txid)") }
        }
        self.inputs = inputs
        self.outputs = outputs
        self.version = version
        self.locktime = locktime
    }

    private func outpoint(_ w: inout ByteWriter, _ input: Input) {
        w.append([UInt8](hex: input.txid)!.reversed())
        w.u32(input.vout)
    }

    private func serializeOutputs(_ w: inout ByteWriter) {
        w.varInt(UInt64(outputs.count))
        for o in outputs {
            w.u64(o.valueSats)
            w.varInt(UInt64(o.scriptPubKey.count))
            w.append(o.scriptPubKey)
        }
    }

    /// Serialization without witness data — what the txid commits to.
    public func serializeStripped() -> [UInt8] {
        var w = ByteWriter()
        w.u32(version)
        w.varInt(UInt64(inputs.count))
        for i in inputs {
            outpoint(&w, i)
            w.varInt(0) // empty scriptSig — witness carries the signature
            w.u32(i.sequence)
        }
        serializeOutputs(&w)
        w.u32(locktime)
        return w.bytes
    }

    public func serialize() -> [UInt8] {
        if inputs.allSatisfy({ $0.witness.isEmpty }) { return serializeStripped() }
        var w = ByteWriter()
        w.u32(version)
        w.u8(0x00) // segwit marker
        w.u8(0x01) // segwit flag
        w.varInt(UInt64(inputs.count))
        for i in inputs {
            outpoint(&w, i)
            w.varInt(0)
            w.u32(i.sequence)
        }
        serializeOutputs(&w)
        for i in inputs {
            w.varInt(UInt64(i.witness.count))
            for item in i.witness {
                w.varInt(UInt64(item.count))
                w.append(item)
            }
        }
        w.u32(locktime)
        return w.bytes
    }

    public func txid() -> String { Hashes.doubleSha256(serializeStripped()).reversed().hex }

    /// vsize in vbytes = ceil(weight / 4).
    public func vsize() -> Int {
        let stripped = serializeStripped().count
        let full = serialize().count
        let weight = stripped * 3 + full
        return (weight + 3) / 4
    }

    /// BIP 143 signature hash for P2WPKH input [index], SIGHASH_ALL.
    public func sighash(_ index: Int) -> [UInt8] {
        let input = inputs[index]

        var prevouts = ByteWriter()
        for i in inputs { outpoint(&prevouts, i) }
        let hashPrevouts = Hashes.doubleSha256(prevouts.bytes)

        var sequences = ByteWriter()
        for i in inputs { sequences.u32(i.sequence) }
        let hashSequence = Hashes.doubleSha256(sequences.bytes)

        var outs = ByteWriter()
        for o in outputs {
            outs.u64(o.valueSats)
            outs.varInt(UInt64(o.scriptPubKey.count))
            outs.append(o.scriptPubKey)
        }
        let hashOutputs = Hashes.doubleSha256(outs.bytes)

        var w = ByteWriter()
        w.u32(version)
        w.append(hashPrevouts)
        w.append(hashSequence)
        outpoint(&w, input)
        // scriptCode of P2WPKH: the classic P2PKH script over the program.
        w.varInt(25)
        w.append([0x76, 0xa9, 0x14])
        w.append(input.pubKeyHash)
        w.append([0x88, 0xac])
        w.u64(input.valueSats)
        w.u32(input.sequence)
        w.append(hashOutputs)
        w.u32(locktime)
        w.u32(Self.sighashAll)
        return Hashes.doubleSha256(w.bytes)
    }

    /// Sign every input with its key (keys[i] owns inputs[i]).
    public mutating func sign(keys: [[UInt8]]) throws {
        try require(keys.count == inputs.count, "one key per input")
        for (i, key) in keys.enumerated() {
            let pub = try Secp256k1.pubKeyFromSecKey(key)
            try require(Hashes.hash160(pub) == inputs[i].pubKeyHash, "key does not own input \(i)")
            let der = try Secp256k1.signDer(hash: sighash(i), sec: key)
            inputs[i].witness = [der + [UInt8(Self.sighashAll)], pub]
        }
    }

    /// Pre-selection fee estimate: vsize of a tx with n P2WPKH inputs and
    /// the given output scripts.
    public static func estimateVsize(inputCount: Int, outputScripts: [[UInt8]]) -> Int {
        // Non-witness: version(4) + in-count + out-count varints + locktime(4);
        // witness adds marker+flag (2 WU) once any input is segwit.
        var weight = (4 + varIntSize(UInt64(inputCount)) + varIntSize(UInt64(outputScripts.count)) + 4) * 4 + 2
        // Input: outpoint 36 + empty-script varint 1 + sequence 4 (×4), witness ≈ 108 WU
        // (count 1 + 72-byte DER+sighash with its varint + 33-byte pubkey with its varint).
        weight += inputCount * ((36 + 1 + 4) * 4 + 108)
        for s in outputScripts { weight += (8 + varIntSize(UInt64(s.count)) + s.count) * 4 }
        return (weight + 3) / 4
    }

    private static func varIntSize(_ v: UInt64) -> Int {
        switch v {
        case ..<0xfd: 1
        case ...0xffff: 3
        case ...0xffff_ffff: 5
        default: 9
        }
    }
}
