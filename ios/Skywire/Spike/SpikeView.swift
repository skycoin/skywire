import CoreBridge
import SwiftUI

/// The M1 spike screen: connect and disconnect the in-app core, and see that
/// it answers, what it costs in memory, and what it logs.
struct SpikeView: View {
    @ObservedObject var model: SpikeModel
    @Environment(\.scenePhase) private var scenePhase

    var body: some View {
        NavigationStack {
            VStack(alignment: .leading, spacing: 12) {
                status
                controls
                if let error = model.lastError {
                    Text(error)
                        .font(.footnote)
                        .foregroundStyle(.red)
                        .textSelection(.enabled)
                }
                facts
                Divider()
                logTail
            }
            .padding(.horizontal, 16)
            .navigationTitle("Skywire")
            .navigationBarTitleDisplayMode(.inline)
        }
        .onAppear { model.launch() }
        .onChange(of: scenePhase) { phase in
            if phase == .active { model.becameActive() }
        }
    }

    private var status: some View {
        HStack(spacing: 8) {
            Circle()
                .fill(stateColor)
                .frame(width: 12, height: 12)
            Text("Core \(model.state.description)")
                .font(.headline)
            if model.busy || model.state == .starting || model.state == .stopping {
                ProgressView()
            }
        }
        .padding(.top, 8)
    }

    private var controls: some View {
        HStack {
            if model.state == .running || model.state == .starting {
                Button("Disconnect", role: .destructive) { model.disconnect() }
                    .buttonStyle(.borderedProminent)
                    .disabled(model.busy && model.state != .starting)
                Button("Restart") { model.restart() }
                    .buttonStyle(.bordered)
                    .disabled(model.busy || model.state != .running)
            } else {
                Button("Connect") { model.connect() }
                    .buttonStyle(.borderedProminent)
                    .disabled(model.busy || model.state == .stopping)
            }
            Spacer()
            Button("Ping") { Task { await model.refreshPing() } }
                .buttonStyle(.bordered)
        }
    }

    private var facts: some View {
        Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 6) {
            GridRow {
                Text("/api/ping").foregroundStyle(.secondary)
                if let ping = model.ping {
                    Text("\(ping.text)  ·  \(ping.at.formatted(date: .omitted, time: .standard))")
                        .foregroundStyle(ping.ok ? Color.primary : Color.red)
                        .lineLimit(2)
                } else {
                    Text("—")
                }
            }
            GridRow {
                Text("Footprint").foregroundStyle(.secondary)
                Text(footprintText)
            }
        }
        .font(.subheadline.monospacedDigit())
    }

    private var logTail: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("Core log, last \(CoreLog.capacity) lines")
                .font(.caption)
                .foregroundStyle(.secondary)
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 2) {
                        ForEach(Array(model.logLines.enumerated()), id: \.offset) { index, line in
                            Text(line)
                                .font(.system(size: 10, design: .monospaced))
                                .textSelection(.enabled)
                                .id(index)
                        }
                    }
                }
                .onChange(of: model.logLines) { lines in
                    if let last = lines.indices.last {
                        proxy.scrollTo(last, anchor: .bottom)
                    }
                }
            }
        }
    }

    private var footprintText: String {
        guard let bytes = model.footprint else { return "—" }
        return String(format: "%.1f MiB phys_footprint", Double(bytes) / 1_048_576)
    }

    private var stateColor: Color {
        switch model.state {
        case .running: .green
        case .starting, .stopping: .orange
        case .failed: .red
        case .stopped: .gray
        }
    }
}
