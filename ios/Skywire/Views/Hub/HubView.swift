import CoreClient
import SwiftUI
import WalletCore

/// The apps hub behind the bar's cloud (Android: HubScreen): chips, the SkyVPN hero, the tiles.
struct HubView: View {
    @EnvironmentObject private var app: AppModel
    @EnvironmentObject private var navigator: Navigator
    @EnvironmentObject private var notifications: NotificationBridge
    @StateObject private var model = HubModel()
    @State private var filter = HubFilter.all

    var body: some View {
        VStack(spacing: 0) {
            SkyTopBar(
                title: Text("tab_hub_description"),
                subtitle: Text(verbatim: L10n.format("hub_subtitle", HubModel.installed, model.running)),
                onBack: { navigator.back() },
                help: .hub
            )
            ScrollView {
                VStack(alignment: .leading, spacing: 11) {
                    chips
                    if filter == .all || filter == .network {
                        VpnHeroCard(minHops: model.minHops) { navigator.push(.vpn) }
                        SkyDnsCard()
                    }
                    Text("hub_section_apps").skyText(.titleSmall).foregroundStyle(Color.skyOnBackground)
                        .padding(.leading, 2).padding(.top, 8)
                    ForEach(Array(rows.enumerated()), id: \.offset) { _, row in
                        row
                    }
                }
                .padding(.horizontal, 16)
                .padding(.bottom, 24)
            }
        }
        .background(Color.skyBackground)
        .task(id: app.connected) { await model.poll(app) }
    }

    private var chips: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 8) {
                ForEach(HubFilter.allCases, id: \.self) { item in
                    CategoryChip(label: item.label, selected: filter == item) { filter = item }
                }
            }
            .padding(.vertical, 2)
        }
    }

    /// The tiles of the filter, two to a row; a wide tile takes a row of its own.
    private var rows: [AnyView] {
        var rows: [AnyView] = []
        var pending: AnyView?
        for tile in HubTile.all where filter == .all || tile.category == filter {
            let view = tileView(tile)
            if tile.wide {
                if let left = pending { rows.append(pair(left, nil)); pending = nil }
                rows.append(view)
            } else if let left = pending {
                rows.append(pair(left, view))
                pending = nil
            } else {
                pending = view
            }
        }
        if let left = pending { rows.append(pair(left, nil)) }
        return rows
    }

    private func pair(_ left: AnyView, _ right: AnyView?) -> AnyView {
        AnyView(HStack(alignment: .top, spacing: 11) {
            left.frame(maxWidth: .infinity)
            if let right { right.frame(maxWidth: .infinity) } else { Color.clear.frame(maxWidth: .infinity, maxHeight: 0) }
        })
    }

    private func tileView(_ tile: HubTile) -> AnyView {
        switch tile {
        case .socks:
            AnyView(AppCard(icon: MI.roundedAltRoute, name: L10n.key("app_skysocks"), subtitle: Text("hub_socks_sub"),
                            status: model.dot(SocksProfile.app)) { navigator.push(.socks) }
                .accessibilityIdentifier("hub-socks"))
        case .dex:
            AnyView(AppCard(icon: MI.roundedCandlestickChart, name: L10n.key("app_skydex"), subtitle: Text("hub_dex_sub"),
                            status: model.dot(SkydexProfile.app)) { navigator.push(.dex) }
                .accessibilityIdentifier("hub-dex"))
        case .chat:
            AnyView(AppCard(icon: MI.roundedForum, name: L10n.key("app_skychat"), subtitle: Text("hub_chat_sub"),
                            status: model.dot(SkychatProfile.app), badge: notifications.unread ?? 0) { navigator.openFromHub(.chat) }
                .accessibilityIdentifier("hub-chat"))
        case .wallet:
            AnyView(AppCard(icon: MI.roundedAccountBalanceWallet, name: L10n.key("app_wallet"),
                            subtitle: model.skyBalance.map { Text(verbatim: L10n.format("hub_wallet_balance", $0)) } ?? Text("hub_wallet_sub"),
                            status: nil) { navigator.openFromHub(.wallet) }
                .accessibilityIdentifier("hub-wallet"))
        case .fleet:
            AnyView(WideAppCard(icon: MI.roundedHub, name: L10n.key("app_fleet"),
                                subtitle: model.fleetOnline.map { Text(verbatim: L10n.format("hub_fleet_connected", $0)) } ?? Text("hub_fleet_sub")) {
                navigator.push(.fleet)
            }
            .accessibilityIdentifier("hub-fleet"))
        case .meet:
            AnyView(ComingSoonCard())
        }
    }
}

enum HubFilter: CaseIterable {
    case all, network, finance, social

    var label: LocalizedStringKey {
        switch self {
        case .all: L10n.key("hub_filter_all")
        case .network: L10n.key("hub_filter_network")
        case .finance: L10n.key("hub_filter_finance")
        case .social: L10n.key("hub_filter_social")
        }
    }
}

private enum HubTile: CaseIterable {
    case socks, dex, chat, wallet, fleet, meet

    static let all = HubTile.allCases

    var category: HubFilter {
        switch self {
        case .socks, .fleet: .network
        case .dex, .wallet: .finance
        case .chat, .meet: .social
        }
    }

    var wide: Bool { self == .fleet || self == .meet }
}

/// The hub's chip: primary when selected, a hairline pill otherwise.
private struct CategoryChip: View {
    let label: LocalizedStringKey
    let selected: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Text(label)
                .skyText(.labelLarge)
                .foregroundStyle(selected ? Color.skyOnPrimary : Color.skyOnSurfaceVariant)
                .padding(.horizontal, 15)
                .padding(.vertical, 8)
                .background(selected ? Color.skyPrimary : Color.skyContainerLowest, in: .sky(SkyRadius.small))
                .overlay(RoundedRectangle(cornerRadius: SkyRadius.small).strokeBorder(selected ? .clear : Color.skyOutlineVariant, lineWidth: 1))
                .skyElevation(selected ? 4 : 0)
                .padding(.vertical, 6)
        }
        .buttonStyle(PressStyle())
        .accessibilityAddTraits(selected ? .isSelected : [])
    }
}

/// The 44 pt icon plate.
private struct IconTile: View {
    let icon: String
    var fill: Color = .skyPrimaryContainer
    var tint: Color = .skyPrimary

    var body: some View {
        MaterialIcon(icon, size: 25)
            .foregroundStyle(tint)
            .frame(width: 44, height: 44)
            .background(fill, in: .sky(SkyRadius.medium))
    }
}

/// A 16 pt halo at 18 % around an 8 pt dot.
private struct HaloDot: View {
    let color: Color

    var body: some View {
        ZStack {
            Circle().fill(color.opacity(0.18)).frame(width: 16, height: 16)
            Circle().fill(color).frame(width: 8, height: 8)
        }
    }
}

private struct CountBadge: View {
    let count: Int

    var body: some View {
        Text(verbatim: count > 99 ? "99+" : "\(count)")
            .skyText(.labelSmall)
            .foregroundStyle(Color.skyOnPrimary)
            .padding(.horizontal, 7)
            .padding(.vertical, 2)
            .background(Color.skyPrimary, in: Capsule())
    }
}

private struct AppCard: View {
    let icon: String
    let name: LocalizedStringKey
    let subtitle: Text
    let status: Color?
    var badge = 0
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(alignment: .leading, spacing: 0) {
                HStack(alignment: .top) {
                    IconTile(icon: icon)
                    Spacer()
                    HStack(spacing: 6) {
                        if badge > 0 { CountBadge(count: badge) }
                        if let status { HaloDot(color: status) }
                    }
                }
                Text(name).skyText(.titleMedium).foregroundStyle(Color.skyOnSurface).lineLimit(1).padding(.top, 9)
                subtitle.skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).lineLimit(1).padding(.top, 2)
            }
            .padding(14)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Color.skySurfaceVariant, in: .sky(SkyRadius.large))
            .overlay(RoundedRectangle(cornerRadius: SkyRadius.large).strokeBorder(Color.skyOutlineVariant, lineWidth: 1))
            .contentShape(RoundedRectangle(cornerRadius: SkyRadius.large))
        }
        .buttonStyle(PressStyle(layer: .skyOnSurface, shape: AnyShape(RoundedRectangle(cornerRadius: SkyRadius.large))))
    }
}

private struct WideAppCard: View {
    let icon: String
    let name: LocalizedStringKey
    let subtitle: Text
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 13) {
                IconTile(icon: icon)
                VStack(alignment: .leading, spacing: 0) {
                    Text(name).skyText(.titleMedium).foregroundStyle(Color.skyOnSurface).lineLimit(1)
                    subtitle.skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).lineLimit(1).padding(.top, 2)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                MaterialIcon(MI.roundedChevronRight, size: 20).foregroundStyle(Color.skyOutline)
            }
            .padding(14)
            .background(Color.skySurfaceVariant, in: .sky(SkyRadius.large))
            .overlay(RoundedRectangle(cornerRadius: SkyRadius.large).strokeBorder(Color.skyOutlineVariant, lineWidth: 1))
            .contentShape(RoundedRectangle(cornerRadius: SkyRadius.large))
        }
        .buttonStyle(PressStyle(layer: .skyOnSurface, shape: AnyShape(RoundedRectangle(cornerRadius: SkyRadius.large))))
    }
}

/// SkyDNS on its own (Android: SkyDnsCard). It needs a tunnel of its own, which comes with the
/// packet-tunnel extension (Lane D): until then the switch rests off and a tap says why.
private struct SkyDnsCard: View {
    @EnvironmentObject private var dialogs: SkyDialogs

    var body: some View {
        // Android's Row spaces the icon from the text only: the switch sits right after it.
        HStack(spacing: 0) {
            IconTile(icon: MI.roundedDns)
            VStack(alignment: .leading, spacing: 0) {
                Text("app_skydns").skyText(.titleMedium).foregroundStyle(Color.skyOnSurface).lineLimit(1)
                Text("hub_skydns_off").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).lineLimit(1).padding(.top, 2)
            }
            .padding(.leading, 13)
            .frame(maxWidth: .infinity, alignment: .leading)
            Toggle(isOn: Binding(get: { false }, set: { _ in dialogs.snackbar(Text("skydns_ios_pending")) })) {
                Text("hub_skydns_toggle")
            }
            .toggleStyle(.skySwitch)
            .labelsHidden()
            .accessibilityIdentifier("hub-skydns")
        }
        .padding(14)
        .background(Color.skySurfaceVariant, in: .sky(SkyRadius.large))
        .overlay(RoundedRectangle(cornerRadius: SkyRadius.large).strokeBorder(Color.skyOutlineVariant, lineWidth: 1))
    }
}

/// SkyMeet: dashed, not clickable, a COMING SOON badge.
private struct ComingSoonCard: View {
    var body: some View {
        HStack(spacing: 13) {
            IconTile(icon: MI.roundedVideocam, fill: .skyContainerHigh, tint: .skyOutline)
            VStack(alignment: .leading, spacing: 0) {
                Text("app_skymeet").skyText(.titleMedium).foregroundStyle(Color.skyOnSurfaceVariant).lineLimit(1)
                Text("hub_meet_sub").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).lineLimit(1).padding(.top, 2)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            Text(L10n.text("coming_soon").uppercased())
                .skyText(.labelSmall, tracking: 0.5)
                .foregroundStyle(Color.skyOnSurfaceVariant)
                .padding(.horizontal, 10)
                .padding(.vertical, 6)
                .background(Color.skyContainerHigh, in: .sky(SkyRadius.extraSmall))
        }
        .padding(14)
        .background(Color.skyContainerLow, in: .sky(SkyRadius.large))
        .overlay(
            // Android strokes 1.5 dp centred on the edge, clipped: about 0.75 pt shows, dashes 4/3.
            RoundedRectangle(cornerRadius: SkyRadius.large)
                .strokeBorder(Color.skyOutlineVariant, style: StrokeStyle(lineWidth: 0.75, dash: [4, 3]))
        )
    }
}

/// The SkyVPN hero, always full size. Off on iOS until the tunnel exists (M7, Lane D).
private struct VpnHeroCard: View {
    let minHops: Int?
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(alignment: .leading, spacing: 0) {
                HStack(spacing: 0) {
                    MaterialIcon(MI.roundedVpnLock, size: 26)
                        .foregroundStyle(.white)
                        .frame(width: 48, height: 48)
                        .background(Color.white.opacity(0.18), in: .sky(SkyRadius.medium))
                    VStack(alignment: .leading, spacing: 0) {
                        HStack(spacing: 7) {
                            Circle().fill(Color.white.opacity(0.45)).frame(width: 7, height: 7)
                            Text(L10n.text("state_disconnected").uppercased())
                                .skyText(.labelSmall, tracking: 1.2)
                                .foregroundStyle(Color.white.opacity(0.75))
                                .lineLimit(1)
                        }
                        Text("app_skyvpn").skyText(.titleMedium).foregroundStyle(.white)
                        Text("hub_hero_no_exit").skyText(.bodySmall).foregroundStyle(Color.white.opacity(0.8)).lineLimit(1)
                    }
                    .padding(.leading, 13)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    Toggle(isOn: .constant(false)) { EmptyView() }
                        .toggleStyle(SkySwitchStyle(uncheckedTrack: .white.opacity(0.28), uncheckedThumb: .white, uncheckedBorder: .clear))
                        .labelsHidden()
                        // Android's resting look, but it cannot start a tunnel iOS does not have yet.
                        .allowsHitTesting(false)
                        .padding(.leading, 8)
                        .accessibilityLabel(Text("hub_vpn_toggle"))
                }
                HStack(spacing: 8) {
                    HeroStat(label: L10n.text("hub_stat_down"))
                    HeroStat(label: L10n.text("hub_stat_up"))
                    HeroStat(label: L10n.text("hub_stat_data"))
                }
                .padding(.top, 15)
                HStack(spacing: 8) {
                    HStack(spacing: 6) {
                        MaterialIcon(MI.roundedRoute, size: 15)
                        Text(verbatim: hops).skyText(.labelMedium)
                    }
                    .foregroundStyle(Color.white.opacity(0.95))
                    .heroChip()
                    Text("hub_killswitch_off").skyText(.labelMedium).foregroundStyle(Color.skyDangerBright).heroChip()
                }
                .padding(.top, 10)
            }
            .padding(18)
            .background {
                GeometryReader { geometry in
                    ZStack {
                        SkyGradient.hero
                        Circle().fill(Color.white.opacity(0.10)).frame(width: 170, height: 170)
                            .position(x: geometry.size.width + 15, y: -15)
                        Circle().fill(Color.white.opacity(0.07)).frame(width: 120, height: 120)
                            .position(x: geometry.size.width - 44, y: geometry.size.height + 30)
                    }
                }
            }
            .clipShape(.sky(SkyRadius.large))
            .contentShape(RoundedRectangle(cornerRadius: SkyRadius.large))
        }
        .buttonStyle(PressStyle(layer: .white, shape: AnyShape(RoundedRectangle(cornerRadius: SkyRadius.large))))
        .accessibilityIdentifier("hub-vpn")
    }

    private var hops: String {
        guard let minHops, minHops > 0 else { return L10n.text("hub_hops_unknown") }
        return L10n.format("hub_hops", minHops)
    }
}

private struct HeroStat: View {
    let label: String

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text(verbatim: label.uppercased()).skyText(.labelSmall, tracking: 0.8).foregroundStyle(Color.white.opacity(0.72))
            Text(verbatim: "—").skyText(.titleMedium).foregroundStyle(.white).lineLimit(1)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 9)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.white.opacity(0.13), in: .sky(SkyRadius.medium))
    }
}

private extension View {
    func heroChip() -> some View {
        padding(.horizontal, 10)
            .padding(.vertical, 6)
            .background(Color.white.opacity(0.13), in: .sky(SkyRadius.small))
    }
}

/// The hub's live facts, polled every 5 s while it is up (Android: HubViewModel).
@MainActor
final class HubModel: ObservableObject {
    @Published private(set) var states: [String: AppState] = [:]
    @Published private(set) var minHops: Int?
    @Published private(set) var skyBalance: String?
    @Published private(set) var fleetOnline: Int?

    /// Android's "installed" count: the five tiles that run, SkyVPN and SkyDNS.
    static let installed = 7
    static let apps = [SocksProfile.app, SkychatProfile.app, SkydexProfile.app]
    static let pollInterval: Duration = .seconds(5)

    var running: Int { states.values.filter(\.running).count }

    func poll(_ app: AppModel) async {
        readBalance()
        guard app.connected else {
            states = [:]
            return
        }
        minHops = try? await app.client.routerSettings().minHops
        var tick = 0
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
            readBalance()
            if tick % 3 == 0 { await countFleet(app) }
            tick += 1
            do { try await Task.sleep(for: Self.pollInterval) } catch { return }
        }
    }

    /// Running green, starting amber, errored red; the hairline grey otherwise.
    func dot(_ name: String) -> Color {
        switch states[name]?.status {
        case AppState.statusRunning: .skySuccess
        case AppState.statusStarting: .skyWarning
        case AppState.statusErrored: .skyError
        default: .skyOutlineVariant
        }
    }

    /// The active SKY wallet's cached balance: the wallet tab owns talking to the node.
    private func readBalance() {
        let store = WalletStore.app()
        skyBalance = store.activeWalletId(CoinSpec.sky.id)
            .flatMap { store.cachedSnapshot($0) }
            .map { Amounts.format($0.confirmed, exponent: CoinSpec.sky.exponent, minDecimals: 0) }
    }

    private func countFleet(_ app: AppModel) async {
        guard app.settings.fleetEnabled else {
            fleetOnline = nil
            return
        }
        let local = app.publicKey
        if let visors = try? await app.client.visorsSummary() {
            fleetOnline = visors.filter { !$0.isHypervisor && $0.overview.localPK != local && $0.online }.count
        }
    }
}
