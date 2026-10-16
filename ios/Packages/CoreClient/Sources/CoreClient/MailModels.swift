import Foundation

// The visor's mail routes, /api/visors/{pk}/mail (pkg/visor/hypervisor_handlers_mail.go),
// as the Skymail screen reads them: the port of Android's api/MailModels.kt.
// Decoded leniently, as every model in Models.swift.

/// The visor's mailbox as its app list names it: the hub's status dot and the
/// notifications' app (pkg/visor/skymail.go, init_apps.go).
public enum MailProfile {
    public static let app = "skymail"
}

/// The two folders the mailbox keeps.
public enum MailFolder {
    public static let inbox = "INBOX"
    public static let sent = "Sent"
}

public struct MailLimits: Decodable, Sendable, Equatable {
    public var maxMessageSize: Int64
    public var maxTotalSize: Int64
    /// Go's time.Duration, in nanoseconds.
    public var maxAgeNanos: Int64

    enum CodingKeys: String, CodingKey {
        case maxMessageSize = "max_message_size", maxTotalSize = "max_total_size", maxAgeNanos = "max_age"
    }

    public init(maxMessageSize: Int64 = 0, maxTotalSize: Int64 = 0, maxAgeNanos: Int64 = 0) {
        self.maxMessageSize = maxMessageSize
        self.maxTotalSize = maxTotalSize
        self.maxAgeNanos = maxAgeNanos
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        maxMessageSize = c.lenient(.maxMessageSize, 0)
        maxTotalSize = c.lenient(.maxTotalSize, 0)
        maxAgeNanos = c.lenient(.maxAgeNanos, 0)
    }

    /// The age limit in whole days.
    public var maxAgeDays: Int { Int(maxAgeNanos / 86_400_000_000_000) }
}

/// GET /api/visors/{pk}/mail.
public struct MailStatus: Decodable, Sendable, Equatable {
    public var enabled: Bool
    public var running: Bool
    /// Why the mailbox is not running, when it is not.
    public var reason: String
    public var limits: MailLimits
    /// Bytes held by Inbox and Sent together.
    public var usage: Int64
    public var address: String
    public var addressDmsg: String
    /// Hex keys allowed to deliver; empty means everyone.
    public var whitelist: [String]
    public var unread: Int
    public var total: Int

    enum CodingKeys: String, CodingKey {
        case enabled, running, reason, limits, usage, address, whitelist, unread, total
        case addressDmsg = "address_dmsg"
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        enabled = c.lenient(.enabled, false)
        running = c.lenient(.running, false)
        reason = c.lenient(.reason, "")
        limits = c.lenient(.limits, MailLimits())
        usage = c.lenient(.usage, 0)
        address = c.lenient(.address, "")
        addressDmsg = c.lenient(.addressDmsg, "")
        whitelist = c.lenient(.whitelist, [])
        unread = c.lenient(.unread, 0)
        total = c.lenient(.total, 0)
    }

    /// The address to hand out: dmsg needs no route, which a phone rarely has.
    public var myAddress: String? {
        let address = addressDmsg.isEmpty ? self.address : addressDmsg
        return address.isEmpty ? nil : address
    }
}

/// One entry of GET /api/visors/{pk}/mail/{folder}.
public struct MailSummary: Decodable, Sendable, Equatable, Identifiable {
    public var id: String
    public var folder: String
    public var from: String
    public var to: String
    public var subject: String
    /// RFC 3339; Go's zero time when the message has no Date header.
    public var date: String
    public var seen: Bool
    public var size: Int64
    /// The sender key the transport authenticated (inbox only).
    public var peerPK: String
    /// The From address names that key, so it cannot be forged.
    public var fromVerified: Bool

    enum CodingKeys: String, CodingKey {
        case id, folder, from, to, subject, date, seen, size
        case peerPK = "peer_pk", fromVerified = "from_verified"
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        folder = c.lenient(.folder, "")
        from = c.lenient(.from, "")
        to = c.lenient(.to, "")
        subject = c.lenient(.subject, "")
        date = c.lenient(.date, "")
        seen = c.lenient(.seen, false)
        size = c.lenient(.size, 0)
        peerPK = c.lenient(.peerPK, "")
        fromVerified = c.lenient(.fromVerified, false)
    }
}

public struct MailAttachmentInfo: Decodable, Sendable, Equatable {
    public var name: String
    public var contentType: String
    public var size: Int64

    enum CodingKeys: String, CodingKey {
        case name, size
        case contentType = "content_type"
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        name = c.lenient(.name, "")
        contentType = c.lenient(.contentType, "")
        size = c.lenient(.size, 0)
    }
}

/// GET /api/visors/{pk}/mail/{folder}/{id}: the message reduced to text.
public struct MailMessage: Decodable, Sendable, Equatable {
    public var from: String
    public var to: String
    public var cc: String
    public var subject: String
    /// The Date header as sent.
    public var date: String
    public var messageID: String
    public var peerPK: String
    public var fromVerified: Bool
    public var text: String
    /// The text was reduced from an HTML-only message.
    public var fromHTML: Bool
    public var attachments: [MailAttachmentInfo]

    enum CodingKeys: String, CodingKey {
        case from, to, cc, subject, date, text, attachments
        case messageID = "message_id", peerPK = "peer_pk", fromVerified = "from_verified", fromHTML = "from_html"
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        from = c.lenient(.from, "")
        to = c.lenient(.to, "")
        cc = c.lenient(.cc, "")
        subject = c.lenient(.subject, "")
        date = c.lenient(.date, "")
        messageID = c.lenient(.messageID, "")
        peerPK = c.lenient(.peerPK, "")
        fromVerified = c.lenient(.fromVerified, false)
        text = c.lenient(.text, "")
        fromHTML = c.lenient(.fromHTML, false)
        attachments = c.lenient(.attachments, [])
    }
}

/// A file going out with a message.
public struct MailOutgoingAttachment: Encodable, Sendable, Equatable {
    public var name: String
    public var contentType: String
    /// Encoded as standard base64, as Go decodes a []byte.
    public var data: Data

    enum CodingKeys: String, CodingKey {
        case name, data
        case contentType = "content_type"
    }

    public init(name: String, contentType: String, data: Data) {
        self.name = name
        self.contentType = contentType
        self.data = data
    }
}

/// POST /api/visors/{pk}/mail/send.
public struct MailOutgoing: Encodable, Sendable, Equatable {
    public var to: [String]
    public var cc: [String]
    public var subject: String
    public var body: String
    public var inReplyTo: String
    public var attachments: [MailOutgoingAttachment]

    enum CodingKeys: String, CodingKey {
        case to, cc, subject, body, attachments
        case inReplyTo = "in_reply_to"
    }

    public init(
        to: [String], cc: [String] = [], subject: String = "", body: String = "",
        inReplyTo: String = "", attachments: [MailOutgoingAttachment] = []
    ) {
        self.to = to
        self.cc = cc
        self.subject = subject
        self.body = body
        self.inReplyTo = inReplyTo
        self.attachments = attachments
    }
}

public struct MailRecipientResult: Decodable, Sendable, Equatable {
    public var rcpt: String
    public var error: String
    /// skynet, dmsg, or local for this visor's own key.
    public var via: String

    enum CodingKeys: String, CodingKey { case rcpt, error, via }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        rcpt = c.lenient(.rcpt, "")
        error = c.lenient(.error, "")
        via = c.lenient(.via, "")
    }
}

/// What a send did, recipient by recipient.
public struct MailSendResult: Decodable, Sendable, Equatable {
    public var id: String
    public var messageID: String
    public var recipients: [MailRecipientResult]
    /// Set when nothing was delivered.
    public var error: String

    enum CodingKeys: String, CodingKey {
        case id, recipients, error
        case messageID = "message_id"
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = c.lenient(.id, "")
        messageID = c.lenient(.messageID, "")
        recipients = c.lenient(.recipients, [])
        error = c.lenient(.error, "")
    }

    public var delivered: Int { recipients.filter(\.error.isEmpty).count }
}
