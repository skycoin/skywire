import Foundation

// skychat's HTTP and /sse shapes (Android: chat/SkychatModels.kt). Read with
// JSONFields, not Codable: every field is optional on the wire, Go writes an
// empty slice as null, and ts_nano is an int64 kept as its exact text.

/// Typed reads over one decoded JSON object; a missing or null field is the
/// type's zero.
struct JSONFields {
    let raw: [String: Any]

    init(_ raw: [String: Any]) {
        self.raw = raw
    }

    init?(data: Data) {
        guard let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return nil }
        raw = object
    }

    func has(_ key: String) -> Bool {
        guard let value = raw[key] else { return false }
        return !(value is NSNull)
    }

    func string(_ key: String) -> String {
        switch raw[key] {
        case let text as String: text
        case let number as NSNumber: number.stringValue
        default: ""
        }
    }

    func bool(_ key: String) -> Bool { optionalBool(key) ?? false }

    func optionalBool(_ key: String) -> Bool? {
        switch raw[key] {
        case let number as NSNumber: number.boolValue
        case let text as String: text == "true"
        default: nil
        }
    }

    func int(_ key: String) -> Int { Int(int64(key)) }

    func int64(_ key: String) -> Int64 { optionalInt64(key) ?? 0 }

    func optionalInt64(_ key: String) -> Int64? {
        switch raw[key] {
        case let number as NSNumber: number.int64Value
        case let text as String: Int64(text)
        default: nil
        }
    }

    func strings(_ key: String) -> [String] {
        (raw[key] as? [Any])?.compactMap { $0 as? String } ?? []
    }

    func object(_ key: String) -> JSONFields? {
        (raw[key] as? [String: Any]).map(JSONFields.init)
    }

    func objects(_ key: String) -> [JSONFields] {
        (raw[key] as? [Any])?.compactMap { ($0 as? [String: Any]).map(JSONFields.init) } ?? []
    }

    /// An int64 feed id as its decimal text; "" for none or zero.
    func nano(_ key: String) -> String {
        let text = string(key)
        return text == "0" ? "" : text
    }
}

/// A list body, where Go's empty slice is `null`.
func jsonArray(_ data: Data) -> [Any] {
    (try? JSONSerialization.jsonObject(with: data, options: .fragmentsAllowed)) as? [Any] ?? []
}

public struct SkychatStatus: Sendable, Equatable {
    public var visorPk = ""
    public var persistence = false
    public var pairing = false

    init(_ f: JSONFields) {
        visorPk = f.string("visor_pk")
        persistence = f.bool("persistence_enabled")
        pairing = f.bool("pairing_enabled")
    }
}

/// One stored DM, from `GET /history`.
public struct HistoryRecord: Sendable, Equatable {
    public var id = "", peer = "", from = ""
    public var outgoing = false
    public var text = "", timestamp = ""
    public var fileId = "", fileName = "", fileStatus = "", fileUrl = ""
    public var fileSize: Int64 = 0
    public var replyToTs = "", replyToPreview = "", replyToSender = ""
    public var forwarded = false
    public var forwardFrom = ""

    public init() {}

    init(_ f: JSONFields) {
        id = f.string("id")
        peer = f.string("peer")
        from = f.string("from")
        outgoing = f.bool("outgoing")
        text = f.string("text")
        timestamp = f.string("timestamp")
        fileId = f.string("file_id")
        fileName = f.string("file_name")
        fileSize = f.int64("file_size")
        fileStatus = f.string("file_status")
        fileUrl = f.string("file_url")
        replyToTs = f.string("reply_to_ts")
        replyToPreview = f.string("reply_to_preview")
        replyToSender = f.string("reply_to_sender")
        forwarded = f.bool("forwarded")
        forwardFrom = f.string("forward_from")
    }
}

/// One group feed row, from `GET /group/<id>/history`.
public struct GroupHistoryRecord: Sendable, Equatable {
    public var ts = "", tsNano = "", senderPk = "", text = ""
    public var fileId = "", fileName = "", fileUrl = ""
    public var fileSize: Int64 = 0
    public var replyToTs = "", replyToPreview = "", replyToSender = ""
    public var forwarded = false
    public var forwardFrom = ""

    init(_ f: JSONFields) {
        ts = f.string("ts")
        tsNano = f.nano("ts_nano")
        senderPk = f.string("sender_pk")
        text = f.string("text")
        fileId = f.string("file_id")
        fileName = f.string("file_name")
        fileSize = f.int64("file_size")
        fileUrl = f.string("file_url")
        replyToTs = f.string("reply_to_ts")
        replyToPreview = f.string("reply_to_preview")
        replyToSender = f.string("reply_to_sender")
        forwarded = f.bool("forwarded")
        forwardFrom = f.string("forward_from")
    }
}

/// The group record `GET /group` lists and every group action answers with.
public struct GroupInfo: Sendable, Equatable, Identifiable {
    public var id = "", name = "", kind = "", mode = "", role = "", ownerPk = ""
    public var admins: [String] = [], members: [String] = [], muted: [String] = [], banned: [String] = []
    public var joinPolicy = "", policy = "", status = ""
    public var readOnly = false
    public var canPost: Bool?
    public var cannotPostReason = ""
    public var pendingJoins = 0
    public var avatar = ""
    public var listed = false
    public var peerBackfill: Bool?
    public var joinPowBits: Int?
    public var keyEpoch: Int64 = 0
    public var keyAgeSeconds: Int64 = 0
    public var keyRotatesInSeconds: Int64?
    public var governanceSealed = false

    public init(id: String = "", name: String = "", kind: String = "") {
        self.id = id
        self.name = name
        self.kind = kind
    }

    init(_ f: JSONFields) {
        id = f.string("id")
        name = f.string("name")
        kind = f.string("kind")
        mode = f.string("mode")
        role = f.string("role")
        ownerPk = f.string("owner_pk")
        admins = f.strings("admins")
        members = f.strings("members")
        muted = f.strings("muted")
        banned = f.strings("banned")
        joinPolicy = f.string("join_policy")
        policy = f.string("policy")
        status = f.string("status")
        readOnly = f.bool("read_only")
        canPost = f.optionalBool("can_post")
        cannotPostReason = f.string("cannot_post_reason")
        pendingJoins = f.int("pending_joins")
        avatar = f.string("avatar")
        listed = f.bool("listed")
        peerBackfill = f.optionalBool("peer_backfill")
        joinPowBits = f.optionalInt64("join_pow_bits").map { Int($0) }
        keyEpoch = f.int64("key_epoch")
        keyAgeSeconds = f.int64("key_age_seconds")
        keyRotatesInSeconds = f.optionalInt64("key_rotates_in_seconds")
        governanceSealed = f.bool("governance_sealed")
    }

    public var isChannel: Bool { kind == "channel" }
    public var encrypted: Bool { kind == "private" || mode == "private" }
    public var approval: Bool { joinPolicy == "approval" || policy == "approval" }
    public var postable: Bool { canPost != false }
}

public struct JoinRequest: Sendable, Equatable {
    public var pk = "", note = ""

    init(_ f: JSONFields) {
        pk = f.string("pk")
        note = f.string("note")
    }
}

/// `GET /group/resolve`: what an address or invite names.
public struct Resolved: Sendable, Equatable {
    public var target = "", pk = "", address = "", invite = ""
    public var joined = false
    public var status = ""
    public var group: ResolvedGroup?

    public init(target: String = "", pk: String = "", address: String = "") {
        self.target = target
        self.pk = pk
        self.address = address
    }

    init(_ f: JSONFields) {
        target = f.string("target")
        pk = f.string("pk")
        address = f.string("address")
        invite = f.string("invite")
        joined = f.bool("joined")
        status = f.string("status")
        group = f.object("group").map(ResolvedGroup.init)
    }
}

public struct ResolvedGroup: Sendable, Equatable {
    public var id = "", name = "", kind = "", policy = "", mode = ""
    public var banned = false
    public var hostPk = "", priceHint = ""

    init(_ f: JSONFields) {
        id = f.string("id")
        name = f.string("name")
        kind = f.string("kind")
        policy = f.string("policy")
        mode = f.string("mode")
        banned = f.bool("banned")
        hostPk = f.string("host_pk")
        priceHint = f.string("price_hint")
    }
}

public struct Catalog: Sendable, Equatable {
    public var entries: [CatalogEntry] = []
    public var truncated = false

    init(_ f: JSONFields) {
        entries = f.objects("entries").map(CatalogEntry.init)
        truncated = f.bool("truncated")
    }
}

public struct CatalogEntry: Sendable, Equatable, Identifiable {
    public var id = "", name = "", kind = "", policy = "", priceHint = "", address = ""

    init(_ f: JSONFields) {
        id = f.string("id")
        name = f.string("name")
        kind = f.string("kind")
        policy = f.string("policy")
        priceHint = f.string("price_hint")
        address = f.string("address")
    }
}

public struct SkychatProfileInfo: Sendable, Equatable {
    public var pk = "", name = "", avatar = "", address = ""

    init(_ f: JSONFields) {
        pk = f.string("pk")
        name = f.string("name")
        avatar = f.string("avatar")
        address = f.string("address")
    }
}

public struct PrivacyState: Sendable, Equatable {
    public var approval = false
    public var blocked: [String] = []
    public var requests: [String] = []

    public init(approval: Bool = false, blocked: [String] = [], requests: [String] = []) {
        self.approval = approval
        self.blocked = blocked
        self.requests = requests
    }

    init(_ f: JSONFields) {
        approval = f.bool("approval")
        blocked = f.strings("blocked")
        requests = f.strings("requests")
    }
}

public struct PresencePeer: Sendable, Equatable {
    public var online = false
    public var rttMs: Int64 = 0

    init(_ f: JSONFields) {
        online = f.bool("online")
        rttMs = f.int64("rtt_ms")
    }
}

public struct PairRecord: Sendable, Equatable {
    public var peerPk = "", status = "", establishedAt = "", epoch = ""
    public var forwardSecret = false
    public var keyGeneration: Int64 = 0

    init(_ f: JSONFields) {
        peerPk = f.string("peer_pk")
        status = f.string("status")
        establishedAt = f.string("established_at")
        epoch = f.string("epoch")
        forwardSecret = f.bool("forward_secret")
        keyGeneration = f.int64("key_generation")
    }
}

public struct SearchHit: Sendable, Equatable {
    public var peer = "", groupId = "", from = ""
    public var outgoing = false
    public var text = "", ts = ""

    init(_ f: JSONFields) {
        peer = f.string("peer")
        groupId = f.string("group_id")
        from = f.string("from")
        outgoing = f.bool("outgoing")
        text = f.string("text")
        ts = f.string("ts")
    }
}

public struct CallRecord: Sendable, Equatable {
    public var peer = "", direction = "", outcome = "", at = ""
    public var seconds: Int64 = 0

    init(_ f: JSONFields) {
        peer = f.string("peer")
        direction = f.string("direction")
        outcome = f.string("outcome")
        at = f.string("at")
        seconds = f.int64("seconds")
    }
}

public struct LinkState: Sendable, Equatable {
    public var ready = false, connecting = false
    public var error = ""

    public init(ready: Bool = false, connecting: Bool = false, error: String = "") {
        self.ready = ready
        self.connecting = connecting
        self.error = error
    }

    init(_ f: JSONFields) {
        ready = f.bool("ready")
        connecting = f.bool("connecting")
        error = f.string("error")
    }
}

public struct FileSent: Sendable, Equatable {
    public var id = "", fileUrl = ""

    init(_ f: JSONFields) {
        id = f.string("id")
        fileUrl = f.string("file_url")
    }
}

public struct RingbackInfo: Sendable, Equatable {
    public var set = false
    public var size: Int64 = 0
    public var type = ""

    init(_ f: JSONFields) {
        set = f.bool("set")
        size = f.int64("size")
        type = f.string("type")
    }
}

/// `POST /import`'s report; every count is optional.
public struct ImportReport: Sendable, Equatable {
    public var error = ""
    public var messages = 0, groupMessages = 0, contacts = 0, duplicates = 0
    public var persistence: Bool?
    public var evicted = 0, expiring = 0, rejected = 0

    init(_ f: JSONFields) {
        error = f.string("error")
        messages = f.int("messages")
        groupMessages = f.int("group_messages")
        contacts = f.int("contacts")
        duplicates = f.int("duplicates")
        persistence = f.optionalBool("persistence")
        evicted = f.int("evicted")
        expiring = f.int("expiring")
        rejected = f.int("rejected")
    }

    public init?(data: Data) {
        guard let f = JSONFields(data: data) else { return nil }
        self.init(f)
    }
}

/// Every `/sse` payload is one flat object; the fields present depend on `channel`.
public struct SseMessage: Sendable, Equatable {
    public var channel = ""
    /// Set on the replay seed every connect starts with.
    public var history = false
    public var dir = "", sender = "", to = "", network = "", message = "", id = ""
    public var request = false
    public var peer = "", event = "", status = ""
    public var groupId = "", groupName = "", ts = "", tsNano = ""
    public var deletedTsNano = "", deletedBy = ""
    public var fileId = "", fileName = "", fileStatus = "", fileUrl = ""
    public var fileSize: Int64 = 0
    public var replyToTs = "", replyToPreview = "", replyToSender = ""
    public var forwarded = false
    public var forwardFrom = ""

    public init() {}

    init(_ f: JSONFields) {
        channel = f.string("channel")
        history = f.has("history")
        dir = f.string("dir")
        sender = f.string("sender")
        to = f.string("to")
        network = f.string("network")
        message = f.string("message")
        id = f.string("id")
        request = f.bool("request")
        peer = f.string("peer")
        event = f.string("event")
        status = f.string("status")
        groupId = f.string("group_id")
        groupName = f.string("group_name")
        ts = f.string("ts")
        tsNano = f.nano("ts_nano")
        deletedTsNano = f.nano("deleted_ts_nano")
        deletedBy = f.string("deleted_by")
        fileId = f.string("file_id")
        fileName = f.string("file_name")
        fileSize = f.int64("file_size")
        fileStatus = f.string("file_status")
        fileUrl = f.string("file_url")
        replyToTs = f.string("reply_to_ts")
        replyToPreview = f.string("reply_to_preview")
        replyToSender = f.string("reply_to_sender")
        forwarded = f.bool("forwarded")
        forwardFrom = f.string("forward_from")
    }

    public init?(json: String) {
        guard let f = JSONFields(data: Data(json.utf8)) else { return nil }
        self.init(f)
    }
}
