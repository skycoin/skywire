/// BIP 32 hierarchical deterministic private keys — only what BIP 44/84 need.
public enum Bip32 {
    public static let hardened: UInt32 = 0x8000_0000

    public struct ExtKey: Sendable {
        public let key: [UInt8]
        public let chainCode: [UInt8]

        public func pubKey() throws -> [UInt8] { try Secp256k1.pubKeyFromSecKey(key) }
    }

    public static func master(seed: [UInt8]) throws -> ExtKey {
        let i = Hashes.hmacSha512(key: Array("Bitcoin seed".utf8), data: seed)
        let il = Array(i[0..<32])
        try require(Secp256k1.isValidSecKey(il), "invalid master key from this seed")
        return ExtKey(key: il, chainCode: Array(i[32..<64]))
    }

    public static func ckdPriv(_ parent: ExtKey, index: UInt32) throws -> ExtKey {
        var data = [UInt8](repeating: 0, count: 37)
        if index & hardened != 0 {
            // 0x00 ‖ ser256(k)
            data.replaceSubrange(1..<33, with: parent.key)
        } else {
            data.replaceSubrange(0..<33, with: try parent.pubKey())
        }
        data[33] = UInt8(truncatingIfNeeded: index >> 24)
        data[34] = UInt8(truncatingIfNeeded: index >> 16)
        data[35] = UInt8(truncatingIfNeeded: index >> 8)
        data[36] = UInt8(truncatingIfNeeded: index)

        let i = Hashes.hmacSha512(key: parent.chainCode, data: data)
        // parse256(IL) + k mod n, refused when IL >= n or the sum is zero —
        // the two cases BIP 32 says to skip.
        guard let child = Secp256k1.addTweak(sec: parent.key, tweak: Array(i[0..<32])) else {
            throw WalletCoreError("derived key out of range")
        }
        return ExtKey(key: child, chainCode: Array(i[32..<64]))
    }

    /// Derive along a path like [84 | hardened, 0 | hardened, ...].
    public static func derive(_ master: ExtKey, path: [UInt32]) throws -> ExtKey {
        var k = master
        for index in path { k = try ckdPriv(k, index: index) }
        return k
    }
}
