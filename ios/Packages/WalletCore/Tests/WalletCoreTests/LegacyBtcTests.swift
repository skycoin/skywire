@testable import WalletCore
import XCTest

/// Port of LegacyBtcTest.kt. Legacy BIP 44 Bitcoin wallets: P2PKH addresses
/// and the original sighash. The addresses are skycoin's bip44wallet's for
/// the same phrase, and without a passphrase the published BIP 44 vector.
/// The sighashes are skycoin's src/btc computeSigHash over the same
/// transaction.
final class LegacyBtcTests: XCTestCase {

    private let phrase = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
    private var keys: KeySource { KeySource(mnemonic: phrase, derivation: .bip44) }

    func testAddresses() throws {
        let core = try BtcWalletCore(esploraURL: "http://127.0.0.1:1")
        let book = try core.deriveAddresses(seed: keys, receiveCount: 2, changeCount: 2)
        XCTAssertEqual(["1LqBGSKuX5yYUonjxT5qGfpUsXKYYWeabA", "1Ak8PffB2meyfYnbXZR9EGfLfFZVpzJvQP"], book.receive)
        XCTAssertEqual(["1J3J6EvPrv8q6AC3VCjWV45Uf3nssNMRtH", "13vKxXzHXXd8HquAYdpkJoi9ULVXUgfpS5"], book.change)
        let withPass = try core.deriveAddresses(
            seed: KeySource(mnemonic: phrase, passphrase: "TREZOR", derivation: .bip44), receiveCount: 1, changeCount: 1
        )
        XCTAssertEqual(["1PEha8dk5Me5J1rZWpgqSt5F4BroTBLS5y"], withPass.receive)
        XCTAssertEqual(["1MVkh45Zn9A7ZBga697r34DBiRFLXD4UKG"], withPass.change)
    }

    func testLegacySighashAndSignatures() throws {
        let account = try BtcKeys.accountKey(keys)
        let key0 = try Bip84.key(account, change: 0, index: 0)
        let key1 = try Bip84.key(account, change: 0, index: 1)
        var txn = try BtcTxn(
            inputs: [
                BtcTxn.Input(
                    txid: "aa00000000000000000000000000000000000000000000000000000000000001", vout: 0, valueSats: 100_000,
                    pubKeyHash: Hashes.hash160(key0.pubKey()), legacy: true
                ),
                BtcTxn.Input(
                    txid: "bb00000000000000000000000000000000000000000000000000000000000002", vout: 3, valueSats: 100_000,
                    pubKeyHash: Hashes.hash160(key1.pubKey()), legacy: true
                ),
            ],
            outputs: [
                BtcTxn.Output(valueSats: 150_000, scriptPubKey: try XCTUnwrap(BtcAddress.scriptPubKey("1J3J6EvPrv8q6AC3VCjWV45Uf3nssNMRtH"))),
                BtcTxn.Output(valueSats: 40_000, scriptPubKey: try XCTUnwrap(BtcAddress.scriptPubKey("13vKxXzHXXd8HquAYdpkJoi9ULVXUgfpS5"))),
            ]
        )
        XCTAssertEqual("689f4e47442756327f5dda6896e0b427197b6832db83d86770d77abefd2fe130", txn.legacySighash(0).hex)
        XCTAssertEqual("335b6bb3e2009c7a2984f0e7d47c180270288092a81f0338465acfa4d73654ba", txn.legacySighash(1).hex)

        // Signing does not change what was signed, so each signature checks against its sighash.
        try txn.sign(keys: [key0.key, key1.key])
        for (i, k) in [key0, key1].enumerated() {
            let script = txn.inputs[i].scriptSig
            let sigLen = Int(script[0])
            let sig = Array(script[1..<(1 + sigLen)])
            let pub = Array(script[(2 + sigLen)...])
            XCTAssertEqual(1, sig.last, "SIGHASH_ALL")
            XCTAssertEqual(try k.pubKey(), pub)
            XCTAssertTrue(derVerifies(txn.legacySighash(i), Array(sig.dropLast()), pub), "input \(i)")
        }
        // No witness anywhere, so the full and stripped forms are one.
        XCTAssertEqual(txn.serialize(), txn.serializeStripped())
    }

    /// ECDSA check of a DER signature: some recovery id must give back the key.
    private func derVerifies(_ hash: [UInt8], _ der: [UInt8], _ pub: [UInt8]) -> Bool {
        func to32(_ b: ArraySlice<UInt8>) -> [UInt8] {
            let v = b.drop { $0 == 0 }
            return [UInt8](repeating: 0, count: 32 - v.count) + v
        }
        let rLen = Int(der[3])
        let r = to32(der[4..<(4 + rLen)])
        let sLen = Int(der[5 + rLen])
        let s = to32(der[(6 + rLen)..<(6 + rLen + sLen)])
        return (0...3).contains { recid in
            Secp256k1.recoverCompact(hash: hash, sig: r + s + [UInt8(recid)]) == pub
        }
    }
}
