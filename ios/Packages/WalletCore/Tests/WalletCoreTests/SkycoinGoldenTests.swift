@testable import WalletCore
import XCTest

/// Port of SkycoinGoldenTest.kt. Golden vectors from the reference
/// implementation's cipher testsuite: seed → keypair chain → addresses, plus
/// stored signatures that must recover to the stored public keys through our
/// port.
final class SkycoinGoldenTests: XCTestCase {

    private struct GoldenKey: Decodable {
        let address: String
        let secret: String
        let `public`: String
        let signatures: [String]?
    }

    private struct GoldenSeed: Decodable {
        let seed: String
        let keys: [GoldenKey]
    }

    private struct GoldenHashes: Decodable {
        let hashes: [String]
    }

    private func inputHashes() throws -> [[UInt8]] {
        try Fixture.decode(GoldenHashes.self, "input-hashes.golden").hashes.map { bytes($0) }
    }

    private func checkSeedFile(_ name: String) throws {
        let golden = try Fixture.decode(GoldenSeed.self, name)
        let seed = try XCTUnwrap(Data(base64Encoded: golden.seed))
        let keys = try SkycoinCrypto.generateKeyPairs(seed: Array(seed), count: golden.keys.count)
        let hashes = try inputHashes()

        for (i, expected) in golden.keys.enumerated() {
            XCTAssertEqual(expected.secret, keys[i].secret.hex, "secret \(i) of \(name)")
            XCTAssertEqual(expected.public, keys[i].public.hex, "public \(i) of \(name)")
            XCTAssertEqual(expected.address, SkycoinCrypto.addressFromPubKey(keys[i].public), "address \(i) of \(name)")

            for (j, sigHex) in (expected.signatures ?? []).enumerated() {
                let recovered = Secp256k1.recoverCompact(hash: hashes[j], sig: bytes(sigHex))
                XCTAssertNotNil(recovered, "recover sig \(j) of key \(i) in \(name)")
                XCTAssertEqual(expected.public, recovered?.hex, "recovered pubkey sig \(j) key \(i) in \(name)")
            }

            // Our own signature over each hash must verify and stay canonical.
            for hash in hashes {
                let sig = try Secp256k1.signCompact(hash: hash, sec: keys[i].secret)
                XCTAssertTrue(Secp256k1.verifyCompact(hash: hash, sig: sig, pub: keys[i].public), "own sig verifies")
                XCTAssertEqual(sig[32] & 0x80, 0, "own sig is low-S")
            }
        }
    }

    func testSeedFile0() throws { try checkSeedFile("seed-0000.golden") }
    func testSeedFile1() throws { try checkSeedFile("seed-0001.golden") }
    func testSeedFile2() throws { try checkSeedFile("seed-0002.golden") }

    func testAddressCodecRejectsDamage() {
        let addr = "QmnwkcchkjgduYeeMqHaXhgEFKKYiFpc4"
        XCTAssertTrue(SkycoinCrypto.isValidAddress(addr))
        XCTAssertFalse(SkycoinCrypto.isValidAddress(String(addr.dropLast()) + "5"))
        XCTAssertFalse(SkycoinCrypto.isValidAddress(""))
        XCTAssertFalse(SkycoinCrypto.isValidAddress("bc1qcr8te4kr609gcawutmrza0j4xv80jy8z306fyu"))
        XCTAssertFalse(SkycoinCrypto.isValidAddress("0OIl"))
    }
}
