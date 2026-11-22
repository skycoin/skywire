@testable import WalletCore
import XCTest

/// Port of SkycoinBip44Test.kt. BIP 44 Skycoin wallets against skycoin's own
/// wallet files (src/wallet/testdata): the same addresses and secrets on
/// both chains, and the same chain xpubs, with and without a seed
/// passphrase.
final class SkycoinBip44Tests: XCTestCase {

    private func core() throws -> SkyFiberWalletCore { try SkyFiberWalletCore(nodeURL: "http://127.0.0.1:1") }

    private func check(_ name: String, accountXpub: String) throws {
        let w = try XCTUnwrap(JSONSerialization.jsonObject(with: Fixture.data(name)) as? [String: Any])
        let meta = try XCTUnwrap(w["meta"] as? [String: Any])
        let keys = KeySource(
            mnemonic: try XCTUnwrap(meta["seed"] as? String),
            passphrase: try XCTUnwrap(meta["seedPassphrase"] as? String),
            derivation: .bip44
        )
        let accounts = try XCTUnwrap(w["accounts"] as? [[String: Any]])
        var chains = [String: [String: Any]]()
        for c in try XCTUnwrap(accounts.first?["chains"] as? [[String: Any]]) {
            chains[try XCTUnwrap(c["chain"] as? String)] = c
        }
        func entries(_ chain: String, _ field: String) throws -> [String] {
            try XCTUnwrap(chains[chain]?["entries"] as? [[String: Any]]).map { try XCTUnwrap($0[field] as? String) }
        }

        let external = try entries("external", "address")
        let change = try entries("change", "address")
        let book = try core().deriveAddresses(seed: keys, receiveCount: external.count, changeCount: change.count)
        XCTAssertEqual(external, book.receive, name)
        XCTAssertEqual(change, book.change, name)

        let account = try SkycoinBip44.accountKey(mnemonic: keys.mnemonic, passphrase: keys.passphrase)
        XCTAssertEqual(
            try entries("external", "secret"),
            try external.indices.map { try SkycoinBip44.key(account, chain: 0, index: UInt32($0)).key.hex },
            name
        )

        let xpubs = try XCTUnwrap(try core().extendedPublicKeys(seed: keys))
        XCTAssertEqual(chains["external"]?["public_key"] as? String, xpubs.receive, name)
        XCTAssertEqual(chains["change"]?["public_key"] as? String, xpubs.change, name)
        XCTAssertEqual(accountXpub, xpubs.account, name)
    }

    // The account xpubs are skycoin's bip32 public key of each file's account private_key.
    func testWithoutPassphrase() throws {
        try check(
            "test6-bip44.wlt",
            accountXpub: "xpub6DRSVk6Cx1SsjYUmQU5y6zsePxrJti9KPCV8rTDNvvZogXa7ffoe5f6P4a8fXrqMazt3zMe8t3qk8qHYYPm55bRToPHDa4rq1zBAPqAbT4f"
        )
    }

    func testWithPassphrase() throws {
        try check(
            "test6-passphrase-bip44.wlt",
            accountXpub: "xpub6CZypHAXcJcEKgHvDjJQPCSFCT7QaADpLfnpwGcfbuHEbwcki4WNibbb1MfxacYJ3sAWfp5kAZRZvmim7YpWiwoAmE8tFyMQVZr4YU5nk33"
        )
    }

    func testTheClassicWalletHasNoXpub() throws {
        XCTAssertNil(try core().extendedPublicKeys(
            seed: KeySource(mnemonic: "into raccoon purity buzz shock arch artefact stadium bless witness used odor")
        ))
    }
}
