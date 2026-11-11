import Foundation

/// A log line's severity, as the core's logger names them.
public enum LogLevel: Int, CaseIterable, Sendable, Comparable {
    case trace, debug, info, warn, error, fatal, unknown

    public static func < (lhs: LogLevel, rhs: LogLevel) -> Bool { lhs.rawValue < rhs.rawValue }

    public init(name: String) {
        switch name.uppercased() {
        case "TRACE": self = .trace
        case "DEBUG": self = .debug
        case "INFO": self = .info
        case "WARN", "WARNING": self = .warn
        case "ERROR": self = .error
        case "FATAL", "PANIC": self = .fatal
        default: self = .unknown
        }
    }
}

/// One parsed log line.
public struct LogEntry: Sendable, Equatable, Identifiable {
    /// Position in the viewer's list; lines can repeat, so the text is no id.
    public var id: Int
    public var timestamp: String
    public var level: LogLevel
    public var module: String
    public var message: String
    /// The line as it arrived (ANSI colour removed), for copy and share.
    public var raw: String

    public init(id: Int = 0, timestamp: String, level: LogLevel, module: String, message: String, raw: String) {
        self.id = id
        self.timestamp = timestamp
        self.level = level
        self.module = module
        self.message = message
        self.raw = raw
    }

    static func unparsed(_ line: String) -> LogEntry {
        LogEntry(timestamp: "", level: .unknown, module: "", message: line, raw: line)
    }
}

/// The two formats the core logs in: the runtime-logs ring buffer's logrus
/// JSON, and the text formatter's `[ts] LEVEL [module]: msg k="v"` lines (app
/// logs and the core's own log sink). App lines carry ANSI colour, because
/// in-process apps force colour on a non-terminal stream, so it is stripped
/// before parsing. The port of Android's ui/logs/LogModels.kt (LogParser).
public enum LogParser {
    /// One entry of GET …/runtime-logs.
    public static func parseJSONEntry(_ line: String) -> LogEntry {
        let trimmed = line.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let data = trimmed.data(using: .utf8),
              let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
        else { return .unparsed(trimmed) }
        func text(_ key: String) -> String { object[key].map(render) ?? "" }
        let message = text("msg")
        if message.isEmpty, object["level"] == nil { return .unparsed(trimmed) }
        let extras = object.keys
            .filter { !jsonCoreKeys.contains($0) }
            .sorted()
            .map { "\($0)=\(render(object[$0]!))" }
            .joined(separator: " ")
        return LogEntry(
            timestamp: text("time"),
            level: LogLevel(name: text("level")),
            module: text("_module"),
            message: extras.isEmpty ? message : "\(message)  \(extras)",
            raw: trimmed
        )
    }

    /// One text-formatter line.
    public static func parseTextLine(_ line: String) -> LogEntry {
        let clean = stripANSI(line).replacingOccurrences(of: #"\s+$"#, with: "", options: .regularExpression)
        guard !clean.trimmingCharacters(in: .whitespaces).isEmpty,
              let match = textLine.firstMatch(in: clean, range: NSRange(clean.startIndex..., in: clean))
        else { return .unparsed(clean) }
        func group(_ name: String) -> String {
            let range = match.range(withName: name)
            guard range.location != NSNotFound, let swiftRange = Range(range, in: clean) else { return "" }
            return String(clean[swiftRange])
        }
        return LogEntry(
            timestamp: group("ts"),
            level: LogLevel(name: group("level")),
            module: group("module"),
            message: group("msg"),
            raw: clean
        )
    }

    public static func stripANSI(_ line: String) -> String {
        ansi.stringByReplacingMatches(in: line, range: NSRange(line.startIndex..., in: line), withTemplate: "")
    }

    /// A field's value: a string as it is, anything structured as JSON.
    private static func render(_ value: Any) -> String {
        switch value {
        case let string as String:
            return string
        case let number as NSNumber:
            return CFGetTypeID(number) == CFBooleanGetTypeID() ? (number.boolValue ? "true" : "false") : number.stringValue
        case is NSNull:
            return "null"
        default:
            if let data = try? JSONSerialization.data(withJSONObject: value, options: [.sortedKeys, .withoutEscapingSlashes]) {
                return String(decoding: data, as: UTF8.self)
            }
            return "\(value)"
        }
    }

    private static let jsonCoreKeys: Set<String> = ["time", "level", "msg", "_module", "log_line"]

    private static let ansi = try! NSRegularExpression(
        pattern: "[\\u001B\\u009B][\\[\\]()#;?]*(?:(?:(?:[a-zA-Z\\d]*(?:;[a-zA-Z\\d]*)*)?\\u0007)|(?:(?:\\d{1,4}(?:;\\d{0,4})*)?[\\dA-PRZcf-ntqry=><~]))"
    )

    // The formatter sometimes puts a caller token between the level and the
    // [module] ("DEBUG Accept [proc:skychat:…]:"), and it is dropped so the
    // module still parses. It counts as one only when a [module]: follows:
    // Android's lazily optional token is never tried, because the match
    // already succeeds without it (module empty, the rest in the message).
    private static let textLine = try! NSRegularExpression(
        pattern: "^\\[(?<ts>[^\\]]+)]\\s+(?<level>TRACE|DEBUG|INFO|WARN|WARNING|ERROR|FATAL|PANIC)(?:\\s+(?<ctx>[^\\[\\s]\\S*)(?=\\s+\\[[^\\]]*]:))?\\s*(?:\\[(?<module>[^\\]]*)]:)?\\s?(?<msg>.*)$"
    )
}
