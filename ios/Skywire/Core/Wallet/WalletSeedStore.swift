import Foundation
import Security

/// Recovery phrases, sealed at rest (Android: wallet/WalletSeedStore.kt,
/// AES-GCM under a non-exportable AndroidKeyStore key; here the Keychain is
/// both). A separate service and a separate access group from SecretStore's:
/// coins and service passwords must not share a blast radius, and the
/// packet-tunnel extension (Lane D), which shares SecretStore's group, has no
/// business reading a phrase.
///
/// Each item is readable only while the phone is unlocked and never leaves
/// it (`kSecAttrAccessibleWhenUnlockedThisDeviceOnly`): a backup restored on
/// another phone carries no phrase. The phrase exists in plaintext only in
/// memory, on its way to derivation or signing; every send and every reveal
/// is additionally gated by Face ID / Touch ID / the passcode at the screen.
///
/// Deliberately no access-control flag demanding biometry per read, as on
/// Android: deriving a new receive address legitimately runs without a
/// prompt, and a Keychain-enforced prompt would lock the phrase away the
/// moment the passcode is removed.
final class WalletSeedStore: Sendable {
    struct KeychainError: Error, LocalizedError, Equatable {
        let status: OSStatus
        let operation: String

        var errorDescription: String? {
            let reason = SecCopyErrorMessageString(status, nil).map { $0 as String } ?? "OSStatus \(status)"
            return "Keychain \(operation) failed: \(reason)"
        }
    }

    private let service: String
    private let accessGroup: String?

    init(service: String, accessGroup: String?) {
        self.service = service
        self.accessGroup = accessGroup
    }

    /// The app's store: the bundle's service name and the wallet's own
    /// Keychain access group (Info.plist `SkywireWalletKeychainGroup`, from
    /// the xcconfig).
    static func app() -> WalletSeedStore {
        let bundle = Bundle.main
        let group = (bundle.object(forInfoDictionaryKey: "SkywireWalletKeychainGroup") as? String).flatMap { $0.isEmpty ? nil : $0 }
        return WalletSeedStore(service: "\(bundle.bundleIdentifier ?? "com.skycoin.skywire").wallet", accessGroup: group)
    }

    func putSeed(_ walletId: String, _ mnemonic: String) throws {
        let data = Data(mnemonic.utf8)
        var item = query(walletId)
        item[kSecValueData as String] = data
        item[kSecAttrAccessible as String] = kSecAttrAccessibleWhenUnlockedThisDeviceOnly
        var status = SecItemAdd(item as CFDictionary, nil)
        if status == errSecDuplicateItem {
            status = SecItemUpdate(query(walletId) as CFDictionary, [kSecValueData as String: data] as CFDictionary)
        }
        guard status == errSecSuccess else { throw KeychainError(status: status, operation: "write") }
    }

    /// Nil when the id is unknown (or the item was lost).
    func seed(_ walletId: String) throws -> String? {
        var request = query(walletId)
        request[kSecReturnData as String] = true
        request[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(request as CFDictionary, &result)
        switch status {
        case errSecSuccess:
            guard let data = result as? Data else { return nil }
            return String(data: data, encoding: .utf8)
        case errSecItemNotFound:
            return nil
        default:
            throw KeychainError(status: status, operation: "read")
        }
    }

    func deleteSeed(_ walletId: String) throws {
        let status = SecItemDelete(query(walletId) as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw KeychainError(status: status, operation: "delete")
        }
    }

    /// The attributes a stored phrase carries, for the gate review (G5) and
    /// the tests: its accessibility class and access group.
    func attributes(_ walletId: String) throws -> [String: Any]? {
        var request = query(walletId)
        request[kSecReturnAttributes as String] = true
        request[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(request as CFDictionary, &result)
        switch status {
        case errSecSuccess: return result as? [String: Any]
        case errSecItemNotFound: return nil
        default: throw KeychainError(status: status, operation: "read")
        }
    }

    private func query(_ walletId: String) -> [String: Any] {
        var query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: "seed_\(walletId)",
            kSecUseDataProtectionKeychain as String: true,
        ]
        if let accessGroup {
            query[kSecAttrAccessGroup as String] = accessGroup
        }
        return query
    }
}
