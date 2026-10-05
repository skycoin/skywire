import CoreClient
import SwiftUI
import UIKit

/// SkyVPN (Android: VpnScreen), off on iOS until the tunnel exists (M7, Lane D). The cards
/// that hold everywhere work (transport, route length); the tunnel's own controls are inert.
struct VpnView: View {
    @EnvironmentObject private var app: AppModel
    @EnvironmentObject private var navigator: Navigator
    @EnvironmentObject private var dialogs: SkyDialogs
    @ObservedObject private var settings: AppSettings
    @StateObject private var model = VpnModel()
    @StateObject private var settingsModel = SettingsModel()

    init(settings: AppSettings) {
        self.settings = settings
    }

    var body: some View {
        VStack(spacing: 0) {
            SkyTopBar(title: Text("app_skyvpn"), onBack: { navigator.back() }, help: .vpn)
            ScrollView {
                LazyVStack(spacing: 16) {
                    statusCard
                    networkCard
                    killswitchCard
                    TransportPreferenceCard(current: settings.transportPrimary, enabled: true) {
                        dialogs.presentSheet {
                            TransportPreferenceSheet(current: settings.transportPrimary) { settingsModel.setTransportPrimary($0, app) }
                        }
                    }
                    MinHopsCard(hops: settingsModel.minHops ?? 0, enabled: app.connected,
                                multiHopAvailable: settings.publicAutoconnect) { settingsModel.setMinHops($0, app) }
                    if app.connected {
                        exits
                    }
                }
                .padding(.horizontal, 20)
                .padding(.vertical, 16)
            }
        }
        .background(Color.skyBackground)
        .task(id: app.connected) {
            await settingsModel.load(app)
            await model.run(app)
        }
    }

    private var statusCard: some View {
        SectionCard {
            HStack(spacing: 8) {
                StatusDot(color: .skyOnSurfaceVariant)
                Text("state_disconnected").skyText(.titleMedium)
            }
            VStack(alignment: .leading, spacing: 0) {
                if !app.connected {
                    Text(app.coreState == .stopped ? L10n.key("vpn_core_offline") : L10n.key("vpn_core_starting"))
                        .skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant)
                } else {
                    Button {} label: { Text("connect").frame(maxWidth: .infinity) }
                        .buttonStyle(.filled)
                        .disabled(true)
                }
                Text("vpn_ios_pending").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 8)
            }
            .padding(.top, 16)
        }
    }

    /// NetworkAddressCard: this device's public address, and where traffic exits (not through SkyVPN).
    private var networkCard: some View {
        SectionCard {
            Text("net_addr_title").skyText(.labelMedium).foregroundStyle(Color.skyOnSurfaceVariant)
            Group {
                if let ip = model.overview?.publicIPOrNil {
                    SkyInfoRow(label: Text("net_addr_device"), value: ip, mono: true)
                } else {
                    SkyInfoRow(label: Text("net_addr_device"), value: deviceFallback)
                }
                SkyInfoRow(label: Text("net_addr_exit"), value: L10n.text("net_addr_exit_none"))
            }
            .padding(.top, 6)
            Text("net_addr_hint").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 8)
        }
    }

    private var deviceFallback: String {
        guard let overview = model.overview else { return L10n.text("net_addr_waiting") }
        return overview.isSymmetricNAT ? L10n.text("net_addr_carrier_nat") : L10n.text("net_addr_unknown")
    }

    private var killswitchCard: some View {
        SectionCard {
            HStack {
                Text("vpn_killswitch_title").skyText(.titleMedium).frame(maxWidth: .infinity, alignment: .leading)
                Toggle(isOn: .constant(false)) { Text("vpn_killswitch_title") }
                    .toggleStyle(.skySwitch)
                    .allowsHitTesting(false)
            }
            Text("vpn_killswitch_hint").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 4)
        }
    }

    @ViewBuilder
    private var exits: some View {
        let favorites = model.favoriteRows
        if !favorites.isEmpty {
            FavoritesHeader(count: favorites.count)
            ForEach(favorites, id: \.entry.address) { row in
                exitRow(row.entry, unlisted: !row.listed)
            }
        }
        VStack(spacing: 0) {
            HStack {
                Text(verbatim: L10n.format("vpn_servers", model.servers.count)).skyText(.titleMedium)
                Spacer()
                if model.loading {
                    MaterialSpinner(size: 20, stroke: 2).frame(width: 48, height: 48)
                } else {
                    Button { Task { await model.loadServers(app) } } label: {
                        MaterialIcon(MI.filledRefresh).foregroundStyle(Color.skyOnBackground).frame(width: 48, height: 48)
                    }
                    .buttonStyle(PressStyle())
                    .accessibilityLabel(Text("socks_refresh"))
                }
            }
            SkyOutlinedTextField(placeholder: L10n.key("vpn_search_hint"), text: $model.query)
        }
        ForEach(model.filtered, id: \.address) { entry in
            exitRow(entry, unlisted: false)
        }
        if let error = model.error {
            VStack(alignment: .leading, spacing: 0) {
                Text(verbatim: error).skyText(.bodySmall).foregroundStyle(Color.skyError)
                Button { Task { await model.loadServers(app) } } label: { Text("socks_retry") }.buttonStyle(.tonal)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        } else if !model.loading, model.filtered.isEmpty {
            Text(model.servers.isEmpty ? L10n.key("vpn_servers_none") : L10n.key("vpn_servers_empty"))
                .skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant)
                .multilineTextAlignment(.center).frame(maxWidth: .infinity)
        }
    }

    /// An exit can be starred, not connected to: there is no tunnel on iOS yet.
    private func exitRow(_ entry: ServiceEntry, unlisted: Bool) -> some View {
        ServerRow(entry: entry, selected: false, unlisted: unlisted, favorite: model.isFavorite(entry.pk),
                  onTap: {}, onStar: { model.toggleFavorite(SavedServer(entry)) })
    }
}

/// MinHopsCard (Android, SkyVPN): 1, 2, 3 or a custom route length, with what each means.
private struct MinHopsCard: View {
    let hops: Int
    let enabled: Bool
    let multiHopAvailable: Bool
    let set: (Int) -> Void
    @EnvironmentObject private var dialogs: SkyDialogs

    var body: some View {
        SectionCard {
            Text("hops_title").skyText(.labelMedium).foregroundStyle(Color.skyOnSurfaceVariant)
            HStack(spacing: 8) {
                tile(Text(verbatim: L10n.format("hub_hops", 1)), Text("hops_label_fastest"), selected: hops == 1, enabled: enabled && hops != 1) { set(1) }
                tile(Text(verbatim: L10n.format("hub_hops", 2)), Text("hops_label_balanced"), selected: hops == 2,
                     enabled: enabled && multiHopAvailable && hops != 2) { set(2) }
                tile(Text(verbatim: L10n.format("hub_hops", 3)), Text("hops_label_private"), selected: hops == 3,
                     enabled: enabled && multiHopAvailable && hops != 3) { set(3) }
                tile(custom ? Text(verbatim: L10n.format("hub_hops", hops)) : Text("hops_custom_more"), Text("hops_label_custom"),
                     selected: custom, enabled: enabled && multiHopAvailable) {
                    dialogs.presentSheet { CustomHopsSheet(current: custom ? hops : nil, save: set) }
                }
            }
            .padding(.top, 10)
            Text(hint).skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 10)
        }
    }

    private var custom: Bool { hops > 3 }

    private var hint: LocalizedStringKey {
        if !multiHopAvailable { return L10n.key("hops_hint_needs_autoconnect") }
        if hops == 1 { return L10n.key("hops_hint_direct") }
        if hops >= 2 { return L10n.key("hops_hint_multi") }
        return L10n.key("hops_hint_unknown")
    }

    /// HopTile: primary when selected; dimmed to 40 % when neither enabled nor selected.
    private func tile(_ title: Text, _ label: Text, selected: Bool, enabled: Bool, action: @escaping () -> Void) -> some View {
        let ink: Color = selected ? .skyOnPrimary : .skyOnSurface
        let dim = !enabled && !selected ? 0.4 : 1
        return Button(action: action) {
            VStack(spacing: 0) {
                title.skyText(.titleMedium).foregroundStyle(ink.opacity(dim)).lineLimit(1).minimumScaleFactor(0.8)
                label.skyText(.labelSmall).foregroundStyle(ink.opacity(0.75 * dim)).multilineTextAlignment(.center)
            }
            .padding(.vertical, 12)
            .padding(.horizontal, 8)
            .frame(maxWidth: .infinity)
            .background(selected ? Color.skyPrimary : Color.skyContainerHighest, in: .sky(SkyRadius.medium))
            .contentShape(RoundedRectangle(cornerRadius: SkyRadius.medium))
        }
        .buttonStyle(PressStyle())
        .disabled(!enabled)
        .accessibilityAddTraits(selected ? .isSelected : [])
    }
}

/// CustomHopsSheet: a minimum from 1 to 10 (the route finder searches 10 deep at most).
private struct CustomHopsSheet: View {
    let current: Int?
    let save: (Int) -> Void
    @State private var text = ""

    private var valid: Bool { Int(text).map { (1...10).contains($0) } ?? false }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("hops_custom_title").skyText(.titleMedium)
            Text("hops_custom_hint").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 8)
            SkyOutlinedTextField(label: L10n.key("hops_custom_label"), text: $text, isError: !text.isEmpty && !valid,
                                 keyboard: .numberPad, notch: .skyContainerLow)
                .padding(.top, 16)
                .onChange(of: text) { value in
                    let digits = String(value.filter(\.isNumber).prefix(2))
                    if digits != value { text = digits }
                }
            if !text.isEmpty, !valid {
                Text("hops_custom_invalid").skyText(.bodySmall).foregroundStyle(Color.skyError).padding(.top, 6)
            }
            SheetButtons(confirm: L10n.key("save"), enabled: valid) {
                if let value = Int(text), value != current { save(value) }
            }
            .padding(.top, 20)
        }
        .padding(.horizontal, 24)
        .padding(.bottom, 32)
        .onAppear { text = current.map(String.init) ?? "" }
    }
}

/// The exits from service discovery, the VPN favourites and the visor's public address.
@MainActor
final class VpnModel: ObservableObject {
    @Published private(set) var servers: [ServiceEntry]
    @Published private(set) var favorites: [SavedServer]
    @Published private(set) var loading = false
    @Published private(set) var error: String?
    @Published private(set) var overview: Overview?
    @Published var query = ""

    static let serviceType = "vpn"
    private let store = ServerStore(type: VpnModel.serviceType)

    init() {
        servers = store.cachedList ?? []
        favorites = store.favorites
    }

    var filtered: [ServiceEntry] { servers.filter { $0.matches(query) } }
    var favoriteRows: [FavoriteRow] { FavoriteRow.rows(favorites, servers, query: query) }

    func isFavorite(_ pk: String) -> Bool {
        favorites.contains { $0.pk == pk }
    }

    func toggleFavorite(_ server: SavedServer) {
        store.toggleFavorite(server)
        favorites = store.favorites
    }

    func run(_ app: AppModel) async {
        guard app.connected else {
            overview = nil
            return
        }
        async let list: Void = loadServers(app, attempts: SocksModel.initialLoadAttempts)
        async let address: Void = pollAddress(app)
        _ = await (list, address)
    }

    /// A few tries with a growing pause: the first ones after the core comes up find no dmsg session yet.
    func loadServers(_ app: AppModel, attempts: Int = 1) async {
        loading = true
        error = nil
        defer { loading = false }
        var pause: Duration = .seconds(2)
        for attempt in 1...max(1, attempts) {
            do {
                let fetched = try await app.client.services(type: Self.serviceType)
                servers = fetched
                store.cachedList = fetched
                return
            } catch is CancellationError {
                return
            } catch {
                if app.handle(error) { return }
                if attempt == attempts {
                    self.error = error.localizedDescription
                    return
                }
                do { try await Task.sleep(for: pause) } catch { return }
                pause = min(pause * 2, .seconds(30))
            }
        }
    }

    private func pollAddress(_ app: AppModel) async {
        while !Task.isCancelled {
            if let summary = try? await app.client.summary() { overview = summary.overview }
            do { try await Task.sleep(for: .seconds(10)) } catch { return }
        }
    }
}
