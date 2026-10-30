import CoreClient
import SwiftUI

/// A new group or channel (Android: ui/chat/GroupScreens.kt NewGroupScreen).
struct NewGroupView: View {
    let channel: Bool
    let onBack: () -> Void
    @EnvironmentObject private var repo: ChatRepository
    @EnvironmentObject private var nav: ChatNav
    @State private var name = ""
    @State private var privateGroup = false
    @State private var backfill = true
    @State private var listed = false
    @State private var busy = false

    var body: some View {
        VStack(spacing: 0) {
            SkyTopBar(title: Text(channel ? L10n.key("chat_new_channel_title") : L10n.key("chat_create_group")), onBack: onBack)
            ScrollView {
                VStack(alignment: .leading, spacing: 8) {
                    SkyOutlinedTextField(label: channel ? L10n.key("chat_channel_name") : L10n.key("chat_group_name"), text: Binding(
                        get: { name }, set: { name = String($0.prefix(64)) }
                    ), identifier: "group-name", prose: true)
                    if channel {
                        ChatHelp(text: L10n.text("chat_channel_help_1"))
                        ChatHelp(text: L10n.text("chat_channel_help_2"))
                        ChatCheckRow(label: L10n.text("chat_list_catalog"), checked: listed) { listed = $0 }
                        ChatHelp(text: L10n.text("chat_list_catalog_help"))
                    } else {
                        SkySegmentedRow(items: [false, true], selected: privateGroup, label: { isPrivate in
                            Text(isPrivate ? L10n.key("chat_kind_private_short") : L10n.key("chat_kind_public_short"))
                        }, onSelect: { privateGroup = $0 })
                        Text(privateGroup ? L10n.key("chat_kind_private_label") : L10n.key("chat_kind_public_label"))
                            .skyText(.titleSmall).foregroundStyle(Color.skyOnSurface)
                        ChatHelp(text: privateGroup ? L10n.text("chat_kind_private_help") : L10n.text("chat_kind_public_help"))
                        ChatCheckRow(label: L10n.text("chat_backfill"), checked: backfill) { backfill = $0 }
                        ChatHelp(text: L10n.text("chat_backfill_help"))
                    }
                    Button { create() } label: { Text("chat_create").frame(maxWidth: .infinity) }
                        .buttonStyle(.filled)
                        .disabled(busy)
                        .accessibilityIdentifier("group-create")
                        .padding(.top, 8)
                }
                .padding(20)
            }
        }
        .background(Color.skyBackground)
    }

    private func create() {
        guard !name.trimmingCharacters(in: .whitespaces).isEmpty else {
            repo.note(channel ? L10n.text("chat_enter_channel_name") : L10n.text("chat_enter_group_name"), error: true)
            return
        }
        busy = true
        Task {
            let kind = channel ? "channel" : (privateGroup ? "private" : "public")
            let created = await repo.createGroup(
                name: name.trimmingCharacters(in: .whitespaces), kind: kind,
                peerBackfill: channel ? nil : backfill, listed: channel ? listed : nil
            )
            busy = false
            guard let created else { return }
            if !created.invite.isEmpty {
                UIPasteboard.general.string = created.invite
                repo.note(channel ? L10n.text("chat_channel_created") : L10n.text("chat_group_created"))
            }
            nav.openThread(created.id)
        }
    }
}

/// The admin panel (index.html groupAdminModal): what may be changed depends on founder, admin or member.
struct GroupSettingsView: View {
    let gid: String
    let onBack: () -> Void
    @EnvironmentObject private var repo: ChatRepository
    @ObservedObject private var store = ChatRepository.shared.store
    @State private var requests: [JoinRequest]?
    @State private var rotating = false
    @State private var name = ""

    private static let joinBits = [0, 16, 18, 22, 26]

    private static func costLabel(_ bits: Int) -> String {
        switch bits {
        case 0: L10n.text("chat_cost_free")
        case 16: L10n.text("chat_cost_low")
        case 22: L10n.text("chat_cost_high")
        case 26: L10n.text("chat_cost_max")
        default: L10n.text("chat_cost_normal")
        }
    }

    var body: some View {
        let g = repo.groupInfo(gid)
        VStack(spacing: 0) {
            SkyTopBar(title: Text(g?.isChannel == true ? L10n.key("chat_channel_settings") : L10n.key("chat_group_settings")), onBack: onBack)
            if let g {
                ScrollView { panel(g).padding(.horizontal, 20).padding(.vertical, 8) }
            } else {
                ChatHint(text: L10n.text("chat_group_gone"))
                Spacer()
            }
        }
        .background(Color.skyBackground)
        .onAppear { name = g?.name ?? "" }
        .onChange(of: g?.name ?? "") { name = $0 }
        .task(id: RequestsKey(admin: g.map(repo.isAdmin) ?? false, tick: repo.groupChanged.gid == gid ? repo.groupChanged.tick : 0)) {
            guard let g, repo.isAdmin(g) else { return }
            requests = try? await repo.api.groupRequests(gid: gid)
        }
    }

    private struct RequestsKey: Equatable {
        let admin: Bool
        let tick: Int
    }

    @ViewBuilder private func panel(_ g: GroupInfo) -> some View {
        let admin = repo.isAdmin(g)
        let founder = repo.isFounder(g)
        let channel = g.isChannel
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 12) {
                ChatAvatar(key: g.id, label: g.name, size: 64, kind: channel ? .channel : .group, version: g.avatar.hashValue,
                           load: g.avatar.isEmpty ? nil : { @MainActor [avatar = g.avatar] in DataURL.decode(avatar) })
                VStack(alignment: .leading, spacing: 0) {
                    Text(verbatim: g.name.isEmpty ? g.id : g.name).skyText(.titleMedium).foregroundStyle(Color.skyOnSurface)
                    Text(verbatim: ChatText.groupSubtitle(g)).skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                }
            }
            if founder {
                HStack(spacing: 8) {
                    SkyOutlinedTextField(label: channel ? L10n.key("chat_channel_name") : L10n.key("chat_group_name"), text: Binding(
                        get: { name }, set: { name = String($0.prefix(64)) }
                    ), prose: true)
                    Button { repo.setGroupName(gid, name) } label: { Text("chat_rename") }.buttonStyle(.outlined)
                }
                .padding(.top, 12)
                HStack(spacing: 8) {
                    AvatarPickerButton(target: .group, onPicked: { repo.setGroupAvatar(gid, DataURL.encode($0)) }) {
                        Text("chat_change_picture").skyText(.labelLarge).foregroundStyle(Color.skyPrimary)
                            .padding(.horizontal, 16).frame(height: 40)
                            .overlay(Capsule().stroke(Color.skyOutline, lineWidth: 1))
                    }
                    if !g.avatar.isEmpty {
                        Button { repo.setGroupAvatar(gid, "") } label: { Text("chat_remove_picture") }.buttonStyle(.outlined)
                    }
                }
                ChatHelp(text: L10n.text("chat_meta_note"))
            }
            if admin {
                ChatDivider()
                if !channel {
                    ChatCheckRow(label: L10n.text("chat_read_only"), checked: g.readOnly) { repo.setReadOnly(gid, $0) }
                }
                ChatCheckRow(label: L10n.text("chat_list_visor_catalog"), checked: g.listed) { repo.setListed(gid, $0) }
                ChatHelp(text: L10n.text("chat_list_visor_catalog_help"))
                joinCost(g)
                ChatHelp(text: L10n.text("chat_join_cost_help"))
                if !channel {
                    ChatCheckRow(label: L10n.text("chat_backfill"), checked: g.peerBackfill != false) { repo.setPeerBackfill(gid, $0) }
                    ChatHelp(text: L10n.text("chat_backfill_admin_help"))
                }
            }
            if g.governanceSealed {
                ChatDivider()
                Text("chat_sealed_title").skyText(.titleSmall).foregroundStyle(Color.skyOnSurface)
                ChatHelp(text: L10n.text("chat_sealed_help"))
            }
            if g.encrypted && admin {
                ChatDivider()
                HStack {
                    VStack(alignment: .leading, spacing: 0) {
                        Text(verbatim: L10n.format("chat_key_epoch", String(g.keyEpoch))).skyText(.titleSmall).foregroundStyle(Color.skyOnSurface)
                        if let age = keyAge(g) {
                            Text(verbatim: age).skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                        }
                    }
                    Spacer(minLength: 0)
                    Button {
                        rotating = true
                        Task {
                            await repo.rotateKey(gid)
                            rotating = false
                        }
                    } label: { Text(rotating ? L10n.key("chat_rotating") : L10n.key("chat_rotate_key")) }
                    .buttonStyle(.outlined)
                    .disabled(rotating)
                }
                ChatHelp(text: L10n.text("chat_key_help"))
            }
            ChatDivider()
            members(g, admin: admin, channel: channel)
            Spacer(minLength: 24)
        }
    }

    @ViewBuilder private func members(_ g: GroupInfo, admin: Bool, channel: Bool) -> some View {
        let pending = requests ?? []
        if admin && !pending.isEmpty {
            Text(verbatim: L10n.text("chat_join_requests") + " (\(pending.count))").skyText(.titleSmall).foregroundStyle(Color.skyOnSurface)
            ForEach(Array(pending.enumerated()), id: \.offset) { _, r in
                HStack {
                    VStack(alignment: .leading, spacing: 0) {
                        Text(verbatim: repo.names[r.pk.lowercased()] ?? Skychat.abbrevPk(r.pk)).skyText(.bodyLarge).foregroundStyle(Color.skyOnSurface).lineLimit(1)
                        Text(verbatim: r.note.isEmpty ? L10n.text("chat_wants_to_join") : r.note).skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                    }
                    Spacer(minLength: 0)
                    Button { repo.memberAction(gid, "requests/approve", r.pk) } label: { Text("chat_approve") }.buttonStyle(.skyText)
                    Button { repo.memberAction(gid, "requests/deny", r.pk) } label: { Text("chat_decline").foregroundStyle(Color.skyError) }.buttonStyle(.skyText)
                }
                .padding(.vertical, 4)
            }
            Text(channel ? L10n.key("chat_in_channel") : L10n.key("chat_in_group")).skyText(.titleSmall).foregroundStyle(Color.skyOnSurface).padding(.top, 8)
        } else {
            Text("chat_members").skyText(.titleSmall).foregroundStyle(Color.skyOnSurface)
        }
        if g.members.isEmpty { ChatHelp(text: L10n.text("chat_no_members")) }
        let admins = Set((g.admins + [g.ownerPk]).map { $0.lowercased() }.filter { !$0.isEmpty })
        ForEach(g.members.map { $0.lowercased() }, id: \.self) { pk in
            memberRow(g, pk: pk, admins: admins, admin: admin)
        }
        if admin && admins.count <= 1 && g.members.count > 1 {
            ChatHelp(text: L10n.text("chat_solo_admin"))
        }
        if !g.banned.isEmpty {
            ChatDivider()
            Text("chat_banned").skyText(.titleSmall).foregroundStyle(Color.skyOnSurface)
            ForEach(g.banned.map { $0.lowercased() }, id: \.self) { pk in
                HStack {
                    Text(verbatim: repo.displayName(pk)).skyText(.bodyLarge).foregroundStyle(Color.skyOnSurface).lineLimit(1)
                    Spacer(minLength: 0)
                    if admin {
                        Button { repo.memberAction(gid, "members/unban", pk) } label: { Text("chat_unban") }.buttonStyle(.skyText)
                    }
                }
            }
        }
    }

    private func memberRow(_ g: GroupInfo, pk: String, admins: Set<String>, admin: Bool) -> some View {
        let isOwner = pk == g.ownerPk.lowercased()
        let isAdminRow = admins.contains(pk)
        let restricted = g.muted.contains { $0.lowercased() == pk }
        var tags: [String] = []
        if isOwner {
            tags.append(L10n.text("chat_tag_founder"))
        } else if isAdminRow {
            tags.append(L10n.text("chat_tag_admin"))
        }
        if pk == repo.myPk { tags.append(L10n.text("chat_tag_you")) }
        if restricted { tags.append(L10n.text("chat_state_restricted")) }
        return VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 10) {
                ChatAvatar(key: pk, label: repo.displayName(pk), size: 32, load: { @MainActor [store] in store.avatar(pk) })
                VStack(alignment: .leading, spacing: 0) {
                    Text(verbatim: repo.displayName(pk)).skyText(.bodyLarge).foregroundStyle(Color.skyOnSurface).lineLimit(1)
                    if !tags.isEmpty {
                        Text(verbatim: tags.joined(separator: " · ")).skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                    }
                }
            }
            if admin && !isOwner && pk != repo.myPk {
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 4) {
                        Button { repo.memberAction(gid, isAdminRow ? "members/demote" : "members/promote", pk) } label: {
                            Text(isAdminRow ? L10n.key("chat_demote") : L10n.key("chat_make_admin"))
                        }
                        .buttonStyle(.skyText)
                        Button { repo.memberAction(gid, restricted ? "members/unmute" : "members/mute", pk) } label: {
                            Text(restricted ? L10n.key("chat_unrestrict") : L10n.key("chat_restrict"))
                        }
                        .buttonStyle(.skyText)
                        Button { repo.memberAction(gid, "members/remove", pk) } label: { Text("chat_remove") }.buttonStyle(.skyText)
                        Button { repo.memberAction(gid, "members/ban", pk) } label: { Text("chat_ban").foregroundStyle(Color.skyError) }.buttonStyle(.skyText)
                    }
                }
                .padding(.leading, 42)
            }
        }
        .padding(.vertical, 4)
    }

    private func joinCost(_ g: GroupInfo) -> some View {
        let bits = g.joinPowBits ?? 18
        let shown = Self.joinBits.min { abs($0 - bits) < abs($1 - bits) } ?? 18
        return HStack {
            Text("chat_join_cost").skyText(.bodyLarge).foregroundStyle(Color.skyOnSurface)
            Spacer(minLength: 0)
            Menu {
                ForEach(Self.joinBits, id: \.self) { cost in
                    Button { if cost != bits { repo.setJoinCost(gid, cost) } } label: { Text(verbatim: Self.costLabel(cost)) }
                }
            } label: {
                Text(verbatim: Self.costLabel(shown)).skyText(.labelLarge).foregroundStyle(Color.skyPrimary)
            }
        }
        .padding(.top, 8)
    }

    private func keyAge(_ g: GroupInfo) -> String? {
        guard g.keyAgeSeconds > 0 else { return nil }
        var parts = [L10n.format("chat_key_in_use", ChatText.humanDuration(g.keyAgeSeconds))]
        if let next = g.keyRotatesInSeconds {
            parts.append(next > 0 ? L10n.format("chat_key_rotates_in", ChatText.humanDuration(next)) : L10n.text("chat_key_rotation_due"))
        }
        return parts.joined(separator: " · ")
    }
}
