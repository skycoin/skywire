import Foundation

// What the visor's local API answers, as the app reads it: the port of
// Android's api/VisorModels.kt. Only the fields a screen uses are declared.
// Decoding is lenient in the way the Kotlin decoder is configured to be
// (ignoreUnknownKeys, coerceInputValues): an unknown field is skipped, and a
// field that is missing, null or of another type reads as its default. A
// server-side addition or a null never fails a whole screen.

extension KeyedDecodingContainer {
    /// The field's value, or `fallback` when it is missing, null or not a `T`.
    func lenient<T: Decodable>(_ key: Key, _ fallback: T) -> T {
        (try? decodeIfPresent(T.self, forKey: key)) ?? fallback
    }

    /// The field's value, or nil when it is missing, null or not a `T`.
    func lenient<T: Decodable>(_ key: Key) -> T? {
        try? decodeIfPresent(T.self, forKey: key)
    }
}

/// GET /api/about.
public struct About: Decodable, Sendable, Equatable {
    public var publicKey: String
    public var dmsgConnected: Bool
    public var dmsgSessions: Int
    /// The core's build; `version` is what the visor card shows.
    public var build: BuildInfo?

    enum CodingKeys: String, CodingKey {
        case publicKey = "public_key", dmsgConnected = "dmsg_connected", dmsgSessions = "dmsg_sessions", build
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        // The one field a screen cannot do without: every per-visor route is
        // addressed by it.
        publicKey = try c.decode(String.self, forKey: .publicKey)
        dmsgConnected = c.lenient(.dmsgConnected, false)
        dmsgSessions = c.lenient(.dmsgSessions, 0)
        build = c.lenient(.build)
    }
}

public struct BuildInfo: Decodable, Sendable, Equatable {
    public var version: String
    public var commit: String
    public var date: String

    enum CodingKeys: String, CodingKey { case version, commit, date }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        version = c.lenient(.version, "")
        commit = c.lenient(.commit, "")
        date = c.lenient(.date, "")
    }
}

/// GET /api/visors/{pk}/summary, and each entry of /api/visors-summary.
public struct VisorSummary: Decodable, Sendable, Equatable {
    public var overview: Overview
    public var health: HealthInfo?
    /// Seconds since the visor started.
    public var uptime: Double
    public var dmsgServers: [DmsgServerInfo]
    public var buildTag: String
    public var configVersion: String
    /// Whether the visor answered. Only meaningful in the visors-summary list,
    /// where an offline visor is still listed from its last snapshot.
    public var online: Bool
    /// True for the visor serving the API: this phone, in the Fleet list.
    public var isHypervisor: Bool
    /// RFC 3339; the last successful summary. Absent for a never-seen visor.
    public var lastSeenAt: String?
    /// RFC 3339; set only while `online` is false.
    public var offlineSince: String?

    enum CodingKeys: String, CodingKey {
        case overview, health, uptime
        case dmsgServers = "dmsg_servers", buildTag = "build_tag", configVersion = "config_version"
        case online, isHypervisor = "is_hypervisor", lastSeenAt = "last_seen_at", offlineSince = "offline_since"
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        overview = c.lenient(.overview) ?? Overview()
        health = c.lenient(.health)
        uptime = c.lenient(.uptime, 0)
        dmsgServers = c.lenient(.dmsgServers, [])
        buildTag = c.lenient(.buildTag, "")
        configVersion = c.lenient(.configVersion, "")
        online = c.lenient(.online, false)
        isHypervisor = c.lenient(.isHypervisor, false)
        lastSeenAt = c.lenient(.lastSeenAt)
        offlineSince = c.lenient(.offlineSince)
    }
}

public struct Overview: Decodable, Sendable, Equatable {
    public var localPK: String = ""
    public var buildInfo: BuildInfo?
    public var apps: [AppState] = []
    public var transports: [TransportSummary] = []
    public var localIP: String = ""
    /// What the visor's STUN probe saw at startup: the phone's underlay
    /// address, empty behind symmetric NAT, and the NAT-type word itself when
    /// the probe failed. Read `publicIPOrNil`, not this.
    public var publicIP: String = ""
    public var isSymmetricNAT: Bool = false
    public var natType: String = ""
    /// Where the visor appears to be, resolved by a dmsg server at startup.
    public var countryCode: String = ""

    enum CodingKeys: String, CodingKey {
        case localPK = "local_pk", buildInfo = "build_info", apps, transports, localIP = "local_ip"
        case publicIP = "public_ip"
        // The wire format's spelling (api.go), not a typo here.
        case isSymmetricNAT = "is_symmetic_nat"
        case natType = "nat_type", countryCode = "country_code"
    }

    public init() {}

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        localPK = c.lenient(.localPK, "")
        buildInfo = c.lenient(.buildInfo)
        apps = c.lenient(.apps, [])
        transports = c.lenient(.transports, [])
        localIP = c.lenient(.localIP, "")
        publicIP = c.lenient(.publicIP, "")
        isSymmetricNAT = c.lenient(.isSymmetricNAT, false)
        natType = c.lenient(.natType, "")
        countryCode = c.lenient(.countryCode, "")
    }

    /// `publicIP` when it is an address, else nil. The visor writes the NAT
    /// type into that field when STUN fails (api_visor.go) and leaves it empty
    /// behind symmetric NAT; neither is an address to show.
    public var publicIPOrNil: String? {
        let hex = Set("0123456789abcdefABCDEF.:")
        guard !publicIP.isEmpty,
              publicIP.contains(where: { $0 == "." || $0 == ":" }),
              publicIP.allSatisfy({ hex.contains($0) })
        else { return nil }
        return publicIP
    }
}

/// One live transport.
public struct TransportSummary: Decodable, Sendable, Equatable {
    public var id: String
    public var remotePK: String
    public var type: String
    public var latencyMS: Double

    enum CodingKeys: String, CodingKey {
        case id, remotePK = "remote_pk", type, latencyMS = "latency_ms"
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = c.lenient(.id, "")
        remotePK = c.lenient(.remotePK, "")
        type = c.lenient(.type, "")
        latencyMS = c.lenient(.latencyMS, 0)
    }
}

/// An app as the launcher reports it.
public struct AppState: Decodable, Sendable, Equatable {
    public static let statusStopped = 0
    public static let statusRunning = 1
    public static let statusErrored = 2
    public static let statusStarting = 3

    public var name: String
    /// 0 stopped, 1 running, 2 errored, 3 starting.
    public var status: Int
    public var detailedStatus: String
    public var autoStart: Bool
    public var port: Int
    /// The launcher argv. The API always sends the array form; the
    /// space-joined string is the config file's rendering only.
    public var args: [String]

    public var running: Bool { status == Self.statusRunning }

    enum CodingKeys: String, CodingKey {
        case name, status, detailedStatus = "detailed_status", autoStart = "auto_start", port, args
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        name = try c.decode(String.self, forKey: .name)
        status = c.lenient(.status, 0)
        detailedStatus = c.lenient(.detailedStatus, "")
        autoStart = c.lenient(.autoStart, false)
        port = c.lenient(.port, 0)
        args = c.lenient(.args, [])
    }
}

/// One service-discovery entry, proxied verbatim from SD by /api/svc-fetch.
public struct ServiceEntry: Codable, Sendable, Equatable {
    /// `<public key>:<port>`.
    public var address: String
    public var type: String
    public var geo: GeoInfo?
    public var version: String

    public var pk: String { String(address.prefix { $0 != ":" }) }

    enum CodingKeys: String, CodingKey { case address, type, geo, version }

    /// Encodable too, so the phone can keep the last list it was given.
    public init(address: String, type: String = "", geo: GeoInfo? = nil, version: String = "") {
        self.address = address
        self.type = type
        self.geo = geo
        self.version = version
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        address = c.lenient(.address, "")
        type = c.lenient(.type, "")
        geo = c.lenient(.geo)
        version = c.lenient(.version, "")
    }
}

public struct GeoInfo: Codable, Sendable, Equatable {
    public var country: String
    public var region: String

    enum CodingKeys: String, CodingKey { case country, region }

    public init(country: String = "", region: String = "") {
        self.country = country
        self.region = region
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        country = c.lenient(.country, "")
        region = c.lenient(.region, "")
    }
}

/// One live connection of an app. skysocks-client and vpn-client each hold
/// one, so the first element is the one a screen shows.
public struct AppConnection: Decodable, Sendable, Equatable {
    public var isAlive: Bool
    /// Milliseconds already: the visor converts the Go duration before it
    /// serializes (ConnectionsSummary, pkg/app/appserver/proc.go).
    public var latencyMS: Int64
    /// Bytes per second.
    public var uploadSpeed: Int64
    public var downloadSpeed: Int64
    public var bandwidthSent: Int64
    public var bandwidthReceived: Int64
    /// Seconds the tunnel has carried traffic; 0 when it does not.
    public var connectionSeconds: Int64
    public var error: String

    enum CodingKeys: String, CodingKey {
        case isAlive = "is_alive", latencyMS = "latency", uploadSpeed = "upload_speed"
        case downloadSpeed = "download_speed", bandwidthSent = "bandwidth_sent"
        case bandwidthReceived = "bandwidth_received", connectionSeconds = "connection_duration", error
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        isAlive = c.lenient(.isAlive, false)
        latencyMS = c.lenient(.latencyMS, 0)
        uploadSpeed = c.lenient(.uploadSpeed, 0)
        downloadSpeed = c.lenient(.downloadSpeed, 0)
        bandwidthSent = c.lenient(.bandwidthSent, 0)
        bandwidthReceived = c.lenient(.bandwidthReceived, 0)
        connectionSeconds = c.lenient(.connectionSeconds, 0)
        error = c.lenient(.error, "")
    }
}

/// GET …/apps/{app}/stats. `startTime` is when the app's process started;
/// absent while it is not running.
public struct AppStats: Decodable, Sendable, Equatable {
    public var connections: [AppConnection]?
    /// RFC 3339, as Go renders a `*time.Time`.
    public var startTime: String?

    enum CodingKeys: String, CodingKey { case connections, startTime = "start_time" }

    public init() {}

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        connections = c.lenient(.connections)
        startTime = c.lenient(.startTime)
    }
}

public struct HealthInfo: Decodable, Sendable, Equatable {
    public var servicesHealth: String
    public var uptimeTrackerHealth: String
    public var autoconnectHealth: String
    public var transportabilityHealth: String

    enum CodingKeys: String, CodingKey {
        case servicesHealth = "services_health", uptimeTrackerHealth = "uptime_tracker_health"
        case autoconnectHealth = "autoconnect_health", transportabilityHealth = "transportability_health"
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        servicesHealth = c.lenient(.servicesHealth, "")
        uptimeTrackerHealth = c.lenient(.uptimeTrackerHealth, "")
        autoconnectHealth = c.lenient(.autoconnectHealth, "")
        transportabilityHealth = c.lenient(.transportabilityHealth, "")
    }
}

/// A dmsg server this visor holds a session with, from the summary's live
/// session list (populated whenever dmsg is up, unlike /api/dmsg).
public struct DmsgServerInfo: Decodable, Sendable, Equatable {
    public var pk: String
    /// Nanoseconds; 0 until the hourly self-ping lands, and it can stay 0 on a
    /// phone, so show it only when positive.
    public var latencyNS: Int64
    /// Raw session carrier: tcp, ws, wt or quic.
    public var carrier: String
    /// The carrier as people read it: "tcp", "wss", "quic".
    public var `protocol`: String

    enum CodingKeys: String, CodingKey { case pk, latencyNS = "latency", carrier, `protocol` }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        pk = c.lenient(.pk, "")
        latencyNS = c.lenient(.latencyNS, 0)
        carrier = c.lenient(.carrier, "")
        `protocol` = c.lenient(.protocol, "")
    }
}

/// GET …/router-settings: the visor-wide routing choices the phone shows.
/// The document also carries the router's tuning knobs, which the app never
/// reads or writes (see `CoreClient.updateRouterSettings`). Mux routes are not
/// here: they are a config pin (`routing.mux_routes`, the phone profile), not
/// an API field.
public struct RouterSettings: Decodable, Sendable, Equatable {
    public var forceLocalRoutes: Bool
    public var existingTransportsOnly: Bool
    public var minHops: Int
    /// Transport types, most preferred first; the GET always answers the full
    /// order in effect.
    public var transportPreference: [String]

    enum CodingKeys: String, CodingKey {
        case forceLocalRoutes = "force_local_routes", existingTransportsOnly = "existing_tp_only"
        case minHops = "min_hops", transportPreference = "transport_preference"
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        forceLocalRoutes = c.lenient(.forceLocalRoutes, false)
        existingTransportsOnly = c.lenient(.existingTransportsOnly, false)
        minHops = c.lenient(.minHops, 0)
        transportPreference = c.lenient(.transportPreference, [])
    }
}

/// The body of PUT …/router-settings: the four fields above and nothing else.
struct RouterSettingsWrite: Encodable {
    var forceLocalRoutes: Bool
    var existingTransportsOnly: Bool
    var minHops: Int
    var transportPreference: [String]

    init(_ current: RouterSettings) {
        forceLocalRoutes = current.forceLocalRoutes
        existingTransportsOnly = current.existingTransportsOnly
        minHops = current.minHops
        transportPreference = current.transportPreference
    }

    enum CodingKeys: String, CodingKey {
        case forceLocalRoutes = "force_local_routes", existingTransportsOnly = "existing_tp_only"
        case minHops = "min_hops", transportPreference = "transport_preference"
    }
}

/// One row of GET /api/service-health.
public struct ServiceHealthEntry: Decodable, Sendable, Equatable {
    public var name: String
    public var status: String
    public var latencyMS: Double
    public var transport: String
    public var error: String

    enum CodingKeys: String, CodingKey { case name, status, latencyMS = "latency_ms", transport, error }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        name = c.lenient(.name, "")
        status = c.lenient(.status, "")
        latencyMS = c.lenient(.latencyMS, 0)
        transport = c.lenient(.transport, "")
        error = c.lenient(.error, "")
    }
}

/// GET …/runtime-logs?since=N: the lines after cursor N. `entries` is null
/// on an empty buffer; `dropped` counts lines the ring overwrote before this
/// reader saw them.
public struct RuntimeLogsDelta: Decodable, Sendable, Equatable {
    public var entries: [String]
    public var latest: Int64
    public var dropped: Int64

    enum CodingKeys: String, CodingKey { case entries, latest, dropped }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        entries = c.lenient(.entries, [])
        latest = c.lenient(.latest, 0)
        dropped = c.lenient(.dropped, 0)
    }
}

/// GET …/apps/{app}/logs?since=RFC3339Nano.
public struct AppLogs: Decodable, Sendable, Equatable {
    public var lastLogTimestamp: String
    public var logs: [String]

    enum CodingKeys: String, CodingKey { case lastLogTimestamp = "last_log_timestamp", logs }

    public init(lastLogTimestamp: String, logs: [String] = []) {
        self.lastLogTimestamp = lastLogTimestamp
        self.logs = logs
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        lastLogTimestamp = c.lenient(.lastLogTimestamp, "")
        logs = c.lenient(.logs, [])
    }
}

/// One notification from the visor's hub (GET /api/notifications/stream),
/// the wire shape pkg/visor/hypervisor_handlers_notify.go declares.
public struct NotifyEvent: Decodable, Sendable, Equatable {
    /// The publishing app, stamped by the visor: an app cannot forge it.
    /// "visor" for the visor's own events.
    public var app: String
    public var title: String
    public var body: String
    /// The publisher's "this again": a new notification with the same app
    /// and tag replaces the one before it. Empty means "like nothing else".
    /// skychat uses the conversation (the peer's key, or the group's ID).
    public var tag: String

    enum CodingKeys: String, CodingKey { case app, title, body, tag }

    public init(app: String, title: String, body: String, tag: String = "") {
        self.app = app
        self.title = title
        self.body = body
        self.tag = tag
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        app = c.lenient(.app, "")
        title = c.lenient(.title, "")
        body = c.lenient(.body, "")
        tag = c.lenient(.tag, "")
    }
}

struct Credentials: Encodable {
    let username: String
    let password: String
}

struct UserExists: Decodable {
    let exists: Bool

    enum CodingKeys: String, CodingKey { case exists }

    init(from decoder: any Decoder) throws {
        exists = try decoder.container(keyedBy: CodingKeys.self).lenient(.exists, false)
    }
}

struct CSRFToken: Decodable {
    let token: String

    enum CodingKeys: String, CodingKey { case token = "csrf_token" }
}

/// What POST /api/dmsg/reconnect reports: the sessions torn down to re-dial.
struct DmsgReconnectResult: Decodable {
    let sessionsClosed: Int

    enum CodingKeys: String, CodingKey { case sessionsClosed = "sessions_closed" }

    init(from decoder: any Decoder) throws {
        sessionsClosed = try decoder.container(keyedBy: CodingKeys.self).lenient(.sessionsClosed, 0)
    }
}

/// The API's error body: `{"error": "..."}`.
struct APIError: Decodable {
    let error: String
}

// MARK: Voice calls (the port of Android's api/VisorModels.kt, voice part)

/// How a call this phone is placing is going. The first three are progress;
/// the rest are why it ended unanswered, which the visor keeps listing for a
/// few seconds so the call screen can say so instead of just closing.
public enum DialState: String, Sendable, Equatable, CaseIterable {
    case connecting, calling, ringing
    case offline, declined, busy, noAnswer = "no_answer", failed

    public var ended: Bool { self >= .offline }

    /// The visor's wire strings; "calling", and a visor that sends no state
    /// at all, reads as `.calling`.
    public static func parse(_ value: String) -> DialState {
        DialState(rawValue: value) ?? .calling
    }
}

extension DialState: Comparable {
    /// The declared order is progress before outcomes, as Android's ordinals
    /// are; `ended` reads it.
    public static func < (lhs: DialState, rhs: DialState) -> Bool {
        let order: [DialState] = DialState.allCases
        return order.firstIndex(of: lhs)! < order.firstIndex(of: rhs)!
    }
}

/// A call this phone is placing, from `…/skychat/voice/dialing`. `ringback`
/// is true once the other side's own ringback tone has arrived and can be
/// played (`…/skychat/voice/ringback`).
public struct OutgoingCall: Decodable, Sendable, Equatable {
    public var callId: String
    public var peerPk: String
    public var state: DialState
    public var ringback: Bool

    enum CodingKeys: String, CodingKey { case callId = "call_id", peerPk = "peer", state, ringback }

    public init(callId: String, peerPk: String, state: DialState = .calling, ringback: Bool = false) {
        self.callId = callId
        self.peerPk = peerPk
        self.state = state
        self.ringback = ringback
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        callId = c.lenient(.callId, "")
        peerPk = c.lenient(.peerPk, "")
        state = DialState.parse(c.lenient(.state, ""))
        ringback = c.lenient(.ringback, false)
    }
}

/// A ringing inbound call. The visor formats these as `"<call-id> from <pk>"`
/// — one string, because the surface it was built for is a CLI listing — so
/// the shape is parsed here rather than deserialized.
public struct VoiceInvite: Sendable, Equatable {
    public var callId: String
    public var fromPk: String

    public init(callId: String, fromPk: String) {
        self.callId = callId
        self.fromPk = fromPk
    }

    /// nil when the line isn't the expected shape, so a poll can skip it.
    public static func parse(_ line: String) -> VoiceInvite? {
        guard let at = line.range(of: " from ") else { return nil }
        let id = String(line[..<at.lowerBound]).trimmingCharacters(in: .whitespaces)
        let pk = String(line[at.upperBound...]).trimmingCharacters(in: .whitespaces)
        guard !id.isEmpty, !pk.isEmpty else { return nil }
        return VoiceInvite(callId: id, fromPk: pk)
    }
}
