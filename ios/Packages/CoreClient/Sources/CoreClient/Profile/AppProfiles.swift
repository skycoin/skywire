import Foundation

/// The phone's profile for skychat: the argv pinned on every start and the
/// files it points at. The port of Android's core/SkychatProfile.kt.
///
/// Why skychat gets a password when the desktop leaves it off: a listener on
/// 127.0.0.1 is reachable by every other process on the device (and, on the
/// Simulator, on the Mac), and skychat's surface is the whole account:
/// history, contacts, sending. The gate skychat ships (`--password-file`,
/// cmd/apps/skychat/commands/auth.go) closes it, with a device-local secret
/// the WebView answers the challenge with. The file is written before the
/// app first starts, so there is no window in which the surface is open.
public enum SkychatProfile {
    public static let app = "skychat"
    public static let defaultPort = 8001
    /// Basic-auth user name. skychat checks the password alone, but a
    /// challenge has to be answered with some name.
    public static let user = "skywire"

    static let passwordFileName = "skychat-password"
    static let historyFileName = "skychat-history.db"

    private static let address = ["--addr", "-addr"]
    private static let passwordFile = ["--password-file", "-password-file"]
    private static let portless = ["--portless", "-portless"]
    private static let persist = ["--persist", "-persist"]
    private static let persistDB = ["--persist-db", "-persist-db"]

    /// Where the WebView loads the chat UI from.
    public static func baseURL(port: Int) -> URL {
        URL(string: "http://\(AppArgs.loopbackHost):\(port)/")!
    }

    /// The same listener as an origin (no path), for `LoopbackTransport`.
    public static func origin(port: Int) -> URL {
        URL(string: "http://\(AppArgs.loopbackHost):\(port)")!
    }

    public static func listenPort(_ args: [String]) -> Int {
        AppArgs.listenPort(args, defaultPort: defaultPort)
    }

    /// The argv the phone owns; everything else (`--pair-enable`, the
    /// visor-managed `--internal-token`) passes through.
    ///  - `--portless` dropped: the port is the phone's only way into the UI
    ///    (every fetch in the page is root-absolute, so the visor's
    ///    `/skychat/proxy/…` mount cannot host it).
    ///  - `--addr` host forced to loopback, the port left alone.
    ///  - `--password-file` pinned at `passwordFile` (above).
    ///  - `--persist` on, at `historyFile`: off, every conversation is erased
    ///    the next time the core restarts, and the call log (which reads the
    ///    history) forgets missed calls.
    public static func phoneArgs(_ args: [String], passwordFile: String, historyFile: String) -> [String] {
        var pinned = args.filter { token in !portless.contains { token == $0 || token.hasPrefix("\($0)=") } }
        if !AppArgs.contains(pinned, persist) {
            pinned.append("--persist")
        }
        AppArgs.pinValue(&pinned, persistDB) { _ in historyFile }
        AppArgs.pinValue(&pinned, address) { AppArgs.loopbackAddress($0, defaultPort: defaultPort) }
        AppArgs.pinValue(&pinned, self.passwordFile) { _ in passwordFile }
        return pinned
    }
}

/// The phone's profile for skydex-client, the port of Android's
/// core/SkydexProfile.kt. The same reasoning as skychat's gate, with more
/// behind the port: the live market session, the wallet addresses registered
/// with it, and placing or cancelling orders. The gate is skywire's own
/// wrapper (cmd/apps/skydex-client/commands/auth.go).
public enum SkydexProfile {
    public static let app = "skydex-client"
    public static let defaultPort = 8051
    public static let user = "skywire"

    static let passwordFileName = "skydex-password"

    private static let address = ["--addr", "-addr"]
    private static let passwordFile = ["--password-file", "-password-file"]
    private static let market = ["--market-pk", "-market-pk"]

    public static func baseURL(port: Int) -> URL {
        URL(string: "http://\(AppArgs.loopbackHost):\(port)/")!
    }

    /// The same listener as an origin (no path), for `LoopbackTransport`.
    public static func origin(port: Int) -> URL {
        URL(string: "http://\(AppArgs.loopbackHost):\(port)")!
    }

    /// The market the config records (`--market-pk`, which pre-fills the
    /// page's form and connects nothing by itself); nil when none.
    public static func marketPK(_ args: [String]) -> String? {
        AppArgs.value(args, market).flatMap { $0.isEmpty ? nil : $0 }
    }

    /// `args` with `--market-pk` set to `pk`, as the API's one-string `args`.
    /// The listen address and the password file are the profile's, untouched
    /// (Android: DexArgs.withMarketPk).
    public static func args(_ args: [String], withMarketPK pk: String) -> String {
        var pinned = args
        AppArgs.pinValue(&pinned, market) { _ in pk }
        return AppArgs.join(pinned)
    }

    /// Shaped like a market's key: 33 bytes of hex behind a compressed-point
    /// prefix, what the client's UnmarshalText accepts, so a typo is caught in
    /// the field instead of after a dial that was never going to resolve.
    public static func isMarketPK(_ value: String) -> Bool {
        value.count == 66 && (value.hasPrefix("02") || value.hasPrefix("03")) && value.allSatisfy(\.isHexDigit)
    }

    public static func listenPort(_ args: [String]) -> Int {
        AppArgs.listenPort(args, defaultPort: defaultPort)
    }

    /// `--addr` forced to loopback (the generated `:8051` listens on every
    /// interface) and `--password-file` pinned. `--market-pk`, which the DEX
    /// screen writes, passes through untouched.
    public static func phoneArgs(_ args: [String], passwordFile: String) -> [String] {
        var pinned = args
        AppArgs.pinValue(&pinned, address) { AppArgs.loopbackAddress($0, defaultPort: defaultPort) }
        AppArgs.pinValue(&pinned, self.passwordFile) { _ in passwordFile }
        return pinned
    }
}

/// The phone's pins on skysocks-client (Android: ConfigManager.phoneSocksArgs).
public enum SocksProfile {
    public static let app = "skysocks-client"
    public static let defaultPort = 1080

    private static let address = ["--addr", "-addr"]
    private static let reconnect = ["--reconnect"]
    private static let server = ["--srv", "-srv"]

    public static func listenPort(_ args: [String]) -> Int {
        AppArgs.listenPort(args, defaultPort: defaultPort)
    }

    /// The server the app is pointed at, from `--srv` (which the visor writes
    /// itself on a PUT carrying `pk`); nil when there is none.
    public static func serverPK(_ args: [String]) -> String? {
        AppArgs.value(args, server).flatMap { $0.isEmpty ? nil : $0 }
    }

    /// `args` with the listener moved to `port` on loopback, as the one string
    /// the API's `args` field takes (quoted as Go's splitArgs reads it). Only
    /// the address changes: the server key and `--reconnect` stay put
    /// (Android: SocksArgs.withPort).
    public static func args(_ args: [String], withPort port: Int) -> String {
        var pinned = args
        AppArgs.pinValue(&pinned, address) { _ in "\(AppArgs.loopbackHost):\(port)" }
        return AppArgs.join(pinned)
    }

    /// The ports the listener may take: above the privileged range.
    public static let ports = 1024...65535

    /// - `--addr` host forced to loopback: the generated `:1080` is a SOCKS5
    ///   proxy for anything that can reach the device. Only the host is
    ///   rewritten; the port is the knob the SkySOCKS screen exposes.
    /// - `--reconnect` always on: a phone loses routes routinely (a network
    ///   change is enough), and without it the app exits when its route group
    ///   dies, leaving a dead proxy.
    public static func phoneArgs(_ args: [String]) -> [String] {
        var pinned = args
        AppArgs.pinValue(&pinned, address) { AppArgs.loopbackAddress($0, defaultPort: defaultPort) }
        if !AppArgs.contains(pinned, reconnect) {
            pinned.append("--reconnect")
        }
        return pinned
    }
}
