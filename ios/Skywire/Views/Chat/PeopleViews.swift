import CoreClient
import SwiftUI

/// The address book: browse opens a chat, pick hands the key to Add by address
/// (Android: ui/chat/PeopleScreens.kt ContactsScreen).
struct ContactsView: View {
    let pick: Bool
    let onBack: () -> Void
    @EnvironmentObject private var repo: ChatRepository
    @EnvironmentObject private var nav: ChatNav
    @EnvironmentObject private var dialogs: SkyDialogs
    @ObservedObject private var store = ChatRepository.shared.store
    @State private var query = ""

    var body: some View {
        let q = query.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        let list = store.state.contacts
            .filter { q.isEmpty || $0.contains(q) || (repo.names[$0]?.lowercased().contains(q) ?? false) }
            .sorted { a, b in
                let (na, nb) = (repo.names[a] ?? "", repo.names[b] ?? "")
                if na.isEmpty != nb.isEmpty { return !na.isEmpty }
                return (na.isEmpty ? a : na).lowercased() < (nb.isEmpty ? b : nb).lowercased()
            }
        VStack(spacing: 0) {
            SkyTopBar(title: Text(pick ? L10n.key("chat_choose_contact") : L10n.key("chat_address_book")), onBack: onBack) {
                if !pick {
                    SquareBarButton(icon: MI.roundedPersonAdd) { nav.push(.newContact("")) }
                        .accessibilityLabel(Text("chat_add_contact"))
                        .accessibilityIdentifier("contacts-add")
                }
            }
            ChatSearchField(placeholder: L10n.text("chat_search_contacts"), text: $query)
            if list.isEmpty {
                ChatHint(text: store.state.contacts.isEmpty ? L10n.text("chat_no_contacts") : L10n.text("chat_no_contact_match"))
            }
            ScrollView {
                LazyVStack(spacing: 0) {
                    ForEach(list, id: \.self) { pk in row(pk) }
                }
            }
        }
        .background(Color.skyBackground)
    }

    private func row(_ pk: String) -> some View {
        let label = repo.names[pk].flatMap { $0.isEmpty ? nil : $0 } ?? Skychat.abbrevPk(pk)
        return HStack(spacing: 12) {
            Button {
                if pick {
                    nav.replace(.addAddress("skychat://\(pk)", fromLink: false))
                } else {
                    repo.ensureRecipient(pk)
                    nav.openThread(pk)
                }
            } label: {
                HStack(spacing: 12) {
                    ChatAvatar(key: pk, label: label, size: 44, version: store.avatarVersion, online: repo.presence[pk]?.online, load: { @MainActor [store] in store.avatar(pk) })
                    VStack(alignment: .leading, spacing: 0) {
                        Text(verbatim: label).skyText(.titleSmall).foregroundStyle(Color.skyOnSurface).lineLimit(1)
                        Text(verbatim: Skychat.abbrevPk(pk)).skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                    }
                    Spacer(minLength: 0)
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            if !pick {
                Button {
                    repo.ensureRecipient(pk)
                    nav.push(.contactSettings(pk))
                } label: { MaterialIcon(MI.roundedEdit).foregroundStyle(Color.skyOnSurfaceVariant).frame(width: 44, height: 44) }
                .accessibilityLabel(Text("chat_edit_contact"))
                Button {
                    dialogs.confirm(L10n.text("chat_confirm_remove_contact"), confirm: L10n.text("chat_remove")) { repo.removeContact(pk) }
                } label: { MaterialIcon(MI.roundedClose).foregroundStyle(Color.skyOnSurfaceVariant).frame(width: 44, height: 44) }
                .accessibilityLabel(Text("chat_remove_contact"))
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
        .accessibilityIdentifier("contact-\(pk)")
    }
}

/// Per-peer nickname and local picture (index.html Contact Settings).
struct ContactSettingsView: View {
    let pk: String
    let onBack: () -> Void
    @EnvironmentObject private var repo: ChatRepository
    @ObservedObject private var store = ChatRepository.shared.store
    @State private var name = ""
    @State private var offered: Data?
    @State private var asking = false

    var body: some View {
        VStack(spacing: 0) {
            SkyTopBar(title: Text("chat_contact_settings"), onBack: onBack)
            ScrollView {
                VStack(spacing: 12) {
                    ChatAvatar(key: pk, label: name, size: 112, version: store.avatarVersion + (offered?.count ?? 0),
                               load: { @MainActor [store, offered, pk] in offered ?? store.avatar(pk) })
                    Text(verbatim: pk).skyText(.bodySmall, mono: true).foregroundStyle(Color.skyOnSurface).textSelection(.enabled)
                    SkyOutlinedTextField(label: L10n.key("chat_contact_name"), text: Binding(
                        get: { name }, set: { name = String($0.prefix(Skychat.nameMax)) }
                    ), identifier: "contact-name", prose: true)
                    ChatHelp(text: L10n.text("chat_contact_name_help"))
                    buttons
                }
                .padding(20)
            }
        }
        .background(Color.skyBackground)
        .onAppear { name = repo.names[pk] ?? "" }
    }

    private var buttons: some View {
        VStack(alignment: .leading, spacing: 8) {
            Button { saveName() } label: { Text("chat_save_name").frame(maxWidth: .infinity) }
                .buttonStyle(.filled)
                .accessibilityIdentifier("contact-save")
            Button { Task { await askProfile() } } label: {
                HStack(spacing: 8) {
                    if asking { MaterialSpinner(size: 16, stroke: 2) }
                    Text("chat_get_published")
                }
                .frame(maxWidth: .infinity)
            }
            .buttonStyle(.outlined)
            .disabled(asking)
            AvatarPickerButton(target: .contact, onPicked: { bytes in
                store.setAvatar(pk, bytes)
                repo.note(L10n.text("chat_avatar_updated"))
            }) {
                Text("chat_change_avatar").skyText(.labelLarge).foregroundStyle(Color.skyPrimary)
                    .frame(maxWidth: .infinity).frame(height: 40)
                    .overlay(Capsule().stroke(Color.skyOutline, lineWidth: 1))
            }
            Button {
                if !store.hasAvatar(pk) {
                    repo.note(L10n.text("chat_no_custom_avatar"), error: true)
                } else {
                    store.setAvatar(pk, nil)
                    repo.note(L10n.text("chat_avatar_removed"))
                }
            } label: { Text("chat_remove_avatar").frame(maxWidth: .infinity) }
            .buttonStyle(.outlined)
            if repo.isContact(pk) {
                Button { repo.removeContact(pk) } label: { Text("chat_remove_from_contacts").foregroundStyle(Color.skyError) }
                    .buttonStyle(.skyText)
            } else {
                Button {
                    repo.addContact(pk)
                    repo.note(L10n.text("chat_saved_to_contacts"))
                } label: { Text("chat_save_to_contacts") }
                .buttonStyle(.skyText)
            }
        }
    }

    private func saveName() {
        let had = repo.names[pk] ?? ""
        repo.setName(pk, name)
        let picture = offered
        if let picture {
            store.setAvatar(pk, picture)
            offered = nil
        }
        let trimmed = name.trimmingCharacters(in: .whitespaces)
        if !trimmed.isEmpty && picture != nil {
            repo.note(L10n.format("chat_saved_as_with_picture", trimmed))
        } else if trimmed.isEmpty && picture != nil {
            repo.note(L10n.text("chat_picture_saved"))
        } else if !trimmed.isEmpty {
            repo.note(L10n.format("chat_saved_as", trimmed))
        } else if !had.isEmpty {
            repo.note(L10n.text("chat_name_cleared"))
        } else {
            repo.note(L10n.text("chat_enter_name_first"))
        }
    }

    private func askProfile() async {
        asking = true
        repo.note(L10n.text("chat_asking_profile"))
        let p = await repo.lookupPeer(pk)
        asking = false
        guard let p, !p.unreachable else {
            repo.note(L10n.text("chat_profile_unreachable"), error: true)
            return
        }
        if p.name.isEmpty && p.avatar == nil {
            repo.note(L10n.text("chat_profile_nothing"), error: true)
            return
        }
        if !p.name.isEmpty { name = p.name }
        offered = p.avatar
        if !p.name.isEmpty && p.avatar != nil {
            repo.note(L10n.format("chat_profile_offer_both", p.name))
        } else if !p.name.isEmpty {
            repo.note(L10n.format("chat_profile_offer_name", p.name))
        } else {
            repo.note(L10n.text("chat_profile_offer_picture"))
        }
    }
}

/// Adds someone by key; their published name and picture are offered (index.html New contact).
struct NewContactView: View {
    let prefill: String
    let onBack: () -> Void
    @EnvironmentObject private var repo: ChatRepository
    @State private var raw = ""
    @State private var name = ""
    @State private var chosen: Data?
    @State private var lookup: PeerLookup?
    @State private var looking = false
    @State private var retry = 0

    private struct LookupKey: Equatable {
        let pk: String
        let retry: Int
    }

    var body: some View {
        let pk = Skychat.keyFrom(raw)
        let already = !pk.isEmpty && repo.isContact(pk)
        VStack(spacing: 0) {
            SkyTopBar(title: Text("chat_new_contact"), onBack: onBack)
            ScrollView {
                VStack(alignment: .leading, spacing: 12) {
                    SkyOutlinedTextField(label: L10n.key("chat_their_key"), text: $raw, identifier: "new-contact-key", minLines: 2)
                    status(pk: pk, already: already)
                    HStack(spacing: 12) {
                        ChatAvatar(key: pk.isEmpty ? "new" : pk, label: name, size: 64, version: chosen?.count ?? 0, load: { @MainActor [chosen] in chosen })
                        AvatarPickerButton(target: .contact, onPicked: { chosen = $0 }) {
                            Text("chat_choose_picture").skyText(.labelLarge).foregroundStyle(Color.skyPrimary)
                                .padding(.horizontal, 16).frame(height: 40)
                                .overlay(Capsule().stroke(Color.skyOutline, lineWidth: 1))
                        }
                    }
                    SkyOutlinedTextField(label: L10n.key("chat_nickname"), text: Binding(
                        get: { name }, set: { name = String($0.prefix(Skychat.nameMax)) }
                    ), identifier: "new-contact-name", prose: true)
                    ChatHelp(text: L10n.text("chat_new_contact_help"))
                    Button { save(pk: pk, already: already) } label: { Text("chat_save_contact").frame(maxWidth: .infinity) }
                        .buttonStyle(.filled)
                        .accessibilityIdentifier("new-contact-save")
                }
                .padding(20)
            }
        }
        .background(Color.skyBackground)
        .onAppear { if raw.isEmpty { raw = prefill } }
        .task(id: LookupKey(pk: pk, retry: retry)) {
            lookup = nil
            guard !pk.isEmpty, !already else { return }
            looking = true
            try? await Task.sleep(for: .milliseconds(350))
            if Task.isCancelled { return }
            let p = await repo.lookupPeer(pk)
            looking = false
            lookup = p
            if let p, !p.unreachable {
                if name.isEmpty && !p.name.isEmpty { name = p.name }
                if chosen == nil { chosen = p.avatar }
            }
        }
    }

    @ViewBuilder private func status(pk: String, already: Bool) -> some View {
        if pk.isEmpty {
            EmptyView()
        } else if already {
            Text("chat_already_in_book").skyText(.bodySmall).foregroundStyle(Color.skyError)
        } else if looking {
            HStack(spacing: 8) {
                MaterialSpinner(size: 16, stroke: 2)
                Text("chat_looking_up").skyText(.bodySmall).foregroundStyle(Color.skyOnSurface)
            }
        } else if let lookup {
            if lookup.unreachable {
                HStack {
                    Text("chat_lookup_unreachable").skyText(.bodySmall).foregroundStyle(Color.skyOnSurface)
                    Spacer(minLength: 0)
                    Button { retry += 1 } label: { Text("chat_try_again") }.buttonStyle(.skyText)
                }
            } else if lookup.name.isEmpty && lookup.avatar == nil {
                Text("chat_publishes_nothing").skyText(.bodySmall).foregroundStyle(Color.skyOnSurface)
            } else {
                Text(lookup.name.isEmpty ? L10n.key("chat_picture_no_name") : L10n.key("chat_name_published")).skyText(.bodySmall).foregroundStyle(Color.skyOnSurface)
            }
        }
    }

    private func save(pk: String, already: Bool) {
        let published = lookup?.name ?? ""
        if pk.isEmpty {
            repo.note(L10n.text("chat_enter_valid_key"), error: true)
        } else if already {
            repo.note(L10n.text("chat_key_already_saved"), error: true)
        } else if name.trimmingCharacters(in: .whitespaces).isEmpty && published.isEmpty {
            repo.note(L10n.text("chat_give_nickname"), error: true)
        } else {
            let final = name.trimmingCharacters(in: .whitespaces).isEmpty ? published : name
            repo.setName(pk, final)
            repo.addContact(pk, name: final, avatar: chosen)
            repo.note(L10n.format("chat_saved_to_contacts_named", final))
            onBack()
        }
    }
}

/// What this visor publishes about itself (index.html "Your profile").
struct MyProfileView: View {
    let onBack: () -> Void
    @EnvironmentObject private var repo: ChatRepository
    @EnvironmentObject private var dialogs: SkyDialogs
    @State private var name = ""
    @State private var pending: Data?
    @State private var busy = false

    var body: some View {
        let current = repo.profile?.avatar ?? ""
        VStack(spacing: 0) {
            SkyTopBar(title: Text("chat_your_profile"), onBack: onBack)
            ScrollView {
                VStack(spacing: 12) {
                    ChatAvatar(key: repo.myPk.isEmpty ? "me" : repo.myPk, label: name, size: 112, version: (pending?.count ?? 0) + current.hashValue,
                               load: { @MainActor [pending] in pending ?? DataURL.decode(current) })
                    Text(verbatim: repo.myAddress.isEmpty ? "—" : repo.myAddress).skyText(.bodySmall, mono: true).foregroundStyle(Color.skyOnSurface).textSelection(.enabled)
                    if repo.profileAvailable == false {
                        Text("chat_no_publish").skyText(.bodyMedium).foregroundStyle(Color.skyOnSurface)
                    } else {
                        form(current)
                    }
                }
                .padding(20)
            }
        }
        .background(Color.skyBackground)
        .onAppear { name = repo.profile?.name ?? "" }
        .onChange(of: repo.profile?.name ?? "") { name = $0 }
    }

    @ViewBuilder private func form(_ current: String) -> some View {
        SkyOutlinedTextField(label: L10n.key("chat_display_name"), text: Binding(
            get: { name }, set: { name = String($0.prefix(Skychat.nameMax)) }
        ), identifier: "profile-name", prose: true)
        ChatHelp(text: L10n.text("chat_profile_help"))
        Button {
            busy = true
            Task {
                if await repo.saveProfile(name: name, avatar: pending) { pending = nil }
                busy = false
            }
        } label: { Text("chat_save").frame(maxWidth: .infinity) }
        .buttonStyle(.filled)
        .disabled(busy)
        .accessibilityIdentifier("profile-save")
        AvatarPickerButton(target: .me, onPicked: { bytes in
            pending = bytes
            repo.note(L10n.text("chat_press_save"))
        }) {
            Text("chat_change_picture").skyText(.labelLarge).foregroundStyle(Color.skyPrimary)
                .frame(maxWidth: .infinity).frame(height: 40)
                .overlay(Capsule().stroke(Color.skyOutline, lineWidth: 1))
        }
        if !current.isEmpty {
            Button {
                busy = true
                Task {
                    _ = await repo.removeProfilePicture()
                    busy = false
                }
            } label: { Text("chat_remove_picture").frame(maxWidth: .infinity) }
            .buttonStyle(.outlined)
            .disabled(busy)
        }
        Button {
            dialogs.confirm(L10n.text("chat_confirm_stop_publishing"), confirm: L10n.text("chat_stop_publishing")) { repo.clearProfile() }
        } label: { Text("chat_stop_publishing").foregroundStyle(Color.skyError) }
        .buttonStyle(.skyText)
    }
}
