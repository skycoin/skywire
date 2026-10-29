import Foundation

// The wire details the native chat shares with skychat and its web UI
// (Android: chat/ChatMessage.kt, the envelopes in SkychatClient.kt and the
// pure parts of SseDispatch.kt). A mismatch shows up on the other side as a
// reply rendered as raw JSON, or a clip played with the wrong player.

public enum Skychat {
    /// The Saved Messages thread; neither a key nor a group id.
    public static let savedKey = "saved"
    /// Our own messages' author.
    public static let me = "me"
    /// skychat's keep-alive post on a group feed, never shown.
    public static let heartbeat = "__skychat_group_heartbeat__"
    public static let nameMax = 40

    public static func isPk(_ s: String) -> Bool {
        s.count == 66 && s.allSatisfy { $0.isHexDigit && !$0.isUppercase }
    }

    public static func isGroupKey(_ key: String) -> Bool {
        key != savedKey && !isPk(key)
    }

    /// A pasted key or `skychat://<pk>`, normalised; "" when it is neither.
    public static func keyFrom(_ raw: String) -> String {
        var bare = raw.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        if bare.hasPrefix("skychat://") { bare.removeFirst("skychat://".count) }
        while bare.hasSuffix("/") { bare.removeLast() }
        return isPk(bare) ? bare : ""
    }

    /// `abcdef12…wxyz`, the short form of a key every list shows.
    public static func abbrevPk(_ pk: String) -> String {
        pk.count >= 14 ? String(pk.prefix(8)) + "…" + String(pk.suffix(4)) : pk
    }

    /// The web page's size format: one decimal from KB up.
    public static func humanSize(_ n: Int64) -> String {
        if n < 1024 { return "\(n) B" }
        let units = ["KB", "MB", "GB", "TB"]
        var value = Double(n) / 1024
        var i = 0
        while value >= 1024 && i < units.count - 1 {
            value /= 1024
            i += 1
        }
        return String(format: "%.1f %@", locale: Locale(identifier: "en_US_POSIX"), value, units[i])
    }

    /// Parse an RFC 3339 time from the server (nanoseconds and offsets included), as ms.
    public static func parseTime(_ s: String) -> Int64? {
        let text = s.trimmingCharacters(in: .whitespaces)
        guard !text.isEmpty else { return nil }
        // ISO8601DateFormatter takes at most milliseconds: cut longer fractions down.
        var trimmed = text
        if let dot = text.firstIndex(of: ".") {
            let digits = text[text.index(after: dot)...].prefix { $0.isNumber }
            if digits.count > 3 {
                let end = text.index(text.index(after: dot), offsetBy: digits.count)
                trimmed = String(text[..<text.index(after: dot)]) + digits.prefix(3) + text[end...]
            }
        }
        let withFraction = ISO8601DateFormatter()
        withFraction.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let date = withFraction.date(from: trimmed) { return Int64((date.timeIntervalSince1970 * 1000).rounded()) }
        let plain = ISO8601DateFormatter()
        plain.formatOptions = [.withInternetDateTime]
        if let date = plain.date(from: trimmed) { return Int64((date.timeIntervalSince1970 * 1000).rounded()) }
        return nil
    }

    /// What JavaScript's `toISOString()` writes, which reply envelopes carry.
    public static func isoMillis(_ ms: Int64) -> String {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        formatter.timeZone = TimeZone(identifier: "UTC")
        return formatter.string(from: Date(timeIntervalSince1970: Double(ms) / 1000))
    }

    public static func nowMs() -> Int64 {
        Int64((Date().timeIntervalSince1970 * 1000).rounded())
    }

    /// Delivery order; `failed` sits outside it.
    public static func statusRank(_ status: String) -> Int {
        switch status {
        case "queued": 0
        case "pending": 1
        case "sent": 2
        case "received": 3
        case "read": 4
        default: -1
        }
    }

    /// Statuses only move forward; failed only replaces nothing or pending.
    public static func advance(_ current: String, _ next: String) -> String {
        if next == "failed" { return current.isEmpty || current == "pending" ? "failed" : current }
        if current == "failed" { return next }
        return statusRank(next) > statusRank(current) ? next : current
    }

    /// What a tick shows for `m`, including the file-transfer states (index.html _msgTickStatus).
    public static func tickStatus(_ m: ChatMessage, group: Bool) -> String {
        guard m.mine else { return "" }
        guard let f = m.file else { return m.status.isEmpty ? "sent" : m.status }
        if m.status == "read" { return "read" }
        switch f.status {
        case "sending": return "pending"
        case "failed": return "failed"
        case "received": return "received"
        case "stored": return "sent"
        default: return group ? "sent" : "received"
        }
    }

    /// The body a reply travels in (index.html _encodeReply).
    public static func replyEnvelope(_ text: String, _ reply: ReplyRef) -> String {
        json(["skychat_reply": [
            "to_sender": reply.sender,
            "to_ts": reply.ts,
            "to_preview": String(reply.preview.prefix(120)),
            "text": text,
        ]])
    }

    /// A forward carries the text and, out of a channel only, the channel's name (forward.go).
    public static func forwardEnvelope(_ text: String, from: String?) -> String {
        var inner: [String: Any] = ["text": text]
        if let from, !from.isEmpty { inner["from"] = String(from.prefix(64)) }
        return json(["skychat_forward": inner])
    }

    public struct Unwrapped: Equatable, Sendable {
        public var text: String
        public var reply: ReplyRef?
        public var forwarded: Bool
        public var forwardFrom: String

        public init(text: String, reply: ReplyRef?, forwarded: Bool, forwardFrom: String) {
            self.text = text
            self.reply = reply
            self.forwarded = forwarded
            self.forwardFrom = forwardFrom
        }
    }

    /// CXO pair messages arrive with their reply/forward envelope still on; open it here.
    public static func unwrap(_ body: String) -> Unwrapped {
        let plain = Unwrapped(text: body, reply: nil, forwarded: false, forwardFrom: "")
        let trimmed = body.trimmingCharacters(in: .whitespacesAndNewlines)
        guard trimmed.hasPrefix("{"), let f = JSONFields(data: Data(trimmed.utf8)) else { return plain }
        if let r = f.object("skychat_reply") {
            return Unwrapped(
                text: r.string("text"),
                reply: ReplyRef(sender: r.string("to_sender").lowercased(), ts: r.string("to_ts"), preview: r.string("to_preview")),
                forwarded: false, forwardFrom: ""
            )
        }
        if let r = f.object("skychat_forward") {
            return Unwrapped(text: r.string("text"), reply: nil, forwarded: true, forwardFrom: r.string("from"))
        }
        return plain
    }

    /// A tombstone is author-signed: it removes only that author's message with that feed id.
    public static func withoutDeleted(_ list: [ChatMessage], nano: String, by: String, me: String) -> [ChatMessage] {
        list.filter { !($0.tsNano == nano && ($0.from == by || ($0.mine && by == me))) }
    }

    /// One-line preview of a message for quotes and the touch sheet (at most 120 characters).
    public static func messagePreview(_ m: ChatMessage) -> String {
        if let f = m.file { return f.name.isEmpty ? "Attachment" : f.name }
        let collapsed = m.text.split(whereSeparator: { $0.isWhitespace }).joined(separator: " ")
        return String(collapsed.prefix(120))
    }

    public static func replyOf(sender: String, ts: String, preview: String) -> ReplyRef? {
        sender.isEmpty && ts.isEmpty && preview.isEmpty ? nil : ReplyRef(sender: sender.lowercased(), ts: ts, preview: preview)
    }

    /// `YYYYMMDD-HHMMSS` in local time, the stamp the web recorder names clips with.
    public static func clipStamp(_ date: Date = Date()) -> String {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.dateFormat = "yyyyMMdd-HHmmss"
        return formatter.string(from: date)
    }

    static func json(_ object: [String: Any]) -> String {
        let data = (try? JSONSerialization.data(withJSONObject: object, options: [.sortedKeys, .withoutEscapingSlashes])) ?? Data("{}".utf8)
        return String(decoding: data, as: UTF8.self)
    }
}

public struct ReplyRef: Codable, Equatable, Hashable, Sendable {
    public var sender: String
    public var ts: String
    public var preview: String

    public init(sender: String = "", ts: String = "", preview: String = "") {
        self.sender = sender
        self.ts = ts
        self.preview = preview
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        sender = try c.decodeIfPresent(String.self, forKey: .sender) ?? ""
        ts = try c.decodeIfPresent(String.self, forKey: .ts) ?? ""
        preview = try c.decodeIfPresent(String.self, forKey: .preview) ?? ""
    }
}

public struct ChatFile: Codable, Equatable, Sendable {
    public var id: String
    public var name: String
    public var size: Int64
    /// `sending`, `sent`, `stored`, `failed`, `received` or the server's `file_status`.
    public var status: String
    /// A served `/files/…` path, or "" when this visor does not hold the bytes.
    public var url: String
    /// The sender (inbound only), who a re-request goes to.
    public var from: String
    /// Our own upload's copy, relative to the chat's upload folder, kept for retry and preview.
    public var localPath: String

    public init(id: String = "", name: String = "file", size: Int64 = 0, status: String = "", url: String = "", from: String = "", localPath: String = "") {
        self.id = id
        self.name = name
        self.size = size
        self.status = status
        self.url = url
        self.from = from
        self.localPath = localPath
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decodeIfPresent(String.self, forKey: .id) ?? ""
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? "file"
        size = try c.decodeIfPresent(Int64.self, forKey: .size) ?? 0
        status = try c.decodeIfPresent(String.self, forKey: .status) ?? ""
        url = try c.decodeIfPresent(String.self, forKey: .url) ?? ""
        from = try c.decodeIfPresent(String.self, forKey: .from) ?? ""
        localPath = try c.decodeIfPresent(String.self, forKey: .localPath) ?? ""
    }

    private func matches(_ pattern: String) -> Bool {
        name.range(of: pattern, options: [.regularExpression, .caseInsensitive]) != nil
    }

    public var isImage: Bool { matches(#"\.(png|jpe?g|gif|webp|bmp|svg|avif|heic)$"#) }
    public var isVideo: Bool { matches(#"\.(mp4|m4v|webm|ogv|mov|mkv|3gp)$"#) }
    public var isAudio: Bool { matches(#"\.(mp3|ogg|oga|opus|wav|m4a|aac|flac|weba)$"#) }
    /// A clip recorded in a composer: the name prefix is the only signal (index.html isVoiceMessage).
    public var isVoiceMessage: Bool { matches(#"^voice-\d"#) && isAudio }
    public var isVideoMessage: Bool { matches(#"^video-(sq-)?\d"#) && isVideo }
    public var isSquareVideoMessage: Bool { matches(#"^video-sq-\d"#) && isVideo }

    /// `/thumb/<rest>` for a served `/files/<rest>`, or nil.
    public var thumbPath: String? {
        url.hasPrefix("/files/") ? "/thumb/" + url.dropFirst("/files/".count) : nil
    }
}

/// One message as the UI holds it: the web page's message object, with
/// times as epoch ms and a stable `key` in place of its `mid`.
public struct ChatMessage: Codable, Equatable, Identifiable, Sendable {
    public var key: String
    public var ts: Int64
    /// `Skychat.me` for our own, else the author's key.
    public var from: String
    public var text: String
    public var viaPair: Bool
    public var file: ChatFile?
    public var reply: ReplyRef?
    public var forwarded: Bool
    public var forwardFrom: String
    /// The id both visors know a DM by.
    public var wireId: String
    /// Own messages: queued, pending, sent, received, read, failed.
    public var status: String
    /// Group feed timestamp, the exact id of a group message.
    public var tsNano: String
    public var readSent: Bool
    public var request: Bool

    public var id: String { key }

    public init(
        key: String = UUID().uuidString, ts: Int64 = Skychat.nowMs(), from: String = Skychat.me, text: String = "",
        viaPair: Bool = false, file: ChatFile? = nil, reply: ReplyRef? = nil, forwarded: Bool = false,
        forwardFrom: String = "", wireId: String = "", status: String = "", tsNano: String = "",
        readSent: Bool = false, request: Bool = false
    ) {
        self.key = key
        self.ts = ts
        self.from = from
        self.text = text
        self.viaPair = viaPair
        self.file = file
        self.reply = reply
        self.forwarded = forwarded
        self.forwardFrom = forwardFrom
        self.wireId = wireId
        self.status = status
        self.tsNano = tsNano
        self.readSent = readSent
        self.request = request
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        key = try c.decodeIfPresent(String.self, forKey: .key) ?? UUID().uuidString
        ts = try c.decodeIfPresent(Int64.self, forKey: .ts) ?? 0
        from = try c.decodeIfPresent(String.self, forKey: .from) ?? Skychat.me
        text = try c.decodeIfPresent(String.self, forKey: .text) ?? ""
        viaPair = try c.decodeIfPresent(Bool.self, forKey: .viaPair) ?? false
        file = try c.decodeIfPresent(ChatFile.self, forKey: .file)
        reply = try c.decodeIfPresent(ReplyRef.self, forKey: .reply)
        forwarded = try c.decodeIfPresent(Bool.self, forKey: .forwarded) ?? false
        forwardFrom = try c.decodeIfPresent(String.self, forKey: .forwardFrom) ?? ""
        wireId = try c.decodeIfPresent(String.self, forKey: .wireId) ?? ""
        status = try c.decodeIfPresent(String.self, forKey: .status) ?? ""
        tsNano = try c.decodeIfPresent(String.self, forKey: .tsNano) ?? ""
        readSent = try c.decodeIfPresent(Bool.self, forKey: .readSent) ?? false
        request = try c.decodeIfPresent(Bool.self, forKey: .request) ?? false
    }

    public var mine: Bool { from == Skychat.me }

    /// Id a DM delete or receipt names this message by.
    public var dmWireId: String { wireId.isEmpty ? (file?.id ?? "") : wireId }
}
