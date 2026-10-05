import CoreClient
import SwiftUI
import UIKit

/// SkySOCKS (Android: SocksScreen): pick a public proxy, point skysocks-client at it,
/// and watch what it does.
struct SocksView: View {
    @EnvironmentObject private var app: AppModel
    @EnvironmentObject private var navigator: Navigator
    @EnvironmentObject private var dialogs: SkyDialogs
    @ObservedObject private var settings: AppSettings
    @StateObject private var model = SocksModel()
    @StateObject private var settingsModel = SettingsModel()

    init(settings: AppSettings) {
        self.settings = settings
    }

    var body: some View {
        VStack(spacing: 0) {
            SkyTopBar(title: Text("app_skysocks"), onBack: { navigator.back() }, help: .socks)
            ScrollView {
                LazyVStack(spacing: 16) {
                    statusCard
                    proxyCard
                    TransportPreferenceCard(current: settings.transportPrimary, enabled: !model.busy) {
                        dialogs.presentSheet {
                            TransportPreferenceSheet(current: settings.transportPrimary) { settingsModel.setTransportPrimary($0, app) }
                        }
                    }
                    if app.connected {
                        servers
                    }
                }
                .padding(.horizontal, 20)
                .padding(.vertical, 16)
            }
        }
        .background(Color.skyBackground)
        .task(id: app.connected) { await model.run(app) }
        .onChange(of: settings.transportPrimary) { _ in model.transportChanged(app) }
    }

    // MARK: Status

    private var statusCard: some View {
        SectionCard {
            HStack(spacing: 0) {
                let (label, color) = statusLabel
                StatusDot(color: color)
                Text(verbatim: label).skyText(.titleMedium).padding(.leading, 8)
                    .accessibilityIdentifier("socks-state")
                if model.busy {
                    MaterialSpinner(size: 16, stroke: 2).padding(.leading, 10)
                }
            }
            // The visor's own words for what the app is doing, when they say more than the label.
            if let detail = model.app?.detailedStatus, !detail.isEmpty, detail.lowercased() != AppDetail.running {
                Text(verbatim: Format.appStatus(detail)).skyText(.bodySmall)
                    .foregroundStyle(model.errored ? Color.skyError : Color.skyOnSurfaceVariant)
                    .padding(.top, 4)
            }
            if let pk = model.selectedPK {
                HStack(spacing: 0) {
                    Button {
                        UIPasteboard.general.string = pk
                        dialogs.toast(Text("copied_to_clipboard"))
                    } label: {
                        SkyInfoRow(label: Text("socks_server"),
                                   value: [model.selectedCountry.flatMap(Format.flag), Format.shortPK(pk)].compactMap { $0 }.joined(separator: " "),
                                   mono: true)
                            .contentShape(Rectangle())
                    }
                    .buttonStyle(PressStyle(layer: .skyOnSurface))
                    .accessibilityIdentifier("socks-server")
                    // The star here too: the moment a proxy turns out fast is while connected to it.
                    FavoriteStar(favorite: model.isFavorite(pk)) {
                        if let server = model.selectedServer { model.toggleFavorite(server) }
                    }
                }
                .padding(.top, 12)
            }
            if let connection = model.connection {
                // skysocks-client never measures round trips: its latency is a constant zero.
                if connection.latencyMS > 0 {
                    SkyInfoRow(label: Text("socks_latency"), value: "\(connection.latencyMS) ms")
                }
                SkyInfoRow(label: Text("socks_transferred"),
                           value: "↑ \(Format.bytes(connection.bandwidthSent))  ↓ \(Format.bytes(connection.bandwidthReceived))")
                if !connection.error.isEmpty {
                    Text(verbatim: connection.error).skyText(.bodySmall).foregroundStyle(Color.skyError)
                }
            }
            if let error = model.error {
                Text(verbatim: error).skyText(.bodySmall).foregroundStyle(Color.skyError).padding(.top, 8)
            }
            connectControl.padding(.top, 16)
        }
    }

    private var statusLabel: (String, Color) {
        if !app.connected { return (L10n.text("state_disconnected"), .skyOnSurfaceVariant) }
        if model.running { return (L10n.text("state_connected"), .skySuccess) }
        if model.starting { return (L10n.text("socks_state_connecting"), .skyWarning) }
        if model.errored { return (L10n.text("socks_state_error"), .skyError) }
        return (L10n.text("state_disconnected"), .skyOnSurfaceVariant)
    }

    @ViewBuilder
    private var connectControl: some View {
        if !app.connected {
            Text(app.coreState == .stopped ? L10n.key("socks_core_offline") : L10n.key("socks_core_starting"))
                .skyText(.bodyMedium)
                .foregroundStyle(Color.skyOnSurfaceVariant)
        } else {
            let active = model.running || model.starting
            VStack(alignment: .leading, spacing: 0) {
                Button {
                    active ? model.disconnect(app) : model.reconnect(app)
                } label: {
                    Text(active ? L10n.key("disconnect") : (model.selectedPK == nil ? L10n.key("connect") : L10n.key("socks_reconnect")))
                        .frame(maxWidth: .infinity)
                }
                .buttonStyle(ConnectButtonStyle(active: active))
                .disabled(model.busy || (!active && model.selectedPK == nil))
                .accessibilityIdentifier("socks-connect")
                if !active, model.selectedPK == nil {
                    Text("socks_pick_server").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 8)
                }
            }
        }
    }

    // MARK: The address other apps use

    private var proxyCard: some View {
        SectionCard {
            Text("socks_proxy_title").skyText(.labelMedium).foregroundStyle(Color.skyOnSurfaceVariant)
            HStack {
                Button {
                    UIPasteboard.general.string = model.listenAddress
                    dialogs.toast(Text("copied_to_clipboard"))
                } label: {
                    Text(verbatim: model.listenAddress).skyText(.titleMedium, mono: true)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .contentShape(Rectangle())
                }
                .buttonStyle(PressStyle())
                .accessibilityLabel(Text(verbatim: model.listenAddress))
                .accessibilityIdentifier("socks-address")
                Button {
                    dialogs.presentSheet { PortSheet(port: model.listenPort) { model.setListenPort($0, app) } }
                } label: {
                    Text("socks_change_port")
                }
                .buttonStyle(.tonal)
                .disabled(model.busy || !app.connected)
                .accessibilityIdentifier("socks-change-port")
            }
            .padding(.top, 6)
            Text("socks_proxy_hint").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 4)
        }
    }

    // MARK: Servers

    @ViewBuilder
    private var servers: some View {
        let favorites = model.favoriteRows
        if !favorites.isEmpty {
            FavoritesHeader(count: favorites.count)
            ForEach(favorites, id: \.entry.address) { row in
                serverRow(row.entry, unlisted: !row.listed)
            }
        }
        VStack(spacing: 0) {
            HStack {
                Text(verbatim: L10n.format("socks_servers", model.servers.count)).skyText(.titleMedium)
                Spacer()
                if model.serversLoading {
                    MaterialSpinner(size: 20, stroke: 2).frame(width: 48, height: 48)
                } else {
                    Button { Task { await model.loadServers(app) } } label: {
                        MaterialIcon(MI.filledRefresh).foregroundStyle(Color.skyOnBackground).frame(width: 48, height: 48)
                    }
                    .buttonStyle(PressStyle())
                    .accessibilityLabel(Text("socks_refresh"))
                }
            }
            SkyOutlinedTextField(placeholder: L10n.key("socks_search_hint"), text: $model.query)
        }
        ForEach(model.filteredServers, id: \.address) { entry in
            serverRow(entry, unlisted: false)
        }
        if let error = model.serversError {
            VStack(alignment: .leading, spacing: 0) {
                Text(verbatim: error).skyText(.bodySmall).foregroundStyle(Color.skyError)
                Button { Task { await model.loadServers(app) } } label: { Text("socks_retry") }.buttonStyle(.tonal)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        } else if !model.serversLoading, model.filteredServers.isEmpty {
            Text(model.servers.isEmpty ? L10n.key("socks_servers_none") : L10n.key("socks_servers_empty"))
                .skyText(.bodyMedium)
                .foregroundStyle(Color.skyOnSurfaceVariant)
                .multilineTextAlignment(.center)
                .frame(maxWidth: .infinity)
        }
    }

    private func serverRow(_ entry: ServiceEntry, unlisted: Bool) -> some View {
        ServerRow(entry: entry, selected: entry.pk == model.selectedPK, unlisted: unlisted,
                  favorite: model.isFavorite(entry.pk),
                  onTap: { if !model.busy { model.connect(SavedServer(entry), app) } },
                  onStar: { model.toggleFavorite(SavedServer(entry)) })
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("socks-server-row")
    }
}

/// The full-width Connect: primary, or tonal while the app is active (Android's ConnectControl).
struct ConnectButtonStyle: ButtonStyle {
    let active: Bool

    @ViewBuilder
    func makeBody(configuration: Configuration) -> some View {
        if active {
            TonalButtonStyle().makeBody(configuration: configuration)
        } else {
            FilledButtonStyle().makeBody(configuration: configuration)
        }
    }
}

/// PortSheet: the listener stays on 127.0.0.1; a port from 1024 to 65535.
private struct PortSheet: View {
    let port: Int
    let save: (Int) -> Void
    @State private var text = ""

    private var valid: Bool {
        guard let value = Int(text) else { return false }
        return SocksProfile.ports.contains(value)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("socks_port_title").skyText(.titleMedium)
            Text("socks_port_hint").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 8)
            SkyOutlinedTextField(label: L10n.key("socks_port_label"), text: $text,
                                 isError: !text.isEmpty && !valid, keyboard: .numberPad, notch: .skyContainerLow)
                .padding(.top, 16)
                .onChange(of: text) { value in
                    let digits = String(value.filter(\.isNumber).prefix(5))
                    if digits != value { text = digits }
                }
            if !text.isEmpty, !valid {
                Text("socks_port_invalid").skyText(.bodySmall).foregroundStyle(Color.skyError).padding(.top, 6)
            }
            SheetButtons(confirm: L10n.key("save"), enabled: valid) {
                if let value = Int(text) { save(value) }
            }
            .padding(.top, 20)
        }
        .padding(.horizontal, 24)
        .padding(.bottom, 32)
        .onAppear { text = String(port) }
    }
}
