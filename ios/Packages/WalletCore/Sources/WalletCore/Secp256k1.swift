import CSecp256k1
import Foundation

/// secp256k1 over bitcoin-core's libsecp256k1 (Sources/CSecp256k1). The same
/// surface as the Kotlin object, which runs on BouncyCastle.
///
/// Signatures are deterministic (RFC 6979, libsecp256k1's default nonce, the
/// construction BouncyCastle's HMacDSAKCalculator implements) and low-S
/// (libsecp256k1 never produces another), so the two ports sign byte for
/// byte alike; the BIP 143 and EIP-155 vectors pin that. Skycoin's own
/// signer draws a random nonce, but the chain only verifies that a signature
/// recovers to the right key and is not malleable — both hold here, and a
/// deterministic nonce removes the one failure mode (a bad RNG) that has
/// actually lost coins on phones.
public enum Secp256k1 {

    /// One context for the process, randomized once against side channels.
    /// libsecp256k1 documents a context as safe to share between threads for
    /// every call that takes it const, which is every call made here.
    ///
    /// A misused argument (an API precondition) would by default print and
    /// abort(); the callback here returns instead, so the call reports
    /// failure and the wrapper throws or answers nil. Inputs are checked
    /// before each call anyway, so it never fires on a path this file has.
    nonisolated(unsafe) private static let context: OpaquePointer = {
        guard let ctx = secp256k1_context_create(UInt32(SECP256K1_CONTEXT_NONE)) else {
            fatalError("secp256k1_context_create failed")
        }
        secp256k1_context_set_illegal_callback(ctx, { _, _ in }, nil)
        var seed = [UInt8](repeating: 0, count: 32)
        var rng = SystemRandomNumberGenerator()
        for i in 0..<32 { seed[i] = UInt8.random(in: .min ... .max, using: &rng) }
        _ = secp256k1_context_randomize(ctx, seed)
        return ctx
    }()

    /// The group order n, big-endian.
    public static let order: [UInt8] = [UInt8](hex: "fffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364141")!

    public static func isValidSecKey(_ sec: [UInt8]) -> Bool {
        sec.count == 32 && secp256k1_ec_seckey_verify(context, sec) == 1
    }

    /// The compressed (33-byte) public key.
    public static func pubKeyFromSecKey(_ sec: [UInt8]) throws -> [UInt8] {
        try require(isValidSecKey(sec), "invalid secret key")
        var pub = secp256k1_pubkey()
        try require(secp256k1_ec_pubkey_create(context, &pub, sec) == 1, "invalid secret key")
        return serialize(pub, compressed: true)
    }

    /// A 33-byte compressed key that is a point on the curve.
    public static func isValidPubKey(_ pub: [UInt8]) -> Bool {
        parse(pub) != nil
    }

    /// Compact recoverable signature: R(32) ‖ S(32) ‖ recovery id(1), the
    /// Skycoin wire format. Low-S.
    public static func signCompact(hash: [UInt8], sec: [UInt8]) throws -> [UInt8] {
        try require(hash.count == 32, "hash must be 32 bytes")
        try require(isValidSecKey(sec), "invalid secret key")
        var sig = secp256k1_ecdsa_recoverable_signature()
        try require(
            secp256k1_ecdsa_sign_recoverable(context, &sig, hash, sec, nil, nil) == 1,
            "signing failed"
        )
        var out = [UInt8](repeating: 0, count: 64)
        var recid: Int32 = 0
        _ = secp256k1_ecdsa_recoverable_signature_serialize_compact(context, &out, &recid, &sig)
        return out + [UInt8(recid)]
    }

    /// DER-encoded low-S signature — Bitcoin script format (without the
    /// sighash byte).
    public static func signDer(hash: [UInt8], sec: [UInt8]) throws -> [UInt8] {
        try require(hash.count == 32, "hash must be 32 bytes")
        try require(isValidSecKey(sec), "invalid secret key")
        var sig = secp256k1_ecdsa_signature()
        try require(secp256k1_ecdsa_sign(context, &sig, hash, sec, nil, nil) == 1, "signing failed")
        var out = [UInt8](repeating: 0, count: 72)
        var length = out.count
        try require(
            secp256k1_ecdsa_signature_serialize_der(context, &out, &length, &sig) == 1,
            "DER serialization failed"
        )
        return Array(out[0..<length])
    }

    /// Recover the compressed public key from a 65-byte compact signature,
    /// or nil (wrong lengths, a recovery id past 3, r or s zero or not
    /// below n, no such point).
    public static func recoverCompact(hash: [UInt8], sig: [UInt8]) -> [UInt8]? {
        guard sig.count == 65, hash.count == 32 else { return nil }
        let recid = Int32(sig[64])
        guard recid < 4 else { return nil }
        var parsed = secp256k1_ecdsa_recoverable_signature()
        guard secp256k1_ecdsa_recoverable_signature_parse_compact(context, &parsed, Array(sig[0..<64]), recid) == 1
        else { return nil }
        var pub = secp256k1_pubkey()
        guard secp256k1_ecdsa_recover(context, &pub, &parsed, hash) == 1 else { return nil }
        return serialize(pub, compressed: true)
    }

    /// Skycoin's verification semantics for received signatures: recover
    /// must succeed, match the given key, and S's top bit must be clear
    /// (their malleability rule — implied by low-S but checked for foreign
    /// signatures).
    public static func verifyCompact(hash: [UInt8], sig: [UInt8], pub: [UInt8]) -> Bool {
        guard sig.count == 65 else { return false }
        if sig[32] & 0x80 != 0 { return false }
        guard let recovered = recoverCompact(hash: hash, sig: sig) else { return false }
        return recovered == pub
    }

    /// ECDH as Skycoin uses it: the compressed product point, un-hashed (not
    /// libsecp256k1's ECDH module, which hashes).
    public static func multiply(pub: [UInt8], sec: [UInt8]) throws -> [UInt8] {
        guard var point = parse(pub) else { throw WalletCoreError("invalid public key") }
        try require(isValidSecKey(sec), "invalid secret key")
        try require(secp256k1_ec_pubkey_tweak_mul(context, &point, sec) == 1, "point multiplication failed")
        return serialize(point, compressed: true)
    }

    /// (sec + tweak) mod n — the BIP 32 child key step. Nil when the tweak
    /// is not below n or the sum is zero, the two cases BIP 32 skips.
    public static func addTweak(sec: [UInt8], tweak: [UInt8]) -> [UInt8]? {
        guard isValidSecKey(sec), tweak.count == 32 else { return nil }
        var key = sec
        guard secp256k1_ec_seckey_tweak_add(context, &key, tweak) == 1 else { return nil }
        return key
    }

    /// The 65-byte uncompressed form (0x04 ‖ X ‖ Y) of a compressed key.
    public static func decompress(_ pub: [UInt8]) -> [UInt8]? {
        guard let point = parse(pub) else { return nil }
        return serialize(point, compressed: false)
    }

    private static func parse(_ pub: [UInt8]) -> secp256k1_pubkey? {
        guard pub.count == 33 else { return nil }
        var point = secp256k1_pubkey()
        guard secp256k1_ec_pubkey_parse(context, &point, pub, pub.count) == 1 else { return nil }
        return point
    }

    private static func serialize(_ point: secp256k1_pubkey, compressed: Bool) -> [UInt8] {
        var p = point
        var out = [UInt8](repeating: 0, count: compressed ? 33 : 65)
        var length = out.count
        let flags = UInt32(compressed ? SECP256K1_EC_COMPRESSED : SECP256K1_EC_UNCOMPRESSED)
        _ = secp256k1_ec_pubkey_serialize(context, &out, &length, &p, flags)
        return out
    }
}
