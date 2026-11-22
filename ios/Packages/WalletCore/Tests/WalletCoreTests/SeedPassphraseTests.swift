@testable import WalletCore
import XCTest

/// Port of SeedPassphraseTest.kt. A BIP 39 passphrase changes every key.
/// The expected addresses are what skycoin's bip44wallet derives for the
/// same phrase and passphrase at the version go.mod pins; without a
/// passphrase they are the published BIP 44 and BIP 84 vectors.
final class SeedPassphraseTests: XCTestCase {

    private let phrase = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

    func testBitcoinSegwit() throws {
        let core = try BtcWalletCore(esploraURL: "http://127.0.0.1:1")
        let book = try core.deriveAddresses(seed: KeySource(mnemonic: phrase, passphrase: "TREZOR"), receiveCount: 2, changeCount: 2)
        XCTAssertEqual(["bc1qv5rmq0kt9yz3pm36wvzct7p3x6mtgehjul0feu", "bc1qq4w9xyzsylpcy6hd76xr8pecxsvzqe743t2uj0"], book.receive)
        XCTAssertEqual(["bc1qest2x6zfncjezklycdghhjxzlptky4xx6rgewa", "bc1qdrnsf9gw4wuk42n9a3xuzjzzfpv079fd4a6jdq"], book.change)
        XCTAssertEqual(
            "bc1qcr8te4kr609gcawutmrza0j4xv80jy8z306fyu",
            try core.deriveAddresses(seed: KeySource(mnemonic: phrase), receiveCount: 1, changeCount: 0).receive[0]
        )
    }

    func testEthereum() throws {
        let core = try EthWalletCore(rpcURL: "http://127.0.0.1:1", indexerURL: nil)
        XCTAssertEqual(
            ["0x9c32F71D4DB8Fb9e1A58B0a80dF79935e7256FA6", "0x7AF7283bd1462C3b957e8FAc28Dc19cBbF2FAdfe"],
            try core.deriveAddresses(seed: KeySource(mnemonic: phrase, passphrase: "TREZOR"), receiveCount: 2, changeCount: 0).receive
        )
        XCTAssertEqual(
            "0x9858EfFD232B4033E47d90003D41EC34EcaEda94",
            try core.deriveAddresses(seed: KeySource(mnemonic: phrase), receiveCount: 1, changeCount: 0).receive[0]
        )
    }

    func testSkycoinBip44() throws {
        let book = try SkyFiberWalletCore(nodeURL: "http://127.0.0.1:1")
            .deriveAddresses(seed: KeySource(mnemonic: phrase, passphrase: "TREZOR", derivation: .bip44), receiveCount: 2, changeCount: 2)
        XCTAssertEqual(["zaGHV3FFnn98Bf6QXs3V1gPGuCNKdQPew3", "2DA27wgBuD9wvpWrfhV3g9uB4qh2sLLhgmv"], book.receive)
        XCTAssertEqual(["2bAwYXgbWEF9cs7mVfHbd18oSWDYdE3QiHX", "2YijEC2kuUg8ew4mWnmFRiFbctGcLxt8Y98"], book.change)
    }
}
