import CoreClient
import Foundation
import UniformTypeIdentifiers

/// A file picked for the draft, read in full: a Skymail message is 1 MiB by default.
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
    /// A one-off line for the snackbar, cleared once shown.
    @Published var notice: String?
    @Published var share: SharedFile?

    static let pollInterval: Duration = .seconds(15)
    static let defaultMaxMessage: Int64 = 1 << 20
    /// The text and headers that ride along with the attachments.
    static let envelopeBytes: Int64 = 16 * 1024

    private var app: AppModel?

    var messages: [MailSummary]? { folder == MailFolder.sent ? sent : inbox }
    var myAddress: String? { status?.myAddress }
    /// An inner view is up (a draft, the settings or a message): back steps out of it first.
    var inner: Bool { draft != nil || settingsOpen || open != nil }

    // MARK: Loop

    /// Runs while the screen is up and its key holds (`.task(id: app.connected)`).
    func run(_ app: AppModel) async {
        self.app = app
        guard app.connected else { return }
        while !Task.isCancelled {
            await refreshNow()
            openPendingLink()
            do { try await Task.sleep(for: Self.pollInterval) } catch { return }
        }
    }

    func selectFolder(_ folder: String) {
        self.folder = folder
        Task { await refreshNow() }
    }

    private func refreshNow() async {
        guard let app, app.connected else { return }
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
            inReplyTo: message.messageID
        )
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
        // Base64 grows a file by a third, and the text and headers ride along.
        if Int64(all.reduce(0) { $0 + $1.bytes.count }) * 4 / 3 + Self.envelopeBytes > limit {
            notice = L10n.format("mail_attachments_too_big", Self.formatBytes(limit))
            return
        }
        current.attachments = all
        draft = current
    }

    func removeAttachment(_ index: Int) {
        guard draft?.attachments.indices.contains(index) == true else { return }
        draft?.attachments.remove(at: index)
    }

    /// A contact's address appended to To.
    func addRecipient(_ pk: String) {
        guard let address = SkymailAddress.of(pk), var current = draft else { return }
        var to = current.to.trimmingCharacters(in: .whitespacesAndNewlines)
        while to.hasSuffix(",") { to.removeLast() }
        current.to = [to, address].filter { !$0.isEmpty }.joined(separator: ", ")
        draft = current
    }

    func send() {
        guard let current = draft, let app else { return }
        let to = Self.parseRecipients(current.to)
        let cc = Self.parseRecipients(current.cc)
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
            } catch {
                notice = L10n.format("mail_send_failed", error.localizedDescription)
            }
        }
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

    /// Opens the message a notification named, once the inbox has it.
    func openPendingLink() {
        guard let link = MailRouter.shared.pending else { return }
        let summary = inbox?.first { $0.id == link.id }
        if let summary {
            folder = MailFolder.inbox
            draft = nil
            settingsOpen = false
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
    static func parseRecipients(_ text: String) -> [(typed: String, address: String?)] {
        text.components(separatedBy: CharacterSet(charactersIn: ",; \n"))
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
            .map { ($0, SkymailAddress.recipient($0)) }
    }

    private static func readAttachment(_ url: URL) -> DraftAttachment? {
        let scoped = url.startAccessingSecurityScopedResource()
        defer { if scoped { url.stopAccessingSecurityScopedResource() } }
        guard let bytes = try? Data(contentsOf: url) else { return nil }
        let mime = UTType(filenameExtension: url.pathExtension)?.preferredMIMEType ?? "application/octet-stream"
        return DraftAttachment(name: url.lastPathComponent, mime: mime, bytes: bytes)
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
