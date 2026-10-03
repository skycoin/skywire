import Foundation
import Security

/// The device-local passwords the app generates for the services it starts,
/// kept in the Keychain. The port of Android's core/SecretStore.kt, where
/// DataStore holds an AES-GCM ciphertext under an AndroidKeyStore key; here
/// the Keychain is both.
///
/// Each item is readable after the first unlock since boot and never leaves
/// the device (`kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly`): the core
/// needs them while the screen is locked (in the packet-tunnel extension, on
/// a device), and a backup restored elsewhere must not carry them. They are
/// written in the app's Keychain access group so the extension can read the
/// same items (Lane D).
///
/// The Keychain needs a signed app: an unsigned one (CODE_SIGNING_ALLOWED=NO)
/// gets errSecMissingEntitlement on every call, on the Simulator too. So the
/// Simulator build is signed to run locally, which needs no Apple account,
/// with the same entitlements file a device build uses; Xcode gives it the
/// team prefix FAKETEAMID, in the entitlement and in Info.plist alike, so the
/// access group is checked there as it will be on a device.
public final class SecretStore: Sendable {
    public enum Secret: String, CaseIterable, Sendable {
        /// The password of the visor's single `admin` account.
        case apiPassword = "api_password"
        /// Gates skychat's HTTP surface (SkychatProfile). Its own secret,
        /// because it is handed to a different server than the API's.
        case skychatPassword = "skychat_password"
        /// Gates skydex-client's trading UI (SkydexProfile).
        case skydexPassword = "skydex_password"
    }

    public struct KeychainError: Error, LocalizedError, Equatable {
        public let status: OSStatus
        public let operation: String

        public var errorDescription: String? {
            let reason = SecCopyErrorMessageString(status, nil).map { $0 as String } ?? "OSStatus \(status)"
            return "Keychain \(operation) failed: \(reason)"
        }
    }

    private let service: String
    private let accessGroup: String?

    /// - Parameters:
    ///   - service: the items' `kSecAttrService`.
    ///   - accessGroup: the Keychain access group shared with the extension,
    ///     or nil for the app's default group.
    public init(service: String, accessGroup: String?) {
        self.service = service
        self.accessGroup = accessGroup
    }

    /// The secret, generated and stored the first time it is asked for.
    /// Concurrent first calls agree: a losing add reads the winner's item.
    public func password(_ secret: Secret) throws -> String {
        if let stored = try read(secret) {
            return stored
        }
        let fresh = Self.generatePassword()
        var item = query(secret)
        item[kSecValueData as String] = Data(fresh.utf8)
        item[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        let status = SecItemAdd(item as CFDictionary, nil)
        switch status {
        case errSecSuccess:
            return fresh
        case errSecDuplicateItem:
            if let stored = try read(secret) { return stored }
            throw KeychainError(status: status, operation: "add")
        default:
            throw KeychainError(status: status, operation: "add")
        }
    }

    /// Removes the secret; the next `password` generates a new one.
    public func delete(_ secret: Secret) throws {
        let status = SecItemDelete(query(secret) as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw KeychainError(status: status, operation: "delete")
        }
    }

    private func read(_ secret: Secret) throws -> String? {
        var request = query(secret)
        request[kSecReturnData as String] = true
        request[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(request as CFDictionary, &result)
        switch status {
        case errSecSuccess:
            guard let data = result as? Data, let text = String(data: data, encoding: .utf8), !text.isEmpty else {
                // Unreadable: treat it as missing, so it is replaced.
                try delete(secret)
                return nil
            }
            return text
        case errSecItemNotFound:
            return nil
        default:
            throw KeychainError(status: status, operation: "read")
        }
    }

    private func query(_ secret: Secret) -> [String: Any] {
        var query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: secret.rawValue,
            kSecUseDataProtectionKeychain as String: true,
        ]
        if let accessGroup {
            query[kSecAttrAccessGroup as String] = accessGroup
        }
        return query
    }

    /// A random password the API's policy accepts: 6 to 64 characters, at
    /// least one upper case letter, lower case letter, digit and symbol,
    /// printable ASCII with no spaces. skychat's policy is a subset of it.
    /// Characters that read alike (I, l, O, 0, 1) are left out, as on Android.
    static func generatePassword() -> String {
        let upper = Array("ABCDEFGHJKLMNPQRSTUVWXYZ")
        let lower = Array("abcdefghijkmnopqrstuvwxyz")
        let digit = Array("23456789")
        let special = Array("!@#$%^&*()-_=+[]{}<>.,?/")
        let all = upper + lower + digit + special
        var generator = SystemRandomNumberGenerator()
        var characters = (0..<20).map { _ in all.randomElement(using: &generator)! }
        for set in [upper, lower, digit, special] {
            characters.append(set.randomElement(using: &generator)!)
        }
        characters.shuffle(using: &generator)
        return String(characters)
    }
}
