/// BIP 32 hierarchical deterministic private keys, and the xpub of one.
public enum Bip32 {
    public static let hardened: UInt32 = 0x8000_0000

    public struct ExtKey: Sendable {
        public let key: [UInt8]
        public let chainCode: [UInt8]
        public let depth: Int
        public let parentFingerprint: [UInt8]
        public let childNumber: UInt32

        public init(
            key: [UInt8], chainCode: [UInt8], depth: Int = 0,
            parentFingerprint: [UInt8] = [0, 0, 0, 0], childNumber: UInt32 = 0
        ) {
            self.key = key
            self.chainCode = chainCode
            self.depth = depth
            self.parentFingerprint = parentFingerprint
            self.childNumber = childNumber
        }

        public func pubKey() throws -> [UInt8] { try Secp256k1.pubKeyFromSecKey(key) }

        /// The serialized extended public key, BIP 32 "Serialization
        /// format", mainnet.
        public func xpub() throws -> String {
            let body: [UInt8] = [0x04, 0x88, 0xb2, 0x1e, UInt8(truncatingIfNeeded: depth)]
                + parentFingerprint + beBytes(childNumber) + chainCode + (try pubKey())
            return Base58.encode(body + Array(Hashes.doubleSha256(body)[0..<4]))
        }
    }

    private static func beBytes(_ v: UInt32) -> [UInt8] {
        [UInt8(truncatingIfNeeded: v >> 24), UInt8(truncatingIfNeeded: v >> 16),
         UInt8(truncatingIfNeeded: v >> 8), UInt8(truncatingIfNeeded: v)]
    }

    public static func master(seed: [UInt8]) throws -> ExtKey {
        let i = Hashes.hmacSha512(key: Array("Bitcoin seed".utf8), data: seed)
        let il = Array(i[0..<32])
        try require(Secp256k1.isValidSecKey(il), "invalid master key from this seed")
        return ExtKey(key: il, chainCode: Array(i[32..<64]))
    }

    public static func ckdPriv(_ parent: ExtKey, index: UInt32) throws -> ExtKey {
        let parentPub = try parent.pubKey()
        var data = [UInt8](repeating: 0, count: 37)
        if index & hardened != 0 {
            // 0x00 ‖ ser256(k)
            data.replaceSubrange(1..<33, with: parent.key)
        } else {
            data.replaceSubrange(0..<33, with: parentPub)
        }
        data.replaceSubrange(33..<37, with: beBytes(index))

        let i = Hashes.hmacSha512(key: parent.chainCode, data: data)
        // parse256(IL) + k mod n, refused when IL >= n or the sum is zero —
        // the two cases BIP 32 says to skip.
        guard let child = Secp256k1.addTweak(sec: parent.key, tweak: Array(i[0..<32])) else {
            throw WalletCoreError("derived key out of range")
        }
        return ExtKey(
            key: child,
            chainCode: Array(i[32..<64]),
            depth: parent.depth + 1,
            parentFingerprint: Array(Hashes.hash160(parentPub)[0..<4]),
            childNumber: index
        )
    }

    /// Derive along a path like [84 | hardened, 0 | hardened, ...].
    public static func derive(_ master: ExtKey, path: [UInt32]) throws -> ExtKey {
        var k = master
        for index in path { k = try ckdPriv(k, index: index) }
        return k
    }
}
