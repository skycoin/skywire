import CoreClient
import Foundation

/// A name for a SkyMail address.
struct MailContact: Codable, Equatable, Hashable, Sendable {
    let name: String
    let address: String
}

/// SkyMail's own address book (Android: core/MailContacts.kt), a JSON file in the identity's
/// local folder, so replacing the key clears it with the rest. SkyChat's names are separate.
struct MailContacts: Sendable {
    let file: URL

    static let fileName = "skymail-contacts.json"

    /// The saved contacts by name, or none when the file is missing or unreadable.
    func load() -> [MailContact] {
        guard let data = try? Data(contentsOf: file),
              let list = try? JSONDecoder().decode([MailContact].self, from: data)
        else { return [] }
        return Self.sorted(list)
    }

    /// Saves `contact`, replacing the one at `replacing` when editing and any other entry for
    /// the same mailbox. Returns the new list.
    func put(_ contact: MailContact, replacing: String? = nil) throws -> [MailContact] {
        let drop = Set([Self.key(contact.address)] + (replacing.map { [Self.key($0)] } ?? []))
        return try write(load().filter { !drop.contains(Self.key($0.address)) } + [contact])
    }

    func remove(_ address: String) throws -> [MailContact] {
        try write(load().filter { Self.key($0.address) != Self.key(address) })
    }

    private func write(_ list: [MailContact]) throws -> [MailContact] {
        try FileManager.default.createDirectory(at: file.deletingLastPathComponent(), withIntermediateDirectories: true)
        try JSONEncoder().encode(list).write(to: file, options: .atomic)
        return Self.sorted(list)
    }

    /// One mailbox whichever network an address was written for.
    static func key(_ address: String) -> String {
        SkymailAddress.withNetwork(address.trimmingCharacters(in: .whitespacesAndNewlines).lowercased(), suffix: SkymailAddress.dmsg)
    }

    private static func sorted(_ list: [MailContact]) -> [MailContact] {
        list.sorted { $0.name.lowercased() < $1.name.lowercased() }
    }
}
