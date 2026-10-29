import CoreClient
import Foundation

/// Groups and channels (Android: chat/ChatGroups.kt).
extension ChatRepository {
    private static let chunk = 50
    private static let metaThrottle: TimeInterval = 5 * 60

    /// The first page of a group, unless it was already loaded (index.html selectGroup).
    func openGroup(_ gid: String) async {
        markRead(gid)
        await maybeRefreshMeta(gid)
        if hist[gid]?.loaded == true { return }
        await loadGroupPage(gid, older: false)
    }

    /// After a reconnect: pull the newest page again so a gap in the stream fills in.
    func refreshGroupHead(_ gid: String) async {
        guard hist[gid]?.loaded == true, let rows = try? await api.groupHistory(gid: gid, limit: Self.chunk, before: nil) else { return }
        mergeGroupRows(gid, rows)
    }

    /// Older backlog as the reader scrolls up (index.html loadOlderGroupHistory).
    func loadOlder(_ gid: String) {
        guard let page = hist[gid], page.loaded, !page.done, !page.loading else { return }
        Task { await loadGroupPage(gid, older: true) }
    }

    private func loadGroupPage(_ gid: String, older: Bool) async {
        var page = hist[gid] ?? HistPage()
        if page.loading { return }
        while true {
            page.loading = true
            hist[gid] = page
            let rows: [GroupHistoryRecord]
            do {
                rows = try await api.groupHistory(gid: gid, limit: Self.chunk, before: older ? page.cursor : nil)
            } catch {
                page.loading = false
                hist[gid] = page
                return
            }
            var cursor = page.cursor
            if let first = rows.first {
                cursor = first.tsNano.isEmpty ? Skychat.parseTime(first.ts).map { String($0 * 1_000_000) } ?? cursor : first.tsNano
            }
            page = HistPage(cursor: cursor, done: rows.count < Self.chunk, loading: false, loaded: true)
            hist[gid] = page
            let shown = mergeGroupRows(gid, rows)
            // A page that was entirely deleted-for-me shows nothing; ask for the next one.
            if !older || shown > 0 || page.done { return }
        }
    }

    /// Returns how many rows were visible.
    @discardableResult
    private func mergeGroupRows(_ gid: String, _ rows: [GroupHistoryRecord]) -> Int {
        let me = myPk
        let hidden = store.state.hiddenGroup
        let incoming: [ChatMessage] = rows.compactMap { r in
            if r.text == Skychat.heartbeat { return nil }
            if !r.tsNano.isEmpty && hidden.contains("\(gid):\(r.tsNano)") { return nil }
            guard let ts = Skychat.parseTime(r.ts) else { return nil }
            let sender = r.senderPk.lowercased()
            let mine = sender == me
            return ChatMessage(
                key: "g:\(gid):\(r.tsNano):\(sender)", ts: ts, from: mine ? Skychat.me : sender, text: r.text,
                file: r.fileId.isEmpty ? nil : ChatFile(id: r.fileId, name: r.fileName.isEmpty ? "file" : r.fileName, size: r.fileSize, url: r.fileUrl, from: mine ? "" : sender),
                reply: Skychat.replyOf(sender: r.replyToSender, ts: r.replyToTs, preview: r.replyToPreview),
                forwarded: r.forwarded, forwardFrom: r.forwardFrom, status: mine ? "sent" : "", tsNano: r.tsNano
            )
        }
        guard !incoming.isEmpty else { return 0 }
        mutate(gid) { current in
            let known = Set(current.compactMap { $0.tsNano.isEmpty ? nil : $0.tsNano })
            let fresh = incoming.filter { $0.tsNano.isEmpty || !known.contains($0.tsNano) }
            return fresh.isEmpty ? current : (current + fresh).sorted { $0.ts < $1.ts }
        }
        return incoming.count
    }

    /// A member's copy of the founder's name and picture, refreshed at most every few minutes.
    private func maybeRefreshMeta(_ gid: String) async {
        guard let g = groupInfo(gid), g.ownerPk.lowercased() != myPk else { return }
        if let asked = groupMetaAskedAt[gid], Date().timeIntervalSince(asked) < Self.metaThrottle { return }
        groupMetaAskedAt[gid] = Date()
        if let info = try? await api.refreshMeta(gid: gid) { applyMeta(gid, info) }
    }

    /// Only name and picture move; the rest of the cached record stays live.
    func applyMeta(_ gid: String, _ info: GroupInfo) {
        groups = groups.map { g in
            guard g.id == gid else { return g }
            var g = g
            if !info.name.isEmpty { g.name = info.name }
            g.avatar = info.avatar
            return g
        }
    }

    func replaceGroup(_ info: GroupInfo) {
        guard !info.id.isEmpty else { return }
        if groups.contains(where: { $0.id == info.id }) {
            groups = groups.map { $0.id == info.id ? info : $0 }
        } else {
            groups.append(info)
        }
        signalGroupChanged(info.id)
    }

    func isAdmin(_ g: GroupInfo) -> Bool {
        g.ownerPk.lowercased() == myPk || g.admins.contains { $0.lowercased() == myPk }
    }

    func isFounder(_ g: GroupInfo) -> Bool { g.ownerPk.lowercased() == myPk }

    /// Creates a group or channel; its id and invite link.
    func createGroup(name: String, kind: String, peerBackfill: Bool?, listed: Bool?) async -> (id: String, invite: String)? {
        do {
            let created = try await api.createGroup(name: name, kind: kind, peerBackfill: peerBackfill, listed: listed)
            await syncGroups()
            _ = thread(created.info.id)
            return (created.info.id, created.invite)
        } catch {
            note(L10n.format("chat_note_create_failed", errorText(error)), error: true)
            return nil
        }
    }

    /// Joins from a resolved address; the group once admitted, or nil (waiting or failed).
    func join(_ res: Resolved) async -> GroupInfo? {
        let info: GroupInfo
        do {
            info = try await api.join(invite: res.invite.isEmpty ? nil : res.invite, address: res.address)
        } catch {
            note(L10n.format("chat_note_join_failed", errorText(error)), error: true)
            return nil
        }
        await syncGroups()
        if info.status == "awaiting_approval" {
            note(L10n.text("chat_note_request_sent"))
            return nil
        }
        let channel = res.group?.kind == "channel" || info.isChannel
        note(channel ? L10n.text("chat_note_joined_channel") : L10n.text("chat_note_joined_group"))
        return info
    }

    func memberAction(_ gid: String, _ action: String, _ pk: String) {
        Task {
            do {
                replaceGroup(try await api.groupMemberAction(gid: gid, action: action, pk: pk))
                note(L10n.text("chat_note_done"))
            } catch {
                note(L10n.format("chat_note_failed", errorText(error)), error: true)
            }
        }
    }

    private func groupSetting(_ gid: String, ok: String, _ call: @escaping () async throws -> GroupInfo) {
        Task {
            do {
                replaceGroup(try await call())
                note(ok)
            } catch {
                signalGroupChanged(gid)
                note(L10n.format("chat_note_failed", errorText(error)), error: true)
            }
        }
    }

    func setReadOnly(_ gid: String, _ on: Bool) {
        groupSetting(gid, ok: on ? L10n.text("chat_note_read_only_on") : L10n.text("chat_note_read_only_off")) { [api] in
            try await api.setReadOnly(gid: gid, readOnly: on)
        }
    }

    func setPeerBackfill(_ gid: String, _ on: Bool) {
        groupSetting(gid, ok: on ? L10n.text("chat_note_backfill_on") : L10n.text("chat_note_backfill_off")) { [api] in
            try await api.setPeerBackfill(gid: gid, enabled: on)
        }
    }

    func setJoinCost(_ gid: String, _ bits: Int) {
        groupSetting(gid, ok: bits == 0 ? L10n.text("chat_note_join_free") : L10n.text("chat_note_join_cost")) { [api] in
            try await api.setJoinPow(gid: gid, bits: bits)
        }
    }

    func rotateKey(_ gid: String) async {
        do {
            let info = try await api.rotateKey(gid: gid)
            replaceGroup(info)
            note(L10n.format("chat_note_rekeyed", info.keyEpoch))
        } catch {
            note(L10n.format("chat_note_rotate_failed", errorText(error)), error: true)
        }
    }

    func setListed(_ gid: String, _ on: Bool) {
        Task {
            do {
                try await api.setListed(gid: gid, listed: on)
                await syncGroups()
                signalGroupChanged(gid)
                note(on ? L10n.text("chat_note_listed_on") : L10n.text("chat_note_listed_off"))
            } catch {
                signalGroupChanged(gid)
                note(L10n.format("chat_note_failed", errorText(error)), error: true)
            }
        }
    }

    func setGroupName(_ gid: String, _ name: String) {
        let clean = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !clean.isEmpty else {
            note(L10n.text("chat_note_group_needs_name"), error: true)
            return
        }
        Task {
            do {
                applyMeta(gid, try await api.setGroupMeta(gid: gid, name: clean))
                signalGroupChanged(gid)
                note(L10n.text("chat_note_renamed"))
            } catch {
                note(L10n.format("chat_note_failed", errorText(error)), error: true)
            }
        }
    }

    /// An empty `dataURL` clears the picture.
    func setGroupAvatar(_ gid: String, _ dataURL: String) {
        let channel = groupInfo(gid)?.isChannel == true
        Task {
            do {
                applyMeta(gid, try await api.setGroupMeta(gid: gid, avatar: dataURL))
                signalGroupChanged(gid)
                if dataURL.isEmpty {
                    note(L10n.text("chat_note_picture_removed"))
                } else {
                    note(channel ? L10n.text("chat_note_channel_picture") : L10n.text("chat_note_group_picture"))
                }
            } catch {
                note(L10n.format("chat_note_failed", errorText(error)), error: true)
            }
        }
    }

    func inviteLink(_ gid: String) async -> String? {
        do {
            return try await api.invite(gid: gid)
        } catch {
            note(L10n.text("chat_note_no_invite"), error: true)
            return nil
        }
    }

    func leaveGroup(_ gid: String) async -> Bool {
        let channel = groupInfo(gid)?.isChannel == true
        do {
            try await api.leave(gid: gid)
            removeGroup(gid)
            note(channel ? L10n.text("chat_note_left_channel") : L10n.text("chat_note_left_group"))
            return true
        } catch {
            note(L10n.format("chat_note_failed", errorText(error)), error: true)
            return false
        }
    }

    func deleteGroup(_ gid: String) async -> Bool {
        let channel = groupInfo(gid)?.isChannel == true
        do {
            try await api.deleteGroup(gid: gid)
            removeGroup(gid)
            note(channel ? L10n.text("chat_note_channel_deleted") : L10n.text("chat_note_group_deleted"))
            return true
        } catch {
            note(L10n.format("chat_note_failed", errorText(error)), error: true)
            return false
        }
    }

    private func removeGroup(_ gid: String) {
        groups.removeAll { $0.id == gid }
        unpinIf("grp:\(gid)")
        summaries[gid] = nil
        hist[gid] = nil
    }

    func unpinIf(_ pin: String) {
        if store.state.pinned == pin { store.update { $0.pinned = nil } }
    }
}
