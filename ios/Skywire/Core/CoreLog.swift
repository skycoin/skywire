import CoreBridge
import Foundation
import os

/// The core's log on the app side: the last lines for the log viewer, and
/// every line in the unified log (subsystem = the bundle ID, category "core"),
/// so `xcrun simctl spawn booted log stream` and Console.app see it. On iOS
/// the process's stderr goes nowhere the app can read; this is the
/// equivalent of the process log Android captures, and it keeps working
/// while the visor's API is down (or never came up).
final class CoreLog: Sendable {
    /// Lines kept, as many as the viewer shows.
    static let capacity = 2_000

    private struct Ring {
        var lines: [String] = []
        var next = 0
        /// Lines ever appended; line n (1-based) is `sequence` n.
        var appended = 0
    }

    /// A page of lines after a cursor.
    struct Page {
        let lines: [String]
        /// The cursor to ask from next time.
        let cursor: Int
        /// Lines the ring overwrote before this reader saw them.
        let dropped: Int
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
            ring.appended += 1
        }
    }

    /// The lines appended after `cursor` (0 for all that are kept).
    func lines(after cursor: Int) -> Page {
        ring.withLock { ring in
            let oldest = ring.appended - ring.lines.count
            let from = max(cursor, oldest)
            let wanted = ring.appended - from
            let ordered = ring.lines.count < Self.capacity
                ? ring.lines
                : Array(ring.lines[ring.next...] + ring.lines[..<ring.next])
            return Page(
                lines: Array(ordered.suffix(wanted)),
                cursor: ring.appended,
                dropped: cursor > 0 ? max(0, oldest - cursor) : 0
            )
        }
    }
}
