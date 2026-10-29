import CoreClient
import SwiftUI

/// The conversation list: the "me" row, the tabs, and every section the page
/// had (Android: ui/chat/ChatHome.kt).
struct ChatHome: View {
    let onBack: () -> Void
    @EnvironmentObject private var repo: ChatRepository
    @EnvironmentObject private var nav: ChatNav
    @EnvironmentObject private var dialogs: SkyDialogs
    @ObservedObject private var store = ChatRepository.shared.store

    private static let tabs = ["all", "dm", "groups", "channels", "calls"]

    private var tab: String { Self.tabs.contains(store.state.tab) ? store.state.tab : "all" }

    var body: some View {
        VStack(spacing: 0) {
            SkyTopBar(
                title: Text("app_skychat"),
                subtitle: Text(repo.sseUp ? L10n.key("chat_connected") : L10n.key("chat_reconnecting")),
                onBack: onBack, help: .chat
            ) {
                SquareBarButton(icon: MI.roundedSearch) { nav.push(.search) }
                    .accessibilityLabel(Text("chat_search"))
                    .accessibilityIdentifier("chat-search")
                Menu {
                    Button {
                        let address = repo.myAddress
                        if !address.isEmpty {
                            dialogs.showQr(ChatQrSpec(address: address, title: L10n.text("chat_your_address"), help: L10n.text("chat_qr_help_me")), repo: repo)
                        }
                    } label: { Label(L10n.text("chat_your_address"), image: MI.roundedQrCode2) }
                    Button { nav.push(.contacts(pick: false)) } label: { Label(L10n.text("chat_address_book"), image: MI.roundedContacts) }
                    Button { nav.push(.settings) } label: { Label(L10n.text("chat_settings"), image: MI.roundedSettings) }
                } label: {
                    MaterialIcon(MI.roundedMoreVert)
                        .foregroundStyle(Color.skyOnSecondaryContainer)
                        .frame(width: 42, height: 42)
                        .background(Color.skySecondaryContainer, in: .sky(SkyRadius.medium))
                }
                .accessibilityLabel(Text("chat_actions"))
                .accessibilityIdentifier("chat-more")
            }
            ZStack(alignment: .bottomTrailing) {
                ScrollViewReader { proxy in
                    ScrollView {
                        LazyVStack(spacing: 0) {
                            Color.clear.frame(height: 0).id("top")
                            MeRow(name: repo.profile?.name ?? "", pk: repo.myPk, avatar: repo.profile?.avatar ?? "") { nav.push(.myProfile) }
                            ScrollView(.horizontal, showsIndicators: false) {
                                HStack(spacing: 6) {
                                    ForEach(Self.tabs, id: \.self) { t in
                                        SkyFilterChip(label: Text(verbatim: Self.tabLabel(t)), selected: tab == t) { repo.setTab(t) }
                                            .accessibilityIdentifier("chat-tab-\(t)")
                                    }
                                }
                                .padding(.horizontal, 12)
                            }
                            if tab == "calls" {
                                CallsList()
                            } else {
                                rows
                            }
                        }
                        .padding(.bottom, 96)
                    }
                    .onChange(of: tab) { _ in proxy.scrollTo("top", anchor: .top) }
                }
                StartMenu()
                    .padding(16)
            }
        }
        .background(Color.skyBackground)
    }

    static func tabLabel(_ tab: String) -> String {
        switch tab {
        case "dm": L10n.text("chat_tab_dms")
        case "groups": L10n.text("chat_groups")
        case "channels": L10n.text("chat_channels")
        case "calls": L10n.text("chat_tab_calls")
        default: L10n.text("chat_tab_all")
        }
    }

    private func fits(_ group: GroupInfo?) -> Bool {
        switch tab {
        case "all": true
        case "dm": group == nil
        case "groups": group.map { !$0.isChannel } ?? false
        case "channels": group?.isChannel == true
        default: false
        }
    }

    private func last(_ key: String) -> Int64 { repo.summaries[key]?.last?.ts ?? 0 }

    @ViewBuilder private var rows: some View {
        let local = store.state
        let requests = Set(repo.privacy?.requests ?? [])
        let dmKeys = local.recipients.filter { $0 != repo.myPk }
        let sortedDms = dmKeys.sorted { last($0) > last($1) }
        let pinnedGroup = local.pinned.flatMap { pin in pin.hasPrefix("grp:") ? repo.groups.first { $0.id == String(pin.dropFirst(4)) } : nil }
        let pinnedDm = local.pinned.flatMap { $0.hasPrefix("dm:") ? String($0.dropFirst(3)) : nil }
        let showPinned = pinnedGroup.map(fits) ?? (pinnedDm != nil && (tab == "all" || tab == "dm"))
        let dmTabs = tab == "all" || tab == "dm"
        let nothing = dmKeys.isEmpty && repo.groups.isEmpty && repo.summaries[Skychat.savedKey]?.last == nil

        if dmTabs {
            let saved = repo.summaries[Skychat.savedKey]
            ConversationRow(
                title: L10n.text("chat_saved_messages"),
                preview: saved?.last.map { ChatText.preview($0, own: false) } ?? L10n.text("chat_saved_sub"),
                time: saved?.last?.ts,
                avatar: AnyView(ChatAvatar(key: Skychat.savedKey, label: "", size: 48, kind: .saved))
            ) { nav.openThread(Skychat.savedKey) }
                .accessibilityIdentifier("chat-row-saved")
        }
        if showPinned {
            ChatSectionTitle(text: L10n.text("chat_pinned"))
            if let g = pinnedGroup {
                GroupRow(g: g, pinned: true)
            } else if let pk = pinnedDm {
                DmRow(pk: pk, pinned: true)
            }
        }
        if dmTabs && !repo.pair.invites.isEmpty {
            ChatSectionTitle(text: L10n.text("chat_pair_invites"))
            ForEach(repo.pair.invites, id: \.self) { pk in
                InviteRow(pk: pk)
            }
        }
        if tab == "all" || tab == "groups" {
            let list = repo.groups.filter { !$0.isChannel && $0.id != pinnedGroup?.id }.sorted { last($0.id) > last($1.id) }
            if !list.isEmpty {
                ChatSectionTitle(text: L10n.text("chat_groups"))
                ForEach(list) { GroupRow(g: $0) }
            }
        }
        if tab == "all" || tab == "channels" {
            let list = repo.groups.filter { $0.isChannel && $0.id != pinnedGroup?.id }.sorted { last($0.id) > last($1.id) }
            if !list.isEmpty {
                ChatSectionTitle(text: L10n.text("chat_channels"))
                ForEach(list) { GroupRow(g: $0) }
            }
        }
        if dmTabs {
            let pending = sortedDms.filter { requests.contains($0) }
            if !pending.isEmpty {
                ChatSectionTitle(text: L10n.text("chat_requests"))
                ForEach(pending, id: \.self) { DmRow(pk: $0) }
            }
            let chats = sortedDms.filter { !requests.contains($0) && $0 != pinnedDm }
            if !chats.isEmpty {
                ChatSectionTitle(text: L10n.text("chat_chats"))
                ForEach(chats, id: \.self) { DmRow(pk: $0) }
            }
        }
        if nothing {
            VStack(spacing: 8) {
                Text("chat_welcome").skyText(.titleMedium).foregroundStyle(Color.skyOnSurface)
                Text("chat_welcome_body").skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant).multilineTextAlignment(.center)
            }
            .padding(32)
        }
    }
}

/// The + button's menu: every way to start a conversation.
private struct StartMenu: View {
    @EnvironmentObject private var nav: ChatNav

    var body: some View {
        Menu {
            Button { nav.push(.addAddress("", fromLink: false)) } label: {
                Label(L10n.text("chat_add_by_address"), image: MI.roundedPersonAdd)
            }
            Button { nav.push(.contacts(pick: true)) } label: {
                Label(L10n.text("chat_pick_contact"), image: MI.roundedContacts)
            }
            Button { nav.push(.newGroup(channel: false)) } label: {
                Label(L10n.text("chat_new_group"), image: MI.roundedGroupAdd)
            }
            Button { nav.push(.newGroup(channel: true)) } label: {
                Label(L10n.text("chat_new_channel"), image: MI.roundedCampaign)
            }
        } label: {
            MaterialIcon(MI.roundedAdd)
                .foregroundStyle(Color.skyOnPrimaryContainer)
                .frame(width: 56, height: 56)
                .background(Color.skyPrimaryContainer, in: .sky(SkyRadius.medium))
                .skyElevation(6)
        }
        .accessibilityLabel(Text("chat_start_menu"))
        .accessibilityIdentifier("chat-start")
    }
}

private struct MeRow: View {
    let name: String
    let pk: String
    let avatar: String
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 12) {
                ChatAvatar(key: pk.isEmpty ? "me" : pk, label: name, size: 40, version: avatar.hashValue, load: avatar.isEmpty ? nil : { @MainActor in DataURL.decode(avatar) })
                VStack(alignment: .leading, spacing: 0) {
                    Text(verbatim: name.isEmpty ? (pk.isEmpty ? L10n.text("chat_your_profile") : L10n.text("chat_set_your_name")) : name)
                        .skyText(.titleSmall).foregroundStyle(Color.skyOnSurface).lineLimit(1)
                    Text(verbatim: pk.isEmpty ? "—" : Skychat.abbrevPk(pk)).skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                }
                Spacer(minLength: 0)
                MaterialIcon(MI.roundedChevronRight).foregroundStyle(Color.skyOnSurfaceVariant)
            }
            .padding(12)
            .background(Color.skyContainerHigh, in: .sky(SkyRadius.medium))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .padding(.horizontal, 12)
        .padding(.vertical, 4)
        .accessibilityIdentifier("chat-me")
    }
}

/// One row: avatar, title, preview, time and badges.
struct ConversationRow: View {
    let title: String
    let preview: String
    let time: Int64?
    let avatar: AnyView
    var unread = 0
    var muted = false
    var pinned = false
    var badge: String?
    var pending = 0
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 12) {
                avatar
                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 4) {
                        if pinned { MaterialIcon(MI.roundedPushPin, size: 14).foregroundStyle(Color.skyOnSurfaceVariant) }
                        Text(verbatim: title).skyText(.titleSmall).foregroundStyle(Color.skyOnSurface).lineLimit(1)
                        if let badge {
                            Text(verbatim: badge).skyText(.labelSmall).foregroundStyle(Color.skyOnPrimaryContainer)
                                .padding(.horizontal, 5).background(Color.skyPrimaryContainer, in: .sky(6))
                        }
                        if muted { MaterialIcon(MI.roundedNotificationsOff, size: 14).foregroundStyle(Color.skyOnSurfaceVariant) }
                        Spacer(minLength: 4)
                        if let time, time > 0 {
                            Text(verbatim: ChatText.listTime(time)).skyText(.labelSmall).foregroundStyle(Color.skyOnSurfaceVariant)
                        }
                    }
                    HStack(spacing: 6) {
                        Text(verbatim: preview).skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant).lineLimit(1)
                        Spacer(minLength: 0)
                        if pending > 0 { CountBadge(n: pending, color: Color.skyWarning) }
                        if unread > 0 { CountBadge(n: unread, color: muted ? Color.skyOutline : Color.skyPrimary) }
                    }
                }
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 10)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }
}

private struct CountBadge: View {
    let n: Int
    let color: Color

    var body: some View {
        Text(verbatim: n > 999 ? "999+" : String(n))
            .skyText(.labelSmall)
            .foregroundStyle(.white)
            .padding(.horizontal, 7)
            .background(color, in: Capsule())
    }
}

private struct DmRow: View {
    let pk: String
    var pinned = false
    @EnvironmentObject private var repo: ChatRepository
    @EnvironmentObject private var nav: ChatNav
    @ObservedObject private var store = ChatRepository.shared.store

    var body: some View {
        let label = repo.names[pk].flatMap { $0.isEmpty ? nil : $0 } ?? Skychat.abbrevPk(pk)
        let summary = repo.summaries[pk]
        ConversationRow(
            title: label,
            preview: summary?.last.map { ChatText.preview($0, own: true) } ?? (pinned ? L10n.text("chat_pinned") : L10n.text("chat_new_conversation")),
            time: summary?.last?.ts,
            avatar: AnyView(ChatAvatar(key: pk, label: label, size: 48, version: store.avatarVersion, online: repo.presence[pk]?.online, load: { @MainActor [store] in store.avatar(pk) })),
            unread: summary?.unread ?? 0,
            muted: store.notif.isMuted(pk),
            pinned: pinned,
            badge: repo.pair.paired.contains(pk) ? "CXO" : nil
        ) { nav.openThread(pk) }
            .accessibilityIdentifier("chat-row-\(pk)")
    }
}

private struct GroupRow: View {
    let g: GroupInfo
    var pinned = false
    @EnvironmentObject private var repo: ChatRepository
    @EnvironmentObject private var nav: ChatNav
    @ObservedObject private var store = ChatRepository.shared.store

    var body: some View {
        let summary = repo.summaries[g.id]
        ConversationRow(
            title: g.name.isEmpty ? g.id : g.name,
            preview: summary?.last.map { ChatText.preview($0, own: true, group: true, sender: repo.displayName($0.from)) } ?? ChatText.groupSubtitle(g),
            time: summary?.last?.ts,
            avatar: AnyView(ChatAvatar(
                key: g.id, label: g.name, size: 48, kind: g.isChannel ? .channel : .group, version: g.avatar.hashValue,
                load: g.avatar.isEmpty ? nil : { @MainActor [avatar = g.avatar] in DataURL.decode(avatar) }
            )),
            unread: summary?.unread ?? 0,
            muted: store.notif.isMuted(g.id),
            pinned: pinned,
            pending: g.pendingJoins
        ) { nav.openThread(g.id) }
            .accessibilityIdentifier("chat-row-\(g.id)")
    }
}

private struct InviteRow: View {
    let pk: String
    @EnvironmentObject private var repo: ChatRepository

    var body: some View {
        HStack(spacing: 12) {
            ChatAvatar(key: pk, label: "", size: 40)
            Text(verbatim: String(pk.prefix(8)) + "…" + String(pk.suffix(8))).skyText(.bodyMedium).foregroundStyle(Color.skyOnSurface)
            Spacer(minLength: 0)
            Button { repo.acceptInvite(pk) } label: { MaterialIcon(MI.roundedCheck).foregroundStyle(Color.skyPrimary).frame(width: 44, height: 44) }
                .accessibilityLabel(Text("chat_accept_invite"))
            Button { repo.declineInvite(pk) } label: { MaterialIcon(MI.roundedClose).foregroundStyle(Color.skyOnSurfaceVariant).frame(width: 44, height: 44) }
                .accessibilityLabel(Text("chat_decline_invite"))
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 6)
    }
}

private struct CallsList: View {
    @EnvironmentObject private var repo: ChatRepository
    @State private var calls: [CallRecord]?
    @State private var failed = false

    var body: some View {
        Group {
            if failed {
                ChatHint(text: L10n.text("chat_calls_failed"))
            } else if let calls {
                if calls.isEmpty {
                    ChatHint(text: L10n.text("chat_no_calls"))
                } else {
                    ForEach(Array(calls.enumerated()), id: \.offset) { _, c in row(c) }
                }
            }
        }
        .task(id: repo.names) {
            let list = await repo.callLog()
            failed = list == nil
            calls = list
        }
    }

    private func row(_ c: CallRecord) -> some View {
        let missed = c.outcome == "Missed call"
        let peer = c.peer.lowercased()
        let name = repo.displayName(peer)
        var parts = [ChatText.outcomeLabel(c.outcome)]
        if let at = Skychat.parseTime(c.at) { parts.append(ChatText.listTime(at)) }
        if c.seconds > 0 {
            parts.append(c.seconds < 60 ? L10n.format("chat_secs", String(c.seconds)) : L10n.format("chat_mins_secs", String(c.seconds / 60), String(c.seconds % 60)))
        }
        return Button { repo.placeCall(peer) } label: {
            HStack(spacing: 12) {
                ChatAvatar(key: peer, label: name, size: 44, load: { @MainActor [store = repo.store] in store.avatar(peer) })
                VStack(alignment: .leading, spacing: 0) {
                    Text(verbatim: name).skyText(.titleSmall).foregroundStyle(Color.skyOnSurface).lineLimit(1)
                    HStack(spacing: 4) {
                        MaterialIcon(c.direction == "out" ? MI.roundedCallMade : MI.roundedCallReceived, size: 14)
                        Text(verbatim: parts.joined(separator: " · ")).skyText(.bodySmall)
                    }
                    .foregroundStyle(missed ? Color.skyError : Color.skyOnSurfaceVariant)
                }
                Spacer(minLength: 0)
                MaterialIcon(MI.roundedCall).foregroundStyle(Color.skyPrimary)
                    .accessibilityLabel(Text(verbatim: L10n.format("chat_call_name", name)))
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 10)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }
}
