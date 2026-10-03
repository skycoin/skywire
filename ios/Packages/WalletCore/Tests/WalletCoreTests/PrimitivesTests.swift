@testable import WalletCore
import XCTest

/// What the Kotlin module gets from BouncyCastle and the JDK, and this port
/// writes itself (RIPEMD-160, Keccak-256, the big integer) or calls through
/// a new seam (libsecp256k1, base58 without BigInteger): each against
/// published or independently computed values.
final class PrimitivesTests: XCTestCase {

    /// The test vectors published with RIPEMD-160 (Bosselaers' page),
    /// the block-edge lengths cross-checked with LibreSSL's
    /// `openssl dgst -rmd160` on the Mac.
    func testRipemd160Vectors() {
        let vectors: [(String, String)] = [
            ("", "9c1185a5c5e9fc54612808977ee8f548b2258d31"),
            ("a", "0bdc9d2d256b3ee9daae347be6f4dc835a467ffe"),
            ("abc", "8eb208f7e05d987a9b044a8e98c6b087f15a0bfc"),
            ("message digest", "5d0689ef49d2fae572b881b123a85ffa21595f36"),
            ("abcdefghijklmnopqrstuvwxyz", "f71c27109c692c1b56bbdceb5b9d2865b3708dbc"),
            ("abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq", "12a053384a9c0c88e405a06c27dcf49ada62eb2b"),
            ("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789", "b0e20b6e3116640286ed3a87a5713079b21f5189"),
            (String(repeating: "1234567890", count: 8), "9b752e45573d4b39f4dbd3323cab82bf63326bfb"),
            (String(repeating: "a", count: 55), "0d8a8c9063a48576a7c97e9f95253a6e53ff6765"),
            (String(repeating: "a", count: 56), "e72334b46c83cc70bef979e15453706c95b888be"),
            (String(repeating: "a", count: 64), "9dfb7d374ad924f3f88de96291c33e9abed53e32"),
        ]
        for (input, expected) in vectors {
            XCTAssertEqual(expected, Hashes.ripemd160(Array(input.utf8)).hex, "ripemd160(\(input.count) bytes)")
        }
        XCTAssertEqual(
            "52783243c1697bdbe16d37f97f68f08325dc1528",
            Hashes.ripemd160([UInt8](repeating: UInt8(ascii: "a"), count: 1_000_000)).hex
        )
    }

    /// Keccak-256 around the 136-byte rate (one block, a full block whose
    /// padding takes a second one, two blocks, several), the values from
    /// Go's golang.org/x/crypto/sha3 NewLegacyKeccak256 (vendored in this
    /// repo) over runs of 'a'.
    func testKeccak256AcrossBlocks() {
        let vectors: [(Int, String)] = [
            (135, "34367dc248bbd832f4e3e69dfaac2f92638bd0bbd18f2912ba4ef454919cf446"),
            (136, "a6c4d403279fe3e0af03729caada8374b5ca54d8065329a3ebcaeb4b60aa386e"),
            (137, "d869f639c7046b4929fc92a4d988a8b22c55fbadb802c0c66ebcd484f1915f39"),
            (200, "96ea54061def936c4be90b518992fdc6f12f535068a256229aca54267b4d084d"),
            (272, "cf7fcd4f705ee749930d19ca84561a9bf62516bd90a471545fa2f49fdc7e63c8"),
            (1000, "b6a4ac1f51884d71f30fa397a5e155de3099e11fc0edef5d08b646e621e19de9"),
        ]
        for (n, expected) in vectors {
            XCTAssertEqual(expected, Keccak256.hash([UInt8](repeating: UInt8(ascii: "a"), count: n)).hex, "keccak256(\(n) × a)")
        }
    }

    func testHashCompositions() {
        let data = Array("skywire".utf8)
        XCTAssertEqual(Hashes.hash160(data), Hashes.ripemd160(Hashes.sha256(data)))
        XCTAssertEqual(Hashes.skyAddressHash(data), Hashes.ripemd160(Hashes.sha256(Hashes.sha256(data))))
        XCTAssertEqual(Hashes.sha256(Array("ab".utf8)), Hashes.sha256(Array("a".utf8), Array("b".utf8)))
        // RFC 4231 test case 2 (HMAC-SHA-512).
        XCTAssertEqual(
            "164b7a7bfcf819e2e395fbe73b56e0a387bd64222e831fd610270cd7ea2505549758bf75c05a994a6d034f65f8f0e6fdcaeab1a34d4a6b4b636e070a38bce737",
            Hashes.hmacSha512(key: Array("Jefe".utf8), data: Array("what do ya want for nothing?".utf8)).hex
        )
    }

    func testBigUInt() {
        let a = BigUInt(decimal: "115792089237316195423570985008687907853269984665640564039457584007913129639935")!
        XCTAssertEqual(a.hex, String(repeating: "f", count: 64))
        XCTAssertEqual(a.bitWidth, 256)
        XCTAssertEqual((a + BigUInt(1)).hex, "1" + String(repeating: "0", count: 64))
        XCTAssertEqual((a - a), .zero)
        XCTAssertEqual(BigUInt(hex: "de0b6b3a7640000"), BigUInt.pow10(18))
        XCTAssertEqual(BigUInt.pow10(18).description, "1000000000000000000")
        XCTAssertEqual(BigUInt.pow10(18).bigEndianBytes.hex, "0de0b6b3a7640000")
        XCTAssertEqual(BigUInt(hex: "")!, .zero)
        XCTAssertNil(BigUInt(hex: "0x10"))
        XCTAssertNil(BigUInt(decimal: "12a"))
        XCTAssertNil(BigUInt(decimal: ""))
        XCTAssertEqual(BigUInt.zero.hex, "0")
        XCTAssertEqual(BigUInt.zero.bigEndianBytes, [])
        XCTAssertEqual(BigUInt(bigEndian: [0, 0, 1, 2]).bigEndianBytes, [1, 2])

        // Division by a multi-limb divisor, checked by multiplying back.
        let wei = BigUInt(decimal: "123456789012345678901234567890")!
        let scale = BigUInt.pow10(18)
        let (q, r) = wei.quotientAndRemainder(dividingBy: scale)
        XCTAssertEqual(q.description, "123456789012")
        XCTAssertEqual(r.description, "345678901234567890")
        XCTAssertEqual(q * scale + r, wei)
        XCTAssertEqual((BigUInt(1_000_000_000) * BigUInt(21_000) / BigUInt(1_000_000_000)).description, "21000")
        XCTAssertEqual(BigUInt(decimal: "18446744073709551616")!.clampedUInt64, .max)
        XCTAssertEqual(BigUInt(UInt64.max).clampedUInt64, .max)
        XCTAssertTrue(BigUInt(5) < BigUInt(decimal: "18446744073709551616")!)
    }

    /// Base58 without BigInteger: leading zero bytes, round trips, rejects.
    func testBase58() {
        XCTAssertEqual(Base58.encode([]), "")
        XCTAssertEqual(Base58.encode([0]), "1")
        XCTAssertEqual(Base58.encode([0, 0, 1]), "112")
        XCTAssertEqual(Base58.encode(Array("hello world".utf8)), "StV1DL6CwTryKyV")
        XCTAssertEqual(Base58.decode("StV1DL6CwTryKyV"), Array("hello world".utf8))
        XCTAssertEqual(Base58.decode("1"), [0])
        XCTAssertEqual(Base58.decode("112"), [0, 0, 1])
        XCTAssertNil(Base58.decode(""))
        XCTAssertNil(Base58.decode("0"))
        XCTAssertNil(Base58.decode("I"))
        XCTAssertNil(Base58.decode("é"))
        for _ in 0..<50 {
            let n = Int.random(in: 0...40)
            var b = (0..<n).map { _ in UInt8.random(in: 0...255) }
            if Bool.random() { b = [0, 0] + b }
            XCTAssertEqual(Base58.decode(Base58.encode(b)) ?? [9], b.isEmpty ? [9] : b)
        }
    }

    /// The secp256k1 seam: refusals come back as nil or a thrown error,
    /// never libsecp256k1's abort.
    func testSecp256k1Refusals() throws {
        let sec = bytes("4646464646464646464646464646464646464646464646464646464646464646")
        let hash = Hashes.sha256(Array("x".utf8))
        let sig = try Secp256k1.signCompact(hash: hash, sec: sec)

        XCTAssertFalse(Secp256k1.isValidSecKey([UInt8](repeating: 0, count: 32)))
        XCTAssertFalse(Secp256k1.isValidSecKey(Secp256k1.order))
        XCTAssertFalse(Secp256k1.isValidSecKey(Array(sec[0..<31])))
        XCTAssertThrowsError(try Secp256k1.pubKeyFromSecKey(Secp256k1.order))
        XCTAssertThrowsError(try Secp256k1.signCompact(hash: Array(hash[0..<31]), sec: sec))
        XCTAssertNil(Secp256k1.recoverCompact(hash: hash, sig: Array(sig[0..<64]) + [4]), "recovery id past 3")
        XCTAssertNil(Secp256k1.recoverCompact(hash: hash, sig: [UInt8](repeating: 0, count: 65)), "r = s = 0")
        XCTAssertNil(Secp256k1.recoverCompact(hash: hash, sig: Secp256k1.order + Array(sig[32..<65])), "r = n")
        XCTAssertNil(Secp256k1.recoverCompact(hash: hash, sig: Array(sig[0..<32]) + Secp256k1.order + [sig[64]]), "s = n")

        // A high-S twin recovers to the same key but fails Skycoin's verify.
        let pub = try Secp256k1.pubKeyFromSecKey(sec)
        XCTAssertTrue(Secp256k1.verifyCompact(hash: hash, sig: sig, pub: pub))
        let s = BigUInt(bigEndian: Array(sig[32..<64]))
        let highS = (BigUInt(bigEndian: Secp256k1.order) - s).bigEndianBytes
        let twin = Array(sig[0..<32]) + [UInt8](repeating: 0, count: 32 - highS.count) + highS + [sig[64] ^ 1]
        XCTAssertEqual(Secp256k1.recoverCompact(hash: hash, sig: twin), pub)
        XCTAssertFalse(Secp256k1.verifyCompact(hash: hash, sig: twin, pub: pub))

        XCTAssertFalse(Secp256k1.isValidPubKey([0x04] + Array(pub[1...])))
        XCTAssertFalse(Secp256k1.isValidPubKey([0x02] + [UInt8](repeating: 0xff, count: 32)), "x past p")
        XCTAssertThrowsError(try Secp256k1.multiply(pub: [0x02] + [UInt8](repeating: 0xff, count: 32), sec: sec))
        XCTAssertNil(Secp256k1.addTweak(sec: sec, tweak: Secp256k1.order), "tweak not below n")
        XCTAssertEqual(Secp256k1.decompress(pub)?.count, 65)
    }

    /// ECDH as Skycoin uses it is symmetric: a·(b·G) = b·(a·G).
    func testMultiplyIsSymmetric() throws {
        let a = Hashes.sha256(Array("a".utf8))
        let b = Hashes.sha256(Array("b".utf8))
        let ab = try Secp256k1.multiply(pub: Secp256k1.pubKeyFromSecKey(b), sec: a)
        let ba = try Secp256k1.multiply(pub: Secp256k1.pubKeyFromSecKey(a), sec: b)
        XCTAssertEqual(ab, ba)
        XCTAssertEqual(ab.count, 33)
    }

    func testBech32RoundTrip() {
        let program = [UInt8](repeating: 0xab, count: 20)
        let addr = Bech32.segwitEncode(hrp: "bc", version: 0, program: program)
        XCTAssertEqual(Bech32.segwitDecode(hrp: "bc", address: addr)?.program, program)
        XCTAssertNil(Bech32.segwitDecode(hrp: "tb", address: addr))
        XCTAssertNil(Bech32.decode(addr + String(repeating: "q", count: 60)), "over 90 characters")
        XCTAssertNil(Bech32.decode("bc1QW508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4"), "mixed case")
    }
}
