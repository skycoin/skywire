import CoreBridge
import CoreClient
import Foundation
import WalletCore

/// The visor's identity on this phone, the keypair in its config (Android: ConfigManager's
/// derivePublicKey, replaceSecretKey and resetIdentity). Change it with the core stopped.
enum Identity {
    static let secretKeyLength = 64

    /// The compressed public key of `secretKey`, which this validates first: a mistyped key must
    /// be an error here, because the core's generator, handed a key it cannot use, quietly makes
    /// a new one. The curve is libsecp256k1's (the wallet's), not this code's.
    static func publicKey(of secretKey: String) throws -> String {
        let sk = secretKey.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard sk.count == secretKeyLength, sk.allSatisfy(\.isHexDigit), let bytes = [UInt8](hex: sk) else {
            throw IdentityError(message: L10n.format("settings_sk_invalid", secretKeyLength))
        }
        guard let pk = try? Secp256k1.pubKeyFromSecKey(bytes) else {
            throw IdentityError(message: L10n.text("settings_sk_not_a_key_ios"))
        }
        return pk.hex
    }

    /// The `pk` a config file names, or nil.
    static func publicKey(inConfig url: URL) -> String? {
        guard let data = try? Data(contentsOf: url),
              let config = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let pk = config["pk"] as? String, !pk.isEmpty
        else { return nil }
        return pk
    }

    /// Rebuilds the config around `secretKey` and returns its public key. The generator has no
    /// key option: regenerating takes `sk` from the file it overwrites, so the key goes in first,
    /// and the `pk` it writes must be the one derived here or the previous config goes back.
    static func replace(secretKey: String, paths: CorePaths, vault: ConfigVault) async throws -> String {
        let pk = try publicKey(of: secretKey)
        try vault.unseal()
        try paths.createDirectories()
        let previous = try? Data(contentsOf: paths.configFile)
        var config = previous.flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: Any] } ?? [:]
        config["sk"] = secretKey.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        // Dropped, not corrected: the generator derives it from the key.
        config.removeValue(forKey: "pk")
        try ConfigVault.writePrivate(try JSONSerialization.data(withJSONObject: config, options: [.prettyPrinted]), to: paths.configFile)
        do {
            try await CoreBridge.shared.configGen(outPath: paths.configFile.path, options: ConfigProfile.genOptions(paths: paths, regen: true))
            guard publicKey(inConfig: paths.configFile) == pk else {
                throw IdentityError(message: L10n.text("settings_sk_mismatch_ios"))
            }
        } catch {
            if let previous {
                try? ConfigVault.writePrivate(previous, to: paths.configFile)
            } else {
                try? FileManager.default.removeItem(at: paths.configFile)
            }
            throw error
        }
        try clearIdentityData(paths)
        return pk
    }

    /// Throws the identity away: no config, sealed or not, so the next start generates one.
    static func reset(paths: CorePaths, vault: ConfigVault) throws {
        for url in [paths.configFile, vault.sealedFile] where FileManager.default.fileExists(atPath: url.path) {
            try FileManager.default.removeItem(at: url)
        }
        try clearIdentityData(paths)
    }

    /// What belonged to the replaced identity: chat history and the apps' work in `local/`.
    /// users.db stays; the API account is this app's, not the visor's.
    private static func clearIdentityData(_ paths: CorePaths) throws {
        if FileManager.default.fileExists(atPath: paths.localDir.path) {
            try FileManager.default.removeItem(at: paths.localDir)
        }
        try paths.createDirectories()
    }

    struct IdentityError: LocalizedError {
        let message: String
        var errorDescription: String? { message }
    }
}
