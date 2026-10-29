import CoreClient
import SwiftUI

/// One entry of the newest-first list: a message or the day line above a run of them.
private enum ThreadRow: Identifiable {
    case message(ChatMessage)
    case day(Date)

    var id: String {
        switch self {
        case let .message(m): m.key
        case let .day(d): "day-\(d.timeIntervalSince1970)"
        }
    }
}

private struct BottomOffsetKey: PreferenceKey {
    static let defaultValue: CGFloat = 0
    static func reduce(value: inout CGFloat, nextValue: () -> CGFloat) { value = nextValue() }
}

/// A conversation (Android: ui/chat/ThreadScreen.kt). The list is drawn
/// upside down so it opens at the newest message and grows from the bottom.
struct ThreadView: View {
    let key: String
    let onBack: () -> Void
    @EnvironmentObject private var repo: ChatRepository
    @EnvironmentObject private var nav: ChatNav
    @EnvironmentObject private var dialogs: SkyDialogs
    @ObservedObject private var store = ChatRepository.shared.store
    @ObservedObject private var thread: ChatRepository.Thread
    @State private var highlight: String?
    @State private var atBottom = true
    @State private var lastNewest: String?

    init(key: String, onBack: @escaping () -> Void) {
        self.key = key
        self.onBack = onBack
        _thread = ObservedObject(wrappedValue: ChatRepository.shared.thread(key))
    }

    private var saved: Bool { key == Skychat.savedKey }
    private var isGroup: Bool { !saved && Skychat.isGroupKey(key) }
    private var group: GroupInfo? { repo.groupInfo(key) }
    private var paired: Bool { !isGroup && !saved && repo.pair.paired.contains(key) }

    private var title: String {
        if saved { return L10n.text("chat_saved_messages") }
        if let group { return group.name.isEmpty ? group.id : group.name }
        if isGroup { return key }
        return repo.names[key].flatMap { $0.isEmpty ? nil : $0 } ?? Skychat.abbrevPk(key)
    }

    var body: some View {
        let messages = thread.messages
        let rows = Self.rows(messages)
        VStack(spacing: 0) {
            header
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(spacing: 2) {
                        GeometryReader { geo in
                            Color.clear.preference(key: BottomOffsetKey.self, value: geo.frame(in: .named("thread")).minY)
                        }
                        .frame(height: 0)
                        ForEach(rows) { row in
                            rowView(row).scaleEffect(x: 1, y: -1).id(row.id)
                        }
                        if isGroup {
                            Color.clear.frame(height: 1).onAppear { repo.loadOlder(key) }
                            if repo.hist[key]?.loading == true {
                                Text("chat_loading_earlier").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                                    .frame(maxWidth: .infinity).padding(12).scaleEffect(x: 1, y: -1)
                            }
                        }
                    }
                    .padding(.vertical, 8)
                }
                .coordinateSpace(name: "thread")
                .scaleEffect(x: 1, y: -1)
                .scrollDismissesKeyboard(.interactively)
                .onPreferenceChange(BottomOffsetKey.self) { offset in
                    let bottom = offset > -80
                    if bottom != atBottom {
                        atBottom = bottom
                        repo.setAtBottom(key, bottom)
                    }
                }
                .onChange(of: messages.last?.key) { newest in
                    guard let newest, newest != lastNewest, let last = messages.last else { return }
                    lastNewest = newest
                    if atBottom || last.mine {
                        withAnimation { proxy.scrollTo(newest, anchor: .top) }
                    }
                    if atBottom { repo.markRead(key) }
                }
                .task(id: scrollKey(rows.count)) {
                    guard let target = nav.scrollTarget, target.key == key else { return }
                    for _ in 0..<20 {
                        if let hit = thread.messages.first(where: { abs($0.ts - target.ts) < 2000 }) {
                            nav.scrollTarget = nil
                            await jump(to: hit.key, proxy)
                            return
                        }
                        try? await Task.sleep(for: .milliseconds(150))
                    }
                    nav.scrollTarget = nil
                }
                .environment(\.quoteJump) { reply in
                    guard let ts = Skychat.parseTime(reply.ts), let hit = thread.messages.first(where: { abs($0.ts - ts) < 1000 }) else { return }
                    Task { await jump(to: hit.key, proxy) }
                }
            }
            .frame(maxHeight: .infinity)
            bars(messages)
        }
        .background(Color.skyBackground)
        .onAppear { lastNewest = messages.last?.key }
    }

    private struct ScrollKey: Equatable {
        let count: Int
        let target: Int64?
    }

    private func scrollKey(_ count: Int) -> ScrollKey { ScrollKey(count: count, target: nav.scrollTarget?.ts) }

    private func jump(to msgKey: String, _ proxy: ScrollViewProxy) async {
        withAnimation { proxy.scrollTo(msgKey, anchor: .center) }
        highlight = msgKey
        try? await Task.sleep(for: .milliseconds(1600))
        if highlight == msgKey { highlight = nil }
    }

    private static func rows(_ messages: [ChatMessage]) -> [ThreadRow] {
        let calendar = Calendar.current
        var out: [ThreadRow] = []
        out.reserveCapacity(messages.count + 8)
        for i in messages.indices.reversed() {
            let m = messages[i]
            out.append(.message(m))
            let day = calendar.startOfDay(for: Date(timeIntervalSince1970: Double(m.ts) / 1000))
            let previous = i > 0 ? calendar.startOfDay(for: Date(timeIntervalSince1970: Double(messages[i - 1].ts) / 1000)) : nil
            if previous != day { out.append(.day(day)) }
        }
        return out
    }

    @ViewBuilder private func rowView(_ row: ThreadRow) -> some View {
        switch row {
        case let .day(day):
            Text(verbatim: Self.dayText(day))
                .skyText(.labelMedium)
                .foregroundStyle(Color.skyOnSurfaceVariant)
                .padding(.horizontal, 10)
                .padding(.vertical, 3)
                .background(Color.skyContainerHigh, in: .sky(SkyRadius.small))
                .frame(maxWidth: .infinity)
                .padding(.vertical, 8)
        case let .message(m):
            MessageRow(key: key, m: m, group: isGroup, highlighted: highlight == m.key) {
                // The keyboard would cover the sheet's lower actions.
                UIApplication.shared.sendAction(#selector(UIResponder.resignFirstResponder), to: nil, from: nil, for: nil)
                dialogs.presentSheet { MessageSheet(key: key, m: m, group: isGroup).environmentObject(repo).environmentObject(nav) }
            }
        }
    }

    private static func dayText(_ day: Date) -> String {
        let formatter = DateFormatter()
        formatter.locale = L10n.locale
        formatter.setLocalizedDateFormatFromTemplate("EEEMMMd")
        return formatter.string(from: day)
    }

    // MARK: Header

    private var header: some View {
        HStack(spacing: 4) {
            SquareBarButton(icon: MI.roundedArrowBack, action: onBack)
                .accessibilityLabel(Text("chat_back_to_chats"))
                .accessibilityIdentifier("top-back")
            Button {
                if isGroup {
                    nav.push(.groupSettings(key))
                } else if !saved {
                    nav.push(.contactSettings(key))
                }
            } label: {
                HStack(spacing: 10) {
                    headerAvatar
                    VStack(alignment: .leading, spacing: 0) {
                        Text(verbatim: title).skyText(.titleMedium).foregroundStyle(Color.skyOnSurface).lineLimit(1)
                        let sub = subtitle
                        if !sub.isEmpty {
                            Text(verbatim: sub).skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).lineLimit(1)
                        }
                    }
                    Spacer(minLength: 0)
                }
                .padding(4)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityIdentifier("thread-title")
            if !saved && !isGroup && repo.voiceAvailable {
                Button { repo.placeCall(key) } label: {
                    MaterialIcon(MI.roundedCall).foregroundStyle(Color.skyOnSurface).frame(width: 44, height: 44)
                }
                .accessibilityLabel(Text("chat_call"))
                .accessibilityIdentifier("thread-call")
            }
            ThreadMenu(key: key, saved: saved, isGroup: isGroup, group: group, paired: paired, title: title)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 6)
        .background(Color.skyBackground)
    }

    @ViewBuilder private var headerAvatar: some View {
        if saved {
            ChatAvatar(key: Skychat.savedKey, label: "", size: 40, kind: .saved)
        } else if isGroup {
            ChatAvatar(
                key: key, label: title, size: 40, kind: group?.isChannel == true ? .channel : .group, version: group?.avatar.hashValue ?? 0,
                load: (group?.avatar).flatMap { $0.isEmpty ? nil : $0 }.map { avatar in { @MainActor in DataURL.decode(avatar) } }
            )
        } else {
            ChatAvatar(key: key, label: title, size: 40, version: store.avatarVersion, online: repo.presence[key]?.online, load: { @MainActor [store, key] in store.avatar(key) })
        }
    }

    private var subtitle: String {
        if saved { return L10n.text("chat_saved_sub") }
        if let group { return ChatText.groupSubtitle(group) }
        if isGroup { return "" }
        var parts: [String] = []
        if let p = repo.presence[key] {
            if !p.online {
                parts.append(L10n.text("chat_presence_offline"))
            } else if p.rttMs > 0 {
                parts.append(L10n.format("chat_presence_rtt", String(p.rttMs)))
            } else {
                parts.append(L10n.text("chat_presence_online"))
            }
        }
        if paired, let record = repo.pair.records[key] {
            parts.append(record.forwardSecret && !record.epoch.isEmpty ? L10n.format("chat_epoch_secret", String(record.epoch.prefix(8))) : L10n.text("chat_epoch_pending"))
        }
        return parts.joined(separator: " · ")
    }

    // MARK: Bars and composer

    @ViewBuilder private func bars(_ messages: [ChatMessage]) -> some View {
        if !saved && !isGroup && repo.network != "cxo" {
            LinkBar(pk: key, name: title, state: repo.links[key], queued: messages.filter { $0.status == "queued" }.count)
        }
        if !saved && !isGroup && repo.privacy?.requests.contains(key) == true {
            VStack(alignment: .leading, spacing: 4) {
                Text("chat_request_bar").skyText(.bodySmall).foregroundStyle(Color.skyOnSurface)
                HStack {
                    Spacer()
                    Button {
                        Task { if await repo.blockRequest(key) { nav.home() } }
                    } label: { Text("chat_block").foregroundStyle(Color.skyError) }
                    .buttonStyle(.skyText)
                    Button { repo.acceptRequest(key) } label: { Text("chat_accept") }
                        .buttonStyle(.skyText)
                        .accessibilityIdentifier("thread-accept")
                }
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 8)
            .background(Color.skyContainerHigh)
        }
        if let reply = nav.replies[key] {
            HStack(spacing: 10) {
                Rectangle().fill(Color.skyPrimary).frame(width: 3, height: 32)
                VStack(alignment: .leading, spacing: 0) {
                    let who = reply.sender == Skychat.me || reply.sender == repo.myPk ? L10n.text("chat_you") : repo.displayName(reply.sender)
                    Text(verbatim: L10n.format("chat_replying_to", who)).skyText(.labelMedium).foregroundStyle(Color.skyPrimary)
                    Text(verbatim: ChatText.shown(reply.preview)).skyText(.bodySmall).foregroundStyle(Color.skyOnSurface).lineLimit(1)
                }
                Spacer(minLength: 0)
                Button { nav.setReply(key, nil) } label: {
                    MaterialIcon(MI.roundedClose).foregroundStyle(Color.skyOnSurfaceVariant).frame(width: 44, height: 44)
                }
                .accessibilityLabel(Text("cancel"))
            }
            .padding(.leading, 16)
            .padding(.trailing, 4)
            .padding(.vertical, 6)
            .background(Color.skyContainer)
        }
        if let group, !group.postable {
            Text(verbatim: group.cannotPostReason.isEmpty ? L10n.text("chat_cannot_post") : group.cannotPostReason)
                .skyText(.bodyMedium)
                .foregroundStyle(Color.skyOnSurfaceVariant)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(16)
                .background(Color.skyContainer)
        } else {
            Composer(key: key)
        }
    }
}

/// Where a quote's tap jumps to, set by the open thread.
private struct QuoteJumpKey: EnvironmentKey {
    static let defaultValue: @MainActor (ReplyRef) -> Void = { _ in }
}

extension EnvironmentValues {
    var quoteJump: @MainActor (ReplyRef) -> Void {
        get { self[QuoteJumpKey.self] }
        set { self[QuoteJumpKey.self] = newValue }
    }
}

private struct ThreadMenu: View {
    let key: String
    let saved: Bool
    let isGroup: Bool
    let group: GroupInfo?
    let paired: Bool
    let title: String
    @EnvironmentObject private var repo: ChatRepository
    @EnvironmentObject private var nav: ChatNav
    @EnvironmentObject private var dialogs: SkyDialogs
    @ObservedObject private var store = ChatRepository.shared.store

    private static func routes(paired: Bool) -> [String] {
        paired ? ["skynet", "dmsg", "cxo"] : ["skynet", "dmsg"]
    }

    private static func routeName(_ route: String) -> String {
        switch route {
        case "dmsg": "DMSG"
        case "cxo": L10n.text("chat_network_cxo")
        default: "Skynet"
        }
    }

    var body: some View {
        let muted = store.notif.isMuted(key)
        let pinned = store.state.pinned == ChatRepository.pinKey(key, group: isGroup)
        Menu {
            if saved {
                Button(role: .destructive) {
                    dialogs.confirm(L10n.text("chat_confirm_clear_notes"), confirm: L10n.text("chat_ok")) { repo.deleteChat(Skychat.savedKey) }
                } label: { Label(L10n.text("chat_clear_notes"), image: MI.roundedDeleteForever) }
            } else if isGroup {
                let channel = group?.isChannel == true
                Button { repo.togglePin(key) } label: { Label(pinned ? L10n.text("chat_unpin") : L10n.text("chat_pin"), image: MI.roundedPushPin) }
                Button { repo.toggleMute(key) } label: {
                    Label(muted ? L10n.text("chat_unmute") : L10n.text("chat_mute"), image: muted ? MI.roundedNotifications : MI.roundedNotificationsOff)
                }
                Button { nav.push(.groupSettings(key)) } label: { Label(L10n.text("chat_settings"), image: MI.roundedSettings) }
                Button {
                    Task {
                        if let link = await repo.inviteLink(key) {
                            UIPasteboard.general.string = link
                            repo.note(L10n.text("chat_invite_copied"))
                        }
                    }
                } label: { Label(L10n.text("chat_invite_link"), image: MI.roundedLink) }
                if let group {
                    Button {
                        dialogs.showQr(ChatQrSpec(
                            address: "skychat://\(group.ownerPk.lowercased())/\(group.id)",
                            title: L10n.format("chat_qr_title_group", title),
                            help: channel ? L10n.text("chat_qr_help_channel") : L10n.text("chat_qr_help_group"),
                            inviteGid: repo.isAdmin(group) ? group.id : nil
                        ), repo: repo)
                    } label: { Label(L10n.text("chat_qr_code"), image: MI.roundedQrCode2) }
                }
                Divider()
                Button {
                    dialogs.confirm(channel ? L10n.text("chat_confirm_leave_channel") : L10n.text("chat_confirm_leave_group"), confirm: L10n.text("chat_ok")) {
                        Task { if await repo.leaveGroup(key) { nav.home() } }
                    }
                } label: { Label(channel ? L10n.text("chat_leave_channel") : L10n.text("chat_leave_group"), image: MI.roundedLogout) }
                Button(role: .destructive) {
                    dialogs.confirm(channel ? L10n.text("chat_confirm_delete_channel") : L10n.text("chat_confirm_delete_group"), confirm: L10n.text("chat_ok")) {
                        Task { if await repo.deleteGroup(key) { nav.home() } }
                    }
                } label: { Label(channel ? L10n.text("chat_delete_channel") : L10n.text("chat_delete_group"), image: MI.roundedDeleteForever) }
            } else {
                if repo.voiceAvailable {
                    Button { repo.placeCall(key) } label: { Label(L10n.text("chat_call"), image: MI.roundedCall) }
                }
                Button { repo.togglePin(key) } label: { Label(pinned ? L10n.text("chat_unpin") : L10n.text("chat_pin"), image: MI.roundedPushPin) }
                Button { repo.toggleMute(key) } label: {
                    Label(muted ? L10n.text("chat_unmute") : L10n.text("chat_mute"), image: muted ? MI.roundedNotifications : MI.roundedNotificationsOff)
                }
                if repo.pair.available {
                    Button {
                        if paired {
                            dialogs.confirm(L10n.text("chat_confirm_unpair"), confirm: L10n.text("chat_ok")) { repo.togglePair(key) }
                        } else {
                            repo.togglePair(key)
                        }
                    } label: { Label(paired ? L10n.text("chat_unpair") : L10n.text("chat_pair"), image: paired ? MI.roundedLinkOff : MI.roundedLink) }
                }
                Picker(selection: Binding(get: { repo.network }, set: { value in
                    repo.network = value
                    repo.rewatchLink(key)
                })) {
                    ForEach(Self.routes(paired: paired), id: \.self) { route in
                        Text(verbatim: Self.routeName(route)).tag(route)
                    }
                } label: {
                    Label(L10n.text("chat_network"), image: MI.roundedRoute)
                }
                .pickerStyle(.menu)
                Button {
                    dialogs.showQr(ChatQrSpec(address: "skychat://\(key)", title: L10n.text("chat_contact_address"), help: L10n.text("chat_qr_help_me")), repo: repo)
                } label: { Label(L10n.text("chat_qr_code"), image: MI.roundedQrCode2) }
                Button { nav.push(.contactSettings(key)) } label: { Label(L10n.text("chat_settings"), image: MI.roundedSettings) }
                if let privacy = repo.privacy {
                    let blocked = privacy.blocked.contains(key)
                    Button { repo.setBlocked(key, !blocked) } label: { Label(blocked ? L10n.text("chat_unblock") : L10n.text("chat_block"), image: MI.roundedBlock) }
                }
                Divider()
                Button(role: .destructive) {
                    dialogs.confirm(L10n.text("chat_confirm_delete_chat"), confirm: L10n.text("chat_ok")) {
                        repo.deleteChat(key)
                        nav.home()
                    }
                } label: { Label(L10n.text("chat_delete_chat"), image: MI.roundedDeleteForever) }
            }
        } label: {
            ZStack(alignment: .topTrailing) {
                MaterialIcon(MI.roundedMoreVert)
                    .foregroundStyle(muted ? Color.skyWarning : Color.skyOnSurface)
                    .frame(width: 44, height: 44)
                if let pending = group?.pendingJoins, pending > 0 {
                    Text(verbatim: String(pending)).skyText(.labelSmall).foregroundStyle(.white)
                        .padding(.horizontal, 5).background(Color.skyError, in: Capsule())
                }
            }
        }
        .accessibilityLabel(Text("chat_actions"))
        .accessibilityIdentifier("thread-menu")
    }
}

private struct LinkBar: View {
    let pk: String
    let name: String
    let state: LinkState?
    let queued: Int
    @EnvironmentObject private var repo: ChatRepository

    var body: some View {
        if !((state == nil || state?.ready == true) && queued == 0) {
            let error = state.map { !$0.error.isEmpty && !$0.ready } ?? false
            HStack(spacing: 10) {
                if !error { MaterialSpinner(size: 16, stroke: 2) }
                VStack(alignment: .leading, spacing: 0) {
                    Text(verbatim: error ? L10n.format("chat_link_no_route", name) : L10n.format("chat_link_connecting", name))
                        .skyText(.bodySmall).foregroundStyle(Color.skyOnSurface)
                    if queued > 0 {
                        Text(verbatim: queued == 1 ? L10n.format("chat_link_waiting_one", String(queued)) : L10n.format("chat_link_waiting_many", String(queued)))
                            .skyText(.labelSmall).foregroundStyle(Color.skyOnSurfaceVariant)
                    }
                }
                Spacer(minLength: 0)
                if error {
                    Button { repo.retryLink(pk) } label: { Text("chat_retry") }.buttonStyle(.skyText)
                    if queued > 0 {
                        Button { repo.sendAnyway(pk) } label: { Text("chat_send_anyway") }.buttonStyle(.skyText)
                    }
                }
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 8)
            .background(Color.skyContainerHigh)
            .accessibilityElement(children: .contain)
        .accessibilityIdentifier("thread-link")
        }
    }
}

/// The touch sheet (index.html openMsgSheet), with the desktop ⋯ menu's file actions folded in.
private struct MessageSheet: View {
    let key: String
    let m: ChatMessage
    let group: Bool
    @EnvironmentObject private var repo: ChatRepository
    @EnvironmentObject private var nav: ChatNav
    @EnvironmentObject private var dialogs: SkyDialogs

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            let shown = ChatText.shown(m.text.isEmpty ? (m.file?.name ?? "") : m.text)
            Text(verbatim: shown.isEmpty ? L10n.text("chat_message") : shown)
                .skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant).lineLimit(2)
                .padding(.horizontal, 24).padding(.bottom, 8)
            if m.status != "queued" {
                action(MI.roundedReply, L10n.text("chat_reply"), id: "msg-reply") {
                    nav.setReply(key, ReplyRef(sender: m.mine ? Skychat.me : m.from, ts: Skychat.isoMillis(m.ts), preview: Skychat.messagePreview(m)))
                }
            }
            if !m.text.isEmpty && m.file == nil {
                action(MI.roundedContentCopy, L10n.text("chat_copy"), id: "msg-copy") {
                    UIPasteboard.general.string = m.text
                    repo.note(L10n.text("chat_copied"))
                }
            }
            action(MI.roundedShortcut, L10n.text("chat_forward"), id: "msg-forward") { nav.push(.forward(from: key, message: m.key)) }
            if key != Skychat.savedKey {
                action(MI.roundedBookmark, L10n.text("chat_save_to_saved"), id: "msg-save") { repo.saveToSaved(m) }
            }
            if let f = m.file {
                if f.url.hasPrefix("/files/") {
                    action(MI.roundedDownload, L10n.text("chat_download"), id: "msg-download") { repo.download(m) }
                }
                if !f.from.isEmpty && !f.id.isEmpty {
                    action(MI.roundedRefresh, L10n.text("chat_rerequest"), id: "msg-rerequest") { repo.requestFile(m) }
                }
            }
            action(MI.roundedDelete, L10n.text("chat_delete_for_me"), danger: true, id: "msg-delete-me") { repo.deleteForMe(key, m) }
            if repo.canDeleteForEveryone(key, m) {
                action(MI.roundedDeleteForever, L10n.text("chat_delete_for_all"), danger: true, id: "msg-delete-all") { repo.deleteForEveryone(key, m) }
            }
        }
        .padding(.bottom, 24)
    }

    private func action(_ icon: String, _ label: String, danger: Bool = false, id: String, _ act: @escaping () -> Void) -> some View {
        Button {
            dialogs.dismissSheet()
            act()
        } label: {
            HStack(spacing: 20) {
                MaterialIcon(icon).foregroundStyle(danger ? Color.skyError : Color.skyOnSurfaceVariant)
                Text(verbatim: label).skyText(.bodyLarge).foregroundStyle(danger ? Color.skyError : Color.skyOnSurface)
                Spacer(minLength: 0)
            }
            .padding(.horizontal, 24)
            .padding(.vertical, 14)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier(id)
    }
}
