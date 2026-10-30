import CoreClient
import CoreImage
import PhotosUI
import SwiftUI

/// One field for every way of naming something; resolve first, then offer the
/// one action that applies (Android: ui/chat/FinderScreens.kt AddAddressScreen).
struct AddAddressView: View {
    let prefill: String
    let fromLink: Bool
    let onBack: () -> Void
    @EnvironmentObject private var repo: ChatRepository
    @EnvironmentObject private var nav: ChatNav
    @ObservedObject private var store = ChatRepository.shared.store
    @State private var raw: String
    @State private var state = AddrState.empty
    @State private var now: Int
    @State private var profile: PeerLookup?
    @State private var catalog: Catalog?
    @State private var save = true
    @State private var working = false
    @State private var scanning = false
    @State private var photo: PhotosPickerItem?

    private enum AddrState: Equatable {
        case empty, pending
        case error(String)
        case done(Resolved)
    }

    private struct LookupKey: Equatable {
        let raw: String
        let now: Int
    }

    init(prefill: String, fromLink: Bool, onBack: @escaping () -> Void) {
        self.prefill = prefill
        self.fromLink = fromLink
        self.onBack = onBack
        _raw = State(initialValue: prefill)
        _now = State(initialValue: prefill.isEmpty ? 0 : 1)
    }

    var body: some View {
        VStack(spacing: 0) {
            SkyTopBar(title: Text("chat_add_by_address"), onBack: onBack)
            ScrollView {
                VStack(alignment: .leading, spacing: 8) {
                    SkyOutlinedTextField(placeholder: L10n.key("chat_address_placeholder"), text: $raw, identifier: "address-field", minLines: 2)
                    HStack(spacing: 8) {
                        if QRScannerSheet.available {
                            Button { scanning = true } label: {
                                Label(L10n.text("chat_scan_qr"), image: MI.roundedQrCodeScanner)
                            }
                            .buttonStyle(.outlined)
                        }
                        PhotosPicker(selection: $photo, matching: .images) {
                            Label(L10n.text("chat_from_image"), image: MI.roundedImage)
                        }
                        .buttonStyle(.outlined)
                    }
                    content.padding(.top, 8)
                }
                .padding(20)
            }
        }
        .background(Color.skyBackground)
        .task(id: LookupKey(raw: raw, now: now)) { await lookup() }
        .sheet(isPresented: $scanning) {
            QRScannerSheet { text in
                scanning = false
                scanned(text)
            }
            .ignoresSafeArea()
        }
        .onChange(of: photo) { item in
            guard let item else { return }
            photo = nil
            Task {
                let text: String
                if let data = try? await item.loadTransferable(type: Data.self), let image = CIImage(data: data) {
                    text = await Task.detached { QrDecoder.decode(image) }.value
                } else {
                    text = ""
                }
                if text.isEmpty {
                    repo.note(L10n.text("chat_no_qr_in_image"), error: true)
                } else {
                    scanned(text)
                }
            }
        }
    }

    private func scanned(_ text: String) {
        raw = text.trimmingCharacters(in: .whitespacesAndNewlines)
        now += 1
        repo.note(L10n.text("chat_code_scanned"))
    }

    private func lookup() async {
        profile = nil
        catalog = nil
        let text = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else {
            state = .empty
            return
        }
        let pk = Skychat.keyFrom(text)
        if !pk.isEmpty {
            state = .done(Resolved(target: "dm", pk: pk, address: "skychat://\(pk)"))
        } else {
            if now == 0 {
                try? await Task.sleep(for: .milliseconds(350))
                if Task.isCancelled { return }
            }
            state = .pending
            do {
                state = .done(try await repo.api.resolve(text))
            } catch is CancellationError {
                return
            } catch {
                let message = repo.errorText(error)
                state = .error(message.isEmpty ? L10n.text("chat_could_not_resolve") : message)
            }
        }
        now = 0
        guard case let .done(res) = state, res.target == "dm" else { return }
        save = !repo.isContact(res.pk)
        async let found = repo.lookupPeer(res.pk)
        async let listed = try? repo.api.catalog(pk: res.pk)
        let peer = await found
        profile = peer.flatMap { !$0.unreachable && (!$0.name.isEmpty || $0.avatar != nil) ? $0 : nil }
        catalog = await listed
    }

    @ViewBuilder private var content: some View {
        switch state {
        case .empty:
            ChatHelp(text: L10n.text("chat_address_hint"))
        case .pending:
            ResultCard(key: nil, title: L10n.text("chat_looking_it_up"), sub: L10n.text("chat_asking_host"), loading: true)
        case let .error(text):
            ResultCard(key: nil, title: L10n.text("chat_not_resolved"), sub: text, bad: true)
            ChatHelp(text: L10n.text("chat_address_offline_hint"))
        case let .done(res):
            if res.target == "dm" { person(res) } else { group(res) }
        }
    }

    @ViewBuilder private func person(_ res: Resolved) -> some View {
        let nick = repo.names[res.pk]
        let published = profile?.name ?? ""
        let title = nick ?? (published.isEmpty ? Skychat.abbrevPk(res.pk) : published)
        let sub = !published.isEmpty && nick != nil
            ? L10n.format("chat_person_publishes", published)
            : (!published.isEmpty ? L10n.text("chat_person_published") : L10n.text("chat_person_dm"))
        ResultCard(key: res.pk, title: title, sub: sub, avatar: profile?.avatar)
        ChatHelp(text: L10n.text("chat_person_hint"))
        if !repo.isContact(res.pk) {
            ChatCheckRow(label: L10n.text("chat_save_to_contacts"), checked: save) { save = $0 }
        }
        Button {
            if save && !repo.isContact(res.pk) {
                repo.addContact(res.pk, name: profile?.name, avatar: profile?.avatar)
                repo.note(L10n.text("chat_saved_to_contacts"))
            }
            repo.ensureRecipient(res.pk)
            nav.openThread(res.pk)
        } label: {
            Text(store.state.recipients.contains(res.pk) ? L10n.key("chat_open_chat") : L10n.key("chat_start_chat")).frame(maxWidth: .infinity)
        }
        .buttonStyle(.filled)
        .accessibilityIdentifier("address-start")
        .padding(.top, 8)
        if let catalog, !catalog.entries.isEmpty {
            Text(verbatim: L10n.text("chat_catalog_title") + (catalog.truncated ? " " + L10n.text("chat_catalog_truncated") : ""))
                .skyText(.titleSmall).foregroundStyle(Color.skyOnSurface).padding(.top, 16)
            ForEach(catalog.entries) { e in
                let joined = repo.groups.contains { $0.id == e.id }
                let meta = Self.catalogMeta(e)
                Button { raw = e.address } label: {
                    HStack(spacing: 10) {
                        ChatAvatar(key: e.id, label: e.name, size: 36, kind: e.kind == "channel" ? .channel : .group)
                        VStack(alignment: .leading, spacing: 0) {
                            Text(verbatim: e.name.isEmpty ? e.id : e.name).skyText(.bodyLarge).foregroundStyle(Color.skyOnSurface).lineLimit(1)
                            Text(verbatim: meta.joined(separator: " · ")).skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                        }
                        Spacer(minLength: 0)
                        Text(joined ? L10n.key("chat_catalog_open") : L10n.key("chat_catalog_view")).skyText(.labelLarge).foregroundStyle(Color.skyPrimary)
                    }
                    .padding(.vertical, 8)
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
            }
        }
    }

    private static func catalogMeta(_ e: CatalogEntry) -> [String] {
        let channel = e.kind == "channel"
        let approval = e.policy == "approval"
        var meta = [
            channel ? L10n.text("chat_noun_channel") : L10n.text("chat_noun_group"),
            approval ? L10n.text("chat_approval_needed") : L10n.text("chat_open_to_join"),
        ]
        if !e.priceHint.isEmpty { meta.append(L10n.format("chat_price_claimed", e.priceHint)) }
        return meta
    }

    @ViewBuilder private func group(_ res: Resolved) -> some View {
        let g = res.group
        let channel = g?.kind == "channel"
        let noun = channel ? L10n.text("chat_noun_channel") : L10n.text("chat_noun_group")
        let approval = g?.policy == "approval"
        let waiting = res.status == "awaiting_approval"
        let denied = res.status == "denied"
        let sub: String = {
            if g?.banned == true { return noun + " · " + L10n.text("chat_addr_banned") }
            if res.joined { return noun + " · " + L10n.text("chat_addr_joined") }
            if waiting { return noun + " · " + L10n.text("chat_addr_waiting") }
            if denied { return noun + " · " + L10n.text("chat_addr_denied") }
            let policy = approval ? L10n.text("chat_approval_needed") : L10n.text("chat_open_to_join")
            let privacy: String
            if channel {
                privacy = L10n.text("chat_only_admins_post")
            } else if g?.mode == "private" {
                privacy = L10n.text("chat_encrypted")
            } else {
                privacy = L10n.text("chat_not_encrypted")
            }
            return [noun, policy, privacy].joined(separator: " · ")
        }()
        let name = g.map { $0.name.isEmpty ? $0.id : $0.name } ?? L10n.text("chat_unnamed")
        ResultCard(key: g?.id ?? "", title: name, sub: sub, bad: g?.banned == true, group: true, channel: channel)
        if let price = g?.priceHint, !price.isEmpty {
            Text(verbatim: L10n.format("chat_price_hint", price)).skyText(.bodySmall).foregroundStyle(Color.skyOnSurface)
        } else if channel {
            ChatHelp(text: L10n.text("chat_channel_join_hint"))
        } else if g?.policy == "approval" {
            ChatHelp(text: L10n.text("chat_approval_join_hint"))
        } else {
            ChatHelp(text: L10n.text("chat_open_join_hint"))
        }
        if let g, !g.banned, res.status != "awaiting_approval" {
            let label: String = {
                if res.joined { return channel ? L10n.text("chat_open_channel") : L10n.text("chat_open_group") }
                if channel { return L10n.text("chat_join_channel") }
                return approval ? L10n.text("chat_send_request") : L10n.text("chat_join_group")
            }()
            Button {
                if res.joined {
                    nav.openThread(g.id)
                    return
                }
                working = true
                Task {
                    let info = await repo.join(res)
                    working = false
                    if let info {
                        nav.openThread(info.id.isEmpty ? g.id : info.id)
                    } else if repo.groupInfo(g.id) != nil {
                        onBack()
                    }
                }
            } label: {
                HStack(spacing: 8) {
                    if working { MaterialSpinner(size: 16, stroke: 2, color: .skyOnPrimary) }
                    Text(verbatim: working ? L10n.text("chat_working") : label)
                }
                .frame(maxWidth: .infinity)
            }
            .buttonStyle(.filled)
            .disabled(working)
            .accessibilityIdentifier("address-join")
            .padding(.top, 8)
        }
    }
}

private struct ResultCard: View {
    let key: String?
    let title: String
    let sub: String
    var loading = false
    var bad = false
    var group = false
    var channel = false
    var avatar: Data?

    var body: some View {
        HStack(spacing: 12) {
            if loading {
                MaterialSpinner(size: 40)
            } else if let key {
                ChatAvatar(key: key.isEmpty ? title : key, label: title, size: 44, kind: channel ? .channel : (group ? .group : .person),
                           version: avatar?.count ?? 0, load: avatar.map { bytes in { @MainActor in bytes } })
            } else {
                Text(verbatim: "!").skyText(.titleMedium).foregroundStyle(Color.skyError)
                    .frame(width: 44, height: 44).background(Color.skyErrorContainer, in: Circle())
            }
            VStack(alignment: .leading, spacing: 0) {
                Text(verbatim: title).skyText(.titleSmall).foregroundStyle(Color.skyOnSurface).lineLimit(1)
                Text(verbatim: sub).skyText(.bodySmall).foregroundStyle(bad ? Color.skyError : Color.skyOnSurfaceVariant)
            }
            Spacer(minLength: 0)
        }
        .padding(12)
        .background(Color.skyContainerHigh, in: .sky(SkyRadius.medium))
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("address-result")
    }
}

/// People, groups, channels and messages in one field (index.html openSearch).
struct ChatSearchView: View {
    let onBack: () -> Void
    @EnvironmentObject private var repo: ChatRepository
    @EnvironmentObject private var nav: ChatNav
    @ObservedObject private var store = ChatRepository.shared.store
    @State private var query = ""
    @State private var hits: [ChatSearchResult]?

    var body: some View {
        let q = query.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        VStack(spacing: 0) {
            SkyTopBar(title: Text("chat_search"), onBack: onBack)
            ChatSearchField(placeholder: L10n.text("chat_search_placeholder"), text: $query, focus: true)
            if q.isEmpty {
                ChatHint(text: L10n.text("chat_search_empty"))
                Spacer()
            } else {
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 0) { results(q) }
                }
            }
        }
        .background(Color.skyBackground)
        .task(id: query) {
            hits = nil
            guard !q.isEmpty else { return }
            try? await Task.sleep(for: .milliseconds(250))
            if Task.isCancelled { return }
            hits = await repo.searchMessages(query)
        }
    }

    @ViewBuilder private func results(_ q: String) -> some View {
        let local = store.state
        var seen = Set<String>()
        let people = (local.recipients + local.contacts).filter { seen.insert($0).inserted }.filter { pk in
            pk.contains(q) || (repo.names[pk] ?? Skychat.abbrevPk(pk)).lowercased().contains(q)
        }
        let groups = repo.groups.filter { $0.name.lowercased().contains(q) || $0.id.lowercased().contains(q) }
        if !people.isEmpty {
            ChatSectionTitle(text: L10n.text("chat_contacts"))
            ForEach(people, id: \.self) { pk in
                ChatSimpleRow(key: pk, title: repo.names[pk] ?? Skychat.abbrevPk(pk), sub: Skychat.abbrevPk(pk), kind: .person, load: { @MainActor [store] in store.avatar(pk) }) {
                    repo.ensureRecipient(pk)
                    nav.openThread(pk)
                }
            }
        }
        ForEach([false, true], id: \.self) { channel in
            let list = groups.filter { $0.isChannel == channel }
            if !list.isEmpty {
                ChatSectionTitle(text: channel ? L10n.text("chat_channels") : L10n.text("chat_groups"))
                ForEach(list) { g in
                    ChatSimpleRow(key: g.id, title: g.name.isEmpty ? g.id : g.name, sub: ChatText.groupSubtitle(g), kind: channel ? .channel : .group) { nav.openThread(g.id) }
                }
            }
        }
        ChatSectionTitle(text: L10n.text("chat_messages_header"))
        if let hits {
            if hits.isEmpty && people.isEmpty && groups.isEmpty {
                ChatHint(text: L10n.text("chat_nothing_found"))
            } else {
                ForEach(hits) { hit in hitRow(hit, q) }
            }
        } else {
            ChatHint(text: L10n.text("chat_searching"))
        }
    }

    private func hitRow(_ hit: ChatSearchResult, _ q: String) -> some View {
        let title: String
        if hit.key == Skychat.savedKey {
            title = L10n.text("chat_saved_messages")
        } else if hit.isGroup {
            title = repo.groupInfo(hit.key)?.name ?? hit.key
        } else {
            title = repo.displayName(hit.key)
        }
        let prefix: String
        if hit.from == Skychat.me || hit.from == repo.myPk {
            prefix = L10n.text("chat_you_prefix") + " "
        } else if hit.isGroup {
            prefix = repo.displayName(hit.from) + ": "
        } else {
            prefix = ""
        }
        return Button {
            if !hit.isGroup && hit.key != Skychat.savedKey { repo.ensureRecipient(hit.key) }
            nav.scrollTarget = (hit.key, hit.ts)
            nav.openThread(hit.key)
        } label: {
            VStack(alignment: .leading, spacing: 2) {
                HStack {
                    Text(verbatim: title).skyText(.titleSmall).foregroundStyle(Color.skyOnSurface).lineLimit(1)
                    Spacer(minLength: 4)
                    Text(verbatim: ChatText.listTime(hit.ts)).skyText(.labelSmall).foregroundStyle(Color.skyOnSurfaceVariant)
                }
                Self.snippet(prefix, ChatText.shown(hit.text), q).skyText(.bodySmall).foregroundStyle(Color.skyOnSurface).lineLimit(2)
            }
            .padding(.horizontal, 20)
            .padding(.vertical, 8)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }

    private static func snippet(_ prefix: String, _ text: String, _ q: String) -> Text {
        guard let range = text.lowercased().range(of: q) else { return Text(verbatim: prefix + String(text.prefix(90))) }
        let lower = text.lowercased()
        let at = lower.distance(from: lower.startIndex, to: range.lowerBound)
        let start = max(0, at - 30)
        let chars = Array(text)
        let end = min(chars.count, at + q.count + 60)
        let before = (start > 0 ? "…" : "") + String(chars[start..<at])
        let match = String(chars[at..<min(chars.count, at + q.count)])
        let after = String(chars[min(chars.count, at + q.count)..<end]) + (end < chars.count ? "…" : "")
        return Text(verbatim: prefix + before) + Text(verbatim: match).bold().foregroundColor(.skyPrimary) + Text(verbatim: after)
    }
}

/// Every destination in one list; picking one sends at once (index.html forward modal).
struct ForwardView: View {
    let fromKey: String
    let msgKey: String
    let onBack: () -> Void
    @EnvironmentObject private var repo: ChatRepository
    @EnvironmentObject private var nav: ChatNav
    @ObservedObject private var store = ChatRepository.shared.store
    @State private var query = ""

    var body: some View {
        let q = query.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        let match = { (label: String, id: String) in q.isEmpty || label.lowercased().contains(q) || id.lowercased().contains(q) }
        let local = store.state
        let chats = local.recipients.filter { match(repo.names[$0] ?? Skychat.abbrevPk($0), $0) }
        let book = local.contacts.filter { !local.recipients.contains($0) && match(repo.names[$0] ?? Skychat.abbrevPk($0), $0) }
        let groups = repo.groups.filter { $0.postable && !$0.readOnly && ($0.status.isEmpty || $0.status == "active") && match($0.name, $0.id) }
        let saved = match(L10n.text("chat_saved_messages"), Skychat.savedKey)
        let origin = repo.groupInfo(fromKey).flatMap { $0.isChannel ? $0.name : nil }
        VStack(spacing: 0) {
            SkyTopBar(title: Text("chat_forward_to"), onBack: onBack)
            Text(verbatim: origin.map { L10n.format("chat_forward_origin_channel", $0) } ?? L10n.text("chat_forward_origin"))
                .skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                .frame(maxWidth: .infinity, alignment: .leading).padding(.horizontal, 20)
            ChatSearchField(placeholder: L10n.text("chat_forward_search"), text: $query)
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0) {
                    if saved {
                        ChatSimpleRow(key: Skychat.savedKey, title: L10n.text("chat_saved_messages"), sub: L10n.text("chat_notes_on_device"), kind: .saved) { pick(Skychat.savedKey) }
                    }
                    if !chats.isEmpty {
                        ChatSectionTitle(text: L10n.text("chat_chats"))
                        ForEach(chats, id: \.self) { pk in person(pk) }
                    }
                    if !book.isEmpty {
                        ChatSectionTitle(text: L10n.text("chat_address_book"))
                        ForEach(book, id: \.self) { pk in person(pk) }
                    }
                    if !groups.isEmpty {
                        ChatSectionTitle(text: L10n.text("chat_groups_and_channels"))
                        ForEach(groups) { g in
                            ChatSimpleRow(key: g.id, title: g.name.isEmpty ? g.id : g.name, sub: "", kind: g.isChannel ? .channel : .group) { pick(g.id) }
                        }
                    }
                    if !saved && chats.isEmpty && book.isEmpty && groups.isEmpty {
                        ChatHint(text: L10n.text("chat_nothing_matches"))
                    }
                }
            }
        }
        .background(Color.skyBackground)
    }

    private func person(_ pk: String) -> some View {
        let name = repo.names[pk]
        return ChatSimpleRow(key: pk, title: name ?? Skychat.abbrevPk(pk), sub: name != nil ? Skychat.abbrevPk(pk) : "", kind: .person, load: { @MainActor [store] in store.avatar(pk) }) {
            pick(pk)
        }
    }

    private func pick(_ to: String) {
        guard let m = repo.findMessage(fromKey, msgKey) else { return }
        repo.forward(from: fromKey, m, to: to)
        if !Skychat.isGroupKey(to) && to != Skychat.savedKey { repo.ensureRecipient(to) }
        nav.openThread(to)
    }
}
