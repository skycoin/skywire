import CoreClient
import Foundation

/// What the log viewer can tail (Android: LogSources).
enum LogSource: Hashable, Sendable {
    /// The visor's runtime ring buffer, over the local API.
    case core
    /// The core's own log sink (CoreLog): what the process wrote, kept in the
    /// app, so it works while the API is down or never came up.
    case process
    /// One app's log, over the local API.
    case app(String)
    /// A Fleet visor's runtime log, fetched over dmsg through the local API.
    case visor(String)

    /// The apps the viewer offers.
    static let apps = [SkychatProfile.app, SocksProfile.app, "vpn-client", SkydexProfile.app]

    /// An app's product name ("SkyChat"), or nil for one the app does not know.
    static func productName(_ app: String) -> String? {
        switch app {
        case SkychatProfile.app: L10n.text("app_skychat")
        case SocksProfile.app: L10n.text("app_skysocks")
        case "vpn-client": L10n.text("app_skyvpn")
        case SkydexProfile.app: L10n.text("app_skydex")
        default: nil
        }
    }
}

/// Tails one source (Android: LogViewModel). Polling stops while paused and
/// resumes from the kept cursor, so a pause loses nothing the source still
/// holds.
@MainActor
final class LogsModel: ObservableObject {
    @Published private(set) var entries: [LogEntry] = []
    /// The first page for this source has not arrived yet.
    @Published private(set) var loading = true
    @Published var following = true
    /// Levels to show; empty shows everything.
    @Published var levels: Set<LogLevel> = []
    @Published var query = ""
    /// Lines the source overwrote before this viewer read them.
    @Published private(set) var dropped = 0
    @Published private(set) var error: String?

    static let pollInterval: Duration = .milliseconds(1_200)
    static let maxEntries = 2_000
    static let maxShareLines = 500

    private var runtimeSince: Int64 = 0
    private var appSince: String?
    private var sinkCursor = 0
    private var nextID = 0

    /// Level chips add up; ERROR also shows FATAL, and a line that did not
    /// parse always shows: it is often the crash text a filter must not hide.
    var visible: [LogEntry] {
        entries.filter { entry in
            let levelOK = levels.isEmpty || levels.contains(entry.level) || entry.level == .unknown
                || (entry.level == .fatal && levels.contains(.error))
            return levelOK && (query.isEmpty || entry.raw.localizedCaseInsensitiveContains(query))
        }
    }

    /// The lines the share sheet gets, capped.
    var shareText: String {
        visible.suffix(Self.maxShareLines).map(\.raw).joined(separator: "\n")
    }

    /// Runs until cancelled (the view's `.task(id: source)`).
    func run(_ source: LogSource, app: AppModel) async {
        reset()
        while !Task.isCancelled {
            if following {
                do {
                    try await poll(source, app: app)
                    error = nil
                } catch is CancellationError {
                    return
                } catch {
                    if !app.handle(error) {
                        self.error = error.localizedDescription
                    }
                }
                loading = false
            }
            try? await Task.sleep(for: Self.pollInterval)
        }
    }

    private func reset() {
        entries = []
        loading = true
        dropped = 0
        error = nil
        runtimeSince = 0
        appSince = nil
        sinkCursor = 0
    }

    private func poll(_ source: LogSource, app: AppModel) async throws {
        switch source {
        case .process:
            let page = app.log.lines(after: sinkCursor)
            sinkCursor = page.cursor
            append(page.lines.map(LogParser.parseTextLine), dropped: page.dropped)
        case .core, .visor:
            // No session probe here: the client logs in again on a 401 by
            // itself, and probing /api/user each poll would fill the very
            // buffer this shows.
            let first = runtimeSince == 0
            let remote: String? = if case let .visor(pk) = source { pk } else { nil }
            let delta = try await app.client.runtimeLogs(since: runtimeSince, pk: remote)
            if delta.latest < runtimeSince {
                // The visor restarted and its counter began again: start over,
                // so the new run's first lines are not skipped.
                runtimeSince = 0
                return
            }
            runtimeSince = delta.latest
            // On the first poll the whole ring counts as dropped relative to
            // cursor 0: that is the buffer's age, not lines this viewer missed.
            append(delta.entries.map(LogParser.parseJSONEntry), dropped: first ? 0 : Int(delta.dropped))
        case let .app(name):
            let page = try await app.client.appLogs(name, since: appSince)
            let previous = appSince
            // The server repeats the boundary line whenever its fraction ends
            // in zero (RFC 3339 drops trailing zeros), so drop what is at or
            // before the cursor, but keep lines with no timestamp.
            let fresh = page.logs.map(LogParser.parseTextLine).filter {
                previous == nil || $0.timestamp.isEmpty || $0.timestamp > previous!
            }
            if !page.lastLogTimestamp.isEmpty {
                appSince = page.lastLogTimestamp
            }
            append(fresh)
        }
    }

    private func append(_ fresh: [LogEntry], dropped: Int = 0) {
        guard !fresh.isEmpty || dropped > 0 else { return }
        var numbered = fresh
        for index in numbered.indices {
            numbered[index].id = nextID
            nextID += 1
        }
        entries = Array((entries + numbered).suffix(Self.maxEntries))
        self.dropped += dropped
    }
}
