import CoreClient
import SwiftUI

/// One viewer for every log (Android: ui/logs/LogViewerScreen.kt), the source chosen before
/// it opens. Log text is never translated: it is read next to a desktop's `skywire cli`, and a
/// translated line is one nobody can search for.
struct LogsView: View {
    @EnvironmentObject private var app: AppModel
    @EnvironmentObject private var navigator: Navigator
    @EnvironmentObject private var dialogs: SkyDialogs
    @StateObject private var model = LogsModel()
    let source: LogSource

    var body: some View {
        VStack(spacing: 0) {
            SkyTopBar(title: Text(verbatim: title), onBack: { navigator.back() }) {
                Button { model.following.toggle() } label: {
                    MaterialIcon(model.following ? MI.filledPause : MI.filledPlayArrow).frame(width: 48, height: 48)
                }
                .buttonStyle(PressStyle(layer: .skyOnSurface, shape: AnyShape(Circle())))
                .accessibilityLabel(model.following ? Text("logs_pause") : Text("logs_follow"))
                ShareLink(item: model.shareText) {
                    MaterialIcon(MI.filledShare).frame(width: 48, height: 48)
                }
                .accessibilityLabel(Text("logs_share"))
            }
            .foregroundStyle(Color.skyOnBackground)
            filterBar
            if model.dropped > 0 {
                Text(verbatim: L10n.format("logs_dropped", model.dropped)).skyText(.labelSmall)
                    .foregroundStyle(Color.skyOnSurfaceVariant)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 16).padding(.vertical, 4)
            }
            if let error = model.error {
                Text(verbatim: error).skyText(.labelSmall).foregroundStyle(Color.skyError)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 16).padding(.vertical, 4)
            }
            lines
        }
        .background(Color.skyBackground)
        .task(id: source) { await model.run(source, app: app) }
    }

    /// Android's titles: the source's name, an app's product name, a Fleet visor's given name.
    private var title: String {
        switch source {
        case .core: L10n.text("logs_source_core")
        case .process: L10n.text("logs_source_process")
        case let .app(name): LogSource.productName(name) ?? name
        case let .visor(pk): FleetModel().label(pk)
        }
    }

    private var filterBar: some View {
        VStack(spacing: 0) {
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 6) {
                    ForEach([LogLevel.error, .warn, .info, .debug, .trace], id: \.self) { level in
                        SkyFilterChip(label: Text(verbatim: level.name), selected: model.levels.contains(level), style: .labelSmall) {
                            if model.levels.contains(level) { model.levels.remove(level) } else { model.levels.insert(level) }
                        }
                    }
                }
                .frame(minWidth: UIScreen.main.bounds.width - 24)
            }
            ZStack(alignment: .leading) {
                if model.query.isEmpty {
                    Text("logs_search_hint").skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant)
                        .accessibilityHidden(true)
                }
                TextField(text: $model.query, prompt: Text(verbatim: "")) { Text("logs_search_hint") }
                    .font(Font(SkyTextStyle.bodyMedium.uiFont()))
                    .foregroundStyle(Color.skyOnSurface)
                    .tint(.skyPrimary)
                    .autocorrectionDisabled()
                    .textInputAutocapitalization(.never)
                    .accessibilityLabel(Text("logs_search_hint"))
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 10)
            .background(Color.skySurfaceVariant, in: .sky(SkyRadius.small))
            .padding(.vertical, 8)
        }
        .padding(.horizontal, 12)
    }

    @ViewBuilder
    private var lines: some View {
        let visible = model.visible
        if visible.isEmpty {
            VStack(spacing: 12) {
                if model.loading {
                    MaterialSpinner(size: 24, stroke: 2)
                    Text("logs_loading").skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant)
                } else {
                    Text("logs_empty").skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant)
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 0) {
                        ForEach(visible) { entry in
                            Button {
                                UIPasteboard.general.string = entry.raw
                                dialogs.toast(Text("copied_to_clipboard"))
                            } label: { LogLine(entry: entry) }
                            .buttonStyle(PressStyle(layer: .skyOnSurface))
                            .id(entry.id)
                        }
                    }
                    .padding(.horizontal, 12)
                    .padding(.vertical, 8)
                }
                .onChange(of: visible.last?.id) { last in
                    guard model.following, let last else { return }
                    withAnimation { proxy.scrollTo(last, anchor: .bottom) }
                }
                .onAppear { if let last = visible.last?.id { proxy.scrollTo(last, anchor: .bottom) } }
            }
        }
    }
}

/// One line: the level's letter, then time and module over the message.
private struct LogLine: View {
    let entry: LogEntry

    var body: some View {
        HStack(alignment: .top, spacing: 0) {
            Text(verbatim: String((entry.level == .unknown ? "U" : entry.level.name).prefix(1)))
                .skyText(.labelSmall)
                .foregroundStyle(entry.level.color)
                .padding(.trailing, 8)
                .padding(.top, 2)
            VStack(alignment: .leading, spacing: 0) {
                let meta = [String(entry.timestamp.suffix(12)), entry.module].filter { !$0.isEmpty }.joined(separator: " · ")
                if !meta.isEmpty {
                    Text(verbatim: meta).skyText(.labelSmall).foregroundStyle(Color.skyOnSurfaceVariant).lineLimit(1)
                }
                Text(verbatim: entry.message)
                    .skyText(.bodySmall, mono: true)
                    .foregroundStyle(entry.level == .error || entry.level == .fatal ? Color.skyError : Color.skyOnSurface)
                    .multilineTextAlignment(.leading)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(.vertical, 3)
        .contentShape(Rectangle())
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

    /// Android's fixed level colours, the same in both themes.
    var color: Color {
        switch self {
        case .error, .fatal: Color(hex: 0xDC2626)
        case .warn: Color(hex: 0xF59E0B)
        case .info: Color(hex: 0x0F7BF4)
        default: Color(hex: 0x9CA3AF)
        }
    }
}
