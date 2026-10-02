import Foundation

/// BIP 39 mnemonics. Every chain here uses the same English wordlist and the
/// same checksummed phrase format; they differ only in how the phrase
/// becomes key material (Skycoin feeds the phrase bytes to its deterministic
/// generator, Bitcoin and Ethereum run PBKDF2 per the BIP).
public enum Bip39 {

    /// The 2048 words, from the same english.txt the Kotlin module ships.
    public static let words: [String] = {
        guard let url = Bundle.module.url(forResource: "english", withExtension: "txt", subdirectory: "bip39"),
              let text = try? String(contentsOf: url, encoding: .utf8)
        else { fatalError("bip39/english.txt missing from the package resources") }
        let list = text.split(whereSeparator: \.isNewline)
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { !$0.isEmpty }
        precondition(list.count == 2048, "wordlist must hold 2048 words, got \(list.count)")
        return list
    }()

    private static let wordIndex: [String: Int] = {
        var index = [String: Int]()
        for (i, w) in words.enumerated() { index[w] = i }
        return index
    }()

    /// A fresh phrase; 128 bits of entropy → 12 words (24 words from 256).
    /// SystemRandomNumberGenerator is arc4random_buf on Apple platforms, a
    /// CSPRNG, as SecureRandom is on Android.
    public static func newMnemonic(entropyBits: Int = 128) -> String {
        precondition(entropyBits == 128 || entropyBits == 256, "entropy must be 128 or 256 bits")
        var rng = SystemRandomNumberGenerator()
        let entropy = (0..<(entropyBits / 8)).map { _ in UInt8.random(in: .min ... .max, using: &rng) }
        // 16 or 32 bytes: always a valid length.
        return try! entropyToMnemonic(entropy)
    }

    public static func entropyToMnemonic(_ entropy: [UInt8]) throws -> String {
        try require(entropy.count % 4 == 0 && (16...32).contains(entropy.count), "invalid entropy length")
        let entBits = entropy.count * 8
        let csBits = entBits / 32
        let hash = Hashes.sha256(entropy)

        var bits = [Bool](repeating: false, count: entBits + csBits)
        for i in 0..<entBits { bits[i] = (entropy[i / 8] >> (7 - UInt8(i % 8))) & 1 == 1 }
        for i in 0..<csBits { bits[entBits + i] = (hash[i / 8] >> (7 - UInt8(i % 8))) & 1 == 1 }

        return stride(from: 0, to: bits.count, by: 11).map { start in
            var index = 0
            for i in 0..<11 { index = (index << 1) | (bits[start + i] ? 1 : 0) }
            return words[index]
        }.joined(separator: " ")
    }

    /// Normalized word array, or nil if any token is off-list: NFKD, trimmed,
    /// lower-cased, split on runs of whitespace — Kotlin's
    /// `Normalizer NFKD → trim → lowercase → split(\s+)`.
    private static func tokens(_ mnemonic: String) -> [String]? {
        let ts = mnemonic.decomposedStringWithCompatibilityMapping
            .trimmingCharacters(in: .whitespacesAndNewlines)
            .lowercased()
            .split(whereSeparator: { $0 == " " || $0 == "\t" || $0 == "\n" || $0 == "\u{0B}" || $0 == "\u{0C}" || $0 == "\r" })
            .map(String.init)
        if ts.isEmpty || ts.contains(where: { wordIndex[$0] == nil }) { return nil }
        return ts
    }

    /// Full BIP 39 validation: word count, wordlist membership, checksum.
    /// Word-order mistakes and swapped words fail here — the checksum covers
    /// them.
    public static func validate(_ mnemonic: String) -> Bool {
        guard let ts = tokens(mnemonic), [12, 15, 18, 21, 24].contains(ts.count) else { return false }

        let totalBits = ts.count * 11
        let csBits = totalBits / 33
        let entBits = totalBits - csBits
        var bits = [Bool](repeating: false, count: totalBits)
        for (w, word) in ts.enumerated() {
            let index = wordIndex[word]!
            for i in 0..<11 { bits[w * 11 + i] = (index >> (10 - i)) & 1 == 1 }
        }

        var entropy = [UInt8](repeating: 0, count: entBits / 8)
        for i in 0..<entBits where bits[i] { entropy[i / 8] |= 1 << (7 - UInt8(i % 8)) }
        let hash = Hashes.sha256(entropy)
        for i in 0..<csBits where bits[entBits + i] != ((hash[i / 8] >> (7 - UInt8(i % 8))) & 1 == 1) {
            return false
        }
        return true
    }

    /// The canonical single-spaced form of a valid phrase.
    public static func normalize(_ mnemonic: String) -> String {
        tokens(mnemonic)?.joined(separator: " ") ?? mnemonic.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    /// BIP 39 seed derivation — Bitcoin and Ethereum key material.
    public static func toSeed(_ mnemonic: String, passphrase: String = "") throws -> [UInt8] {
        let m = normalize(mnemonic).decomposedStringWithCompatibilityMapping
        let salt = ("mnemonic" + passphrase).decomposedStringWithCompatibilityMapping
        return try Hashes.pbkdf2HmacSha512(
            password: Array(m.utf8),
            salt: Array(salt.utf8),
            iterations: 2048,
            keyLength: 64
        )
    }

    public static func isWord(_ word: String) -> Bool { wordIndex[word.lowercased()] != nil }

    public static func suggestions(prefix: String, limit: Int = 4) -> [String] {
        let p = prefix.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        if p.isEmpty { return [] }
        return Array(words.lazy.filter { $0.hasPrefix(p) }.prefix(limit))
    }
}
