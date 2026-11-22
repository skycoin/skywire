import Foundation

/// One entry of the visor's contact book, `/api/visors/{pk}/contacts`
/// (pkg/contacts). SkyChat and the dashboard read the same book.
public struct BookContact: Codable, Sendable, Equatable {
    /// Empty on a new contact: the visor gives it one.
    public var uid = ""
    public var name = ""
    /// Skywire public keys, as hex.
    public var keys: [String] = []
    public var mail: [String] = []
    public var payments: [BookPayment] = []
    public var groups: [String] = []
    public var notes = ""

    enum CodingKeys: String, CodingKey { case uid, name, keys, mail, payments, groups, notes }

    public init(
        uid: String = "", name: String = "", keys: [String] = [], mail: [String] = [],
        payments: [BookPayment] = [], groups: [String] = [], notes: String = ""
    ) {
        self.uid = uid
        self.name = name
        self.keys = keys
        self.mail = mail
        self.payments = payments
        self.groups = groups
        self.notes = notes
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        uid = c.lenient(.uid, "")
        name = c.lenient(.name, "")
        keys = c.lenient(.keys, [])
        mail = c.lenient(.mail, [])
        payments = c.lenient(.payments, [])
        groups = c.lenient(.groups, [])
        notes = c.lenient(.notes, "")
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(uid, forKey: .uid)
        try c.encode(name, forKey: .name)
        if !keys.isEmpty { try c.encode(keys, forKey: .keys) }
        if !mail.isEmpty { try c.encode(mail, forKey: .mail) }
        if !payments.isEmpty { try c.encode(payments, forKey: .payments) }
        if !groups.isEmpty { try c.encode(groups, forKey: .groups) }
        if !notes.isEmpty { try c.encode(notes, forKey: .notes) }
    }
}

/// An address the contact can be paid at, e.g. skycoin + "2jBbGx…".
public struct BookPayment: Codable, Sendable, Equatable {
    public var coin: String
    public var address: String

    public init(coin: String, address: String) {
        self.coin = coin
        self.address = address
    }
}

/// The visor refused a contact as malformed, so sending it again will not help.
public struct ContactRejected: LocalizedError, Sendable {
    public let message: String
    public var errorDescription: String? { message }
}
