import CoreClient
import SwiftUI
import UIKit

/// SkySOCKS (Android: SocksScreen): pick a public proxy, point
/// skysocks-client at it, and watch what it does. The pattern every app
/// screen repeats: a list from service discovery, configure, start, observe.
struct SocksView: View {
    @EnvironmentObject private var app: AppModel
    @ObservedObject private var settings: AppSettings
    @StateObject private var model = SocksModel()
    @StateObject private var settingsModel = SettingsModel()
    @State private var editingPort = false
    @State private var portText = ""
    @State private var copied = false

    init(settings: AppSettings) {
        self.settings = settings
    }

    var body: some View {
        List {
            statusSection
            proxySection
            Section {
                NavigationLink {
                    TransportChoiceView(model: settingsModel)
                } label: {
                    HStack {
                        Text("transport_sheet_title")
                        Spacer()
                        Text(TransportChoiceView.name(settings.transportPrimary)).foregroundStyle(.secondary)
                    }
                }
                .disabled(model.busy)
            } footer: {
                Text("transport_hint")
            }
            if app.connected {
                serverSections
            }
        }
        .navigationTitle(Text("app_skysocks"))
        .searchable(text: $model.query, prompt: Text("socks_search_hint"))
        .refreshable { await model.loadServers(app) }
        .task(id: app.connected) { await model.run(app) }
        .onChange(of: settings.transportPrimary) { _ in model.transportChanged(app) }
        .overlay(alignment: .top) {
            if copied {
                Text("copied_to_clipboard")
                    .font(.footnote.weight(.semibold))
                    .padding(.horizontal, 12).padding(.vertical, 6)
                    .background(Capsule().fill(.thinMaterial))
                    .task {
                        try? await Task.sleep(for: .seconds(1.5))
                        copied = false
                    }
            }
        }
        .alert(Text("socks_port_title"), isPresented: $editingPort) {
            TextField(L10n.text("socks_port_label"), text: $portText)
                .keyboardType(.numberPad)
            Button("cancel", role: .cancel) {}
            Button("save") {
                if let port = Int(portText), SocksProfile.ports.contains(port) {
                    model.setListenPort(port, app)
                } else {
                    model.error = L10n.text("socks_port_invalid")
                }
            }
        } message: {
            Text("socks_port_hint")
        }
    }

    // MARK: Status

    private var statusSection: some View {
        Section {
            HStack(spacing: 8) {
                let (label, color) = statusLabel
                StatusDot(color: color)
                Text(label).font(.headline)
                    .accessibilityIdentifier("socks-state")
                if model.busy {
                    ProgressView().controlSize(.small)
                }
            }
            // The visor's own words for what the app is doing, whenever they
            // say more than the label above.
            if let detail = model.app?.detailedStatus, !detail.isEmpty, detail.lowercased() != AppDetail.running {
                Text(Format.appStatus(detail))
                    .font(.footnote)
                    .foregroundStyle(model.errored ? Color.red : Color.secondary)
            }
            if let pk = model.selectedPK {
                HStack {
                    Button {
                        UIPasteboard.general.string = pk
                        copied = true
                    } label: {
                        InfoRow(
                            label: Text("socks_server"),
                            value: [model.selectedCountry.flatMap(Format.flag), Format.shortPK(pk)].compactMap { $0 }.joined(separator: " "),
                            monospaced: true
                        )
                    }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("socks-server")
                    // The star here as well as on the list: the moment a
                    // proxy turns out to be fast is while connected to it.
                    FavoriteStar(favorite: model.isFavorite(pk)) {
                        if let server = model.selectedServer { model.toggleFavorite(server) }
                    }
                }
            }
            if let connection = model.connection {
                // skysocks-client never measures round trips, so its latency
                // is a constant zero; "0 ms" would just be wrong.
                if connection.latencyMS > 0 {
                    InfoRow(label: Text("socks_latency"), value: "\(connection.latencyMS) ms")
                }
                InfoRow(
                    label: Text("socks_transferred"),
                    value: "↑ \(Format.bytes(connection.bandwidthSent))  ↓ \(Format.bytes(connection.bandwidthReceived))"
                )
                if !connection.error.isEmpty {
                    Text(connection.error).font(.footnote).foregroundStyle(.red)
                }
            }
            if let error = model.error {
                Text(error).font(.footnote).foregroundStyle(.red)
            }
            connectControl
        }
    }

    private var statusLabel: (String, Color) {
        if !app.connected { return (L10n.text("state_disconnected"), .secondary) }
        if model.running { return (L10n.text("state_connected"), .success) }
        if model.starting { return (L10n.text("socks_state_connecting"), .warning) }
        if model.errored { return (L10n.text("socks_state_error"), .red) }
        return (L10n.text("state_disconnected"), .secondary)
    }

    @ViewBuilder
    private var connectControl: some View {
        if !app.connected {
            Text(app.coreState == .stopped ? L10n.key("socks_core_offline") : L10n.key("socks_core_starting"))
                .font(.subheadline)
                .foregroundStyle(.secondary)
        } else {
            let active = model.running || model.starting
            Button {
                active ? model.disconnect(app) : model.reconnect(app)
            } label: {
                Text(active ? L10n.key("disconnect") : (model.selectedPK == nil ? L10n.key("connect") : L10n.key("socks_reconnect")))
                    .frame(maxWidth: .infinity)
            }
            .buttonStyle(.borderedProminent)
            .tint(active ? Color.secondary : Color.skywire)
            .disabled(model.busy || (!active && model.selectedPK == nil))
            .accessibilityIdentifier("socks-connect")
            if !active, model.selectedPK == nil {
                Text("socks_pick_server").font(.footnote).foregroundStyle(.secondary)
            }
        }
    }

    // MARK: The address other apps use

    private var proxySection: some View {
        Section {
            Button {
                UIPasteboard.general.string = model.listenAddress
                copied = true
            } label: {
                InfoRow(label: Text(verbatim: "SOCKS5"), value: model.listenAddress, monospaced: true)
            }
            .buttonStyle(.plain)
            .accessibilityIdentifier("socks-address")
            Button {
                portText = String(model.listenPort)
                editingPort = true
            } label: {
                Text("socks_change_port")
            }
            .disabled(model.busy || !app.connected)
            .accessibilityIdentifier("socks-change-port")
        } header: {
            Text("socks_proxy_title")
        } footer: {
            Text("socks_proxy_hint")
        }
    }

    // MARK: Servers

    @ViewBuilder
    private var serverSections: some View {
        let favorites = model.favoriteRows
        if !favorites.isEmpty {
            Section {
                ForEach(favorites, id: \.entry.address) { row in
                    serverRow(row.entry, unlisted: !row.listed)
                }
            } header: {
                Text(L10n.format("servers_favorites", favorites.count))
            }
        }
        Section {
            ForEach(model.filteredServers, id: \.address) { entry in
                serverRow(entry, unlisted: false)
            }
            if model.serversLoading {
                HStack {
                    Spacer()
                    ProgressView()
                    Spacer()
                }
            } else if let error = model.serversError {
                VStack(alignment: .leading, spacing: 8) {
                    Text(error).font(.footnote).foregroundStyle(.red)
                    Button("socks_retry") { Task { await model.loadServers(app) } }
                }
            } else if model.filteredServers.isEmpty {
                Text(model.servers.isEmpty ? L10n.key("socks_servers_none") : L10n.key("socks_servers_empty"))
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
        } header: {
            HStack {
                Text(L10n.format("socks_servers", model.servers.count))
                Spacer()
                Button {
                    Task { await model.loadServers(app) }
                } label: {
                    Image(systemName: "arrow.clockwise")
                        .accessibilityLabel(Text("socks_refresh"))
                }
                .disabled(model.serversLoading)
            }
        }
    }

    private func serverRow(_ entry: ServiceEntry, unlisted: Bool) -> some View {
        HStack(spacing: 12) {
            Button {
                model.connect(SavedServer(entry), app)
            } label: {
                ServerRowLabel(entry: entry, selected: entry.pk == model.selectedPK, unlisted: unlisted)
            }
            .buttonStyle(.plain)
            .disabled(model.busy)
            FavoriteStar(favorite: model.isFavorite(entry.pk)) {
                model.toggleFavorite(SavedServer(entry))
            }
        }
        .accessibilityIdentifier("socks-server-row")
    }
}

/// One server: where it is, its key and version, a check when it is the one
/// in use (Android: ServerRow).
struct ServerRowLabel: View {
    let entry: ServiceEntry
    let selected: Bool
    let unlisted: Bool

    var body: some View {
        HStack(spacing: 10) {
            Text(entry.geo.flatMap { Format.flag($0.country) } ?? "🌐").font(.title3)
            VStack(alignment: .leading, spacing: 2) {
                Text(location).font(.body)
                Text(Format.shortPK(entry.pk)).font(.caption.monospaced()).foregroundStyle(.secondary)
                if unlisted {
                    Text("favorite_unlisted").font(.caption2).foregroundStyle(.secondary)
                } else if !entry.version.isEmpty {
                    Text(entry.version).font(.caption2).foregroundStyle(.secondary)
                }
            }
            Spacer(minLength: 8)
            if selected {
                Image(systemName: "checkmark").foregroundStyle(Color.skywire)
            }
        }
        .contentShape(Rectangle())
    }

    private var location: String {
        let country = entry.geo?.country ?? ""
        let name = country.isEmpty ? nil : (Locale.current.localizedString(forRegionCode: country) ?? country)
        let region = entry.geo?.region ?? ""
        switch (name, region.isEmpty) {
        case let (name?, false): return "\(name) · \(region)"
        case let (name?, true): return name
        default: return L10n.text("server_location_unknown")
        }
    }
}

/// The star that keeps a server at the top of its list.
struct FavoriteStar: View {
    let favorite: Bool
    let toggle: () -> Void

    var body: some View {
        Button {
            toggle()
        } label: {
            Image(systemName: favorite ? "star.fill" : "star")
                .foregroundStyle(favorite ? Color.yellow : Color.secondary)
                .accessibilityLabel(Text(favorite ? L10n.key("favorite_remove") : L10n.key("favorite_add")))
        }
        .buttonStyle(.borderless)
    }
}
