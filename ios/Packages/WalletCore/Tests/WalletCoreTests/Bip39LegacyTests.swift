@testable import WalletCore
import XCTest

/// Port of Bip39LegacyTest.kt. The two shapes a restore can arrive in: our
/// own phrases, always checksummed, and the older wallets' 24-word seeds,
/// which were arbitrary strings of list words and carry no checksum.
final class Bip39LegacyTests: XCTestCase {

    func testOurOwnPhrasesStayChecksummed() {
        let phrase = Bip39.newMnemonic(entropyBits: 128)
        XCTAssertTrue(Bip39.validate(phrase), "a generated 12-word phrase validates")
        XCTAssertFalse(Bip39.legacyWords(phrase), "a checksummed phrase is not the legacy shape")
    }

    func testTheOldShapeIsTwentyFourListWords() {
        // Any 24 list words: the older wallets picked words without a
        // checksum, and such a phrase must read as legacy, not as garbage.
        let phrase = Bip39.words.prefix(24).joined(separator: " ")
        XCTAssertFalse(Bip39.validate(phrase), "a chosen-not-derived phrase carries no checksum")
        XCTAssertTrue(Bip39.legacyWords(phrase), "24 list words are the old wallets' shape")
    }

    func testTheLegacyShapeIsExactlyTwentyFourWords() {
        let twelve = Bip39.words.prefix(12).joined(separator: " ")
        XCTAssertFalse(Bip39.legacyWords(twelve), "12 words are not legacy")
        XCTAssertFalse(Bip39.legacyWords(Bip39.words.prefix(24).joined(separator: " ") + " abandon"), "25 words are not legacy")
        XCTAssertFalse(Bip39.legacyWords("notaword " + Bip39.words.prefix(23).joined(separator: " ")), "off-list words are not legacy")
    }

    func testTheSeedOfALegacyPhraseIsThePhraseItself() {
        // The derivation that makes an old phrase restore its own addresses:
        // the fiber cores feed the phrase bytes to the generator, so any
        // canonical single-spaced phrase works regardless of checksum.
        let phrase = Bip39.words.prefix(24).joined(separator: " ")
        XCTAssertEqual(phrase, Bip39.normalize(phrase), "normalize keeps the words as they are")
    }
}
