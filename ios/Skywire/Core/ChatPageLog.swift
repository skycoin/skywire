import CoreClient
import Foundation

/// The chat's own events (skychat's answers at bring-up, the event stream, the
/// one-time import), kept on disk for the diagnostics export (Android:
/// core/ChatPageLog.kt). None of it reaches the core's logs.
enum ChatPageLog {
    static let maxBytes = 256 * 1024
    static let file = CorePaths.appSupport().dataDir.appendingPathComponent("skychat-page.log")

    /// Appends happen here, never on the main thread that reports them.
    private static let queue = DispatchQueue(label: "chat-page-log", qos: .utility)

    static func add(_ line: String, to target: URL = file) {
        let stamped = "\(Date.now.ISO8601Format(.init(includingFractionalSeconds: true))) \(line)\n"
        queue.async { append(stamped, to: target) }
    }

    /// Waits until every line added so far is on disk.
    static func flush() {
        queue.sync {}
    }

    /// The file a full log rolled over into.
    static func previous(of target: URL = file) -> URL {
        target.deletingLastPathComponent().appendingPathComponent(target.lastPathComponent + ".1")
    }

    private static func append(_ text: String, to target: URL) {
        let manager = FileManager.default
        try? manager.createDirectory(at: target.deletingLastPathComponent(), withIntermediateDirectories: true)
        if let size = (try? manager.attributesOfItem(atPath: target.path))?[.size] as? Int, size > maxBytes {
            try? manager.removeItem(at: previous(of: target))
            try? manager.moveItem(at: target, to: previous(of: target))
        }
        guard let handle = try? FileHandle(forWritingTo: target) else {
            try? Data(text.utf8).write(to: target)
            return
        }
        defer { try? handle.close() }
        _ = try? handle.seekToEnd()
        try? handle.write(contentsOf: Data(text.utf8))
    }
}
