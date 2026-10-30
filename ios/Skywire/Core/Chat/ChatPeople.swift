import CoreClient
import Foundation

/// A peer's published profile, or nil when it cannot be had. `unreachable` is told apart.
struct PeerLookup: Equatable {
    var name = ""
    var avatar: Data?
    var unreachable = false
}

/// One search hit, from the server or a local thread.
struct ChatSearchResult: Equatable, Identifiable {
    let key: String
    let isGroup: Bool
    let from: String
    let text: String
    let ts: Int64

    var id: String { "\(key):\(ts):\(text.hashValue)" }
}

enum DataURL {
    static func decode(_ uri: String) -> Data? {
        guard let marker = uri.range(of: "base64,") else { return nil }
        return Data(base64Encoded: String(uri[marker.upperBound...]), options: .ignoreUnknownCharacters)
    }

    static func encode(_ bytes: Data) -> String {
        let png = bytes.count > 4 && bytes[1] == UInt8(ascii: "P") && bytes[2] == UInt8(ascii: "N")
        return "data:image/" + (png ? "png" : "jpeg") + ";base64," + bytes.base64EncodedString()
    }
}

// Conversations, people and the rest of the settings (Android: chat/ChatPeople.kt).
extension ChatRepository {
    // MARK: Conversations

    /// Deletes a DM (or empties the notes): local copy, the stored history and a live pair all go.
    func deleteChat(_ key: String) {
        if key == Skychat.savedKey {
            mutate(key) { _ in [] }
            note(L10n.text("chat_note_saved_cleared"))
            return
        }
        let pk = key
        Task { _ = try? await api.accept(pk: pk, accepted: false) }
        if pair.paired.contains(pk) {
            pair.paired.remove(pk)
            Task {
                do {
                    try await api.unpair(pk: pk)
                } catch let error as SkychatError where error.status == 404 || error.status == 503 {
                } catch {
                    note(L10n.text("chat_note_pair_not_revoked"), error: true)
                }
            }
        }
        Task {
            do {
                try await api.forget(pk: pk)
            } catch let error as SkychatError where error.status == 404 || error.status == 503 {
            } catch {
                note(L10n.text("chat_note_history_not_deleted"), error: true)
            }
        }
        unpinIf("dm:\(pk)")
        store.update { state in
            state.recipients.removeAll { $0 == pk }
            state.readUpTo[pk] = nil
        }
        removeThread(pk)
        store.deleteThread(pk)
        note(L10n.text("chat_note_chat_deleted"))
    }

    static func pinKey(_ key: String, group: Bool) -> String { group ? "grp:\(key)" : "dm:\(key)" }

    func togglePin(_ key: String) {
        let pin = Self.pinKey(key, group: isGroupKey(key))
        store.update { $0.pinned = $0.pinned == pin ? nil : pin }
    }

    func isMuted(_ key: String) -> Bool { store.notif.isMuted(key) }

    func toggleMute(_ key: String) {
        let muted = isMuted(key)
        store.updateNotif { n in
            if muted {
                n.muted.remove(key)
                n.muted.remove(key.lowercased())
            } else {
                n.muted.insert(key)
            }
        }
        note(muted ? L10n.text("chat_note_unmuted") : L10n.text("chat_note_muted"))
    }

    func setTab(_ tab: String) {
        store.update { $0.tab = tab }
    }

    // MARK: Privacy

    func acceptRequest(_ pk: String) {
        Task {
            do {
                applyPrivacy(try await api.accept(pk: pk))
                addContact(pk)
                note(L10n.format("chat_note_can_message", displayName(pk)))
            } catch {
                note(L10n.format("chat_note_accept_failed", errorText(error)), error: true)
            }
        }
    }

    /// From the request bar: block and drop the conversation.
    func blockRequest(_ pk: String) async -> Bool {
        do {
            applyPrivacy(try await api.block(pk: pk, blocked: true))
            let name = displayName(pk)
            deleteChat(pk)
            note(L10n.format("chat_note_blocked", name))
            return true
        } catch {
            note(L10n.format("chat_note_block_failed", errorText(error)), error: true)
            return false
        }
    }

    func setBlocked(_ pk: String, _ blocked: Bool) {
        Task {
            do {
                applyPrivacy(try await api.block(pk: pk, blocked: blocked))
                note(blocked ? L10n.format("chat_note_blocked_keep", displayName(pk)) : L10n.format("chat_note_unblocked", displayName(pk)))
            } catch {
                if blocked {
                    note(L10n.format("chat_note_block_failed", errorText(error)), error: true)
                } else {
                    note(L10n.format("chat_note_unblock_failed", errorText(error)), error: true)
                }
            }
        }
    }

    func setApproval(_ on: Bool) {
        Task {
            do {
                applyPrivacy(try await api.setApproval(on))
            } catch {
                note(L10n.format("chat_note_setting_failed", errorText(error)), error: true)
                await syncPrivacy()
            }
        }
    }

    // MARK: Pairing

    func togglePair(_ pk: String) {
        guard pair.available else { return }
        Task {
            if pair.paired.contains(pk) {
                do {
                    try await api.unpair(pk: pk)
                    pair.paired.remove(pk)
                    if network == "cxo" { network = "skynet" }
                    note(L10n.text("chat_note_pair_removed"))
                } catch {
                    note(L10n.format("chat_note_unpair_failed", errorText(error)), error: true)
                }
            } else {
                do {
                    try await api.pair(pk: pk)
                    pair.paired.insert(pk)
                    note(L10n.text("chat_note_pair_sent"))
                } catch {
                    note(L10n.format("chat_note_pair_failed", errorText(error)), error: true)
                }
            }
        }
    }

    func acceptInvite(_ pk: String) {
        Task {
            do {
                try await api.acceptInvite(pk: pk)
                pair.invites.removeAll { $0 == pk }
                pair.paired.insert(pk)
                addContact(pk)
                ensureRecipient(pk)
                note(L10n.text("chat_note_invite_accepted"))
            } catch {
                note(L10n.format("chat_note_accept_failed", errorText(error)), error: true)
            }
        }
    }

    func declineInvite(_ pk: String) {
        Task {
            do {
                try await api.declineInvite(pk: pk)
                pair.invites.removeAll { $0 == pk }
                note(L10n.text("chat_note_invite_declined"))
            } catch {
                note(L10n.format("chat_note_decline_failed", errorText(error)), error: true)
            }
        }
    }

    // MARK: Contacts

    func removeContact(_ pk: String) {
        store.update { $0.contacts.removeAll { $0 == pk } }
        setName(pk, "")
        Task { _ = try? await api.accept(pk: pk, accepted: false) }
        note(L10n.text("chat_note_contact_removed"))
    }

    func lookupPeer(_ pk: String) async -> PeerLookup? {
        if profileAvailable == false { return nil }
        do {
            let p = try await api.peerProfile(pk: pk)
            return PeerLookup(name: p.name, avatar: DataURL.decode(p.avatar))
        } catch {
            return PeerLookup(unreachable: true)
        }
    }

    // MARK: Own profile

    /// Publishes the name (and picture, when given); a POST without one clears it, so the current one is resent.
    func saveProfile(name: String, avatar: Data?) async -> Bool {
        let keep = avatar.map(DataURL.encode) ?? profile.flatMap { $0.avatar.isEmpty ? nil : $0.avatar }
        do {
            profile = try await api.setProfile(name: name.trimmingCharacters(in: .whitespacesAndNewlines), avatarDataURL: keep)
            note(L10n.text("chat_note_profile_published"))
            return true
        } catch {
            note(L10n.format("chat_note_failed", errorText(error)), error: true)
            return false
        }
    }

    func removeProfilePicture() async -> Bool {
        do {
            profile = try await api.setProfile(name: profile?.name ?? "", avatarDataURL: nil)
            note(L10n.text("chat_note_picture_removed"))
            return true
        } catch {
            note(L10n.format("chat_note_failed", errorText(error)), error: true)
            return false
        }
    }

    func clearProfile() {
        Task {
            do {
                profile = try await api.clearProfile()
                note(L10n.text("chat_note_profile_cleared"))
            } catch {
                note(L10n.format("chat_note_failed", errorText(error)), error: true)
            }
        }
    }

    var myAddress: String {
        if let address = profile?.address, !address.isEmpty { return address }
        return myPk.isEmpty ? "" : "skychat://\(myPk)"
    }

    // MARK: Calls

    func placeCall(_ pk: String) {
        let calls = VoiceCalls.shared.state
        if calls.inCall {
            note(L10n.text("chat_note_in_call"), error: true)
            return
        }
        if let outgoing = calls.outgoing, !outgoing.state.ended {
            note(L10n.text("chat_note_already_calling"), error: true)
            return
        }
        Task {
            do {
                let id = try await api.placeCall(pk: pk)
                // Named at once: an answer that beats the next poll arrives as an id only.
                if !id.isEmpty { VoiceCalls.shared.placed(OutgoingCall(callId: id, peerPk: pk)) }
                VoiceCalls.shared.refresh()
            } catch {
                note(L10n.format("chat_note_call_failed", errorText(error)), error: true)
            }
        }
    }

    func callLog() async -> [CallRecord]? { try? await api.calls() }

    // MARK: Archive

    /// Fetches `/export/<name>` and hands it to the share sheet.
    func exportArchive() async -> Bool {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.dateFormat = "yyyy-MM-dd"
        let name = "skychat-\(formatter.string(from: Date())).json"
        do {
            share = SharedFile(url: try await shareCopy("/export/\(name)", name: name))
            note(L10n.text("chat_note_archive_requested"))
            return true
        } catch {
            note(L10n.format("chat_note_failed", errorText(error)), error: true)
            return false
        }
    }

    /// Imports an archive; the summary lines to show.
    func importArchive(_ url: URL) async -> String {
        let secured = url.startAccessingSecurityScopedResource()
        defer { if secured { url.stopAccessingSecurityScopedResource() } }
        guard let bytes = try? Data(contentsOf: url) else { return L10n.text("chat_import_unreadable") }
        let body: Data
        let ok: Bool
        do {
            body = try await api.importArchive(bytes)
            ok = true
        } catch let error as SkychatError {
            body = Data(error.message.utf8)
            ok = false
        } catch {
            return L10n.format("chat_import_failed", errorText(error))
        }
        guard let report = ImportReport(data: body) else {
            let text = String(decoding: body, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
            if ok { return L10n.text("chat_import_done") }
            return text.isEmpty ? L10n.format("chat_import_failed", "") : text
        }
        await syncNames()
        await catchUp()
        return Self.importSummary(report)
    }

    static func importSummary(_ r: ImportReport) -> String {
        var lines: [String] = []
        if !r.error.isEmpty { lines.append(L10n.format("chat_import_stopped", r.error)) }
        var items: [String] = []
        if r.messages > 0 { items.append(r.messages == 1 ? L10n.format("chat_import_message_one", r.messages) : L10n.format("chat_import_message_many", r.messages)) }
        if r.groupMessages > 0 { items.append(r.groupMessages == 1 ? L10n.format("chat_import_group_one", r.groupMessages) : L10n.format("chat_import_group_many", r.groupMessages)) }
        if r.contacts > 0 { items.append(r.contacts == 1 ? L10n.format("chat_import_name_one", r.contacts) : L10n.format("chat_import_name_many", r.contacts)) }
        if !items.isEmpty { lines.append(L10n.format("chat_import_items", items.joined(separator: ", "))) }
        if r.duplicates > 0 { lines.append(L10n.format("chat_import_duplicates", r.duplicates)) }
        if r.persistence == false { lines.append(L10n.text("chat_import_no_persist")) }
        if r.evicted > 0 { lines.append(L10n.format("chat_import_evicted", r.evicted)) }
        if r.expiring > 0 { lines.append(L10n.format("chat_import_expiring", r.expiring)) }
        if r.rejected > 0 { lines.append(L10n.format("chat_import_rejected", r.rejected)) }
        if items.isEmpty { lines.append(L10n.text("chat_import_nothing")) }
        return lines.joined(separator: "\n")
    }

    // MARK: Ringback

    func ringbackInfo() async -> RingbackInfo? { try? await api.ringbackInfo() }

    /// Uploads a ringback tone; nil on success, else what went wrong.
    func setRingback(_ url: URL) async -> String? {
        let secured = url.startAccessingSecurityScopedResource()
        defer { if secured { url.stopAccessingSecurityScopedResource() } }
        guard let bytes = try? Data(contentsOf: url) else { return L10n.text("chat_import_unreadable") }
        if bytes.count > Self.ringbackMax { return L10n.format("chat_ringback_too_big", Skychat.humanSize(Int64(bytes.count))) }
        let type: String
        switch url.pathExtension.lowercased() {
        case "mp3": type = "audio/mpeg"
        case "ogg", "oga", "opus": type = "audio/ogg"
        case "weba", "webm": type = "audio/webm"
        case "wav": type = "audio/wav"
        case "m4a": type = "audio/mp4"
        case "aac": type = "audio/aac"
        case "flac": type = "audio/flac"
        default: return L10n.text("chat_ringback_bad_type")
        }
        do {
            try await api.setRingback(bytes, type: type)
            return nil
        } catch {
            return errorText(error)
        }
    }

    func clearRingback() async -> String? {
        do {
            try await api.clearRingback()
            return nil
        } catch {
            return errorText(error)
        }
    }

    static let ringbackMax = 1024 * 1024

    // MARK: Search

    /// Server hits first, then the local threads; the same message within two minutes counts once.
    func searchMessages(_ q: String) async -> [ChatSearchResult] {
        let query = q.trimmingCharacters(in: .whitespacesAndNewlines)
        let needle = query.lowercased()
        guard !needle.isEmpty else { return [] }
        let server: [ChatSearchResult] = ((try? await api.search(query)) ?? []).compactMap { h in
            guard let ts = Skychat.parseTime(h.ts) else { return nil }
            let key = h.groupId.isEmpty ? h.peer.lowercased() : h.groupId
            return ChatSearchResult(key: key, isGroup: !h.groupId.isEmpty, from: h.outgoing ? Skychat.me : (h.from.isEmpty ? h.peer : h.from).lowercased(), text: h.text, ts: ts)
        }
        var local: [ChatSearchResult] = []
        for key in threadKeys {
            for m in messages(key) where m.text.lowercased().contains(needle) {
                local.append(ChatSearchResult(key: key, isGroup: isGroupKey(key), from: m.from, text: m.text, ts: m.ts))
            }
        }
        var out: [ChatSearchResult] = []
        for hit in server + local where !out.contains(where: { $0.key == hit.key && $0.text == hit.text && abs($0.ts - hit.ts) < 120_000 }) {
            out.append(hit)
        }
        return Array(out.sorted { $0.ts > $1.ts }.prefix(100))
    }
}
