import Contacts
import CoreClient
import Foundation
import os

/// The visor's contact book copied into the phone's Contacts app, in a
/// Skywire group, so it shows the names (Android: core/PhoneContacts.kt).
/// Off until asked for: any app allowed to read contacts can then read the
/// copy, while the book itself never leaves the visor (pkg/contacts).
///
/// iOS has no read-only accounts, so each copy makes the entries match the
/// book again, and the entries this app made are the only ones it changes.
/// Notes are not copied: writing them needs an entitlement Apple grants on request.
enum PhoneContacts {
    static let prefKey = "phone_contacts"
    private static let heldKey = "phone_contacts_held"
    private static let groupName = "Skywire"
    private static let log = Logger(subsystem: Bundle.main.bundleIdentifier ?? "skywire", category: "contacts")
    private static var keys: [CNKeyDescriptor] {
        [CNContactIdentifierKey, CNContactGivenNameKey, CNContactFamilyNameKey, CNContactUrlAddressesKey, CNContactEmailAddressesKey] as [CNKeyDescriptor]
    }

    @MainActor private static var syncing = false
    @MainActor private static var again = false

    static var wanted: Bool { UserDefaults.standard.bool(forKey: prefKey) }

    static var granted: Bool {
        let status = CNContactStore.authorizationStatus(for: .contacts)
        if status == .authorized { return true }
        // Limited access still lets the app add contacts and see the ones it added.
        if #available(iOS 18, *), status == .limited { return true }
        return false
    }

    /// Asks for access when needed, turns the copy on and fills it; false when access was refused.
    @MainActor
    static func enable(_ client: CoreClient) async -> Bool {
        if CNContactStore.authorizationStatus(for: .contacts) == .notDetermined {
            _ = try? await CNContactStore().requestAccess(for: .contacts)
        }
        guard granted else { return false }
        UserDefaults.standard.set(true, forKey: prefKey)
        await sync(client)
        return true
    }

    @MainActor
    static func disable() async {
        UserDefaults.standard.set(false, forKey: prefKey)
        await Task.detached { forget() }.value
    }

    /// Removes every entry this app copied and its group. The choice stays, so a
    /// new identity's book fills a new copy.
    static func forget() {
        let held = load()
        defer { save([:]) }
        guard granted else { return }
        let store = CNContactStore()
        let request = CNSaveRequest()
        for identifier in held.values {
            if let contact = try? store.unifiedContact(withIdentifier: identifier, keysToFetch: keys).mutableCopy() as? CNMutableContact {
                request.delete(contact)
            }
        }
        if let group = try? store.groups(matching: nil).first(where: { $0.name == groupName }), let mutable = group.mutableCopy() as? CNMutableGroup {
            request.delete(mutable)
        }
        do {
            try store.execute(request)
        } catch {
            log.error("contacts copy not removed: \(error.localizedDescription, privacy: .public)")
        }
    }

    /// Brings the copy up to date when it is on. A visor that does not answer
    /// leaves it as it was; a revoked permission drops the record of it.
    @MainActor
    static func sync(_ client: CoreClient) async {
        guard wanted else { return }
        guard granted else {
            save([:])
            return
        }
        // One copy at a time, or two would both add the same new contact.
        if syncing {
            again = true
            return
        }
        syncing = true
        defer { syncing = false }
        repeat {
            again = false
            guard let book = try? await client.contacts() else { return }
            await Task.detached { write(book) }.value
        } while again
    }

    /// What one copied entry holds: the name, a SkyChat link per key, a SkyMail
    /// link per Skymail address, and the other addresses as email. A Skymail
    /// address is never an email row, or a mail app would try to deliver it.
    struct Rows: Equatable {
        var name: String
        var urls: [String] = []
        var emails: [String] = []
    }

    static func rows(_ c: BookContact) -> Rows {
        Rows(
            name: c.name,
            urls: c.keys.map { chatLink($0.lowercased()) } + c.mail.filter { SkymailAddress.pk(ofAddress: $0) != nil }.map(mailLink),
            emails: c.mail.filter { SkymailAddress.pk(ofAddress: $0) == nil }
        )
    }

    /// The links a row opens Skywire with (Info.plist CFBundleURLTypes).
    static func chatLink(_ pk: String) -> String { "\(ChatRouter.scheme)://\(pk)" }
    static func mailLink(_ address: String) -> String { "\(MailRouter.scheme):\(address)" }

    private static func held(_ contact: CNContact) -> Rows {
        Rows(
            name: [contact.givenName, contact.familyName].filter { !$0.isEmpty }.joined(separator: " "),
            urls: contact.urlAddresses.map { $0.value as String },
            emails: contact.emailAddresses.map { $0.value as String }
        )
    }

    private static func write(_ book: [BookContact]) {
        let store = CNContactStore()
        var held = load()
        let request = CNSaveRequest()
        let group = ensureGroup(store)
        var added: [(uid: String, contact: CNMutableContact)] = []
        var live = Set<String>()
        for c in book where !c.uid.isEmpty {
            live.insert(c.uid)
            let rows = rows(c)
            if let identifier = held[c.uid], let existing = try? store.unifiedContact(withIdentifier: identifier, keysToFetch: keys) {
                guard Self.held(existing) != rows, let contact = existing.mutableCopy() as? CNMutableContact else { continue }
                fill(contact, rows)
                request.update(contact)
            } else {
                // New, or deleted in the Contacts app since the last copy.
                let contact = CNMutableContact()
                fill(contact, rows)
                request.add(contact, toContainerWithIdentifier: nil)
                if let group { request.addMember(contact, to: group) }
                added.append((c.uid, contact))
            }
        }
        for (uid, identifier) in held where !live.contains(uid) {
            if let contact = try? store.unifiedContact(withIdentifier: identifier, keysToFetch: keys).mutableCopy() as? CNMutableContact {
                request.delete(contact)
            }
            held[uid] = nil
        }
        do {
            try store.execute(request)
            for entry in added { held[entry.uid] = entry.contact.identifier }
            save(held)
        } catch {
            log.error("contacts copy not updated: \(error.localizedDescription, privacy: .public)")
        }
    }

    private static func fill(_ contact: CNMutableContact, _ rows: Rows) {
        contact.givenName = rows.name
        contact.familyName = ""
        contact.urlAddresses = rows.urls.map { link in
            CNLabeledValue(label: link.hasPrefix(ChatRouter.scheme) ? "SkyChat" : "SkyMail", value: link as NSString)
        }
        contact.emailAddresses = rows.emails.map { CNLabeledValue(label: CNLabelOther, value: $0 as NSString) }
    }

    private static func ensureGroup(_ store: CNContactStore) -> CNGroup? {
        if let group = try? store.groups(matching: nil).first(where: { $0.name == groupName }) { return group }
        let group = CNMutableGroup()
        group.name = groupName
        let request = CNSaveRequest()
        request.add(group, toContainerWithIdentifier: nil)
        guard (try? store.execute(request)) != nil else { return nil }
        return try? store.groups(matching: nil).first { $0.name == groupName }
    }

    /// The Contacts identifier of each copied entry, by the book's uid.
    private static func load() -> [String: String] {
        UserDefaults.standard.dictionary(forKey: heldKey) as? [String: String] ?? [:]
    }

    private static func save(_ held: [String: String]) {
        UserDefaults.standard.set(held, forKey: heldKey)
    }
}
