/// One EIP-1559 (type 2) transaction. This wallet only ever sends value or
/// calls `transfer` on a token, so `to` is always a real address and the
/// access list is always empty.
public struct EthTxn: Sendable {
    private static let type: UInt8 = 0x02

    public let chainId: BigUInt
    public let nonce: BigUInt
    public let maxPriorityFeePerGas: BigUInt
    public let maxFeePerGas: BigUInt
    public let gasLimit: BigUInt
    /// 20 bytes — contract creation has no place in a phone wallet.
    public let to: [UInt8]
    public let value: BigUInt
    public let data: [UInt8]

    public init(
        chainId: BigUInt, nonce: BigUInt, maxPriorityFeePerGas: BigUInt, maxFeePerGas: BigUInt,
        gasLimit: BigUInt, to: [UInt8], value: BigUInt, data: [UInt8]
    ) throws {
        try require(to.count == 20, "destination must be 20 bytes")
        self.chainId = chainId
        self.nonce = nonce
        self.maxPriorityFeePerGas = maxPriorityFeePerGas
        self.maxFeePerGas = maxFeePerGas
        self.gasLimit = gasLimit
        self.to = to
        self.value = value
        self.data = data
    }

    private func unsignedFields() -> [Rlp.Item] {
        [
            Rlp.of(chainId),
            Rlp.of(nonce),
            Rlp.of(maxPriorityFeePerGas),
            Rlp.of(maxFeePerGas),
            Rlp.of(gasLimit),
            Rlp.of(to),
            Rlp.of(value),
            Rlp.of(data),
            .lst([]), // access list
        ]
    }

    /// What gets signed: keccak(0x02 ‖ rlp(unsigned fields)).
    public func signingHash() -> [UInt8] {
        EthCrypto.keccak256([Self.type] + Rlp.encode(.lst(unsignedFields())))
    }

    /// The wire bytes, their keccak (the txid), and the compact signature.
    public struct Signed: Sendable {
        public let raw: [UInt8]
        public let hash: [UInt8]
        public let signature: [UInt8]
    }

    /// Sign and serialize. The compact signature's recovery id doubles as the
    /// yParity field; with low-S it is 0 or 1 except for one astronomically
    /// improbable r ≥ n case, which is refused rather than broadcast
    /// malformed.
    public func signed(sec: [UInt8]) throws -> Signed {
        let sig = try Secp256k1.signCompact(hash: signingHash(), sec: sec)
        let yParity = sig[64]
        try require(yParity < 2, "signature recovery id not representable in yParity")
        var fields = unsignedFields()
        fields.append(Rlp.of(BigUInt(UInt64(yParity))))
        fields.append(Rlp.of(BigUInt(bigEndian: Array(sig[0..<32]))))
        fields.append(Rlp.of(BigUInt(bigEndian: Array(sig[32..<64]))))
        let raw = [Self.type] + Rlp.encode(.lst(fields))
        return Signed(raw: raw, hash: EthCrypto.keccak256(raw), signature: sig)
    }

    /// `transfer(address,uint256)` call data — the whole ERC-20 surface this
    /// wallet uses.
    public static func erc20Transfer(to: [UInt8], amount: BigUInt) throws -> [UInt8] {
        try require(to.count == 20, "recipient must be 20 bytes")
        return [0xa9, 0x05, 0x9c, 0xbb] + (try pad32(to)) + (try pad32(amount.bigEndianBytes))
    }

    /// `balanceOf(address)` call data.
    public static func erc20BalanceOf(owner: [UInt8]) throws -> [UInt8] {
        try require(owner.count == 20, "owner must be 20 bytes")
        return [0x70, 0xa0, 0x82, 0x31] + (try pad32(owner))
    }

    private static func pad32(_ b: [UInt8]) throws -> [UInt8] {
        try require(b.count <= 32, "ABI word overflow")
        return [UInt8](repeating: 0, count: 32 - b.count) + b
    }
}
