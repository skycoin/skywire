import CoreClient
import SwiftUI

/// Where the app screens live (Android: HubScreen, behind the bar's raised
/// logo): one row per app, its status dot from the visor's own app state.
/// SkyChat is a tab as well, and its row switches to it.
struct HubView: View {
    @EnvironmentObject private var app: AppModel
    @StateObject private var model = HubModel()
    let openChat: () -> Void

    var body: some View {
        NavigationStack {
            List {
                Section {
                    NavigationLink(value: HubRoute.socks) {
                        HubRow(name: L10n.key("app_skysocks"), subtitle: L10n.key("hub_socks_sub"), systemImage: "network", status: model.status(SocksProfile.app))
                    }
                    .accessibilityIdentifier("hub-socks")
                    NavigationLink(value: HubRoute.dex) {
                        HubRow(name: L10n.key("app_skydex"), subtitle: L10n.key("hub_dex_sub"), systemImage: "arrow.left.arrow.right", status: model.status(SkydexProfile.app))
                    }
                    .accessibilityIdentifier("hub-dex")
                    NavigationLink(value: HubRoute.fleet) {
                        HubRow(name: L10n.key("app_fleet"), subtitle: L10n.key("hub_fleet_sub"), systemImage: "server.rack", status: nil)
                    }
                    .accessibilityIdentifier("hub-fleet")
                    Button(action: { openChat() }) {
                        HubRow(name: L10n.key("app_skychat"), subtitle: L10n.key("hub_chat_sub"), systemImage: "bubble.left.and.bubble.right", status: model.status(SkychatProfile.app))
                    }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("hub-chat")
                } header: {
                    Text("hub_section_apps")
                }
            }
            .navigationTitle(Text("tab_hub_description"))
            .navigationDestination(for: HubRoute.self) { route in
                switch route {
                case .socks: SocksView(settings: app.settings)
                case .dex: DexView()
                case .fleet: FleetView(settings: app.settings)
                }
            }
            .task(id: app.connected) { await model.poll(app) }
        }
    }
}

enum HubRoute: Hashable {
    case socks, dex, fleet
}

/// One app's row: its symbol, name and what it is, and a dot for whether it
/// runs (none while the core is down: nothing is known then).
private struct HubRow: View {
    let name: LocalizedStringKey
    let subtitle: LocalizedStringKey
    let systemImage: String
    let status: Color?

    var body: some View {
        HStack(spacing: 14) {
            Image(systemName: systemImage)
                .font(.title3)
                .foregroundStyle(Color.skywire)
                .frame(width: 32)
            VStack(alignment: .leading, spacing: 2) {
                Text(name).font(.body.weight(.semibold))
                Text(subtitle).font(.footnote).foregroundStyle(.secondary)
            }
            Spacer()
            if let status {
                StatusDot(color: status)
            }
        }
        .padding(.vertical, 4)
        .contentShape(Rectangle())
    }
}

/// The hub's status dots: each app's state from the visor, polled while the
/// hub is on screen and the core connected (Android: HubViewModel.poll).
@MainActor
final class HubModel: ObservableObject {
    @Published private(set) var states: [String: AppState] = [:]

    static let apps = [SocksProfile.app, SkychatProfile.app, SkydexProfile.app]
    static let pollInterval: Duration = .seconds(4)

    func poll(_ app: AppModel) async {
        guard app.connected else {
            states = [:]
            return
        }
        while !Task.isCancelled {
            for name in Self.apps {
                do {
                    states[name] = try await app.client.app(name)
                } catch is CancellationError {
                    return
                } catch {
                    if app.handle(error) { return }
                    states[name] = nil
                }
            }
            do { try await Task.sleep(for: Self.pollInterval) } catch { return }
        }
    }

    /// Green running, amber starting, red errored, grey stopped; nil unknown.
    func status(_ name: String) -> Color? {
        guard let state = states[name] else { return nil }
        switch state.status {
        case AppState.statusRunning: return .success
        case AppState.statusStarting: return .warning
        case AppState.statusErrored: return .red
        default: return .secondary
        }
    }
}
