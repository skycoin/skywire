import CommonCrypto
import CryptoKit
import Foundation

/// The hash set the chains are built from.
public enum Hashes {

    public static func sha256(_ data: [UInt8]) -> [UInt8] {
        Array(SHA256.hash(data: data))
    }

    public static func sha256(_ a: [UInt8], _ b: [UInt8]) -> [UInt8] {
        var h = SHA256()
        h.update(data: a)
        h.update(data: b)
        return Array(h.finalize())
    }

    public static func doubleSha256(_ data: [UInt8]) -> [UInt8] { sha256(sha256(data)) }

    public static func ripemd160(_ data: [UInt8]) -> [UInt8] { RIPEMD160.hash(data) }

    /// Bitcoin hash160: ripemd160(sha256(x)).
    public static func hash160(_ data: [UInt8]) -> [UInt8] { ripemd160(sha256(data)) }

    /// Skycoin address key hash: ripemd160(sha256(sha256(pubkey))).
    public static func skyAddressHash(_ pubKey: [UInt8]) -> [UInt8] { ripemd160(doubleSha256(pubKey)) }

    public static func hmacSha512(key: [UInt8], data: [UInt8]) -> [UInt8] {
        Array(HMAC<SHA512>.authenticationCode(for: data, using: SymmetricKey(data: key)))
    }

    /// PBKDF2-HMAC-SHA512. The password is bytes (BIP 39 hands it the phrase
    /// as UTF-8, as Java's PBKDF2 encodes its char[]).
    public static func pbkdf2HmacSha512(
        password: [UInt8],
        salt: [UInt8],
        iterations: Int,
        keyLength: Int
    ) throws -> [UInt8] {
        var out = [UInt8](repeating: 0, count: keyLength)
        // An empty array has no base address; CommonCrypto gets a real
        // pointer with a zero length instead of NULL.
        let pwBuffer = password.isEmpty ? [0] : password
        let status = pwBuffer.withUnsafeBufferPointer { pw in
            pw.withMemoryRebound(to: CChar.self) { pwChars in
                CCKeyDerivationPBKDF(
                    CCPBKDFAlgorithm(kCCPBKDF2),
                    pwChars.baseAddress, password.count,
                    salt, salt.count,
                    CCPseudoRandomAlgorithm(kCCPRFHmacAlgSHA512),
                    UInt32(iterations),
                    &out, keyLength
                )
            }
        }
        try require(status == kCCSuccess, "PBKDF2 failed (CommonCrypto status \(status))")
        return out
    }
}
