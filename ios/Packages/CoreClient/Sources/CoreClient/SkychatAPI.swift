import Foundation

/// A non-2xx answer from skychat, with the body it explained itself in.
public struct SkychatError: LocalizedError, Sendable {
    public let status: Int
    public let message: String

    public var errorDescription: String? { message }
}

/// Every skychat endpoint the native chat uses, typed (Android: chat/SkychatClient.kt).
/// A thin layer: when to call what is the app's ChatRepository.
extension SkychatClient {
    /// Loopback reads and writes that stay on this visor.
    static let localTimeout: TimeInterval = 30
    /// Calls that reach a peer (a DM dial, a group join, a profile lookup) can take tens of seconds over dmsg.
    static let peerTimeout: TimeInterval = 90

    // MARK: Wire

    func request(_ method: HTTPMethod, _ path: String, body: Data? = nil, type: String = "application/json", peer: Bool = false) throws -> HTTPRequest {
        var headers = ["Authorization": Self.authorization(password: try password())]
        if body != nil { headers["Content-Type"] = type }
        return HTTPRequest(method: method, path: path, headers: headers, body: body, timeout: peer ? Self.peerTimeout : Self.localTimeout)
    }

    @discardableResult
    func call(_ method: HTTPMethod, _ path: String, json: [String: any Sendable]? = nil, peer: Bool = false) async throws -> Data {
        let body = try json.map { try JSONSerialization.data(withJSONObject: $0) }
        let response = try await transport.send(request(method, path, body: body ?? (method == .post ? Data("{}".utf8) : nil), peer: peer))
        return try Self.checked(response.status, response.body)
    }

    static func checked(_ status: Int, _ body: Data) throws -> Data {
        guard (200..<300).contains(status) else {
            let text = String(decoding: body, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
            throw SkychatError(status: status, message: text.isEmpty ? "HTTP \(status)" : text)
        }
        return body
    }

    private func fields(_ data: Data) -> JSONFields { JSONFields(data: data) ?? JSONFields([:]) }

    private func list<T>(_ data: Data, _ make: (JSONFields) -> T) -> [T] {
        jsonArray(data).compactMap { ($0 as? [String: Any]).map { make(JSONFields($0)) } }
    }

    /// Some group actions answer `{info: …}`, others the bare record.
    private func groupAnswer(_ data: Data) -> GroupInfo {
        let f = fields(data)
        return GroupInfo(f.object("info") ?? f)
    }

    private static func enc(_ s: String) -> String {
        var allowed = CharacterSet.alphanumerics
        allowed.insert(charactersIn: "-._~")
        return s.addingPercentEncoding(withAllowedCharacters: allowed) ?? s
    }

    // MARK: Identity, history

    public func status() async throws -> SkychatStatus { SkychatStatus(fields(try await call(.get, "/status"))) }

    public func history(peer: String, limit: Int = 100) async throws -> [HistoryRecord] {
        list(try await call(.get, "/history?peer=\(Self.enc(peer))&limit=\(limit)"), HistoryRecord.init)
    }

    /// The newest stored DMs across every peer, oldest first: what a reconnect catches up from.
    public func recentHistory(limit: Int) async throws -> [HistoryRecord] {
        list(try await call(.get, "/history?limit=\(limit)"), HistoryRecord.init)
    }

    public func historyPeers() async throws -> [String] {
        jsonArray(try await call(.get, "/history/peers")).compactMap { $0 as? String }
    }

    public func forget(pk: String, id: String? = nil) async throws {
        var body: [String: any Sendable] = ["pk": pk]
        if let id { body["id"] = id } else { body["all"] = true }
        try await call(.post, "/history/forget", json: body)
    }

    public func search(_ q: String) async throws -> [SearchHit] {
        list(try await call(.get, "/search?q=\(Self.enc(q))"), SearchHit.init)
    }

    public func reportUnread(_ n: Int) async throws {
        try await call(.post, "/unread", json: ["unread": n])
    }

    public func notifyFocus(_ key: String) async throws {
        try await call(.post, "/notify-focus", json: ["key": key, "focused": !key.isEmpty])
    }

    // MARK: DMs

    /// Sends a DM; the answer's id (empty when the server gave none).
    public func sendDm(pk: String, wire: String, network: String) async throws -> String {
        let body: [String: any Sendable] = ["recipient": pk, "message": wire, "network": network, "receipts": true]
        return fields(try await call(.post, "/message", json: body, peer: true)).string("id")
    }

    public func sendPair(pk: String, wire: String) async throws -> String {
        fields(try await call(.post, "/pair/\(pk)/message", json: ["text": wire], peer: true)).string("id")
    }

    public func readReceipt(pk: String, ids: [String]) async throws {
        try await call(.post, "/read-receipt", json: ["pk": pk, "ids": ids])
    }

    public func deleteDm(pk: String, id: String) async throws {
        try await call(.post, "/delete", json: ["pk": pk, "id": id], peer: true)
    }

    public func deletePair(pk: String, id: String) async throws {
        try await call(.delete, "/pair/\(pk)/message?id=\(Self.enc(id))")
    }

    public func linkStart(pk: String, network: String) async throws -> LinkState {
        LinkState(fields(try await call(.post, "/link", json: ["pk": pk, "network": network])))
    }

    public func linkState(pk: String) async throws -> LinkState {
        LinkState(fields(try await call(.get, "/link?pk=\(Self.enc(pk))")))
    }

    // MARK: Files

    public func requestFile(pk: String, fileId: String, fileName: String) async throws {
        try await call(.post, "/request-file", json: ["pk": pk, "file_id": fileId, "file_name": fileName])
    }

    /// Uploads `file` to `/send-file` as multipart, streamed from disk. `destField`
    /// is `pk`, `group` or `local` (Saved Messages). `progress` runs on the writer's thread.
    public func sendFile(
        destField: String, destValue: String, file: URL, name: String, mime: String,
        progress: @escaping @Sendable (_ sent: Int64, _ total: Int64) -> Void
    ) async throws -> FileSent {
        let boundary = "skywire-" + UUID().uuidString
        let total = (try? FileManager.default.attributesOfItem(atPath: file.path)[.size] as? Int64) ?? 0
        let quoted = name.replacingOccurrences(of: "\"", with: "%22").replacingOccurrences(of: "\r", with: "").replacingOccurrences(of: "\n", with: "")
        let head = Data((
            "--\(boundary)\r\nContent-Disposition: form-data; name=\"\(destField)\"\r\n\r\n\(destValue)\r\n" +
            "--\(boundary)\r\nContent-Disposition: form-data; name=\"file\"; filename=\"\(quoted)\"\r\nContent-Type: \(mime)\r\n\r\n"
        ).utf8)
        let tail = Data("\r\n--\(boundary)--\r\n".utf8)
        var req = try request(.post, "/send-file", body: Data(), type: "multipart/form-data; boundary=\(boundary)", peer: true)
        req.timeout = Self.peerTimeout
        let response = try await transport.upload(req) { output in
            guard Self.write(head, to: output), let handle = try? FileHandle(forReadingFrom: file) else { return }
            defer { try? handle.close() }
            var sent: Int64 = 0
            while let chunk = try? handle.read(upToCount: 64 * 1024), !chunk.isEmpty {
                guard Self.write(chunk, to: output) else { return }
                sent += Int64(chunk.count)
                progress(sent, total)
            }
            _ = Self.write(tail, to: output)
        }
        var body = Data()
        for try await chunk in response.body { body.append(chunk) }
        return FileSent(fields(try Self.checked(response.status, body)))
    }

    private static func write(_ data: Data, to output: OutputStream) -> Bool {
        data.withUnsafeBytes { raw in
            guard let base = raw.bindMemory(to: UInt8.self).baseAddress else { return true }
            var offset = 0
            while offset < data.count {
                let n = output.write(base + offset, maxLength: data.count - offset)
                if n <= 0 { return false }
                offset += n
            }
            return true
        }
    }

    /// Streams a served path (`/files/…`, `/thumb/…`, `/export/…`) into `destination`.
    public func download(_ path: String, to destination: URL) async throws {
        var req = try request(.get, path)
        req.timeout = Self.peerTimeout
        let response = try await transport.stream(req)
        let part = destination.appendingPathExtension("part")
        FileManager.default.createFile(atPath: part.path, contents: nil)
        let handle = try FileHandle(forWritingTo: part)
        do {
            if !response.isSuccess {
                var body = Data()
                for try await chunk in response.body { body.append(chunk) }
                _ = try Self.checked(response.status, body)
            }
            for try await chunk in response.body { try handle.write(contentsOf: chunk) }
            try handle.close()
        } catch {
            try? handle.close()
            try? FileManager.default.removeItem(at: part)
            throw error
        }
        try? FileManager.default.removeItem(at: destination)
        try FileManager.default.moveItem(at: part, to: destination)
    }

    public func bytes(_ path: String) async throws -> Data {
        try await call(.get, path)
    }

    // MARK: Event stream

    /// `/sse` as messages. The stream ends when the server closes it and throws when the connection fails.
    public func events() async throws -> AsyncThrowingStream<SseMessage, any Error> {
        var req = try request(.get, "/sse")
        req.timeout = nil
        req.headers["Accept"] = "text/event-stream"
        let response = try await transport.stream(req)
        if !response.isSuccess {
            var body = Data()
            for try await chunk in response.body { body.append(chunk) }
            _ = try Self.checked(response.status, body)
        }
        return AsyncThrowingStream { continuation in
            let task = Task {
                var parser = ServerSentEvents()
                do {
                    for try await chunk in response.body {
                        for data in parser.feed(chunk) {
                            if let message = SseMessage(json: data) { continuation.yield(message) }
                        }
                    }
                    continuation.finish()
                } catch {
                    continuation.finish(throwing: error)
                }
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }

    // MARK: Groups

    public func groups() async throws -> [GroupInfo] { list(try await call(.get, "/group"), GroupInfo.init) }

    public func groupHistory(gid: String, limit: Int, before: String?) async throws -> [GroupHistoryRecord] {
        let cursor = before.map { "&before=\(Self.enc($0))" } ?? ""
        return list(try await call(.get, "/group/\(gid)/history?limit=\(limit)\(cursor)"), GroupHistoryRecord.init)
    }

    public func groupSend(gid: String, wire: String) async throws {
        try await call(.post, "/group/\(gid)/send", json: ["text": wire], peer: true)
    }

    public func groupDeleteMessage(gid: String, tsNano: String) async throws {
        try await call(.delete, "/group/\(gid)/message?ts=\(Self.enc(tsNano))")
    }

    /// Creates a group or channel; its id and invite link.
    public func createGroup(name: String, kind: String, peerBackfill: Bool?, listed: Bool?) async throws -> (info: GroupInfo, invite: String) {
        var body: [String: any Sendable] = ["name": name, "kind": kind, "members": [String]()]
        if let peerBackfill { body["peer_backfill"] = peerBackfill }
        if let listed { body["listed"] = listed }
        let f = fields(try await call(.post, "/group", json: body, peer: true))
        return (GroupInfo(f.object("info") ?? JSONFields([:])), f.string("invite"))
    }

    public func resolve(_ address: String) async throws -> Resolved {
        Resolved(fields(try await call(.get, "/group/resolve?address=\(Self.enc(address))", peer: true)))
    }

    public func catalog(pk: String) async throws -> Catalog {
        Catalog(fields(try await call(.get, "/group/catalog?pk=\(Self.enc(pk))", peer: true)))
    }

    public func join(invite: String?, address: String?) async throws -> GroupInfo {
        let body: [String: any Sendable] = if let invite, !invite.isEmpty { ["invite": invite] } else { ["address": address ?? ""] }
        return groupAnswer(try await call(.post, "/group/join", json: body, peer: true))
    }

    public func groupRequests(gid: String) async throws -> [JoinRequest] {
        list(try await call(.get, "/group/\(gid)/requests"), JoinRequest.init)
    }

    /// `members/promote` … `requests/deny`; answers with the updated group.
    public func groupMemberAction(gid: String, action: String, pk: String) async throws -> GroupInfo {
        groupAnswer(try await call(.post, "/group/\(gid)/\(action)", json: ["pk": pk], peer: true))
    }

    public func rotateKey(gid: String) async throws -> GroupInfo {
        groupAnswer(try await call(.post, "/group/\(gid)/rotate-key", peer: true))
    }

    public func setJoinPow(gid: String, bits: Int) async throws -> GroupInfo {
        groupAnswer(try await call(.post, "/group/\(gid)/join-pow", json: ["bits": bits]))
    }

    public func setPeerBackfill(gid: String, enabled: Bool) async throws -> GroupInfo {
        groupAnswer(try await call(.post, "/group/\(gid)/peer-backfill", json: ["enabled": enabled]))
    }

    public func setGroupMeta(gid: String, name: String? = nil, avatar: String? = nil) async throws -> GroupInfo {
        var body: [String: any Sendable] = [:]
        if let name { body["name"] = name }
        if let avatar { body["avatar"] = avatar }
        return groupAnswer(try await call(.post, "/group/\(gid)/meta", json: body, peer: true))
    }

    public func refreshMeta(gid: String) async throws -> GroupInfo {
        groupAnswer(try await call(.post, "/group/\(gid)/refresh-meta", peer: true))
    }

    public func setListed(gid: String, listed: Bool) async throws {
        try await call(.post, "/group/\(gid)/listed", json: ["listed": listed])
    }

    public func setReadOnly(gid: String, readOnly: Bool) async throws -> GroupInfo {
        groupAnswer(try await call(.post, "/group/\(gid)/readonly", json: ["read_only": readOnly], peer: true))
    }

    public func invite(gid: String) async throws -> String {
        fields(try await call(.get, "/group/\(gid)/invite")).string("invite")
    }

    public func leave(gid: String) async throws {
        try await call(.post, "/group/\(gid)/leave", peer: true)
    }

    public func deleteGroup(gid: String) async throws {
        try await call(.delete, "/group/\(gid)")
    }

    // MARK: People

    /// The address book's names, throwing where `contacts()` swallows a failure.
    public func contactNames() async throws -> [String: String] {
        let raw = (try? JSONSerialization.jsonObject(with: try await call(.get, "/contacts"))) as? [String: Any] ?? [:]
        return raw.compactMapValues { $0 as? String }
    }

    public func setContactName(pk: String, name: String) async throws {
        try await call(.post, "/contacts", json: ["pk": pk, "name": name])
    }

    public func importContacts(_ names: [String: String]) async throws {
        try await call(.post, "/contacts/import", json: ["names": names])
    }

    public func profile() async throws -> SkychatProfileInfo { SkychatProfileInfo(fields(try await call(.get, "/profile"))) }

    /// Publishes the name; a POST without `avatar` clears the picture, so callers resend the current one.
    public func setProfile(name: String, avatarDataURL: String?) async throws -> SkychatProfileInfo {
        var body: [String: any Sendable] = ["name": name]
        if let avatarDataURL { body["avatar"] = avatarDataURL }
        return SkychatProfileInfo(fields(try await call(.post, "/profile", json: body, peer: true)))
    }

    public func clearProfile() async throws -> SkychatProfileInfo {
        SkychatProfileInfo(fields(try await call(.post, "/profile", json: ["clear": true])))
    }

    public func peerProfile(pk: String) async throws -> SkychatProfileInfo {
        SkychatProfileInfo(fields(try await call(.get, "/profile/peer?pk=\(Self.enc(pk))", peer: true)))
    }

    public func presence(_ pks: [String]) async throws -> [String: PresencePeer] {
        let peers = fields(try await call(.post, "/presence", json: ["pks": pks])).object("peers")?.raw ?? [:]
        var out: [String: PresencePeer] = [:]
        for (key, value) in peers {
            if let object = value as? [String: Any] { out[key.lowercased()] = PresencePeer(JSONFields(object)) }
        }
        return out
    }

    public func privacy() async throws -> PrivacyState { PrivacyState(fields(try await call(.get, "/privacy"))) }

    public func setApproval(_ on: Bool) async throws -> PrivacyState {
        PrivacyState(fields(try await call(.post, "/privacy", json: ["approval": on])))
    }

    public func accept(pk: String, accepted: Bool = true) async throws -> PrivacyState {
        var body: [String: any Sendable] = ["pk": pk]
        if !accepted { body["accepted"] = false }
        return PrivacyState(fields(try await call(.post, "/privacy/accept", json: body)))
    }

    public func block(pk: String, blocked: Bool) async throws -> PrivacyState {
        PrivacyState(fields(try await call(.post, "/privacy/block", json: ["pk": pk, "blocked": blocked])))
    }

    // MARK: Pairing

    public func pairs() async throws -> [PairRecord] { list(try await call(.get, "/pair"), PairRecord.init) }

    public func pair(pk: String) async throws {
        try await call(.post, "/pair", json: ["peer_pk": pk], peer: true)
    }

    public func unpair(pk: String) async throws {
        try await call(.delete, "/pair/\(pk)")
    }

    public func pairInvites() async throws -> [String] {
        list(try await call(.get, "/pair/invites")) { $0.string("peer_pk") }
    }

    public func acceptInvite(pk: String) async throws {
        try await call(.post, "/pair/invites/\(pk)/accept", peer: true)
    }

    public func declineInvite(pk: String) async throws {
        try await call(.post, "/pair/invites/\(pk)/decline")
    }

    // MARK: Calls

    public func calls() async throws -> [CallRecord] { list(try await call(.get, "/calls"), CallRecord.init) }

    public func voiceAvailable() async -> Bool { (try? await call(.get, "/voice/active")) != nil }

    /// Rings `pk`; the call's id.
    public func placeCall(pk: String) async throws -> String {
        fields(try await call(.post, "/voice/call", json: ["peer": pk], peer: true)).string("call_id")
    }

    public func ringbackInfo() async throws -> RingbackInfo {
        RingbackInfo(fields(try await call(.get, "/voice/my-ringback?info=1")))
    }

    public func setRingback(_ bytes: Data, type: String) async throws {
        let response = try await transport.send(request(.put, "/voice/my-ringback", body: bytes, type: type, peer: true))
        _ = try Self.checked(response.status, response.body)
    }

    public func clearRingback() async throws {
        try await call(.delete, "/voice/my-ringback")
    }

    // MARK: Archive

    /// Imports an archive; the server's answer, which reports what it took.
    public func importArchive(_ body: Data) async throws -> Data {
        let response = try await transport.send(request(.post, "/import", body: body, peer: true))
        return try Self.checked(response.status, response.body)
    }
}
