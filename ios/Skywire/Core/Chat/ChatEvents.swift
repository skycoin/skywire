import CoreClient
import Foundation
import UIKit

/// Applies `/sse` events (Android: chat/SseDispatch.kt, index.html _sseSubscribe, in the same order).
extension ChatRepository {
    private static let orphans = 64

    private var foreground: Bool { UIApplication.shared.applicationState == .active }

    func handle(_ m: SseMessage) {
        if m.history { return }
        switch m.channel {
        case "pair-invite": pairInvite(m)
        case "group": group(m)
        case "group-delete": groupDelete(m)
        case "group-join-request": joinRequest(m)
        case "file-ready": fileReady(m)
        case "dm-status": dmStatus(m)
        case "pair": dm(m, viaPair: true)
        case "": if m.dir == "out" { outbound(m) } else { dm(m, viaPair: false) }
        default: break
        }
    }

    private func dm(_ m: SseMessage, viaPair: Bool) {
        let sender = m.sender.lowercased()
        guard Skychat.isPk(sender), sender != myPk else { return }
        if !m.id.isEmpty && messages(sender).contains(where: { $0.dmWireId == m.id }) { return }
        let body = viaPair ? Skychat.unwrap(m.message) : Skychat.Unwrapped(
            text: m.message,
            reply: Skychat.replyOf(sender: m.replyToSender, ts: m.replyToTs, preview: m.replyToPreview),
            forwarded: m.forwarded, forwardFrom: m.forwardFrom
        )
        let file = m.fileId.isEmpty ? nil : ChatFile(
            id: m.fileId, name: m.fileName.isEmpty ? "file" : m.fileName, size: m.fileSize,
            status: m.fileStatus, url: m.fileUrl, from: sender
        )
        let msg = ChatMessage(
            from: sender, text: body.text, viaPair: viaPair, file: file, reply: body.reply,
            forwarded: body.forwarded, forwardFrom: body.forwardFrom, wireId: m.id, request: m.request
        )
        if m.request, var p = privacy, !p.requests.contains(sender) {
            p.requests.append(sender)
            privacy = p
        }
        ensureRecipient(sender)
        appendMessage(sender, msg)
        if openKey == sender && atBottom && foreground {
            markRead(sender)
            Task { await sendReadReceipts(sender) }
        }
    }

    /// Our own send, mirrored before `/message` answers. It carries the id,
    /// which matters when the answer itself is lost to the server's 10 s write
    /// deadline. Outgoing call records only ever arrive this way.
    private func outbound(_ m: SseMessage) {
        if m.network == "voice" {
            let peer = m.sender.lowercased()
            guard Skychat.isPk(peer), !messages(peer).contains(where: { $0.wireId == m.id }) else { return }
            ensureRecipient(peer)
            appendMessage(peer, ChatMessage(text: m.message, wireId: m.id, status: "sent"))
            return
        }
        let peer = m.to.lowercased()
        guard Skychat.isPk(peer), !m.id.isEmpty, m.fileId.isEmpty else { return }
        let list = messages(peer)
        if list.contains(where: { $0.wireId == m.id }) { return }
        let waiting: Set<String> = ["pending", "failed", "queued"]
        guard let target = list.last(where: { $0.mine && $0.wireId.isEmpty && $0.file == nil && $0.text == m.message && waiting.contains($0.status) }) else {
            // Sent from elsewhere on this visor (the CLI): it is still ours.
            ensureRecipient(peer)
            appendMessage(peer, ChatMessage(
                text: m.message, reply: Skychat.replyOf(sender: m.replyToSender, ts: m.replyToTs, preview: m.replyToPreview),
                forwarded: m.forwarded, forwardFrom: m.forwardFrom, wireId: m.id, status: "sent"
            ))
            return
        }
        updateMessage(peer, target.key) { msg in
            msg.wireId = m.id
            msg.status = Skychat.advance(msg.status, "sent")
        }
        claimOrphan(peer, target.key, m.id)
    }

    private func dmStatus(_ m: SseMessage) {
        let peer = m.peer.lowercased()
        if m.status == "deleted" {
            mutate(peer) { list in list.filter { $0.dmWireId != m.id } }
            return
        }
        guard let target = messages(peer).last(where: { $0.mine && ($0.wireId == m.id || $0.file?.id == m.id) }) else {
            orphanStatus.removeAll { $0.id == m.id }
            orphanStatus.append((m.id, m.status))
            if orphanStatus.count > Self.orphans { orphanStatus.removeFirst(orphanStatus.count - Self.orphans) }
            return
        }
        updateMessage(peer, target.key) { $0.status = Skychat.advance($0.status, m.status) }
    }

    /// A receipt that beat the send's answer is applied once the bubble knows its id.
    func claimOrphan(_ key: String, _ msgKey: String, _ id: String) {
        guard let i = orphanStatus.firstIndex(where: { $0.id == id }) else { return }
        let status = orphanStatus.remove(at: i).status
        updateMessage(key, msgKey) { $0.status = Skychat.advance($0.status, status) }
    }

    private func fileReady(_ m: SseMessage) {
        for key in threadKeys where messages(key).contains(where: { $0.file?.id == m.fileId }) {
            mutate(key) { list in
                list.map { msg in
                    guard var f = msg.file, f.id == m.fileId else { return msg }
                    var msg = msg
                    if !m.fileUrl.isEmpty { f.url = m.fileUrl }
                    f.status = "received"
                    msg.file = f
                    return msg
                }
            }
        }
    }

    private func group(_ m: SseMessage) {
        if m.message == Skychat.heartbeat { return }
        let gid = m.groupId
        let sender = m.sender.lowercased()
        let nano = m.tsNano
        let me = myPk
        if groupInfo(gid) == nil { Task { await syncGroups() } }
        if !nano.isEmpty && store.state.hiddenGroup.contains("\(gid):\(nano)") { return }
        let list = messages(gid)
        if !nano.isEmpty && list.contains(where: { $0.tsNano == nano && ($0.from == sender || ($0.mine && sender == me)) }) { return }
        if sender == me {
            // Our own post coming back: give the pending bubble its feed id.
            if let own = list.last(where: { $0.mine && $0.tsNano.isEmpty && ($0.text == m.message || (!m.fileId.isEmpty && $0.file?.id == m.fileId)) }) {
                updateMessage(gid, own.key) { msg in
                    msg.tsNano = nano
                    msg.status = Skychat.advance(msg.status, "sent")
                }
                return
            }
        }
        let file = m.fileId.isEmpty ? nil : ChatFile(
            id: m.fileId, name: m.fileName.isEmpty ? "file" : m.fileName, size: m.fileSize,
            url: m.fileUrl, from: sender == me ? "" : sender
        )
        appendMessage(gid, ChatMessage(
            ts: Skychat.parseTime(m.ts) ?? Skychat.nowMs(), from: sender == me ? Skychat.me : sender, text: m.message,
            file: file, reply: Skychat.replyOf(sender: m.replyToSender, ts: m.replyToTs, preview: m.replyToPreview),
            forwarded: m.forwarded, forwardFrom: m.forwardFrom, status: sender == me ? "sent" : "", tsNano: nano
        ))
        let open = openKey == gid && foreground
        if sender != me && !open {
            groupUnread[gid, default: 0] += 1
            recompute(gid)
        }
    }

    private func groupDelete(_ m: SseMessage) {
        let nano = m.deletedTsNano
        guard !nano.isEmpty else { return }
        let gid = m.groupId
        store.update { $0.hiddenGroup.insert("\(gid):\(nano)") }
        let by = m.deletedBy.lowercased()
        let me = myPk
        mutate(gid) { Skychat.withoutDeleted($0, nano: nano, by: by, me: me) }
    }

    private func joinRequest(_ m: SseMessage) {
        Task {
            await syncGroups()
            signalGroupChanged(m.groupId)
        }
        let open = openKey == m.groupId && foreground
        if !open {
            if m.groupName.isEmpty {
                note(L10n.text("chat_note_join_request"))
            } else {
                note(L10n.format("chat_note_join_request_named", m.groupName))
            }
        }
    }

    private func pairInvite(_ m: SseMessage) {
        let peer = m.peer.lowercased()
        let short = String(peer.prefix(8))
        switch m.event {
        case "received":
            if !pair.invites.contains(peer) { pair.invites.append(peer) }
            note(L10n.format("chat_note_pair_invite", short))
        case "accepted":
            pair.paired.insert(peer)
            ensureRecipient(peer)
            note(L10n.format("chat_note_pair_confirmed", short))
        case "declined":
            pair.paired.remove(peer)
            note(L10n.format("chat_note_pair_declined", short), error: true)
        default:
            break
        }
        Task { await syncInvites() }
    }
}
