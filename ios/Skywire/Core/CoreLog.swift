import CoreBridge
import Foundation
import os

/// The core's log on the app side: the last lines for the screen, and every
/// line in the unified log (subsystem = the bundle ID, category "core"), so
/// `xcrun simctl spawn booted log stream` and Console.app see it. On iOS the
/// process's stderr goes nowhere the app can read; this is the equivalent of
/// the process log Android captures, and it keeps working while the visor's
/// API is down.
final class CoreLog: Sendable {
    /// Lines kept for the screen.
    static let capacity = 200

    private struct Ring {
        var lines: [String] = []
        var next = 0
        /// Bumped on every append, so a reader can skip an unchanged tail.
        var generation: UInt64 = 0
    }

    private let ring = OSAllocatedUnfairLock(initialState: Ring())
    private let logger = Logger(subsystem: Bundle.main.bundleIdentifier ?? "skywire", category: "core")

    /// The sink to hand to CoreBridge. It runs on the core's threads, so it
    /// only takes a short lock and writes to the unified log.
    var sink: CoreLogSink {
        { [self] level, line in append(level, line) }
    }

    private func append(_ level: CoreLogLevel, _ line: String) {
        switch level {
        case .panic, .fatal: logger.fault("\(line, privacy: .public)")
        case .error: logger.error("\(line, privacy: .public)")
        case .warning, .info: logger.notice("\(line, privacy: .public)")
        case .debug, .trace: logger.debug("\(line, privacy: .public)")
        }
        ring.withLock { ring in
            if ring.lines.count < Self.capacity {
                ring.lines.append(line)
            } else {
                ring.lines[ring.next] = line
            }
            ring.next = (ring.next + 1) % Self.capacity
            ring.generation &+= 1
        }
    }

    /// The kept lines, oldest first, and the generation they are at.
    func tail() -> (generation: UInt64, lines: [String]) {
        ring.withLock { ring in
            let lines = ring.lines.count < Self.capacity
                ? ring.lines
                : Array(ring.lines[ring.next...] + ring.lines[..<ring.next])
            return (ring.generation, lines)
        }
    }
}
