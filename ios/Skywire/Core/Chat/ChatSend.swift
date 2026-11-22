import CoreClient
import Foundation
import UniformTypeIdentifiers

// Sending, files, deleting and forwarding (Android: chat/ChatSend.kt).

// MARK: The peer link

/// Dial first, send after (index.html _linkWatch). Only the open DM is
/// watched; messages typed before the route is up wait in order.
extension ChatRepository {
    func watchLink(_ pk: String?) {
        if let watch = linkWatch, watch.pk == pk, !watch.task.isCancelled { return }
        linkWatch?.task.cancel()
        linkWatch = nil
        guard let pk, !linkOff, network != "cxo" else { return }
        let task = Task { [weak self] in
            guard let self, var state = await self.startLink(pk) else { return }
            while !Task.isCancelled {
                try? await Task.sleep(for: state.ready ? .seconds(8) : .milliseconds(1500))
                if Task.isCancelled { return }
                do {
                    state = try await self.api.linkState(pk: pk)
                } catch is SkychatError {
                    self.disableLinks()
                    return
                } catch {
                    try? await Task.sleep(for: .seconds(4))
                    continue
                }
                self.setLink(pk, state)
                if !state.ready && !state.connecting && state.error.isEmpty {
                    guard let restarted = await self.startLink(pk) else { return }
                    state = restarted
                }
            }
        }
        linkWatch = (pk, task)
    }

    /// Retry from the link bar.
    func retryLink(_ pk: String) {
        setLink(pk, LinkState(connecting: true))
        linkWatch?.task.cancel()
        linkWatch = nil
        watchLink(pk)
    }

    /// The route changed: dial again over it (or stop watching for CXO).
    func rewatchLink(_ pk: String) {
        watchLink(nil)
        watchLink(pk)
    }

    private func startLink(_ pk: String) async -> LinkState? {
        do {
            let state = try await api.linkStart(pk: pk, network: network)
            setLink(pk, state)
            return state
        } catch is SkychatError {
            disableLinks()
            return nil
        } catch {
            return LinkState()
        }
    }

    private func setLink(_ pk: String, _ state: LinkState) {
        let was = links[pk]?.ready == true
        links[pk] = state
        if state.ready && !was { flushOutbox(pk) }
    }

    /// No `/link` on this skychat: send straight away from now on.
    private func disableLinks() {
        linkOff = true
        for pk in outbox.keys { flushOutbox(pk) }
    }

    /// Waits for `pk`'s link up to `timeout`; true when it is ready (or links are off).
    func awaitLink(_ pk: String, timeout: Duration) async -> Bool {
        if linkOff || links[pk]?.ready == true { return true }
        do {
            setLink(pk, try await api.linkStart(pk: pk, network: network))
        } catch is SkychatError {
            disableLinks()
            return true
        } catch {}
        let deadline = ContinuousClock.now + timeout
        while links[pk]?.ready != true {
            if ContinuousClock.now >= deadline || Task.isCancelled { return false }
            try? await Task.sleep(for: .seconds(1))
            if let state = try? await api.linkState(pk: pk) { setLink(pk, state) }
        }
        return true
    }
}

// MARK: Text

extension ChatRepository {
    private func wire(_ text: String, reply: ReplyRef?, forwarded: Bool, forwardFrom: String) -> String {
        if var reply {
            if reply.sender == Skychat.me { reply.sender = myPk }
            return Skychat.replyEnvelope(text, reply)
        }
        return forwarded ? Skychat.forwardEnvelope(text, from: forwardFrom) : text
    }

    /// Sends text to a thread: a note, a group post, or a DM (index.html sendMessage).
    func sendText(_ key: String, _ raw: String, reply: ReplyRef?) {
        let text = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return }
        if key == Skychat.savedKey {
            appendMessage(key, ChatMessage(text: text, reply: reply, status: "sent"))
        } else if isGroupKey(key) {
            sendGroup(key, text, reply: reply, forwarded: false, forwardFrom: "")
        } else {
            sendDm(key, text, reply: reply, forwarded: false, forwardFrom: "", allowHold: true)
        }
        markRead(key)
    }

    func sendDm(_ pk: String, _ text: String, reply: ReplyRef?, forwarded: Bool, forwardFrom: String, allowHold: Bool) {
        var net = network
        if net == "cxo" && !pair.paired.contains(pk) { net = "skynet" }
        let body = wire(text, reply: reply, forwarded: forwarded, forwardFrom: forwardFrom)
        let hold = allowHold && net != "cxo" && !linkOff && links[pk]?.ready != true
        let msg = ChatMessage(text: text, viaPair: net == "cxo", reply: reply, forwarded: forwarded, forwardFrom: forwardFrom, status: hold ? "queued" : "pending")
        ensureRecipient(pk)
        appendMessage(pk, msg)
        // Replying to a request lets them in; the visor does the same when it goes out.
        privacy?.requests.removeAll { $0 == pk }
        if hold {
            outbox[pk, default: []].append(Outgoing(msgKey: msg.key, wire: body, network: net))
            watchLink(pk)
            return
        }
        Task { await deliverDm(pk, msg.key, body, net) }
    }

    func deliverDm(_ pk: String, _ msgKey: String, _ body: String, _ net: String) async {
        do {
            let id = net == "cxo" ? try await api.sendPair(pk: pk, wire: body) : try await api.sendDm(pk: pk, wire: body, network: net)
            updateMessage(pk, msgKey) { m in
                if m.wireId.isEmpty { m.wireId = id }
                m.status = Skychat.advance(m.status, "sent")
            }
            if !id.isEmpty { claimOrphan(pk, msgKey, id) }
        } catch {
            // The mirror on /sse may already have proved it went out: a lost answer is not a failure.
            if let m = findMessage(pk, msgKey), !m.wireId.isEmpty { return }
            updateMessage(pk, msgKey) { $0.status = Skychat.advance($0.status, "failed") }
            note(L10n.format("chat_note_failed", errorText(error)), error: true)
        }
    }

    /// Sends what waited for the link, one at a time, in typing order.
    func flushOutbox(_ pk: String) {
        guard let held = outbox.removeValue(forKey: pk) else { return }
        Task {
            for o in held {
                updateMessage(pk, o.msgKey) { $0.status = "pending" }
                await deliverDm(pk, o.msgKey, o.wire, o.network)
            }
        }
    }

    func sendAnyway(_ pk: String) { flushOutbox(pk) }

    func sendGroup(_ gid: String, _ text: String, reply: ReplyRef?, forwarded: Bool, forwardFrom: String) {
        let body = wire(text, reply: reply, forwarded: forwarded, forwardFrom: forwardFrom)
        let msg = ChatMessage(text: text, reply: reply, forwarded: forwarded, forwardFrom: forwardFrom, status: "pending")
        appendMessage(gid, msg)
        Task { await deliverGroup(gid, msg.key, body) }
    }

    private func deliverGroup(_ gid: String, _ msgKey: String, _ body: String) async {
        do {
            try await api.groupSend(gid: gid, wire: body)
            updateMessage(gid, msgKey) { $0.status = Skychat.advance($0.status, "sent") }
        } catch {
            if let m = findMessage(gid, msgKey), !m.tsNano.isEmpty { return }
            updateMessage(gid, msgKey) { $0.status = Skychat.advance($0.status, "failed") }
            note(L10n.format("chat_note_group_send_failed", errorText(error)), error: true)
        }
    }

    /// ⚠ Resend on a failed bubble; the reply is re-encoded from the bubble itself.
    func resend(_ key: String, _ msgKey: String) {
        guard let m = findMessage(key, msgKey) else { return }
        if m.file != nil {
            retryFile(key, msgKey)
            return
        }
        updateMessage(key, msgKey) { $0.status = "pending" }
        let body = wire(m.text, reply: m.reply, forwarded: m.forwarded, forwardFrom: m.forwardFrom)
        if isGroupKey(key) {
            Task { await deliverGroup(key, msgKey, body) }
        } else {
            let net = m.viaPair && pair.paired.contains(key) ? "cxo" : (network == "cxo" ? "skynet" : network)
            Task { await deliverDm(key, msgKey, body, net) }
        }
    }
}

// MARK: Files

/// What a recorded or attached upload is, only for the note after it.
enum UploadKind {
    case attachment, voice, video
}

extension ChatRepository {
    /// Copies a picked file into the upload folder so the upload can be retried and previewed; its relative path.
    nonisolated static func stage(_ source: URL, name: String? = nil) throws -> String {
        let folder = UUID().uuidString
        let fileName = ChatDownloads.fileName(name ?? source.lastPathComponent)
        let dir = ChatStore.uploads.appendingPathComponent(folder, isDirectory: true)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let secured = source.startAccessingSecurityScopedResource()
        defer { if secured { source.stopAccessingSecurityScopedResource() } }
        try FileManager.default.copyItem(at: source, to: dir.appendingPathComponent(fileName))
        return folder + "/" + fileName
    }

    static func mime(for name: String) -> String {
        let ext = (name as NSString).pathExtension.lowercased()
        switch ext {
        case "oga", "ogg", "opus": return "audio/ogg"
        case "m4a": return "audio/mp4"
        default: return UTType(filenameExtension: ext)?.preferredMIMEType ?? "application/octet-stream"
        }
    }

    /// Uploads a staged file to `key` (index.html _uploadFile). `caption` is the
    /// bubble's local text, the recorded-clip captions; the wire carries none.
    func sendFile(_ key: String, staged: String, caption: String? = nil, kind: UploadKind = .attachment) {
        guard let url = ChatStore.uploadURL(staged) else { return }
        let name = url.lastPathComponent
        let size = (try? FileManager.default.attributesOfItem(atPath: url.path)[.size] as? Int64) ?? 0
        let msg = ChatMessage(text: caption ?? name, file: ChatFile(name: name, size: size, status: "sending", localPath: staged))
        if Skychat.isPk(key) { ensureRecipient(key) }
        appendMessage(key, msg)
        markRead(key)
        Task { await upload(key, msg.key, url, kind) }
    }

    func retryFile(_ key: String, _ msgKey: String) {
        guard let m = findMessage(key, msgKey), var f = m.file, f.status == "failed",
              Skychat.isPk(key) || key == Skychat.savedKey || isGroupKey(key),
              ChatStore.hasUpload(f.localPath), let url = ChatStore.uploadURL(f.localPath)
        else { return }
        f.status = "sending"
        updateMessage(key, msgKey) { $0.file = f }
        Task { await upload(key, msgKey, url, .attachment) }
    }

    private func upload(_ key: String, _ msgKey: String, _ file: URL, _ kind: UploadKind) async {
        let saved = key == Skychat.savedKey
        let (field, value) = saved ? ("local", "1") : isGroupKey(key) ? ("group", key) : ("pk", key)
        // A 1:1 file is accepted only over a live chat conn (filexfer.go), so open the link first.
        if field == "pk" { _ = await awaitLink(key, timeout: .seconds(25)) }
        defer {
            Task {
                try? await Task.sleep(for: .milliseconds(1400))
                uploads[msgKey] = nil
            }
        }
        do {
            let sent = try await api.sendFile(
                destField: field, destValue: value, file: file, name: file.lastPathComponent,
                mime: Self.mime(for: file.lastPathComponent)
            ) { [weak self] sent, total in
                Task { @MainActor in self?.uploads[msgKey] = Upload(sent: sent, total: total) }
            }
            updateMessage(key, msgKey) { m in
                guard var f = m.file else { return }
                if !sent.id.isEmpty { f.id = sent.id }
                if !sent.fileUrl.isEmpty { f.url = sent.fileUrl }
                f.status = saved ? "stored" : "sent"
                m.file = f
            }
            if !sent.id.isEmpty { claimOrphan(key, msgKey, sent.id) }
            switch (kind, saved) {
            case (.voice, true): note(L10n.text("chat_note_voice_saved"))
            case (.voice, false): note(L10n.text("chat_note_voice_sent"))
            case (.video, true): note(L10n.text("chat_note_video_saved"))
            case (.video, false): note(L10n.text("chat_note_video_sent"))
            case (.attachment, true): note(L10n.text("chat_note_saved"))
            case (.attachment, false): note(L10n.text("chat_note_file_sent"))
            }
        } catch {
            updateMessage(key, msgKey) { m in m.file?.status = "failed" }
            if saved {
                note(L10n.format("chat_note_save_file_failed", errorText(error)), error: true)
            } else {
                note(L10n.format("chat_note_file_failed", errorText(error)), error: true)
            }
        }
    }

    func requestFile(_ m: ChatMessage) {
        guard let f = m.file, !f.from.isEmpty, !f.id.isEmpty else { return }
        Task {
            do {
                try await api.requestFile(pk: f.from, fileId: f.id, fileName: f.name)
                note(L10n.text("chat_note_requested_file"))
            } catch {
                note(L10n.format("chat_note_request_failed", errorText(error)), error: true)
            }
        }
    }

    /// Fetches a served file and hands it to the share sheet, which is how iOS saves one.
    func download(_ m: ChatMessage) {
        guard let f = m.file, f.url.hasPrefix("/files/") else { return }
        note(L10n.format("chat_note_downloading", f.name))
        Task {
            do {
                share = SharedFile(url: try await shareCopy(f.url, name: f.name))
            } catch {
                note(L10n.format("chat_note_failed", errorText(error)), error: true)
            }
        }
    }

    /// A served path written to a folder of its own under the temporary directory.
    func shareCopy(_ path: String, name: String) async throws -> URL {
        let folder = FileManager.default.temporaryDirectory
            .appendingPathComponent("chat-downloads", isDirectory: true)
            .appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        let target = folder.appendingPathComponent(ChatDownloads.fileName(name))
        if let cached = media.cachedFile(path) {
            try FileManager.default.copyItem(at: cached, to: target)
        } else {
            try await api.download(path, to: target)
        }
        return target
    }

    func shareDone(_ file: SharedFile) {
        if share?.id == file.id { share = nil }
        try? FileManager.default.removeItem(at: file.url.deletingLastPathComponent())
    }
}

// MARK: Deleting, forwarding

extension ChatRepository {
    func deleteForMe(_ key: String, _ m: ChatMessage) {
        mutate(key) { $0.filter { $0.key != m.key } }
        if key == Skychat.savedKey { return }
        if isGroupKey(key) {
            if !m.tsNano.isEmpty { store.update { $0.hiddenGroup.insert("\(key):\(m.tsNano)") } }
        } else if !m.dmWireId.isEmpty {
            let id = m.dmWireId
            Task { try? await api.forget(pk: key, id: id) }
        }
    }

    /// Own messages only: a group needs its feed id, a DM its wire id.
    func canDeleteForEveryone(_ key: String, _ m: ChatMessage) -> Bool {
        m.mine && key != Skychat.savedKey
    }

    func deleteForEveryone(_ key: String, _ m: ChatMessage) {
        if isGroupKey(key) {
            guard !m.tsNano.isEmpty else {
                note(L10n.text("chat_note_not_confirmed"), error: true)
                return
            }
            store.update { $0.hiddenGroup.insert("\(key):\(m.tsNano)") }
            mutate(key) { $0.filter { $0.key != m.key } }
            Task {
                do {
                    try await api.groupDeleteMessage(gid: key, tsNano: m.tsNano)
                } catch {
                    note(L10n.format("chat_note_delete_failed", errorText(error)), error: true)
                }
            }
            return
        }
        let id = m.dmWireId
        guard !id.isEmpty else {
            note(L10n.text("chat_note_not_confirmed"), error: true)
            return
        }
        mutate(key) { $0.filter { $0.key != m.key } }
        Task {
            do {
                if m.viaPair {
                    try await api.deletePair(pk: key, id: id)
                } else {
                    try await api.deleteDm(pk: key, id: id)
                }
            } catch {
                note(L10n.format("chat_note_delete_failed", errorText(error)), error: true)
            }
        }
    }

    /// Forwarding sends text only; out of a channel it may name the channel (forward.go).
    func forward(from fromKey: String, _ m: ChatMessage, to toKey: String) {
        var text = m.text.isEmpty ? (m.file?.name ?? "") : m.text
        if text.isEmpty { text = L10n.text("chat_attachment") }
        guard !text.trimmingCharacters(in: .whitespaces).isEmpty else {
            note(L10n.text("chat_note_nothing_forward"), error: true)
            return
        }
        let origin = groupInfo(fromKey).flatMap { $0.isChannel ? $0.name : nil } ?? ""
        if toKey == Skychat.savedKey {
            appendMessage(toKey, ChatMessage(text: text, forwarded: true, forwardFrom: origin, status: "sent"))
            note(L10n.text("chat_note_forwarded_saved"))
        } else if isGroupKey(toKey) {
            sendGroup(toKey, text, reply: nil, forwarded: true, forwardFrom: origin)
            note(L10n.text("chat_note_forwarded"))
        } else {
            sendDm(toKey, text, reply: nil, forwarded: true, forwardFrom: origin, allowHold: false)
            note(L10n.text("chat_note_forwarded"))
        }
    }

    func saveToSaved(_ m: ChatMessage) {
        let who = m.mine ? L10n.text("chat_you") : displayName(m.from)
        var body = m.text.isEmpty ? (m.file?.name ?? "") : m.text
        if body.isEmpty { body = L10n.text("chat_attachment") }
        appendMessage(Skychat.savedKey, ChatMessage(text: "\(who): \(body)", status: "sent"))
        note(L10n.text("chat_note_saved_to_saved"))
    }
}
