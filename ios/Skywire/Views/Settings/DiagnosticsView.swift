import CoreClient
import SwiftUI
import UIKit
import UniformTypeIdentifiers

/// Every log source in one place, all of them in one zip, and how much the core writes
/// (Android: ui/settings/DiagnosticsScreen.kt).
struct DiagnosticsView: View {
    @EnvironmentObject private var app: AppModel
    @EnvironmentObject private var settings: AppSettings
    @EnvironmentObject private var navigator: Navigator
    @EnvironmentObject private var dialogs: SkyDialogs
    @StateObject private var model = DiagnosticsModel()

    var body: some View {
        VStack(spacing: 0) {
            SkyTopBar(title: Text("diag_title"), onBack: { navigator.back() }) { EmptyView() }
            ScrollView {
                LazyVStack(spacing: 16) {
                    sources
                    export
                    level
                }
                .padding(.horizontal, 20)
                .padding(.vertical, 16)
            }
        }
        .background(Color.skyBackground)
        .onChange(of: model.notice) { notice in
            guard let notice else { return }
            dialogs.snackbar(Text(verbatim: notice))
            model.notice = nil
        }
        .fileExporter(isPresented: Binding(get: { model.bundle != nil }, set: { if !$0 { model.bundle = nil } }),
                      document: model.bundle, contentType: .zip, defaultFilename: model.bundleName) { result in
            switch result {
            case .success: model.notice = L10n.text("diag_export_done")
            case .failure(let error): model.notice = error.localizedDescription
            }
        }
    }

    // MARK: Sources

    private var sources: some View {
        SectionCard {
            Text("diag_sources").skyText(.titleMedium)
            Text("diag_sources_hint").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 4)
            VStack(spacing: 0) {
                row(Text("logs_source_core"), hint: Text("diag_source_core_hint"), .core)
                    .accessibilityIdentifier("logs-source-core")
                SkyDivider()
                row(Text("logs_source_process"), hint: Text("diag_source_process_hint"), .process)
                    .accessibilityIdentifier("logs-source-process")
                ForEach(LogSource.apps, id: \.self) { name in
                    SkyDivider()
                    row(Text(verbatim: "\(LogSource.productName(name) ?? name) (\(name))"), hint: Text("diag_source_app_hint"), .app(name))
                        .accessibilityIdentifier("logs-source-\(name)")
                }
            }
            .padding(.top, 8)
        }
    }

    private func row(_ title: Text, hint: Text, _ source: LogSource) -> some View {
        Button { navigator.push(.logs(source)) } label: {
            HStack(spacing: 12) {
                VStack(alignment: .leading, spacing: 0) {
                    title.skyText(.bodyLarge).foregroundStyle(Color.skyOnSurface)
                    hint.skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                MaterialIcon(MI.filledKeyboardArrowRight).foregroundStyle(Color.skyOnSurfaceVariant)
            }
            .padding(.vertical, 12)
            .contentShape(Rectangle())
        }
        .buttonStyle(PressStyle(layer: .skyOnSurface))
    }

    // MARK: Export

    private var export: some View {
        SectionCard {
            Text("diag_export").skyText(.titleMedium)
            Text("diag_export_hint").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 4)
            Group {
                if model.exporting {
                    HStack(spacing: 10) {
                        MaterialSpinner(size: 14, stroke: 2)
                        Text("diag_exporting").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                    }
                } else {
                    Button { Task { await model.collect(app) } } label: { Text("diag_export_action") }
                        .buttonStyle(.tonal)
                        .accessibilityIdentifier("diag-export")
                }
            }
            .padding(.top, 12)
        }
    }

    // MARK: Level

    private var level: some View {
        SectionCard {
            Text("diag_level").skyText(.titleMedium)
            Text("diag_level_hint").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 4)
            FlowLayout(spacing: 6) {
                ForEach(CoreLogLevel.levels, id: \.self) { level in
                    SkyFilterChip(label: Text(verbatim: level.uppercased()), selected: settings.logLevel == level, style: .labelSmall) {
                        choose(level)
                    }
                    .accessibilityIdentifier("log-level-\(level)")
                }
            }
            .disabled(app.busy || app.coreState == .starting || app.coreState == .stopping)
            .padding(.top, 4)
        }
    }

    /// A running core reads the level only when it starts, so the change asks before it restarts
    /// it; a stopped one takes it at the next start.
    private func choose(_ level: String) {
        guard level != settings.logLevel else { return }
        guard app.coreState == .running else {
            settings.logLevel = level
            return
        }
        dialogs.show(SkyDialog(title: Text(verbatim: L10n.format("diag_level_confirm_title", level.uppercased())),
                               message: Text("diag_level_confirm_body"),
                               actions: [SkyDialog.Action(label: Text("cancel")),
                                         SkyDialog.Action(label: Text("fleet_confirm_restart_action")) {
                                             settings.logLevel = level
                                             app.applyPinnedSettingNow()
                                         }]))
    }
}

/// Collects every source into one zip (Android: DiagnosticsViewModel.exportAll).
@MainActor
final class DiagnosticsModel: ObservableObject {
    @Published private(set) var exporting = false
    @Published var bundle: ZipFile?
    @Published var notice: String?
    private(set) var bundleName = "skywire-diagnostics.zip"

    func collect(_ app: AppModel) async {
        exporting = true
        defer { exporting = false }
        let stamp = Self.stamp.string(from: .now)
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("skywire-diagnostics-\(stamp)", isDirectory: true)
        do {
            try? FileManager.default.removeItem(at: dir)
            try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
            defer { try? FileManager.default.removeItem(at: dir) }
            var files: [String: String] = [:]
            files["device.txt"] = await device(app)
            files["process.log"] = app.log.lines(after: 0).lines.joined(separator: "\n")
            if let config = try? app.vault.readText() { files["config.json"] = Self.redacted(config) }
            // What the API holds needs a running core; a log it cannot give says why instead.
            files["core.log"] = await Self.text { try await app.client.runtimeLogs(since: 0).entries.map { LogParser.parseJSONEntry($0).raw } }
            for name in LogSource.apps {
                files["app-\(name).log"] = await Self.text { try await app.client.appLogs(name, since: nil).logs }
            }
            for (name, text) in files {
                try Data(text.utf8).write(to: dir.appendingPathComponent(name))
            }
            bundleName = "skywire-diagnostics-\(stamp).zip"
            bundle = ZipFile(data: try Self.zip(dir))
        } catch {
            notice = error.localizedDescription
        }
    }

    private static func text(_ lines: () async throws -> [String]) async -> String {
        do {
            return try await lines().joined(separator: "\n")
        } catch {
            return "unavailable: \(error.localizedDescription)"
        }
    }

    private func device(_ app: AppModel) async -> String {
        var system = utsname()
        uname(&system)
        let model = withUnsafeBytes(of: system.machine) { String(decoding: $0.prefix { $0 != 0 }, as: UTF8.self) }
        let core = app.connected ? (try? await app.client.about().build?.version) ?? nil : nil
        return [
            "app: \(Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "?") (\(Bundle.main.infoDictionary?["CFBundleVersion"] as? String ?? "?"))",
            "core: \(core ?? "not running")",
            "core state: \(app.coreState)",
            "device: \(model)",
            "system: \(UIDevice.current.systemName) \(UIDevice.current.systemVersion)",
            "public key: \(app.publicKey ?? "none")",
            "log level: \(app.settings.logLevel)",
        ].joined(separator: "\n")
    }

    /// The config without `sk`: the rest is public or this phone's own settings.
    static func redacted(_ config: String) -> String {
        guard var object = try? JSONSerialization.jsonObject(with: Data(config.utf8)) as? [String: Any] else { return "" }
        object.removeValue(forKey: "sk")
        let data = (try? JSONSerialization.data(withJSONObject: object, options: [.prettyPrinted, .sortedKeys])) ?? Data()
        return String(decoding: data, as: UTF8.self)
    }

    /// The directory as a zip, made by the file coordinator (no archive code here).
    private static func zip(_ dir: URL) throws -> Data {
        var result: Result<Data, any Error> = .failure(CocoaError(.fileReadUnknown))
        var failure: NSError?
        NSFileCoordinator().coordinate(readingItemAt: dir, options: .forUploading, error: &failure) { zipped in
            result = Result { try Data(contentsOf: zipped) }
        }
        if let failure { throw failure }
        return try result.get()
    }

    private static let stamp: DateFormatter = {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.dateFormat = "yyyyMMdd-HHmmss"
        return formatter
    }()
}

/// A zip for the save panel.
struct ZipFile: FileDocument {
    static var readableContentTypes: [UTType] { [.zip] }
    let data: Data

    init(data: Data) {
        self.data = data
    }

    init(configuration: ReadConfiguration) throws {
        data = configuration.file.regularFileContents ?? Data()
    }

    func fileWrapper(configuration: WriteConfiguration) throws -> FileWrapper {
        FileWrapper(regularFileWithContents: data)
    }
}
