package com.skycoin.wallet

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/**
 * The two shapes a restore can arrive in: our own phrases, always
 * checksummed, and the older wallets' 24-word seeds, which were arbitrary
 * strings of list words and carry no checksum.
 */
class Bip39LegacyTest {

    @Test
    fun ourOwnPhrasesStayChecksummed() {
        val phrase = Bip39.newMnemonic(128)
        assertTrue(Bip39.validate(phrase), "a generated 12-word phrase validates")
        assertFalse(Bip39.legacyWords(phrase), "a checksummed phrase is not the legacy shape")
    }

    @Test
    fun theOldShapeIsTwentyFourListWords() {
        // Any 24 list words: the older wallets picked words without a
        // checksum, and such a phrase must read as legacy, not as garbage.
        val words = Bip39.words.take(24)
        val phrase = words.joinToString(" ")
        assertFalse(Bip39.validate(phrase), "a chosen-not-derived phrase carries no checksum")
        assertTrue(Bip39.legacyWords(phrase), "24 list words are the old wallets' shape")
    }

    @Test
    fun theLegacyShapeIsExactlyTwentyFourWords() {
        val twelve = Bip39.words.take(12).joinToString(" ")
        assertFalse(Bip39.legacyWords(twelve), "12 words are not legacy")
        assertFalse(Bip39.legacyWords(Bip39.words.take(24).joinToString(" ") + " abandon"), "25 words are not legacy")
        assertFalse(Bip39.legacyWords("notaword " + Bip39.words.take(23).joinToString(" ")), "off-list words are not legacy")
    }

    @Test
    fun theSeedOfALegacyPhraseIsThePhraseItself() {
        // The derivation that makes an old phrase restore its own addresses:
        // the fiber cores feed the phrase bytes to the generator, so any
        // canonical single-spaced phrase works regardless of checksum.
        val phrase = Bip39.words.take(24).joinToString(" ")
        assertEquals(phrase, Bip39.normalize(phrase), "normalize keeps the words as they are")
    }
}
