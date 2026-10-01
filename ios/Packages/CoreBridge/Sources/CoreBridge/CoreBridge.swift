// CoreBridge: the Swift side of the Go core's C API (skywire_core.h in
// SkywireCore.xcframework, documented in cmd/skywire-mobile-core/main.go).
// This is the only file in the app that imports SkywireCore; everything else
// talks to the core through this actor, and to the visor through its local
// HTTP API.
//
// Ownership rules, which every function below keeps:
//
//  1. Every `char *` Go returns (only skywire_last_error returns one) is
//     copied into a String and released with skywire_free in the function
//     that received it, before that function returns.
//  2. Strings Swift passes to Go are borrowed for the duration of the call
//     (withCString); Go copies what it keeps. cgo declares the parameters
//     `char *`, not `const char *`, hence UnsafeMutablePointer(mutating:):
//     Go never writes through them.
//  3. The log callback is `coreLogTrampoline`, a global @convention(c)
//     function that captures nothing. It copies the line (valid only during
//     the call) into a String and hands it to the sink in `LogSinkSlot`.
//     C never holds a reference to a Swift object.
//  4. skywire_start and skywire_stop block, a start for as long as the
//     visor takes to bring its API up. They run on a GCD global queue:
//     never on the main actor, and never on a Swift concurrency thread,
//     whose small pool a 30-second call would starve. A stop can therefore
//     run while a start is still blocked, which is how a start is aborted.
//
// There is one core per process (the Go side enforces it); `shared` is the
// only instance.

import Dispatch
import Foundation
import os
import SkywireCore

/// Where the core is in its lifecycle. The raw values are skywire_state's.
public enum CoreState: Int32, Sendable, CustomStringConvertible {
    case stopped = 0
    case starting = 1
    case running = 2
    case stopping = 3
    case failed = 4

    public var description: String {
        switch self {
        case .stopped: "stopped"
        case .starting: "starting"
        case .running: "running"
        case .stopping: "stopping"
        case .failed: "failed"
        }
    }
}

/// A log line's level, as the core's logger (logrus) numbers them.
public enum CoreLogLevel: Int32, Sendable {
    case panic = 0
    case fatal = 1
    case error = 2
    case warning = 3
    case info = 4
    case debug = 5
    case trace = 6
}

/// Receives the core's log, one formatted line at a time, in order, from one
/// core thread (the core queues lines off the threads that log them, and drops
/// and counts what the app falls behind on). It must return quickly and must
/// not call back into the core.
public typealias CoreLogSink = @Sendable (CoreLogLevel, String) -> Void

/// A call into the core failed; `message` is the core's reason.
public struct CoreError: Error, LocalizedError, Sendable {
    public let message: String
    public var errorDescription: String? { message }
}

/// Overrides of the options the core generates its config with. The core
/// starts from the phone's defaults (mobilecore.PhoneGenOptions, the argv
/// Android runs `config gen` with) and applies every field set here; a nil
/// field keeps the default. The JSON names are mobilecore.GenOptions'.
public struct GenOptions: Encodable, Sendable {
    /// Keep the identity of an existing config at the output path (-r).
    public var regen: Bool?
    /// Enable the local API (-i).
    public var hypervisor: Bool?
    /// Put the local API behind a login (--auth).
    public var auth: Bool?
    /// The local API's listen address (--hvaddr).
    public var hypervisorAddr: String?
    /// No automatic transports to public visors (--autoconn).
    public var disablePublicAutoconnect: Bool?
    /// Autostart skychat, the SOCKS server, the VPN server.
    public var serveChat: Bool?
    public var serveProxy: Bool?
    public var serveVPN: Bool?
    /// Apps left out of the launcher (--disableapps).
    public var disableApps: [String]?
    /// The launcher's bin_path (--binpath).
    public var binPath: String?
    /// Generate offline, from the embedded deployment (--nofetch).
    public var noFetch: Bool?

    public init(
        regen: Bool? = nil, hypervisor: Bool? = nil, auth: Bool? = nil,
        hypervisorAddr: String? = nil, disablePublicAutoconnect: Bool? = nil,
        serveChat: Bool? = nil, serveProxy: Bool? = nil, serveVPN: Bool? = nil,
        disableApps: [String]? = nil, binPath: String? = nil, noFetch: Bool? = nil
    ) {
        self.regen = regen
        self.hypervisor = hypervisor
        self.auth = auth
        self.hypervisorAddr = hypervisorAddr
        self.disablePublicAutoconnect = disablePublicAutoconnect
        self.serveChat = serveChat
        self.serveProxy = serveProxy
        self.serveVPN = serveVPN
        self.disableApps = disableApps
        self.binPath = binPath
        self.noFetch = noFetch
    }

    enum CodingKeys: String, CodingKey {
        case regen, hypervisor, auth
        case hypervisorAddr = "hv_addr"
        case disablePublicAutoconnect = "disable_public_autoconnect"
        case serveChat = "serve_chat"
        case serveProxy = "serve_proxy"
        case serveVPN = "serve_vpn"
        case disableApps = "disable_apps"
        case binPath = "bin_path"
        case noFetch = "no_fetch"
    }
}

/// The Go core, as Swift sees it.
public actor CoreBridge {
    public static let shared = CoreBridge()

    private init() {}

    /// Starts the core with the config at `configPath`, with `dataDir` as its
    /// working directory, and returns once the visor's local API is up.
    public func start(configPath: String, dataDir: String) async throws {
        try await Self.blocking {
            configPath.withCString { config in
                dataDir.withCString { dir in
                    skywire_start(UnsafeMutablePointer(mutating: config), UnsafeMutablePointer(mutating: dir))
                }
            }
        }
    }

    /// Stops the core, or aborts a start in progress, and returns once its
    /// listeners are closed. Past `timeout` it throws and the core finishes
    /// closing in the background (its state stays `stopping` until then).
    public func stop(timeout: TimeInterval) async throws {
        let ms = Int32(clamping: Int((timeout * 1000).rounded()))
        try await Self.blocking { skywire_stop(ms) }
    }

    /// Writes a config to `outPath` with the core's `config gen`, from the
    /// phone's defaults with `options` applied.
    public func configGen(outPath: String, options: GenOptions = GenOptions()) async throws {
        let json = String(decoding: try JSONEncoder().encode(options), as: UTF8.self)
        try await Self.blocking {
            outPath.withCString { out in
                json.withCString { opts in
                    skywire_config_gen(UnsafeMutablePointer(mutating: out), UnsafeMutablePointer(mutating: opts))
                }
            }
        }
    }

    /// The core's state. A cheap read, safe from any thread.
    public nonisolated var state: CoreState {
        CoreState(rawValue: skywire_state()) ?? .failed
    }

    /// Why the last call failed, or why the core failed; nil when nothing did.
    public nonisolated var lastError: String? {
        Self.takeLastError()
    }

    /// Hands the core the packet-tunnel extension's utun descriptor (iOS
    /// device only; the extension keeps ownership of it).
    public nonisolated func setTunFD(_ fd: Int32) throws {
        if skywire_set_tun_fd(fd) != 0 {
            throw CoreError(message: Self.takeLastError() ?? "skywire_set_tun_fd failed")
        }
    }

    /// Installs `sink` as the receiver of the core's log, replacing the
    /// previous one; nil removes it. Can be called before `start`.
    public nonisolated func setLogSink(_ sink: CoreLogSink?) {
        LogSinkSlot.sink.withLock { $0 = sink }
        skywire_set_log_sink(sink == nil ? nil : coreLogTrampoline)
    }

    /// Runs a blocking C call on a GCD global queue (rule 4) and turns its
    /// return code into a throw. The reason is read on the same thread right
    /// after the call, so another call finishing in between rarely replaces
    /// it; the fallback message covers the case where one did.
    private static func blocking(_ call: @escaping @Sendable () -> Int32) async throws {
        let failure: String? = await withCheckedContinuation { continuation in
            DispatchQueue.global(qos: .userInitiated).async {
                guard call() != 0 else {
                    continuation.resume(returning: nil)
                    return
                }
                continuation.resume(returning: takeLastError() ?? "the core reported a failure without a reason")
            }
        }
        if let failure {
            throw CoreError(message: failure)
        }
    }

    /// skywire_last_error, copied and freed (rule 1).
    private static func takeLastError() -> String? {
        guard let cString = skywire_last_error() else { return nil }
        defer { skywire_free(UnsafeMutableRawPointer(cString)) }
        return String(cString: cString)
    }
}

/// The sink the trampoline forwards to. A lock, not an actor: the trampoline
/// runs on Go's threads and must not wait for anything.
private enum LogSinkSlot {
    static let sink = OSAllocatedUnfairLock<CoreLogSink?>(initialState: nil)
}

/// The C log callback (rule 3): no captures, so it converts to
/// @convention(c); the line is copied before the call returns.
private func coreLogTrampoline(_ level: Int32, _ line: UnsafePointer<CChar>?) {
    guard let line, let sink = LogSinkSlot.sink.withLock({ $0 }) else { return }
    sink(CoreLogLevel(rawValue: level) ?? .info, String(cString: line))
}
