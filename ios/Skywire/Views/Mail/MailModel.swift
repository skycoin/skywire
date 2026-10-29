import CoreClient
import Foundation
import UniformTypeIdentifiers

/// A file picked for the draft, read in full: a SkyMail message is 10 MiB by default.
struct DraftAttachment: Equatable {
    let name: String
    let mime: String
    let bytes: Data
}

struct MailDraft: Equatable {
    var to = ""
    var cc = ""
    var subject = ""
    var body = ""
    var inReplyTo = ""
    var attachments: [DraftAttachment] = []
    /// The network every address in the draft goes over.
    var skynet = false

    var suffix: String { skynet ? SkymailAddress.skynet : SkymailAddress.dmsg }
}

struct OpenMail: Equatable {
    let folder: String
    let summary: MailSummary
    var message: MailMessage?
    var error: String?
}

/// An attachment written out for the share sheet, which is how iOS saves a file.
struct SharedFile: Identifiable {
    let id = UUID()
    let url: URL
}

/// Drives the Skymail screen over the visor's mail routes (Android: MailViewModel).
/// The mailbox lives in the core, so everything waits for it, and the lists are
/// polled while the screen is up, as the desktop dashboard's mail tab does.
@MainActor
final class MailModel: ObservableObject {
    @Published private(set) var status: MailStatus?
    @Published private(set) var folder = MailFolder.inbox
    @Published private(set) var inbox: [MailSummary]?
    @Published private(set) var sent: [MailSummary]?
    @Published private(set) var open: OpenMail?
    @Published var draft: MailDraft?
    @Published private(set) var sending = false
    /// The last send: which recipients got it, and why the others did not.
    @Published private(set) var lastSend: MailSendResult?
    @Published private(set) var settingsOpen = false
    /// A settings change or a delete is in flight.
    @Published private(set) var busy = false
    /// Message ids picked in the folder shown, for a batch delete.
    @Published private(set) var selection: Set<String> = []
    /// The visor's contacts that mail can reach, sorted by name.
    @Published private(set) var contacts: [MailContact] = []
    @Published private(set) var contactsOpen = false
    /// A one-off line for the snackbar, cleared once shown.
    @Published var notice: String?
    @Published var share: SharedFile?

    static let pollInterval: Duration = .seconds(15)
    static let defaultMaxMessage: Int64 = 10 << 20
    /// The text and headers that ride along with the attachments.
    static let envelopeBytes: Int64 = 16 * 1024

    private var app: AppModel?

    var messages: [MailSummary]? { folder == MailFolder.sent ? sent : inbox }
    var myAddress: String? { status?.myAddress }
    /// An inner view is up (a draft, the settings or a message): back steps out of it first.
    var inner: Bool { contactsOpen || draft != nil || settingsOpen || open != nil }
    var selecting: Bool { !selection.isEmpty }

    // MARK: Loop

    /// Runs while the screen is up and its key holds (`.task(id: app.connected)`).
    func run(_ app: AppModel) async {
        self.app = app
        guard app.connected else { return }
        await importLegacyContacts(app)
        while !Task.isCancelled {
            await refreshNow()
            openPendingLink()
            do { try await Task.sleep(for: Self.pollInterval) } catch { return }
        }
    }

    func selectFolder(_ folder: String) {
        // A selection belongs to the folder it was made in.
        self.folder = folder
        selection = []
        Task { await refreshNow() }
    }

    private func refreshNow() async {
        guard let app, app.connected else { return }
        // The book changes from SkyChat and the dashboard too.
        await loadContacts()
        do {
            status = try await app.client.mailStatus()
        } catch {
            if !app.handle(error) { notice = error.localizedDescription }
            return
        }
        guard status?.running == true else { return }
        let folder = self.folder
        do {
            let list = try await app.client.mailList(folder)
            if folder == MailFolder.sent { sent = list } else { inbox = list }
        } catch {
            if !app.handle(error) { notice = error.localizedDescription }
        }
    }

    // MARK: Reading

    func open(_ folder: String, _ summary: MailSummary) {
        open = OpenMail(folder: folder, summary: summary)
        guard let app else { return }
        Task {
            do {
                let message = try await app.client.mailRead(folder, id: summary.id)
                if open?.summary.id == summary.id { open?.message = message }
                inbox = inbox?.map { item in
                    var item = item
                    if item.id == summary.id { item.seen = true }
                    return item
                }
                await refreshNow()
            } catch {
                if open?.summary.id == summary.id { open?.error = error.localizedDescription }
            }
        }
    }

    func closeMessage() {
        open = nil
    }

    func delete() {
        guard let current = open, let app else { return }
        busy = true
        Task {
            defer { busy = false }
            do {
                try await app.client.mailDelete(current.folder, id: current.summary.id)
                open = nil
                inbox = inbox?.filter { $0.id != current.summary.id }
                sent = sent?.filter { $0.id != current.summary.id }
                notice = L10n.text("mail_deleted")
                await refreshNow()
            } catch {
                notice = error.localizedDescription
            }
        }
    }

    /// Fetches attachment `n` of the open message and hands it to the share sheet.
    func saveAttachment(_ n: Int) {
        guard let current = open, let info = current.message?.attachments[safe: n], let app else { return }
        Task {
            do {
                let bytes = try await app.client.mailAttachment(current.folder, id: current.summary.id, n: n)
                let folder = FileManager.default.temporaryDirectory
                    .appendingPathComponent("mail-attachments", isDirectory: true)
                    .appendingPathComponent(UUID().uuidString, isDirectory: true)
                try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
                let file = folder.appendingPathComponent(ChatDownloads.fileName(info.name.isEmpty ? "attachment" : info.name))
                try bytes.write(to: file)
                share = SharedFile(url: file)
            } catch {
                notice = L10n.format("mail_attachment_failed", error.localizedDescription)
            }
        }
    }

    /// The share sheet closed: its copy of the attachment goes.
    func shareDone(_ file: SharedFile) {
        try? FileManager.default.removeItem(at: file.url.deletingLastPathComponent())
    }

    // MARK: Batch selection

    func toggleSelect(_ id: String) {
        if selection.contains(id) { selection.remove(id) } else { selection.insert(id) }
    }

    func selectAll() {
        guard let ids = messages?.map(\.id) else { return }
        selection = Set(ids)
    }

    /// Flips the picks within the folder shown.
    func invertSelection() {
        guard let ids = messages?.map(\.id) else { return }
        selection = Set(ids).subtracting(selection)
    }

    func clearSelection() {
        selection = []
    }

    /// Deletes every picked message in the folder shown, one call each.
    func deleteSelected() {
        guard !busy, !selection.isEmpty, let app else { return }
        let folder = self.folder
        let ids = Array(selection)
        busy = true
        Task {
            var failed = 0
            for id in ids {
                do { try await app.client.mailDelete(folder, id: id) } catch { failed += 1 }
            }
            let gone = Set(ids)
            if folder == MailFolder.inbox { inbox = inbox?.filter { !gone.contains($0.id) } }
            if folder == MailFolder.sent { sent = sent?.filter { !gone.contains($0.id) } }
            selection = []
            busy = false
            notice = failed == 0
                ? L10n.format("mail_batch_deleted", ids.count)
                : L10n.format("mail_batch_delete_failed", failed, ids.count)
            await refreshNow()
        }
    }

    // MARK: Writing

    func compose(to: String = "") {
        draft = MailDraft(to: to)
        lastSend = nil
    }

    /// A reply to the open message: to its sender, quoting it.
    func reply() {
        guard let current = open, let message = current.message else { return }
        let to = current.folder == MailFolder.sent ? message.to : message.from
        let subject = message.subject.lowercased().hasPrefix("re:") ? message.subject : "Re: \(message.subject)"
        let quoted = message.text.trimmingTrailingWhitespace()
            .components(separatedBy: "\n").map { "> \($0)" }.joined(separator: "\n")
        open = nil
        lastSend = nil
        draft = MailDraft(
            to: to,
            subject: subject,
            body: "\n\n" + L10n.format("mail_reply_header", message.date, message.from) + "\n" + quoted,
            inReplyTo: message.messageID,
            skynet: Self.overSkynet(to)
        )
    }

    /// Whether a reply to `address` starts on skynet: on the network of the address it answers.
    static func overSkynet(_ address: String) -> Bool {
        var answered = address.trimmingTrailingWhitespace()
        if answered.hasSuffix(">") { answered.removeLast() }
        return answered.lowercased().hasSuffix(SkymailAddress.skynet)
    }

    /// The network is the message's: the addresses already written move with it.
    func setViaSkynet(_ on: Bool) {
        guard var current = draft else { return }
        current.skynet = on
        current.to = SkymailAddress.withNetwork(current.to, suffix: current.suffix)
        current.cc = SkymailAddress.withNetwork(current.cc, suffix: current.suffix)
        draft = current
    }

    func discardDraft() {
        draft = nil
        lastSend = nil
    }

    /// The files the picker returned, read whole; refused together when they make the message too big.
    func addAttachments(_ urls: [URL]) {
        guard !urls.isEmpty else { return }
        let picked = urls.compactMap(Self.readAttachment)
        guard var current = draft else { return }
        let limit = status.map(\.limits.maxMessageSize).flatMap { $0 > 0 ? $0 : nil } ?? Self.defaultMaxMessage
        let all = current.attachments + picked
        // The wire size is what the receiver enforces. The text and headers
        // ride along in the envelope allowance.
        let encoded = all.reduce(Self.envelopeBytes) { $0 + Self.attachmentWireSize(Int64($1.bytes.count)) }
        if encoded > limit {
            // Whole KB on both sides: in MB a mail just over 1 MB and the 1 MB
            // limit both read "1.0 MB".
            notice = L10n.format("mail_attachments_too_big", Self.kilobytes(encoded), Self.kilobytes(limit))
            return
        }
        current.attachments = all
        draft = current
    }

    func removeAttachment(_ index: Int) {
        guard draft?.attachments.indices.contains(index) == true else { return }
        draft?.attachments.remove(at: index)
    }

    /// A picked address appended to To, on the draft's network.
    func addRecipient(address: String) {
        guard var current = draft else { return }
        let address = SkymailAddress.withNetwork(address, suffix: current.suffix)
        var to = current.to.trimmingCharacters(in: .whitespacesAndNewlines)
        while to.hasSuffix(",") { to.removeLast() }
        current.to = [to, address].filter { !$0.isEmpty }.joined(separator: ", ")
        draft = current
    }

    func send() {
        // The button's disabled state lags this by a frame; a second tap in
        // that window would send the mail twice, and nothing dedupes it.
        guard let current = draft, let app, !sending else { return }
        let to = Self.parseRecipients(current.to, suffix: current.suffix)
        let cc = Self.parseRecipients(current.cc, suffix: current.suffix)
        let bad = (to + cc).filter { $0.address == nil }.map(\.typed)
        if !bad.isEmpty {
            notice = L10n.format("mail_bad_address", bad.joined(separator: ", "))
            return
        }
        if to.isEmpty {
            notice = L10n.text("mail_no_recipient")
            return
        }
        let message = MailOutgoing(
            to: to.compactMap(\.address),
            cc: cc.compactMap(\.address),
            subject: current.subject.trimmingCharacters(in: .whitespacesAndNewlines),
            body: current.body,
            inReplyTo: current.inReplyTo,
            attachments: current.attachments.map { MailOutgoingAttachment(name: $0.name, contentType: $0.mime, data: $0.bytes) }
        )
        sending = true
        lastSend = nil
        Task {
            defer { sending = false }
            do {
                let result = try await app.client.mailSend(message)
                if result.delivered > 0 && result.recipients.allSatisfy(\.error.isEmpty) {
                    draft = nil
                    notice = L10n.text("mail_sent")
                } else {
                    lastSend = result
                }
                await refreshNow()
            } catch let error as URLError where error.code == .timedOut {
                // Not a verdict: the visor answers only after delivering and
                // keeps going without us, so the mail may still arrive.
                notice = L10n.text("mail_send_unknown")
            } catch {
                notice = L10n.format("mail_send_failed", error.localizedDescription)
            }
        }
    }

    // MARK: Contacts

    func openContacts() {
        contactsOpen = true
        Task { await loadContacts() }
    }

    func closeContacts() {
        contactsOpen = false
    }

    /// Saves `name` for `address`, an address or a bare key, in the visor's book.
    /// Editing passes the contact's `uid` and the address it had.
    func saveContact(name: String, address: String, uid: String? = nil, replacing: String? = nil) {
        guard let clean = SkymailAddress.recipient(address) else {
            notice = L10n.format("mail_bad_address", address.trimmingCharacters(in: .whitespacesAndNewlines))
            return
        }
        guard !name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
        changeContacts(done: L10n.text("mail_contact_saved")) { client in
            guard let contact = MailContacts.saved(try await client.contacts(), name: name, address: clean, uid: uid, replacing: replacing) else { return }
            _ = try await client.contactPut(contact)
        }
    }

    /// Removes the whole contact from the book, not only its mail addresses.
    func deleteContact(_ uid: String) {
        changeContacts(done: nil) { try await $0.contactDelete(uid) }
    }

    private func loadContacts() async {
        guard let app, let book = try? await app.client.contacts() else { return }
        contacts = MailContacts.of(book)
    }

    /// Moves the contacts SkyMail kept in its own file into the visor's book,
    /// once. The file stays until the visor has answered for every one of them.
    private func importLegacyContacts(_ app: AppModel) async {
        let file = app.paths.localDir.appendingPathComponent(MailContacts.legacyFileName)
        guard FileManager.default.fileExists(atPath: file.path) else { return }
        do {
            for contact in MailContacts.imports(file, try await app.client.contacts()) {
                do {
                    _ = try await app.client.contactPut(contact)
                } catch is ContactRejected {
                    // A key that is no curve point is refused on every try.
                }
            }
            try? FileManager.default.removeItem(at: file)
            await shareNames(app)
        } catch {
            return
        }
    }

    /// SkyChat names people from the same book, but fetches it only when its
    /// session starts. The phone's contacts hold a copy of it when asked to.
    private func shareNames(_ app: AppModel) async {
        await PhoneContacts.sync(app.client)
        await ChatRepository.shared.syncNames()
    }

    private func changeContacts(done: String?, _ change: @escaping @MainActor (CoreClient) async throws -> Void) {
        guard let app else { return }
        Task {
            do {
                try await change(app.client)
                await loadContacts()
                if let done { notice = done }
                await shareNames(app)
            } catch {
                notice = L10n.format("mail_contact_failed", error.localizedDescription)
            }
        }
    }

    /// The saved contact for a key, when there is one.
    func contact(forPK pk: String) -> MailContact? {
        contacts.first { c in c.addresses.contains { SkymailAddress.pk(ofAddress: $0)?.lowercased() == pk.lowercased() } }
    }

    // MARK: Settings

    func openSettings() {
        settingsOpen = true
    }

    func closeSettings() {
        settingsOpen = false
    }

    func setEnabled(_ enabled: Bool) {
        settingsCall { try await $0.client.mailSetEnabled(enabled) }
    }

    /// Lets `input` (a key, a skychat link or an address) deliver; false when it names no key.
    func allowSender(_ input: String) -> Bool {
        guard let pk = SkymailAddress.pk(ofInput: input) else { return false }
        let list = status?.whitelist ?? []
        if !list.contains(pk) {
            settingsCall { try await $0.client.mailSetWhitelist(list + [pk]) }
        }
        return true
    }

    func removeSender(_ pk: String) {
        let list = status?.whitelist ?? []
        settingsCall { try await $0.client.mailSetWhitelist(list.filter { $0 != pk }) }
    }

    private func settingsCall(_ call: @escaping @MainActor (AppModel) async throws -> Void) {
        guard let app else { return }
        busy = true
        Task {
            do {
                try await call(app)
            } catch {
                notice = error.localizedDescription
            }
            busy = false
            await refreshNow()
        }
    }

    // MARK: A tapped notification

    /// Opens the message a notification named, once the inbox has it, or the draft a link asked for.
    func openPendingLink() {
        guard let link = MailRouter.shared.pending else { return }
        if !link.to.isEmpty {
            settingsOpen = false
            contactsOpen = false
            open = nil
            selection = []
            compose(to: link.to)
            MailRouter.shared.handled(link)
            return
        }
        let summary = inbox?.first { $0.id == link.id }
        if let summary {
            folder = MailFolder.inbox
            draft = nil
            settingsOpen = false
            contactsOpen = false
            selection = []
            open(MailFolder.inbox, summary)
        }
        if summary != nil || inbox != nil { MailRouter.shared.handled(link) }
    }

    /// A notification arrived while the screen is up: look at the inbox now rather than at the next poll.
    func pendingLinkArrived() {
        folder = MailFolder.inbox
        Task {
            await refreshNow()
            openPendingLink()
        }
    }

    // MARK: Helpers

    /// Each typed recipient and the address it means, nil when it is not one.
    static func parseRecipients(_ text: String, suffix: String = SkymailAddress.dmsg) -> [(typed: String, address: String?)] {
        text.components(separatedBy: CharacterSet(charactersIn: ",; \n"))
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
            .map { ($0, SkymailAddress.recipient($0, suffix: suffix)) }
    }

    private static func readAttachment(_ url: URL) -> DraftAttachment? {
        let scoped = url.startAccessingSecurityScopedResource()
        defer { if scoped { url.stopAccessingSecurityScopedResource() } }
        guard let bytes = try? Data(contentsOf: url) else { return nil }
        let mime = UTType(filenameExtension: url.pathExtension)?.preferredMIMEType ?? "application/octet-stream"
        return DraftAttachment(name: url.lastPathComponent, mime: mime, bytes: bytes)
    }

    /// The bytes an attachment of `count` bytes takes in the sent mail: base64 in 76-column
    /// lines, each ending CRLF, as compose in pkg/skymail/send.go writes it.
    static func attachmentWireSize(_ count: Int64) -> Int64 {
        let encoded = (count + 2) / 3 * 4
        return encoded + 2 * max(1, (encoded + 75) / 76)
    }

    /// A byte count in whole KB, rounded up and grouped in the app's language.
    static func kilobytes(_ count: Int64) -> String {
        ((count + 1023) / 1024).formatted(.number.locale(L10n.locale)) + " KB"
    }

    /// A byte count as a person reads it, in the app's language (Android: ui/mail formatBytes).
    static func formatBytes(_ count: Int64) -> String {
        if count >= 1 << 20 { return String(format: "%.1f MB", locale: L10n.locale, Double(count) / (1024 * 1024)) }
        if count >= 1 << 10 { return String(format: "%.0f KB", locale: L10n.locale, Double(count) / 1024) }
        return "\(count) B"
    }
}

extension Collection {
    subscript(safe index: Index) -> Element? {
        indices.contains(index) ? self[index] : nil
    }
}

extension String {
    func trimmingTrailingWhitespace() -> String {
        var text = self
        while let last = text.last, last.isWhitespace { text.removeLast() }
        return text
    }
}
