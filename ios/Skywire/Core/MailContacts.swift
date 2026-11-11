import CoreClient
import Foundation

/// A contact of the visor's book as SkyMail sees it: its name and the mailboxes it reaches.
struct MailContact: Equatable, Hashable, Sendable {
    let uid: String
    let name: String
    let addresses: [String]

    /// The address to write to, and the one an edit changes.
    var address: String { addresses[0] }
}

/// SkyMail's side of the visor's contact book (Android: core/MailContacts.kt),
/// which SkyChat and the dashboard share. A contact reaches each Skymail
/// address in its mail and the mailbox of each of its keys, one entry per key
/// whatever the local part or network.
enum MailContacts {
    /// Where SkyMail kept its own book before the visor had one, in the identity's local folder.
    static let legacyFileName = "skymail-contacts.json"

    /// The book's contacts that mail can reach, in the book's name order.
    static func of(_ book: [BookContact]) -> [MailContact] {
        book.compactMap { c in
            var seen = Set<String>()
            let addresses = (c.mail.filter { pk(of: $0) != nil } + c.keys.compactMap { SkymailAddress.of($0) })
                .filter { seen.insert(pk(of: $0) ?? $0).inserted }
            return addresses.isEmpty ? nil : MailContact(uid: c.uid, name: c.name, addresses: addresses)
        }
    }

    /// The contact to store when `name` is saved for `address`, a Skymail address.
    /// Editing `uid` moves it off the mailbox of `replacing`; a new name for a
    /// mailbox already in the book renames the contact holding it. Whatever else
    /// the contact holds is kept.
    static func saved(_ book: [BookContact], name: String, address: String, uid: String? = nil, replacing: String? = nil) -> BookContact? {
        guard let key = pk(of: address) else { return nil }
        let old = uid.flatMap { id in book.first { $0.uid == id } } ?? book.first { keys(of: $0).contains(key) }
        let moved = replacing.flatMap(pk(of:))
        var contact = old ?? BookContact()
        contact.name = name.trimmingCharacters(in: .whitespacesAndNewlines)
        contact.mail = [address] + contact.mail.filter { pk(of: $0).map { $0 != key && $0 != moved } ?? true }
        var seen = Set<String>()
        contact.keys = (contact.keys.map { $0.lowercased() == moved ? key : $0 } + [key]).filter { seen.insert($0.lowercased()).inserted }
        return contact
    }

    /// The contacts to add to `book` from the JSON list SkyMail kept before the
    /// visor had a book. Mailboxes the book holds already are skipped, so
    /// importing twice adds nothing.
    static func imports(_ file: URL, _ book: [BookContact]) -> [BookContact] {
        struct Legacy: Decodable {
            let name: String
            let address: String
        }
        guard let data = try? Data(contentsOf: file), let legacy = try? JSONDecoder().decode([Legacy].self, from: data) else { return [] }
        var held = Set(book.flatMap(keys(of:)))
        return legacy.compactMap { c in
            let name = c.name.trimmingCharacters(in: .whitespacesAndNewlines)
            guard let key = pk(of: c.address), !name.isEmpty, held.insert(key).inserted else { return nil }
            return BookContact(name: name, keys: [key], mail: [c.address.trimmingCharacters(in: .whitespacesAndNewlines)])
        }
    }

    /// The mailboxes of `contact`: the keys of its Skymail addresses and its own keys.
    private static func keys(of contact: BookContact) -> Set<String> {
        Set(contact.mail.compactMap(pk(of:)) + contact.keys.map { $0.lowercased() })
    }

    private static func pk(of address: String) -> String? {
        SkymailAddress.pk(ofAddress: address)?.lowercased()
    }
}
