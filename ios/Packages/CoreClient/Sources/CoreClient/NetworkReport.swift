import Foundation

/// What the app tells the core about the network, the body of POST /api/network
/// (visorapi.NetworkEvent; Android: NetworkWatcher).
public struct NetworkEvent: Encodable, Equatable, Sendable {
    /// "moved" when the network changed under the core, "changed" when only its metered flag did.
    public let event: String
    public let metered: Bool?
    public let ifname: String?
    public let source: String

    public init(event: String, metered: Bool?, ifname: String?) {
        self.event = event
        self.metered = metered
        self.ifname = ifname
        source = "ios"
    }
}

/// The part of the core's network state the reply carries that the app logs.
public struct NetworkReply: Decodable, Equatable, Sendable {
    public let mode: String
    public let metered: Bool
    /// Network changes the core has acted on since it started.
    public let moves: Int

    enum CodingKeys: String, CodingKey { case mode, metered, moves }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        mode = c.lenient(.mode, "")
        metered = c.lenient(.metered, false)
        moves = c.lenient(.moves, 0)
    }
}

/// One attachment to the network: the interface the system routes through and
/// our routable addresses on it.
public struct NetworkAttachment: Equatable, Sendable, CustomStringConvertible {
    public let interface: String
    public let addresses: Set<String>

    public init(interface: String, addresses: Set<String>) {
        self.interface = interface
        self.addresses = addresses
    }

    /// Another interface, or an address of ours going away, kills the core's sockets.
    /// An address arriving does not, as IPv6 privacy addresses do all the time.
    public func invalidated(by next: NetworkAttachment) -> Bool {
        interface != next.interface || !addresses.isSubset(of: next.addresses)
    }

    public var description: String {
        addresses.isEmpty ? interface : "\(interface)[\(addresses.sorted().joined(separator: ","))]"
    }
}

/// Decides which path updates are a move. The attachment the core started on is
/// the baseline and never itself a move.
public struct NetworkMoves: Sendable {
    public private(set) var current: NetworkAttachment?
    private var baselineTaken = false

    public init() {}

    /// Records where we are now and returns whether that is a move.
    public mutating func observe(_ next: NetworkAttachment) -> Bool {
        let was = current
        if next == was { return false }
        current = next
        if !baselineTaken {
            baselineTaken = true
            return false
        }
        // Coming back after the network went away is a move however long the gap was.
        return was.map { $0.invalidated(by: next) } ?? true
    }

    /// The network went away: not a move, but coming back will be one.
    public mutating func lost() {
        current = nil
    }
}

/// The metered flag as last told to the core: its first value and each change are news.
public struct MeteredFlag: Sendable {
    private var told: Bool?

    public init() {}

    public mutating func observe(_ now: Bool) -> Bool {
        if now == told { return false }
        told = now
        return true
    }
}
