import CoreClient
import SwiftUI

/// One viewer for every log (Android: LogViewerScreen): the visor's runtime
/// log, each app's log, and the core's own output. Log text is never
/// translated: it is read next to a desktop's `skywire cli`, and a translated
/// line is one nobody can search for.
struct LogsView: View {
    @EnvironmentObject private var app: AppModel
    @StateObject private var model = LogsModel()
    @State private var source: LogSource

    init(source: LogSource) {
        _source = State(initialValue: source)
    }

    var body: some View {
        VStack(spacing: 0) {
            controls
            Divider()
            lines
        }
        .navigationTitle(Text("logs_title"))
        .navigationBarTitleDisplayMode(.inline)
        .searchable(text: $model.query, placement: .navigationBarDrawer(displayMode: .automatic), prompt: Text("logs_search_hint"))
        .toolbar {
            ToolbarItemGroup(placement: .primaryAction) {
                Button {
                    model.following.toggle()
                } label: {
                    Label(model.following ? L10n.key("logs_pause") : L10n.key("logs_follow"), systemImage: model.following ? "pause.circle" : "play.circle")
                }
                ShareLink(item: model.shareText) {
                    Label("logs_share", systemImage: "square.and.arrow.up")
                }
                .disabled(model.visible.isEmpty)
            }
        }
        .task(id: source) { await model.run(source, app: app) }
    }

    private var controls: some View {
        VStack(spacing: 8) {
            Picker(selection: kind) {
                Text("logs_source_core").tag(0)
                Text("logs_source_process").tag(1)
                Text("logs_source_apps").tag(2)
            } label: {
                Text("logs_source")
            }
            .pickerStyle(.segmented)
            if case let .app(name) = source {
                Picker(selection: Binding(get: { name }, set: { source = .app($0) })) {
                    ForEach(LogSource.apps, id: \.self) { Text(verbatim: $0).tag($0) }
                } label: {
                    Text("logs_source_apps")
                }
                .pickerStyle(.menu)
            }
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 6) {
                    ForEach([LogLevel.error, .warn, .info, .debug, .trace], id: \.self) { level in
                        Toggle(isOn: Binding(
                            get: { model.levels.contains(level) },
                            set: { on in
                                if on { model.levels.insert(level) } else { model.levels.remove(level) }
                            }
                        )) {
                            Text(verbatim: level.name).font(.caption.monospaced())
                        }
                        .toggleStyle(.button)
                        .buttonStyle(.bordered)
                        .controlSize(.small)
                    }
                }
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
    }

    /// The segment for `source`: Core, Process, or an app.
    private var kind: Binding<Int> {
        Binding(
            get: {
                switch source {
                case .core: 0
                case .process: 1
                case .app: 2
                }
            },
            set: { index in
                switch index {
                case 0: source = .core
                case 1: source = .process
                default: if case .app = source {} else { source = .app(LogSource.apps[0]) }
                }
            }
        )
    }

    private var lines: some View {
        let visible = model.visible
        return ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 4) {
                    if model.dropped > 0 {
                        Text(L10n.format("logs_dropped", model.dropped))
                            .font(.caption).foregroundStyle(.orange)
                    }
                    if let error = model.error {
                        Text(error).font(.caption).foregroundStyle(.red)
                        if source != .process {
                            Text("logs_api_down").font(.caption).foregroundStyle(.secondary)
                        }
                    }
                    if visible.isEmpty {
                        Text(model.loading ? L10n.key("logs_loading") : L10n.key("logs_empty"))
                            .font(.footnote).foregroundStyle(.secondary)
                            .frame(maxWidth: .infinity, alignment: .center)
                            .padding(.top, 40)
                    }
                    ForEach(visible) { entry in
                        LogLine(entry: entry).id(entry.id)
                    }
                }
                .padding(.horizontal, 12)
                .padding(.vertical, 8)
                .textSelection(.enabled)
            }
            .onChange(of: visible.last?.id) { last in
                guard model.following, let last else { return }
                proxy.scrollTo(last, anchor: .bottom)
            }
        }
    }
}

private struct LogLine: View {
    let entry: LogEntry

    var body: some View {
        VStack(alignment: .leading, spacing: 1) {
            HStack(spacing: 6) {
                if entry.level != .unknown {
                    Text(verbatim: entry.level.name).foregroundStyle(entry.level.color).fontWeight(.semibold)
                }
                // The time of day is enough on a phone; the date is in the raw
                // line, which is what copy and share carry.
                Text(verbatim: timeOfDay).foregroundStyle(.secondary)
                if !entry.module.isEmpty {
                    Text(verbatim: entry.module).foregroundStyle(.secondary).lineLimit(1)
                }
            }
            Text(verbatim: entry.message)
                .foregroundStyle(entry.level >= .error && entry.level != .unknown ? Color.red : Color.primary)
        }
        .font(.caption2.monospaced())
    }

    /// "06:07:11.0882" out of "2026-09-30T06:07:11.0882+08:00".
    private var timeOfDay: String {
        guard let t = entry.timestamp.firstIndex(of: "T") else { return entry.timestamp }
        return String(entry.timestamp[entry.timestamp.index(after: t)...].prefix { $0.isNumber || $0 == ":" || $0 == "." })
    }
}

extension LogLevel {
    var name: String {
        switch self {
        case .trace: "TRACE"
        case .debug: "DEBUG"
        case .info: "INFO"
        case .warn: "WARN"
        case .error: "ERROR"
        case .fatal: "FATAL"
        case .unknown: ""
        }
    }

    var color: Color {
        switch self {
        case .error, .fatal: .red
        case .warn: .orange
        case .info: .skywire
        default: .secondary
        }
    }
}
