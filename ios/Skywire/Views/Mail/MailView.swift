import CoreClient
import SwiftUI
import UIKit

/// Skymail (Android: MailScreen): the visor's own mailbox, mail between visors over Skywire.
/// One screen with four views (the folders, a message, a draft, the settings), so back steps
/// out of the inner one first.
struct MailView: View {
    @EnvironmentObject private var app: AppModel
    @EnvironmentObject private var navigator: Navigator
    @EnvironmentObject private var dialogs: SkyDialogs
    @ObservedObject private var router = MailRouter.shared
    @StateObject private var model = MailModel()

    var body: some View {
        VStack(spacing: 0) {
            SkyTopBar(title: title, onBack: stepBack, help: .mail) {
                if listView && !model.selecting {
                    SquareBarButton(icon: MI.roundedContacts) { model.openContacts() }
                        .accessibilityLabel(Text("mail_contacts"))
                        .accessibilityIdentifier("mail-contacts")
                    SquareBarButton(icon: MI.roundedSettings) { model.openSettings() }
                        .accessibilityLabel(Text("mail_settings_title"))
                        .accessibilityIdentifier("mail-settings")
                }
            }
            ZStack(alignment: .bottomTrailing) {
                content.frame(maxWidth: .infinity, maxHeight: .infinity)
                if listView && !model.selecting && model.status?.running == true {
                    ExtendedFab(icon: MI.roundedEdit, label: Text("mail_write")) { model.compose() }
                        .padding(16)
                        .accessibilityIdentifier("mail-write")
                }
            }
        }
        .safeAreaInset(edge: .bottom, spacing: 0) {
            if listView && model.selecting { MailSelectionBar(model: model) }
        }
        .background(Color.skyBackground)
        .task(id: app.connected) { await model.run(app) }
        .onChange(of: router.pending) { link in
            if link != nil { model.pendingLinkArrived() }
        }
        .onChange(of: model.notice) { notice in
            guard let notice else { return }
            dialogs.snackbar(Text(verbatim: notice))
            model.notice = nil
        }
        .sheet(item: $model.share, onDismiss: nil) { file in
            ActivitySheet(items: [file.url]) { model.shareDone(file) }
        }
    }

    private var title: Text {
        if model.contactsOpen { return Text("mail_contacts") }
        if model.draft != nil { return Text("mail_compose_title") }
        if model.settingsOpen { return Text("mail_settings_title") }
        if model.selecting && model.open == nil { return Text(verbatim: L10n.format("mail_selected_count", model.selection.count)) }
        return Text("app_skymail")
    }

    private var listView: Bool { !model.inner && app.connected }

    private func stepBack() {
        if model.contactsOpen {
            model.closeContacts()
        } else if model.draft != nil {
            model.discardDraft()
        } else if model.settingsOpen {
            model.closeSettings()
        } else if model.open != nil {
            model.closeMessage()
        } else if model.selecting {
            model.clearSelection()
        } else {
            navigator.back()
        }
    }

    @ViewBuilder private var content: some View {
        if !app.connected {
            Text(app.coreState == .stopped ? L10n.key("socks_core_offline") : L10n.key("socks_core_starting"))
                .skyText(.bodyMedium)
                .foregroundStyle(Color.skyOnSurfaceVariant)
                .padding(24)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if model.contactsOpen {
            MailContactsView(model: model)
        } else if model.draft != nil {
            MailComposeView(model: model)
        } else if model.settingsOpen {
            MailSettingsView(model: model)
        } else if model.open != nil {
            MailMessageView(model: model)
        } else {
            MailFolderView(model: model)
        }
    }
}

// MARK: The folders

private struct MailFolderView: View {
    @ObservedObject var model: MailModel

    var body: some View {
        if let status = model.status {
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 8) {
                    if status.running {
                        AddressCard(address: model.myAddress)
                        SkySegmentedRow(items: [MailFolder.inbox, MailFolder.sent], selected: model.folder, label: { folder in
                            if folder == MailFolder.sent { return Text("mail_sent_folder") }
                            return status.unread > 0 ? Text(verbatim: L10n.format("mail_inbox_unread", status.unread)) : Text("mail_inbox")
                        }, onSelect: model.selectFolder)
                        .padding(.top, 4)
                        .accessibilityIdentifier("mail-folders")
                        rows
                    } else {
                        MailboxOffCard(status: status, busy: model.busy) { model.setEnabled(true) }
                    }
                }
                .padding(.horizontal, 16)
                .padding(.top, 4)
                .padding(.bottom, 96)
            }
        } else {
            MaterialSpinner().frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }

    @ViewBuilder private var rows: some View {
        let sent = model.folder == MailFolder.sent
        if let messages = model.messages {
            if messages.isEmpty {
                Text(sent ? L10n.key("mail_empty_sent") : L10n.key("mail_empty_inbox"))
                    .skyText(.bodyMedium)
                    .foregroundStyle(Color.skyOnSurfaceVariant)
                    .padding(.vertical, 24)
                    .padding(.horizontal, 4)
            } else {
                ForEach(messages) { message in
                    MailRow(message: message, sent: sent,
                            name: sent
                                ? MailText.displayAddress(message.to, contacts: model.contacts)
                                : MailText.displayAddress(message.from, contacts: model.contacts, pk: message.peerPK),
                            selecting: model.selecting,
                            selected: model.selection.contains(message.id),
                            onOpen: { model.open(model.folder, message) },
                            onToggle: { model.toggleSelect(message.id) })
                }
            }
        } else {
            MaterialSpinner().frame(maxWidth: .infinity).padding(24)
        }
    }
}

private struct AddressCard: View {
    let address: String?
    @EnvironmentObject private var dialogs: SkyDialogs

    var body: some View {
        SectionCard {
            Text("mail_your_address").skyText(.titleMedium)
            Text(verbatim: Format.breakable(address ?? ""))
                .skyText(.bodySmall, mono: true)
                .textSelection(.enabled)
                .padding(.top, 6)
                .accessibilityIdentifier("mail-address")
            Text("mail_address_hint").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 6)
            if let address {
                HStack(spacing: 8) {
                    Button {
                        UIPasteboard.general.string = address
                        dialogs.toast(Text("copied_to_clipboard"))
                    } label: {
                        Label { Text("mail_copy") } icon: { MaterialIcon(MI.roundedContentCopy, size: 18) }
                    }
                    .buttonStyle(.tonal)
                    ShareLink(item: address) {
                        Label { Text("mail_share") } icon: { MaterialIcon(MI.roundedShare, size: 18) }
                    }
                    .buttonStyle(.tonal)
                }
                .padding(.top, 8)
            }
        }
    }
}

private struct MailboxOffCard: View {
    let status: MailStatus
    let busy: Bool
    let turnOn: () -> Void

    var body: some View {
        SectionCard {
            Text("mail_off_title").skyText(.titleMedium)
            Group {
                if status.enabled && !status.reason.isEmpty {
                    Text(verbatim: L10n.format("mail_not_running", status.reason))
                } else {
                    Text("mail_off_body")
                }
            }
            .skyText(.bodyMedium)
            .foregroundStyle(Color.skyOnSurfaceVariant)
            .padding(.top, 6)
            if !status.enabled {
                Button(action: turnOn) { Text("mail_turn_on") }
                    .buttonStyle(.filled)
                    .disabled(busy)
                    .padding(.top, 10)
                    .accessibilityIdentifier("mail-turn-on")
            }
        }
    }
}

private struct MailRow: View {
    let message: MailSummary
    let sent: Bool
    let name: String
    let selecting: Bool
    let selected: Bool
    let onOpen: () -> Void
    let onToggle: () -> Void
    @State private var pressed = false

    var body: some View {
        let unread = !sent && !message.seen
        VStack(spacing: 0) {
            HStack(spacing: 0) {
                if selecting {
                    MailCheckbox(checked: selected)
                } else {
                    Circle().fill(unread ? Color.skyPrimary : Color.clear).frame(width: 10, height: 10)
                }
                VStack(alignment: .leading, spacing: 0) {
                    HStack(spacing: 6) {
                        Text(verbatim: name)
                            .skyText(.bodyLarge, bold: unread)
                            // Read mail steps back in colour as well as weight: weight
                            // alone reads as no difference at all on a phone.
                            .foregroundStyle(unread ? Color.skyOnSurface : Color.skyOnSurfaceVariant)
                            .lineLimit(1)
                        if !sent && !message.fromVerified {
                            MaterialIcon(MI.roundedWarning, size: 14)
                                .foregroundStyle(Color.skyError)
                                .accessibilityLabel(Text("mail_unverified"))
                        }
                    }
                    Group {
                        if message.subject.isEmpty { Text("mail_no_subject") } else { Text(verbatim: message.subject) }
                    }
                    .skyText(.bodyMedium, bold: unread)
                    .foregroundStyle(unread ? Color.skyOnSurface : Color.skyOnSurfaceVariant)
                    .lineLimit(1)
                }
                .padding(.leading, 10)
                .frame(maxWidth: .infinity, alignment: .leading)
                Text(verbatim: MailText.shortDate(message.date))
                    .skyText(.labelSmall)
                    .foregroundStyle(Color.skyOnSurfaceVariant)
                    .padding(.leading, 8)
            }
            .padding(.vertical, 8)
            .padding(.horizontal, 4)
            .contentShape(Rectangle())
            .overlay(Rectangle().fill(Color.skyOnSurface.opacity(pressed ? 0.10 : 0)))
            // A long press starts a batch and picks this message; taps pick too
            // while a batch is open.
            .onTapGesture { if selecting { onToggle() } else { onOpen() } }
            .onLongPressGesture(minimumDuration: 0.5) { onToggle() } onPressingChanged: { pressed = $0 }
            .accessibilityElement(children: .combine)
            .accessibilityAddTraits(selected ? [.isButton, .isSelected] : .isButton)
            .accessibilityAction { if selecting { onToggle() } else { onOpen() } }
            .accessibilityAction(named: Text("mail_select_ios")) { onToggle() }
            .accessibilityIdentifier("mail-row")
            SkyDivider()
        }
    }
}

/// Material 3's checkbox, 18 points in a 24 point box: a filled square with a tick when picked.
private struct MailCheckbox: View {
    let checked: Bool

    var body: some View {
        ZStack {
            RoundedRectangle(cornerRadius: 2)
                .strokeBorder(checked ? Color.skyPrimary : Color.skyOnSurfaceVariant, lineWidth: 2)
                .background(RoundedRectangle(cornerRadius: 2).fill(checked ? Color.skyPrimary : Color.clear))
                .frame(width: 18, height: 18)
            if checked {
                MaterialIcon(MI.filledCheck, size: 16).foregroundStyle(Color.skyOnPrimary)
            }
        }
        .frame(width: 24, height: 24)
        .accessibilityHidden(true)
    }
}

/// The batch actions, shown while messages are picked. The verbs wrap rather than truncate:
/// in Spanish and French the three of them outrun a phone's width in one row.
private struct MailSelectionBar: View {
    @ObservedObject var model: MailModel
    @EnvironmentObject private var dialogs: SkyDialogs

    var body: some View {
        let listed = model.messages?.count ?? 0
        VStack(spacing: 0) {
            SkyDivider()
            FlowLayout(spacing: 4, lineSpacing: 0, centered: true) {
                Button { model.selectAll() } label: { Text("mail_select_all") }
                    .buttonStyle(.skyText)
                    .disabled(model.selection.count >= listed)
                    .accessibilityIdentifier("mail-select-all")
                Button { model.invertSelection() } label: { Text("mail_invert_selection") }
                    .buttonStyle(.skyText)
                    .accessibilityIdentifier("mail-invert")
                Button { model.clearSelection() } label: { Text("mail_deselect_all") }
                    .buttonStyle(.skyText)
                    .accessibilityIdentifier("mail-deselect-all")
            }
            .padding(.horizontal, 8)
            .padding(.vertical, 2)
            Button { confirmDelete() } label: {
                Text(verbatim: L10n.format("mail_delete_selected", model.selection.count)).frame(maxWidth: .infinity)
            }
            .buttonStyle(.filled)
            .disabled(model.busy)
            .padding(.horizontal, 16)
            .padding(.vertical, 6)
            .accessibilityIdentifier("mail-delete-selected")
        }
        .background(Color.skyBackground)
    }

    private func confirmDelete() {
        dialogs.show(SkyDialog(message: Text(verbatim: L10n.format("mail_batch_delete_confirm", model.selection.count)), actions: [
            SkyDialog.Action(label: Text("cancel")),
            SkyDialog.Action(label: Text("mail_delete")) { model.deleteSelected() },
        ]))
    }
}

// MARK: A message

private struct MailMessageView: View {
    @ObservedObject var model: MailModel
    @EnvironmentObject private var dialogs: SkyDialogs

    var body: some View {
        if let open = model.open {
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    let subject = open.message?.subject ?? open.summary.subject
                    Group {
                        if subject.isEmpty { Text("mail_no_subject") } else { Text(verbatim: subject) }
                    }
                    .skyText(.titleLarge)
                    .foregroundStyle(Color.skyOnSurface)
                    .padding(.bottom, 10)
                    if let error = open.error {
                        Text(verbatim: error).skyText(.bodyMedium).foregroundStyle(Color.skyError)
                    } else if let message = open.message {
                        body(open, message)
                    } else {
                        MaterialSpinner()
                    }
                }
                .padding(16)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
    }

    @ViewBuilder private func body(_ open: OpenMail, _ message: MailMessage) -> some View {
        let inbox = open.folder == MailFolder.inbox
        HeaderLine(label: Text("mail_from"),
                   value: MailText.displayAddress(message.from, contacts: model.contacts, pk: inbox ? message.peerPK : ""),
                   raw: message.from)
        if inbox {
            VerifiedLine(verified: message.fromVerified, peerPK: message.peerPK)
        }
        HeaderLine(label: Text("mail_to"), value: message.to)
        if !message.cc.isEmpty {
            HeaderLine(label: Text("mail_cc"), value: message.cc)
        }
        HeaderLine(label: Text("mail_date"), value: message.date)
        SkyDivider().padding(.vertical, 12)
        if message.fromHTML {
            Text("mail_from_html").skyText(.labelSmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.bottom, 6)
        }
        Text(verbatim: message.text.trimmingTrailingWhitespace())
            .skyText(.bodyLarge)
            .foregroundStyle(Color.skyOnSurface)
            .textSelection(.enabled)
            .accessibilityIdentifier("mail-text")
        if !message.attachments.isEmpty {
            Text("mail_attachments").skyText(.titleSmall).foregroundStyle(Color.skyOnSurface).padding(.top, 16)
            ForEach(Array(message.attachments.enumerated()), id: \.offset) { index, attachment in
                HStack(spacing: 6) {
                    MaterialIcon(MI.roundedAttachFile, size: 18).foregroundStyle(Color.skyOnSurfaceVariant)
                    Text(verbatim: "\(attachment.name.isEmpty ? "attachment" : attachment.name) · \(MailModel.formatBytes(attachment.size))")
                        .skyText(.bodyMedium)
                        .foregroundStyle(Color.skyOnSurface)
                        .lineLimit(1)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    Button { model.saveAttachment(index) } label: {
                        Label { Text("mail_save") } icon: { MaterialIcon(MI.roundedDownload, size: 18) }
                    }
                    .buttonStyle(.skyText)
                    .accessibilityIdentifier("mail-save-attachment")
                }
            }
        }
        // Wraps: Reply, Save contact and Delete do not fit one line on a narrow phone.
        FlowLayout(spacing: 8) {
            if open.folder == MailFolder.inbox {
                Button { model.reply() } label: {
                    Label { Text("mail_reply") } icon: { MaterialIcon(MI.roundedReply, size: 18) }
                }
                .buttonStyle(.filled)
                .accessibilityIdentifier("mail-reply")
            }
            let party = Self.party(open, message)
            if SkymailAddress.pk(ofAddress: party.address) != nil {
                Button {
                    dialogs.showCustom {
                        MailContactDialog(title: Text("mail_save_contact"), name: party.name, address: party.address) { name, address in
                            model.saveContact(name: name, address: address)
                        }
                    }
                } label: {
                    Label { Text("mail_save_contact") } icon: { MaterialIcon(MI.roundedPersonAdd, size: 18) }
                }
                .buttonStyle(.outlined)
                .accessibilityIdentifier("mail-save-contact")
            }
            Button { confirmDelete() } label: {
                Label { Text("mail_delete") } icon: { MaterialIcon(MI.roundedDelete, size: 18) }
            }
            .buttonStyle(.outlined)
            .disabled(model.busy)
            .accessibilityIdentifier("mail-delete")
        }
        .padding(.top, 20)
    }

    /// Who Save contact saves: Inbox the sender, Sent the first recipient, with the name the
    /// mail gave them. An unverified From line names whoever typed it, so the key the transport
    /// proved wins there.
    static func party(_ open: OpenMail, _ message: MailMessage) -> (name: String, address: String) {
        let raw = open.folder == MailFolder.sent
            ? String(message.to.split(separator: ",", omittingEmptySubsequences: false).first ?? "")
            : message.from
        let written = MailText.bareAddress(raw)
        let name = raw.firstIndex(of: "<").map {
            raw[..<$0].trimmingCharacters(in: .whitespaces).trimmingCharacters(in: CharacterSet(charactersIn: "\""))
        } ?? ""
        let address = open.folder == MailFolder.sent || message.fromVerified ? written : (SkymailAddress.of(message.peerPK) ?? "")
        return (name, address)
    }

    private func confirmDelete() {
        dialogs.show(SkyDialog(message: Text("mail_delete_confirm"), actions: [
            SkyDialog.Action(label: Text("cancel")),
            SkyDialog.Action(label: Text("mail_delete")) { model.delete() },
        ]))
    }
}

/// One header; `raw`, when it says more than `value`, is shown under it as written.
private struct HeaderLine: View {
    let label: Text
    let value: String
    var raw: String?

    var body: some View {
        let differs = raw.map { $0.trimmingCharacters(in: .whitespacesAndNewlines) != value } ?? false
        HStack(alignment: .firstTextBaseline, spacing: 0) {
            label.skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).frame(width: 56, alignment: .leading)
            VStack(alignment: .leading, spacing: 0) {
                Text(verbatim: value).skyText(.bodySmall, bold: differs).foregroundStyle(Color.skyOnSurface)
                if differs, let raw {
                    Text(verbatim: raw).skyText(.labelSmall).foregroundStyle(Color.skyOnSurfaceVariant)
                }
            }
            .textSelection(.enabled)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(.vertical, 2)
    }
}

/// A contact's name and address, new or edited.
private struct MailContactDialog: View {
    let title: Text
    let name: String
    let address: String
    let save: (String, String) -> Void
    @State private var nameText = ""
    @State private var addressText = ""

    var body: some View {
        DialogFrame(title: title, confirm: L10n.key("mail_save"),
                    enabled: !nameText.trimmingCharacters(in: .whitespaces).isEmpty
                        && !addressText.trimmingCharacters(in: .whitespaces).isEmpty,
                    onConfirm: { save(nameText, addressText) }) {
            VStack(spacing: 8) {
                SkyOutlinedTextField(label: L10n.key("mail_contact_name"), text: $nameText, notch: .skyContainerHigh,
                                     identifier: "mail-contact-name", prose: true)
                SkyOutlinedTextField(label: L10n.key("mail_to_hint"), text: $addressText, notch: .skyContainerHigh,
                                     identifier: "mail-contact-address")
            }
        }
        .onAppear {
            nameText = name
            addressText = address
        }
    }
}

/// SkyMail's own contacts: add, edit and delete without a message to start from.
private struct MailContactsView: View {
    @ObservedObject var model: MailModel
    @EnvironmentObject private var dialogs: SkyDialogs

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 4) {
                Button {
                    dialogs.showCustom {
                        MailContactDialog(title: Text("mail_contact_add"), name: "", address: "") { name, address in
                            model.saveContact(name: name, address: address)
                        }
                    }
                } label: {
                    Label { Text("mail_contact_add") } icon: { MaterialIcon(MI.roundedPersonAdd, size: 18) }
                }
                .buttonStyle(.tonal)
                .accessibilityIdentifier("mail-contact-add")
                if model.contacts.isEmpty {
                    Text("mail_no_contacts").skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 12)
                }
                ForEach(model.contacts, id: \.address) { contact in
                    row(contact)
                    SkyDivider()
                }
            }
            .padding(16)
        }
    }

    private func row(_ contact: MailContact) -> some View {
        HStack(spacing: 0) {
            Text(verbatim: String(contact.name.trimmingCharacters(in: .whitespaces).prefix(1)).uppercased())
                .skyText(.titleMedium)
                .foregroundStyle(Color.skyOnPrimaryContainer)
                .frame(width: 36, height: 36)
                .background(Color.skyPrimaryContainer, in: Circle())
            // The name and the address must not read as two lines of one thing.
            VStack(alignment: .leading, spacing: 0) {
                Text(verbatim: contact.name).skyText(.titleMedium, bold: true).foregroundStyle(Color.skyOnSurface).lineLimit(1)
                HStack(spacing: 4) {
                    MaterialIcon(MI.roundedAlternateEmail, size: 14)
                    Text(verbatim: MailText.shortAddress(contact.address.trimmingCharacters(in: .whitespaces)))
                        .skyText(.bodySmall, mono: true)
                        .lineLimit(1)
                }
                .foregroundStyle(Color.skyOnSurfaceVariant)
            }
            .padding(.vertical, 8)
            .padding(.leading, 12)
            .frame(maxWidth: .infinity, alignment: .leading)
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("mail-contact")
            Button {
                dialogs.showCustom {
                    MailContactDialog(title: Text("mail_contact_edit"), name: contact.name, address: contact.address) { name, address in
                        model.saveContact(name: name, address: address, replacing: contact.address)
                    }
                }
            } label: {
                MaterialIcon(MI.roundedEdit).foregroundStyle(Color.skyOnSurfaceVariant).frame(width: 48, height: 48)
            }
            .accessibilityLabel(Text("mail_contact_edit"))
            .accessibilityIdentifier("mail-contact-edit")
            Button {
                dialogs.show(SkyDialog(message: Text(verbatim: L10n.format("mail_contact_delete_confirm", contact.name)), actions: [
                    SkyDialog.Action(label: Text("cancel")),
                    SkyDialog.Action(label: Text("mail_delete")) { model.deleteContact(contact.address) },
                ]))
            } label: {
                MaterialIcon(MI.roundedDelete).foregroundStyle(Color.skyOnSurfaceVariant).frame(width: 48, height: 48)
            }
            .accessibilityLabel(Text("mail_delete"))
            .accessibilityIdentifier("mail-contact-delete")
        }
    }
}

/// What the transport proved about the sender: the From line can be typed by anyone.
private struct VerifiedLine: View {
    let verified: Bool
    let peerPK: String

    /// The line's accessibility value, in any language: what the UI tests read.
    static let provedValue = "verified"
    static let unprovedValue = "unverified"

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 4) {
            MaterialIcon(verified ? MI.roundedCheckCircle : MI.roundedWarning, size: 14)
                .foregroundStyle(verified ? Color.skyPrimary : Color.skyError)
            Group {
                if verified {
                    Text("mail_verified_line")
                } else {
                    Text(verbatim: L10n.format("mail_unverified_line", MailText.shortPK(peerPK)))
                }
            }
            .skyText(.labelSmall)
            .foregroundStyle(verified ? Color.skyOnSurfaceVariant : Color.skyError)
        }
        .padding(.leading, 56)
        .padding(.bottom, 4)
        .accessibilityElement(children: .combine)
        .accessibilityValue(Text(verbatim: verified ? Self.provedValue : Self.unprovedValue))
        .accessibilityIdentifier("mail-verified")
    }
}

// MARK: A draft

private struct MailComposeView: View {
    @ObservedObject var model: MailModel
    @State private var showCc = false
    @State private var picking = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 10) {
                HStack(alignment: .top, spacing: 0) {
                    SkyOutlinedTextField(label: L10n.key("mail_to"), text: binding(\.to), identifier: "mail-to",
                                         supporting: Text("mail_to_hint"))
                    // The picker is the way out of copy-paste, so it stays reachable even with
                    // nothing to pick yet.
                    Menu { picker } label: {
                        MaterialIcon(MI.roundedContacts).foregroundStyle(Color.skyOnSurfaceVariant).frame(width: 48, height: 56)
                    }
                    .accessibilityLabel(Text("mail_contacts"))
                    .accessibilityIdentifier("mail-pick-contact")
                }
                if showCc || !(model.draft?.cc.isEmpty ?? true) {
                    SkyOutlinedTextField(label: L10n.key("mail_cc"), text: binding(\.cc), identifier: "mail-cc")
                } else {
                    Button { showCc = true } label: { Text("mail_add_cc") }.buttonStyle(.skyText)
                }
                // The suffix is the sender's choice of network, and they differ in more
                // than name: dmsg needs no route but relays through a dmsg server;
                // skynet is a direct route when one exists.
                VStack(alignment: .leading, spacing: 4) {
                    SkySegmentedRow(items: [false, true], selected: model.draft?.skynet ?? false,
                                    label: { Text(verbatim: String(($0 ? SkymailAddress.skynet : SkymailAddress.dmsg).dropFirst())) }, onSelect: model.setViaSkynet)
                        .accessibilityIdentifier("mail-network")
                    Text("mail_via_hint").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                }
                SkyOutlinedTextField(label: L10n.key("mail_subject"), text: binding(\.subject), identifier: "mail-subject", prose: true)
                SkyOutlinedTextField(label: L10n.key("mail_body"), text: binding(\.body), identifier: "mail-body", minLines: 6, prose: true)
                ForEach(Array((model.draft?.attachments ?? []).enumerated()), id: \.offset) { index, attachment in
                    HStack(spacing: 6) {
                        MaterialIcon(MI.roundedAttachFile, size: 18).foregroundStyle(Color.skyOnSurfaceVariant)
                        Text(verbatim: "\(attachment.name) · \(MailModel.formatBytes(Int64(attachment.bytes.count)))")
                            .skyText(.bodyMedium)
                            .lineLimit(1)
                            .frame(maxWidth: .infinity, alignment: .leading)
                        Button { model.removeAttachment(index) } label: {
                            MaterialIcon(MI.roundedClose).foregroundStyle(Color.skyOnSurfaceVariant).frame(width: 48, height: 48)
                        }
                        .accessibilityLabel(Text("mail_remove"))
                    }
                }
                Button { picking = true } label: {
                    Label { Text("mail_attach") } icon: { MaterialIcon(MI.roundedAttachFile, size: 18) }
                }
                .buttonStyle(.outlined)
                .disabled(model.sending)
                if let result = model.lastSend {
                    SendResultCard(result: result, contacts: model.contacts)
                }
                Text("mail_send_note").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                Button { model.send() } label: {
                    HStack(spacing: 8) {
                        if model.sending {
                            MaterialSpinner(size: 18, stroke: 2)
                            Text("mail_sending")
                        } else {
                            MaterialIcon(MI.roundedSend, size: 18)
                            Text("mail_send")
                        }
                    }
                    .frame(maxWidth: .infinity)
                }
                .buttonStyle(.filled)
                .disabled(model.sending)
                .accessibilityIdentifier("mail-send")
            }
            .padding(16)
        }
        .fileImporter(isPresented: $picking, allowedContentTypes: [.item], allowsMultipleSelection: true) { result in
            if case let .success(urls) = result { model.addAttachments(urls) }
        }
    }

    /// SkyMail's contacts, then SkyChat's names for keys not saved here, then the way to edit them.
    @ViewBuilder private var picker: some View {
        ForEach(model.contacts, id: \.address) { contact in
            Button { model.addRecipient(address: contact.address) } label: { Text(verbatim: contact.name) }
        }
        let saved = Set(model.contacts.compactMap { SkymailAddress.pk(ofAddress: $0.address)?.lowercased() })
        let chat = VoiceCalls.shared.addressBook
            .filter { !saved.contains($0.key.lowercased()) }
            .sorted { $0.value.lowercased() < $1.value.lowercased() }
        if !chat.isEmpty {
            Section(L10n.text("mail_contacts_from_skychat")) {
                ForEach(chat, id: \.key) { pk, name in
                    if let address = SkymailAddress.of(pk) {
                        Button { model.addRecipient(address: address) } label: { Text(verbatim: name) }
                    }
                }
            }
        }
        if model.contacts.isEmpty && chat.isEmpty {
            Text("mail_no_contacts")
        }
        Divider()
        Button { model.openContacts() } label: { Text("mail_contacts_manage") }
    }

    private func binding(_ field: WritableKeyPath<MailDraft, String>) -> Binding<String> {
        Binding(get: { model.draft?[keyPath: field] ?? "" }, set: { model.draft?[keyPath: field] = $0 })
    }
}

private struct SendResultCard: View {
    let result: MailSendResult
    let contacts: [MailContact]

    var body: some View {
        SectionCard {
            Text("mail_result_title").skyText(.titleSmall)
            ForEach(Array(result.recipients.enumerated()), id: \.offset) { _, recipient in
                Text(verbatim: MailText.displayAddress(recipient.rcpt, contacts: contacts)).skyText(.bodyMedium).padding(.top, 6)
                Text(verbatim: recipient.error.isEmpty
                    ? L10n.format("mail_result_delivered", recipient.via)
                    : L10n.format("mail_result_failed", recipient.error))
                    .skyText(.bodySmall)
                    .foregroundStyle(recipient.error.isEmpty ? Color.skyPrimary : Color.skyError)
            }
            if result.recipients.isEmpty && !result.error.isEmpty {
                Text(verbatim: result.error).skyText(.bodySmall).foregroundStyle(Color.skyError).padding(.top, 6)
            }
        }
        .accessibilityIdentifier("mail-send-result")
    }
}

// MARK: Settings

private struct MailSettingsView: View {
    @ObservedObject var model: MailModel
    @State private var entry = ""
    @State private var entryBad = false

    var body: some View {
        if let status = model.status {
            ScrollView {
                VStack(alignment: .leading, spacing: 12) {
                    SectionCard {
                        HStack(spacing: 12) {
                            VStack(alignment: .leading, spacing: 0) {
                                Text("mail_setting_on").skyText(.titleMedium)
                                Text("mail_setting_on_desc").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                            }
                            .frame(maxWidth: .infinity, alignment: .leading)
                            Toggle(isOn: Binding(get: { status.enabled }, set: { model.setEnabled($0) })) { Text("mail_setting_on") }
                                .toggleStyle(.skySwitch)
                                .labelsHidden()
                                .disabled(model.busy)
                                .accessibilityIdentifier("mail-enabled")
                        }
                        if status.enabled && !status.running && !status.reason.isEmpty {
                            Text(verbatim: L10n.format("mail_not_running", status.reason))
                                .skyText(.bodySmall).foregroundStyle(Color.skyError).padding(.top, 6)
                        }
                    }
                    // The visor counts usage only while the mailbox runs.
                    if status.running {
                        SectionCard {
                            let total = status.limits.maxTotalSize
                            Text(verbatim: L10n.format("mail_usage", MailModel.formatBytes(status.usage), MailModel.formatBytes(total)))
                                .skyText(.bodyMedium)
                            if total > 0 {
                                ProgressBar(value: Double(status.usage) / Double(total)).padding(.top, 6)
                            }
                            Text(verbatim: L10n.format("mail_limits", MailModel.formatBytes(status.limits.maxMessageSize), status.limits.maxAgeDays))
                                .skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 6)
                        }
                    }
                    allowed(status)
                }
                .padding(16)
            }
        }
    }

    private func allowed(_ status: MailStatus) -> some View {
        SectionCard {
            Text("mail_allowed_title").skyText(.titleMedium)
            Text(status.whitelist.isEmpty ? L10n.key("mail_allowed_everyone") : L10n.key("mail_allowed_only"))
                .skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 4)
            ForEach(status.whitelist, id: \.self) { pk in
                HStack(spacing: 0) {
                    Text(verbatim: VoiceCalls.shared.displayName(pk)).skyText(.bodyMedium)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    Button { model.removeSender(pk) } label: { Text("mail_remove") }
                        .buttonStyle(.skyText)
                        .disabled(model.busy)
                }
            }
            SkyOutlinedTextField(label: L10n.key("mail_allow_hint"), text: $entry, isError: entryBad,
                                 notch: .skySurfaceVariant, identifier: "mail-allow-entry",
                                 supporting: entryBad ? Text("mail_allow_bad") : nil)
                .padding(.top, 6)
                .onChange(of: entry) { _ in entryBad = false }
            Button {
                if model.allowSender(entry) { entry = "" } else { entryBad = true }
            } label: { Text("mail_allow_add") }
            .buttonStyle(.skyText)
            .disabled(entry.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || model.busy)
            .accessibilityIdentifier("mail-allow-add")
        }
    }
}

// MARK: Helpers

enum MailText {
    /// What to call a mail party: the user's own name for the key (SkyMail, then SkyChat) before
    /// the name the mail carries, else the address with its key cut. `pk` is the key the transport
    /// proved, which a From line cannot forge.
    @MainActor
    static func displayAddress(_ raw: String, contacts: [MailContact] = [], pk: String = "") -> String {
        let text = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        let address = bareAddress(text)
        if let key = pk.isEmpty ? SkymailAddress.pk(ofAddress: address) : pk {
            if let contact = contacts.first(where: { SkymailAddress.pk(ofAddress: $0.address)?.lowercased() == key.lowercased() }) {
                return contact.name
            }
            if let name = VoiceCalls.shared.addressBook[key.lowercased()] ?? VoiceCalls.shared.addressBook[key], !name.isEmpty {
                return name
            }
        }
        if let open = text.firstIndex(of: "<") {
            let name = text[..<open].trimmingCharacters(in: .whitespaces).trimmingCharacters(in: CharacterSet(charactersIn: "\""))
            if !name.isEmpty { return name }
        }
        return shortAddress(address)
    }

    /// The address inside `Name <address>`, or the text itself.
    static func bareAddress(_ raw: String) -> String {
        var address = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        if let open = address.firstIndex(of: "<") {
            address = String(address[address.index(after: open)...])
            if let close = address.firstIndex(of: ">") { address = String(address[..<close]) }
        }
        return address.trimmingCharacters(in: .whitespaces)
    }

    /// The address itself with its key cut, never a name for it.
    static func shortAddress(_ address: String) -> String {
        guard let at = address.firstIndex(of: "@") else { return address }
        let local = address[..<at]
        let domain = address[address.index(after: at)...]
        if domain.count <= 16 { return address }
        let suffix = domain.lastIndex(of: ".").map { String(domain[$0...]) } ?? ""
        return "\(local)@\(domain.prefix(8))…\(suffix)"
    }

    /// The day a message was dated, in the app's language; empty for Go's zero time (no Date header).
    static func shortDate(_ rfc3339: String) -> String {
        guard let date = parse(rfc3339), Calendar(identifier: .gregorian).component(.year, from: date) >= 1970 else { return "" }
        let formatter = DateFormatter()
        formatter.locale = L10n.locale
        formatter.dateStyle = .short
        formatter.timeStyle = .none
        return formatter.string(from: date)
    }

    /// `03ab…c9d1`, as a call names a peer.
    static func shortPK(_ pk: String) -> String {
        pk.count <= 12 ? pk : "\(pk.prefix(6))…\(pk.suffix(4))"
    }

    private static func parse(_ text: String) -> Date? {
        let fractional = ISO8601DateFormatter()
        fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return fractional.date(from: text) ?? ISO8601DateFormatter().date(from: text)
    }
}

/// The system share sheet over SwiftUI, for a file to save or send on.
struct ActivitySheet: UIViewControllerRepresentable {
    let items: [Any]
    let done: () -> Void

    func makeUIViewController(context: Context) -> UIActivityViewController {
        let sheet = UIActivityViewController(activityItems: items, applicationActivities: nil)
        sheet.completionWithItemsHandler = { _, _, _, _ in done() }
        return sheet
    }

    func updateUIViewController(_ controller: UIActivityViewController, context: Context) {}
}
