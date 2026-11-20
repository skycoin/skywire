import CryptoKit
import Foundation

/// The credential file the gated apps read (skychat and skydex-client, both
/// `--password-file`): one line, `<hex salt>:<hex sha256(password ‖ salt)>`,
/// with a 16-byte salt, the hashing the visor's user store uses. The port of
/// Android's core/PasswordFile.kt.
public enum PasswordFile {
    static let saltLength = 16

    /// The on-disk form of `password`, with a fresh salt.
    public static func record(_ password: String) -> String {
        var generator = SystemRandomNumberGenerator()
        let salt = (0..<saltLength).map { _ in UInt8.random(in: .min ... .max, using: &generator) }
        return hex(salt) + ":" + hex(hash(password, salt))
    }

    /// Whether `record` still stands for `password`, re-hashed with the
    /// record's own salt. That is what lets a normal start leave the file
    /// alone, and a rotated secret (a Keychain that lost its item) rewrite it
    /// instead of the app answering its own gate with the wrong password.
    public static func matches(_ record: String, _ password: String) -> Bool {
        let parts = record.trimmingCharacters(in: .whitespacesAndNewlines).split(separator: ":", maxSplits: 1)
        guard parts.count == 2, let salt = bytes(fromHex: String(parts[0])) else { return false }
        return hex(hash(password, salt)) == parts[1].lowercased()
    }

    /// Writes the file for `password` at `url` unless it already stands for
    /// it. Readable by this app only.
    public static func ensure(at url: URL, password: String) throws {
        if let current = try? String(contentsOf: url, encoding: .utf8), matches(current, password) {
            return
        }
        try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        #if os(iOS)
        // Readable once the device has been unlocked after boot: the core
        // (the packet-tunnel extension, on a device) starts skychat while the
        // screen is locked.
        let options: Data.WritingOptions = [.atomic, .completeFileProtectionUntilFirstUserAuthentication]
        #else
        let options: Data.WritingOptions = [.atomic]
        #endif
        try Data(record(password).utf8).write(to: url, options: options)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: url.path)
    }

    private static func hash(_ password: String, _ salt: [UInt8]) -> [UInt8] {
        Array(SHA256.hash(data: Data(password.utf8) + Data(salt)))
    }

    private static func hex(_ bytes: [UInt8]) -> String {
        bytes.map { String(format: "%02x", $0) }.joined()
    }

    private static func bytes(fromHex text: String) -> [UInt8]? {
        guard text.count % 2 == 0 else { return nil }
        var out: [UInt8] = []
        var index = text.startIndex
        while index < text.endIndex {
            let next = text.index(index, offsetBy: 2)
            guard let byte = UInt8(text[index..<next], radix: 16) else { return nil }
            out.append(byte)
            index = next
        }
        return out
    }
}
