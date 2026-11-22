import CoreClient
import Foundation

/// How the app's recent runs ended, for the diagnostics export (Android: the exit reasons
/// DiagnosticsExport reads from ActivityManager, and CrashLog). iOS keeps no such record an
/// app can read, so each run writes the state it is in and the next launch reads the last one:
/// a run that ended active was in front of the user, one that ended in the background was
/// stopped by the system or from the app switcher. An Objective-C exception also leaves its
/// stack in app-crash.log; a Swift trap does not, it ends the process at once.
enum AppExits {
    static let defaultDir = CorePaths.appSupport().dataDir
    /// Android's MAX_EXITS: the newest this many are kept.
    static let maxExits = 16
    static let maxCrashBytes = 64 * 1024

    static func stateFile(_ dir: URL = defaultDir) -> URL { dir.appendingPathComponent("app-state.json") }
    static func logFile(_ dir: URL = defaultDir) -> URL { dir.appendingPathComponent("app-exits.log") }
    static func crashFile(_ dir: URL = defaultDir) -> URL { dir.appendingPathComponent("app-crash.log") }

    private struct State: Codable {
        var state: String
        var since: Date
        var started: Date
    }

    /// At launch: writes down how the previous run ended, then marks this one as started.
    static func launched(now: Date = .now, dir: URL = defaultDir) {
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        if let previous = read(dir) {
            let line = "\(stamp(previous.started)) run ended \(previous.state), its last change at \(stamp(previous.since))"
            var lines = ((try? String(contentsOf: logFile(dir), encoding: .utf8)) ?? "")
                .split(separator: "\n").map(String.init)
            lines.insert(line, at: 0)
            try? Data((lines.prefix(maxExits).joined(separator: "\n") + "\n").utf8).write(to: logFile(dir), options: .atomic)
        }
        write(State(state: "starting", since: now, started: now), dir)
    }

    /// The scene became active, inactive or background; `terminated` when iOS says it is ending the app.
    static func entered(_ state: String, now: Date = .now, dir: URL = defaultDir) {
        guard var current = read(dir) else { return }
        current.state = state
        current.since = now
        write(current, dir)
    }

    /// Keeps an uncaught Objective-C exception's stack, then lets the previous handler run.
    static func installCrashHandler() {
        previousHandler = NSGetUncaughtExceptionHandler()
        NSSetUncaughtExceptionHandler { exception in
            AppExits.recordCrash(exception)
            AppExits.previousHandler?(exception)
        }
    }

    static func recordCrash(_ exception: NSException, now: Date = .now, dir: URL = defaultDir) {
        let file = crashFile(dir)
        var text = (try? String(contentsOf: file, encoding: .utf8)) ?? ""
        if text.utf8.count > maxCrashBytes {
            text = String(text.suffix(maxCrashBytes / 2))
        }
        text += "=== \(stamp(now)) uncaught \(exception.name.rawValue): \(exception.reason ?? "") ===\n"
            + exception.callStackSymbols.joined(separator: "\n") + "\n"
        try? Data(text.utf8).write(to: file, options: .atomic)
        entered("crashed", now: now, dir: dir)
    }

    /// The export's copy, with how to read it on top.
    static func report(dir: URL = defaultDir) -> String {
        let exits = (try? String(contentsOf: logFile(dir), encoding: .utf8)) ?? "No exits recorded.\n"
        return """
        How the app's recent runs ended, newest first, as the app saw them. "active" means it was \
        in front of the user (a crash, or the system ending it there); "background" means the \
        system stopped it, for memory or a long suspension, or it was swiped away; "crashed" \
        means an exception, with its stack in app-crash.log.

        """ + exits
    }

    nonisolated(unsafe) private static var previousHandler: (@convention(c) (NSException) -> Void)?

    private static func read(_ dir: URL) -> State? {
        guard let data = try? Data(contentsOf: stateFile(dir)) else { return nil }
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        return try? decoder.decode(State.self, from: data)
    }

    private static func write(_ state: State, _ dir: URL) {
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        if let data = try? encoder.encode(state) {
            try? data.write(to: stateFile(dir), options: .atomic)
        }
    }

    private static func stamp(_ date: Date) -> String {
        date.ISO8601Format()
    }
}
