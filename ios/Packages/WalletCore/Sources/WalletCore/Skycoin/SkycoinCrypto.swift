/// Skycoin deterministic key generation and the address codec.
///
/// The derivation chain is Skycoin's own (src/cipher, secp256k1-go): the
/// wallet seed is the raw bytes of the mnemonic string, and each address
/// advances the chain seed by one iterator step. Same seed, same addresses as
/// the desktop wallet — that is the whole point.
public enum SkycoinCrypto {

    public struct KeyPair: Sendable {
        public let `public`: [UInt8]
        public let secret: [UInt8]
    }

    /// One sha256-until-valid step; returns (pub, sec) for the stepped seed.
    private static func iteratorStep(_ seed32: [UInt8]) throws -> KeyPair {
        var s = seed32
        while true {
            s = Hashes.sha256(s)
            if Secp256k1.isValidSecKey(s) {
                return KeyPair(public: try Secp256k1.pubKeyFromSecKey(s), secret: s)
            }
        }
    }

    /// secp256k1Hash: sha256 salted with an ECDH operation on the curve.
    public static func secp256k1Hash(_ seed: [UInt8]) throws -> [UInt8] {
        let hash = Hashes.sha256(seed)
        let secKey = try iteratorStep(hash).secret
        let pubKey = try iteratorStep(Hashes.sha256(hash)).public
        let ecdh = try Secp256k1.multiply(pub: pubKey, sec: secKey)
        return Hashes.sha256(hash, ecdh)
    }

    /// (nextSeed, keyPair) — feed nextSeed back in for the next address.
    public static func deterministicKeyPairIterator(_ seedIn: [UInt8]) throws -> (next: [UInt8], keyPair: KeyPair) {
        try require(!seedIn.isEmpty, "empty seed")
        let seed1 = try secp256k1Hash(seedIn)
        let seed2 = Hashes.sha256(seedIn, seed1)
        return (seed1, try iteratorStep(seed2))
    }

    /// The first n keypairs of the wallet chain, in address order.
    public static func generateKeyPairs(seed: [UInt8], count n: Int) throws -> [KeyPair] {
        var s = seed
        var out = [KeyPair]()
        out.reserveCapacity(n)
        for _ in 0..<n {
            let (next, kp) = try deterministicKeyPairIterator(s)
            s = next
            out.append(kp)
        }
        return out
    }

    // Address = base58( key20 ‖ version ‖ sha256(key20 ‖ version)[0..3] )
    // where key20 = ripemd160(sha256(sha256(pubkey))) and version is 0.

    public static func addressFromPubKey(_ pub: [UInt8]) -> String {
        let body = Hashes.skyAddressHash(pub) + [0]
        let checksum = Array(Hashes.sha256(body)[0..<4])
        return Base58.encode(body + checksum)
    }

    /// Checksum-validated decode; returns the 20-byte key hash or nil.
    public static func decodeAddress(_ address: String) -> [UInt8]? {
        guard let b = Base58.decode(address), b.count == 25 else { return nil }
        let body = Array(b[0..<21])
        if Array(Hashes.sha256(body)[0..<4]) != Array(b[21..<25]) { return nil }
        if body[20] != 0 { return nil } // version must be 0
        return Array(b[0..<20])
    }

    public static func isValidAddress(_ address: String) -> Bool { decodeAddress(address) != nil }
}
