@testable import WalletCore
import XCTest

/// Port of LegacySeedRestoreTest.kt. An older wallet's 24-word seed
/// restores the same addresses it had. The expected addresses come from
/// cipher.GenerateDeterministicKeyPairsSeed in the Skycoin Go module this
/// repo vendors.
final class LegacySeedRestoreTests: XCTestCase {

    private let legacy = "abandon ability able about above absent absorb abstract absurd abuse access accident "
        + "account accuse achieve acid acoustic acquire across act action actor actress actual"

    private func fiber() throws -> SkyFiberWalletCore { try SkyFiberWalletCore(nodeURL: "http://127.0.0.1:1") }

    func testAFiberWalletTakesTheOldPhrase() throws {
        let fiber = try fiber()
        XCTAssertFalse(fiber.validateSeed(legacy), "the old phrase has no checksum")
        XCTAssertTrue(fiber.acceptsLegacySeed(legacy))
        XCTAssertEqual(
            ["2Z5cJuiiUynTHDR9GL3Fm4nq9dS9bWaUDt9", "pQDsuJoS5SmJSZX3ABz5Qz7HtSJGVp9KU"],
            try fiber.deriveAddresses(seed: KeySource(mnemonic: legacy), receiveCount: 2, changeCount: 0).receive
        )
    }

    func testOnlyTwentyFourListWordsAreLegacy() throws {
        let fiber = try fiber()
        let lastSpace = try XCTUnwrap(legacy.lastIndex(of: " "))
        XCTAssertFalse(fiber.acceptsLegacySeed(String(legacy[..<lastSpace])), "23 words")
        XCTAssertFalse(fiber.acceptsLegacySeed(legacy.replacingOccurrences(of: "actual", with: "notaword")), "an off-list word")
    }

    // skycoin's GenerateDeterministicKeyPairs over the exact bytes: case and spacing are part of the seed.
    func testAnyTextIsAFiberSeedExactlyAsWritten() throws {
        let fiber = try fiber()
        XCTAssertTrue(fiber.takesAnySeed())
        XCTAssertEqual(
            ["2XT6sE4ymU5n7cyBhQrb4FLdo3CkqWEHFs8", "2jm9dPF67K9xyAxJUFLmeb7BwncH7EZEwPV"],
            try fiber.deriveAddresses(seed: KeySource(mnemonic: "My Old  Seed!"), receiveCount: 2, changeCount: 0).receive
        )
        XCTAssertEqual(
            ["KqDXwiDd2XdjJwH2kEhzssyRgn1rHFKaNy"],
            try fiber.deriveAddresses(seed: KeySource(mnemonic: "my old seed!"), receiveCount: 1, changeCount: 0).receive
        )
        XCTAssertFalse(try BtcWalletCore(esploraURL: "http://127.0.0.1:1").takesAnySeed())
    }

    func testBitcoinKeepsTheChecksum() throws {
        // BIP 39 seeds a Bitcoin wallet from the checksummed phrase.
        XCTAssertFalse(try BtcWalletCore(esploraURL: "http://127.0.0.1:1").acceptsLegacySeed(legacy))
    }
}
