import CoreClient
import SwiftUI

/// Every log source in one place, how much the core logs, and how much memory
/// the app holds (Android: DiagnosticsScreen, plus the M1 footprint gauge).
struct DiagnosticsView: View {
    @EnvironmentObject private var app: AppModel
    @ObservedObject var model: SettingsModel
    /// A level chosen while the core runs, waiting for the restart's consent.
    @State private var pendingLevel: String?

    var body: some View {
        Form {
            Section {
                NavigationLink(value: LogSource.core) {
                    VStack(alignment: .leading, spacing: 2) {
                        Text("logs_source_core")
                        Text("diag_source_core_hint").font(.footnote).foregroundStyle(.secondary)
                    }
                }
                .accessibilityIdentifier("logs-source-core")
                NavigationLink(value: LogSource.process) {
                    VStack(alignment: .leading, spacing: 2) {
                        Text("logs_source_process")
                        Text("diag_source_process_hint").font(.footnote).foregroundStyle(.secondary)
                    }
                }
                .accessibilityIdentifier("logs-source-process")
                ForEach(LogSource.apps, id: \.self) { name in
                    NavigationLink(value: LogSource.app(name)) {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(verbatim: name)
                            Text("diag_source_app_hint").font(.footnote).foregroundStyle(.secondary)
                        }
                    }
                }
            } header: {
                Text("diag_sources")
            } footer: {
                Text("diag_sources_hint")
            }

            Section {
                // A closure, not `set: choose`: Xcode 26's compiler (Swift
                // 6.3.3) crashes in IRGen on the thunk it makes for a method
                // passed as the setter.
                Picker(selection: Binding(get: { app.settings.logLevel }, set: { choose($0) })) {
                    ForEach(CoreLogLevel.levels, id: \.self) { Text(verbatim: $0).tag($0) }
                } label: {
                    Text("diag_level")
                }
                .accessibilityIdentifier("log-level-picker")
            } footer: {
                Text("diag_level_hint")
            }

            Section {
                InfoRow(
                    label: Text("diag_footprint"),
                    value: app.footprint.map { String(format: "%.1f MiB", Double($0) / 1_048_576) } ?? "—"
                )
            } footer: {
                Text("diag_footprint_hint")
            }
        }
        .navigationTitle(Text("diag_title"))
        .navigationBarTitleDisplayMode(.inline)
        .confirmationDialog(
            Text(L10n.format("diag_level_confirm_title", pendingLevel ?? "")),
            isPresented: Binding(get: { pendingLevel != nil }, set: { if !$0 { pendingLevel = nil } }),
            titleVisibility: .visible
        ) {
            Button("diag_level_confirm_action") {
                if let level = pendingLevel { model.setLogLevel(level, app) }
                pendingLevel = nil
            }
            Button("cancel", role: .cancel) { pendingLevel = nil }
        } message: {
            Text("diag_level_confirm_body")
        }
    }

    /// A running core reads the level only when it starts, so the change asks
    /// before it restarts it; a stopped one just takes it at the next start.
    private func choose(_ level: String) {
        guard level != app.settings.logLevel else { return }
        if app.coreState == .running || app.coreState == .starting {
            pendingLevel = level
        } else {
            model.setLogLevel(level, app)
        }
    }
}
