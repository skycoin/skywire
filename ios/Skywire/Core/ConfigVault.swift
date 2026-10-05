import CoreClient
import CryptoKit
import Foundation

/// Optional encryption of `skywire-config.json` while the core is not running (Android:
/// core/ConfigVault.kt). The file holds `sk`, the whole of this phone's identity; sealed, it is
/// AES-256-GCM ciphertext under a key in the Keychain, which a copy of the file system does not
/// carry (the item is this device's only, after first unlock, never in a backup).
///
/// Two files, never both meaningful at once: the plaintext, which exists while the core runs
/// (the visor opens and rewrites it), and `.enc`, which exists while the core is stopped and the
/// feature is on. `unseal` before anything reads or runs the config, `seal` once the core has
/// stopped. A start that found only `.enc` and did not unseal would generate a new identity, so
/// `ConfigProfile.prepare` unseals first and an unseal that fails stops the start.
struct ConfigVault: Sendable {
    let paths: CorePaths
    let secrets: SecretStore

    /// The sealed form, beside the plaintext's path.
    var sealedFile: URL { paths.configFile.appendingPathExtension("enc") }

    /// A sealed config is on disk, whatever the preference says.
    var sealedExists: Bool { FileManager.default.fileExists(atPath: sealedFile.path) }

    private var plainExists: Bool { FileManager.default.fileExists(atPath: paths.configFile.path) }

    /// Makes the plaintext available, decrypting it if it is sealed; a no-op when nothing is.
    /// A plaintext beside a sealed file means the app died with the core running: the plaintext
    /// is the newer of the two, so it wins and the next `seal` replaces the ciphertext.
    func unseal() throws {
        guard sealedExists, !plainExists else { return }
        guard let plain = decrypt(try Data(contentsOf: sealedFile)) else {
            throw VaultError(message: L10n.text("core_sealed_key_gone_ios"))
        }
        try writePrivate(plain, to: paths.configFile)
        try FileManager.default.removeItem(at: sealedFile)
    }

    /// Encrypts the plaintext and removes it, if `enabled`. The ciphertext is written and read
    /// back before the plaintext goes: a crash in between leaves both, never neither.
    func seal(enabled: Bool) throws {
        guard enabled, plainExists else { return }
        let plain = try Data(contentsOf: paths.configFile)
        try writePrivate(try encrypt(plain), to: sealedFile)
        guard decrypt(try Data(contentsOf: sealedFile)) == plain else {
            throw VaultError(message: L10n.text("core_seal_readback_failed"))
        }
        try FileManager.default.removeItem(at: paths.configFile)
    }

    /// The config as text from whichever form is on disk, so a sealed config is exported
    /// without a plaintext copy landing on the disk.
    func readText() throws -> String {
        if plainExists { return try String(contentsOf: paths.configFile, encoding: .utf8) }
        guard let plain = decrypt(try Data(contentsOf: sealedFile)) else {
            throw VaultError(message: L10n.text("core_sealed_unreadable"))
        }
        return String(decoding: plain, as: UTF8.self)
    }

    /// The preference changed. On: seal now if the core is down, else when it stops (the running
    /// visor rewrites the file). Off: unseal now, whatever the core is doing.
    func apply(enabled: Bool, coreRunning: Bool) throws {
        if enabled {
            if !coreRunning { try seal(enabled: true) }
        } else {
            try unseal()
        }
    }

    // MARK: AES-GCM under the Keychain's key

    /// Its own Keychain item, so a config, a password and a recovery phrase never share one.
    private func key() throws -> SymmetricKey {
        let material = SymmetricKey(data: Data(try secrets.password(.configSealKey).utf8))
        return HKDF<SHA256>.deriveKey(inputKeyMaterial: material, info: Data("skywire config at rest".utf8), outputByteCount: 32)
    }

    /// nonce ‖ ciphertext ‖ tag: the nonce is random per seal, never reused.
    private func encrypt(_ plain: Data) throws -> Data {
        guard let combined = try AES.GCM.seal(plain, using: key()).combined else {
            throw VaultError(message: L10n.text("core_seal_readback_failed"))
        }
        return combined
    }

    private func decrypt(_ blob: Data) -> Data? {
        guard let box = try? AES.GCM.SealedBox(combined: blob), let key = try? key() else { return nil }
        return try? AES.GCM.open(box, using: key)
    }

    /// Owner-only, as the visor writes its config: the sandbox already is, this holds if the file moves.
    static func writePrivate(_ data: Data, to url: URL) throws {
        try data.write(to: url, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: url.path)
    }

    private func writePrivate(_ data: Data, to url: URL) throws {
        try Self.writePrivate(data, to: url)
    }

    struct VaultError: LocalizedError {
        let message: String
        var errorDescription: String? { message }
    }
}
