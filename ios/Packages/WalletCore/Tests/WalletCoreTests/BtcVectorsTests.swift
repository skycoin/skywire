@testable import WalletCore
import XCTest

// Port of BtcVectorsTest.kt: its five classes, one XCTestCase each.

final class Bip39VectorsTests: XCTestCase {

    // Trezor reference vectors (also present in the Go repo's bip39 tests).
    func testEntropyToMnemonicAndSeed() throws {
        let v1 = try Bip39.entropyToMnemonic([UInt8](repeating: 0, count: 16))
        XCTAssertEqual(
            "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
            v1
        )
        XCTAssertEqual(
            "c55257c360c07c72029aebc1b53c05ed0362ada38ead3e3e9efa3708e53495531f09a6987599d18264c1e1c92f2cf141630c7a3c4ab7c81b2f001698e7463b04",
            try Bip39.toSeed(v1, passphrase: "TREZOR").hex
        )

        let v2 = try Bip39.entropyToMnemonic(bytes("7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f"))
        XCTAssertEqual("legal winner thank year wave sausage worth useful legal winner thank yellow", v2)
        XCTAssertEqual(
            "2e8905819b8723fe2c1d161860e5ee1830318dbf49a83bd451cfb8440c28bd6fa457fe1296106559a3c80937a1c1069be3a3a5bd381ee6260e8d9739fce1f607",
            try Bip39.toSeed(v2, passphrase: "TREZOR").hex
        )
    }

    func testValidation() {
        XCTAssertTrue(Bip39.validate("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"))
        // Word swap breaks the checksum.
        XCTAssertFalse(Bip39.validate("about abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon"))
        // Off-list word.
        XCTAssertFalse(Bip39.validate("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon aboot"))
        // Wrong count.
        XCTAssertFalse(Bip39.validate("abandon abandon abandon"))
        // Fresh phrases validate and differ.
        let a = Bip39.newMnemonic()
        let b = Bip39.newMnemonic()
        XCTAssertTrue(Bip39.validate(a))
        XCTAssertNotEqual(a, b)
        XCTAssertEqual(12, a.split(separator: " ").count)
    }
}

final class Bip84FixtureTests: XCTestCase {

    private struct Bip84Fixture: Decodable {
        let mnemonic: String
        let bip39SeedHex: String
        let accountPrivHex: String
        let receivePrivHex0: String
        let receive: [String]
        let change: [String]
    }

    private struct Fixtures: Decodable {
        let bip84: Bip84Fixture
    }

    func testDerivationChainMatchesReference() throws {
        let fx = try Fixture.decode(Fixtures.self, "skycoin-fixtures.json").bip84
        XCTAssertEqual(fx.bip39SeedHex, try Bip39.toSeed(fx.mnemonic).hex)
        let account = try Bip84.accountKey(mnemonic: fx.mnemonic)
        XCTAssertEqual(fx.accountPrivHex, account.key.hex)
        XCTAssertEqual(fx.receivePrivHex0, try Bip84.key(account, change: 0, index: 0).key.hex)
        for (i, addr) in fx.receive.enumerated() {
            XCTAssertEqual(addr, Bip84.address(pubKey: try Bip84.key(account, change: 0, index: UInt32(i)).pubKey()), "receive \(i)")
        }
        for (i, addr) in fx.change.enumerated() {
            XCTAssertEqual(addr, Bip84.address(pubKey: try Bip84.key(account, change: 1, index: UInt32(i)).pubKey()), "change \(i)")
        }
        // The canonical BIP 84 first address, stated in the BIP itself.
        XCTAssertEqual("bc1qcr8te4kr609gcawutmrza0j4xv80jy8z306fyu", fx.receive[0])
    }
}

final class BtcAddressTests: XCTestCase {

    func testScriptForms() throws {
        // Canonical BIP 173 v0 example — hash160 of the generator pubkey.
        XCTAssertEqual(
            "0014751e76e8199196d454941c45d1b3a323f1433bd6",
            BtcAddress.scriptPubKey("bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4")?.hex
        )
        // Case-insensitive bech32.
        XCTAssertEqual(
            "0014751e76e8199196d454941c45d1b3a323f1433bd6",
            BtcAddress.scriptPubKey("BC1QW508D6QEJXTDG4Y5R3ZARVARY0C5XW7KV8F3T4")?.hex
        )
        // Damaged checksum.
        XCTAssertNil(BtcAddress.scriptPubKey("bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t5"))
        // A Skycoin address is not a Bitcoin address.
        XCTAssertNil(BtcAddress.scriptPubKey("QmnwkcchkjgduYeeMqHaXhgEFKKYiFpc4"))
        // Taproot outputs (bech32m) are spendable-to.
        let p2tr = try XCTUnwrap(BtcAddress.scriptPubKey(Bech32.segwitEncode(hrp: "bc", version: 1, program: [UInt8](repeating: 7, count: 32))))
        XCTAssertEqual(0x51, p2tr[0])
        // v1 with a bech32 (not bech32m) checksum must fail.
        let data = [1] + Bech32.convertBits([Int](repeating: 7, count: 32), from: 8, to: 5, pad: true)!
        XCTAssertNil(BtcAddress.scriptPubKey(Bech32.encode(hrp: "bc", data: data, spec: .bech32)))
    }

    func testBase58Forms() throws {
        // Round-trip our own construction of a P2PKH address.
        let hash = Hashes.hash160(Array("test".utf8))
        let body = [0x00] + hash
        let addr = Base58.encode(body + Array(Hashes.doubleSha256(body)[0..<4]))
        let script = try XCTUnwrap(BtcAddress.scriptPubKey(addr))
        XCTAssertEqual(25, script.count)
        XCTAssertEqual(0x76, script[0])
    }
}

/// The BIP 143 native-P2WPKH example, values verbatim from the BIP.
final class Bip143Tests: XCTestCase {

    private let unsignedHex =
        "0100000002fff7f7881a8099afa6940d42d1e7f6362bec38171ea3edf433541db4e4ad969f0000000000eeffffffef51e1b804cc89d182d279655c3aa89e815b1b309fe287d9b2b55d57b90ec68a0100000000ffffffff02202cb206000000001976a9148280b37df378db99f66f85c95a783a76ac7a6d5988ac9093510d000000001976a9143bde42dbee7e4dbe6a21b2d50ce2f0167faa815988ac11000000"

    private let key = "619c335025c7f4012e556c2a58b2506e30b8511b53ade95ea316fd8c3286feb9"

    private func txidDisplay(_ serializedLe: String) -> String { bytes(serializedLe).reversed().hex }

    private func buildTxn() throws -> BtcTxn {
        let pub = try Secp256k1.pubKeyFromSecKey(bytes(key))
        return try BtcTxn(
            inputs: [
                BtcTxn.Input(
                    txid: txidDisplay("fff7f7881a8099afa6940d42d1e7f6362bec38171ea3edf433541db4e4ad969f"),
                    vout: 0,
                    valueSats: 625_000_000, // 6.25 BTC P2PK input, irrelevant to input 1's sighash
                    pubKeyHash: [UInt8](repeating: 0, count: 20),
                    sequence: 0xFFFF_FFEE
                ),
                BtcTxn.Input(
                    txid: txidDisplay("ef51e1b804cc89d182d279655c3aa89e815b1b309fe287d9b2b55d57b90ec68a"),
                    vout: 1,
                    valueSats: 600_000_000,
                    pubKeyHash: Hashes.hash160(pub),
                    sequence: 0xFFFF_FFFF
                ),
            ],
            outputs: [
                BtcTxn.Output(valueSats: 112_340_000, scriptPubKey: bytes("76a9148280b37df378db99f66f85c95a783a76ac7a6d5988ac")),
                BtcTxn.Output(valueSats: 223_450_000, scriptPubKey: bytes("76a9143bde42dbee7e4dbe6a21b2d50ce2f0167faa815988ac")),
            ],
            version: 1,
            locktime: 0x11
        )
    }

    func testUnsignedSerializationMatches() throws {
        XCTAssertEqual(unsignedHex, try buildTxn().serializeStripped().hex)
    }

    func testSighashMatches() throws {
        XCTAssertEqual("c37af31116d1b27caf68aae9e3ac82f1477929014d5b917657d0eb49478cb670", try buildTxn().sighash(1).hex)
    }

    func testSignatureMatchesByteForByte() throws {
        let der = try Secp256k1.signDer(hash: buildTxn().sighash(1), sec: bytes(key)) + [0x01]
        // The BIP's example signature is RFC 6979-deterministic — ours must equal it.
        XCTAssertEqual(
            "304402203609e17b84f6a7d30c80bfa610b5b4542f32a8a0d5447a12fb1366d7f01cc44a0220573a954c4518331561406f90300e8f3358f51928d43c212a8caed02de67eebee01",
            der.hex
        )
    }

    func testWitnessSerializationShape() throws {
        var txn = try buildTxn()
        let pub = try Secp256k1.pubKeyFromSecKey(bytes(key))
        let der = try Secp256k1.signDer(hash: txn.sighash(1), sec: bytes(key)) + [0x01]
        txn.inputs[1].witness = [der, pub]
        let full = txn.serialize().hex
        XCTAssertTrue(full.hasPrefix("01000000000102"), "segwit marker+flag present")
        XCTAssertTrue(full.contains(der.hex), "witness signature embedded")
        XCTAssertTrue(full.hasSuffix("11000000"), "locktime last")
        // txid never changes with witness data.
        XCTAssertEqual(Hashes.doubleSha256(bytes(unsignedHex)).reversed().hex, txn.txid())
        // vsize: stripped both times the discount says so.
        XCTAssertLessThan(txn.vsize(), txn.serialize().count)
    }

    /// Swift-only: sign() writes the same witness the manual steps above do,
    /// and refuses a key that does not own the input.
    func testSignFillsTheWitnessAndChecksOwnership() throws {
        let k = bytes(key)
        var only = try BtcTxn(
            inputs: [try buildTxn().inputs[1]],
            outputs: try buildTxn().outputs,
            version: 1,
            locktime: 0x11
        )
        try only.sign(keys: [k])
        let pub = try Secp256k1.pubKeyFromSecKey(k)
        XCTAssertEqual(only.inputs[0].witness.count, 2)
        XCTAssertEqual(only.inputs[0].witness[1], pub)
        XCTAssertEqual(only.inputs[0].witness[0].last, 0x01)

        var wrong = try buildTxn()
        XCTAssertThrowsError(try wrong.sign(keys: [k, k]), "input 0's program is not this key's")
    }
}

final class AmountsTests: XCTestCase {

    func testParseAndFormat() {
        XCTAssertEqual(2_500_000, Amounts.parse("2.5", exponent: 6))
        XCTAssertEqual(1, Amounts.parse("0.000001", exponent: 6))
        XCTAssertNil(Amounts.parse("0.0000001", exponent: 6))
        XCTAssertNil(Amounts.parse("1..2", exponent: 6))
        XCTAssertNil(Amounts.parse("-1", exponent: 6))
        XCTAssertNil(Amounts.parse("abc", exponent: 6))
        XCTAssertEqual(2, Amounts.parse("0.00000002", exponent: 8))
        XCTAssertEqual("1,204.500", Amounts.format(1_204_500_000, exponent: 6, minDecimals: 3))
        XCTAssertEqual("0.04821930", Amounts.format(4_821_930, exponent: 8, minDecimals: 8))
        XCTAssertEqual("12,500", Amounts.format(12_500_000_000, exponent: 6, minDecimals: 0))
        XCTAssertEqual(3, Amounts.decimals("1.204"))
        XCTAssertEqual(0, Amounts.decimals("1204"))
    }

    /// Swift-only: what Kotlin's isDigit/toULong accept beyond ASCII, and
    /// the edges.
    func testParseEdges() {
        // Persian (U+06F0…) and Arabic-Indic (U+0660…) digits, as a phone
        // keyboard in those locales types them.
        XCTAssertEqual(12_500_000, Amounts.parse("۱۲.۵", exponent: 6))
        XCTAssertEqual(12_500_000, Amounts.parse("١٢.٥", exponent: 6))
        XCTAssertEqual(0, Amounts.parse(".", exponent: 6))
        XCTAssertEqual(1_000_000, Amounts.parse("1.", exponent: 6))
        XCTAssertEqual(500_000, Amounts.parse(".5", exponent: 6))
        XCTAssertEqual(500_000, Amounts.parse("  .5 ", exponent: 6))
        XCTAssertNil(Amounts.parse("", exponent: 6))
        XCTAssertNil(Amounts.parse("+1", exponent: 6))
        XCTAssertNil(Amounts.parse("1,5", exponent: 6))
        XCTAssertNil(Amounts.parse("18446744073709.551616", exponent: 6), "one droplet past UInt64.max")
        XCTAssertEqual(UInt64.max, Amounts.parse("18446744073709.551615", exponent: 6))
        XCTAssertEqual("18,446,744,073,709.551615", Amounts.format(.max, exponent: 6))
        XCTAssertEqual("0", Amounts.format(0, exponent: 8, minDecimals: 0))
        XCTAssertEqual("0.00", Amounts.format(0, exponent: 8, minDecimals: 2))
        XCTAssertEqual("7", Amounts.format(7, exponent: 0, minDecimals: 0))
    }
}
