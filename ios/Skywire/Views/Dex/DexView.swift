import CoreClient
import SwiftUI
import UIKit

/// SkyDEX (Android: ui/dex/DexScreen.kt): pick a market, dial it over Skywire,
/// and trade on native screens over skydex-client's control API, the calls
/// the web trading page made, laid out for a phone.
struct DexView: View {
    @EnvironmentObject private var app: AppModel
    @EnvironmentObject private var navigator: Navigator
    @EnvironmentObject private var dialogs: SkyDialogs
    @StateObject private var model = DexModel()
    @Environment(\.layerVisible) private var onScreen
    @Environment(\.scenePhase) private var scenePhase

    var body: some View {
        VStack(spacing: 0) {
            // Back walks SkyDEX backwards: a tab back to Market, then the market (leaving it is
            // the disconnect), and only then the hub.
            SkyTopBar(title: Text("app_skydex"), onBack: {
                if model.connected && model.tab != .market {
                    model.selectTab(.market)
                } else if model.connected {
                    model.disconnect(app)
                } else {
                    navigator.back()
                }
            }, help: .dex)
            if model.connected {
                header
                DexTabStrip(model: model)
                DexTabContent(model: model)
            } else {
                ScrollView {
                    VStack(alignment: .leading, spacing: 16) {
                        form
                        recents
                    }
                    .frame(maxWidth: 640)
                    .padding(.horizontal, 20)
                    .padding(.vertical, 16)
                    .frame(maxWidth: .infinity)
                }
            }
        }
        .background(Color.skyBackground)
        .task(id: app.connected) { await model.run(app) }
        // The market is polled only while this screen is in front, as the page's timers were.
        .task(id: TradeKey(url: model.connected ? model.uiURL : nil, market: model.market?.marketPK, visible: onScreen && scenePhase == .active)) {
            guard model.connected, onScreen, scenePhase == .active else { return }
            await model.tradeLoop()
        }
        .onChange(of: model.notice) { notice in
            guard let notice else { return }
            dialogs.snackbar(Text(verbatim: notice))
            model.notice = nil
        }
    }

    private struct TradeKey: Equatable {
        let url: URL?
        let market: String?
        let visible: Bool
    }

    /// The market in use, tap to copy its key, and Disconnect, which also stops skydex-client.
    private var header: some View {
        let pk = model.market?.marketPK ?? ""
        let name = model.market?.marketName ?? ""
        return HStack(spacing: 0) {
            StatusDot(color: .skySuccess)
            Button {
                UIPasteboard.general.string = pk
                dialogs.toast(Text("copied_to_clipboard"))
            } label: {
                VStack(alignment: .leading, spacing: 0) {
                    (name.isEmpty ? Text("dex_market_title") : Text(verbatim: name)).skyText(.titleSmall).lineLimit(1)
                    Text(verbatim: SavedMarket.short(pk)).skyText(.bodySmall, mono: true).foregroundStyle(Color.skyOnSurfaceVariant)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .contentShape(Rectangle())
            }
            .buttonStyle(PressStyle())
            .padding(.leading, 10)
            .accessibilityIdentifier("dex-market")
            Button { model.disconnect(app) } label: { Text("disconnect") }
                .buttonStyle(.tonal)
                .disabled(model.busy)
                .accessibilityIdentifier("dex-disconnect")
        }
        .padding(EdgeInsets(top: 4, leading: 20, bottom: 4, trailing: 8))
    }

    /// The market picker, a card.
    private var form: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("dex_market_title").skyText(.labelMedium).foregroundStyle(Color.skyOnSurfaceVariant)
            SkyOutlinedTextField(label: L10n.key("dex_market_label"), text: Binding(get: { model.entry }, set: { model.setEntry($0) }),
                                 isError: !model.entry.isEmpty && !model.entryValid, mono: true,
                                 notch: .skySurfaceVariant, identifier: "dex-market-field")
                .disabled(model.busy)
                .padding(.top, 10)
            if !model.entry.isEmpty, !model.entryValid {
                Text("dex_market_invalid").skyText(.bodySmall).foregroundStyle(Color.skyError).padding(.top, 6)
            }
            if let error = model.error {
                Text(verbatim: error).skyText(.bodySmall).foregroundStyle(Color.skyError).padding(.top, 8)
                    .accessibilityIdentifier("dex-error")
            }
            VStack(alignment: .leading, spacing: 0) {
                if !app.connected {
                    Text(app.coreState == .stopped ? L10n.key("dex_core_offline") : L10n.key("dex_core_starting"))
                        .skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant)
                } else {
                    Button { model.connect(app) } label: { Text("connect").frame(maxWidth: .infinity) }
                        .buttonStyle(.filled)
                        .disabled(model.busy || !model.entryValid)
                        .accessibilityIdentifier("dex-connect")
                    if model.busy {
                        HStack(spacing: 10) {
                            MaterialSpinner(size: 16, stroke: 2)
                            Text("dex_connecting").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                        }
                        .padding(.top, 8)
                    } else {
                        Text("dex_market_hint").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 8)
                    }
                }
            }
            .padding(.top, 16)
        }
        .padding(20)
        .foregroundStyle(Color.skyOnSurface)
        .background(Color.skySurfaceVariant, in: .sky(SkyRadius.medium))
    }

    /// Markets this phone joined before: one tap rejoins, ✕ forgets (ConnectGate.jsx).
    @ViewBuilder private var recents: some View {
        if !model.recents.isEmpty {
            let canConnect = app.connected && !model.busy
            VStack(alignment: .leading, spacing: 0) {
                HStack {
                    Text("dex_recents").skyText(.titleSmall).foregroundStyle(Color.skyOnSurface)
                    Spacer()
                    Button {
                        dialogs.confirm(L10n.text("dex_clear_recents_confirm"), confirm: L10n.text("dex_clear_recents"), cancel: L10n.text("dex_keep")) { model.clearRecents() }
                    } label: { Text("dex_clear_recents") }
                    .buttonStyle(.skyText)
                    .disabled(model.busy)
                }
                ForEach(Array(model.recents.enumerated()), id: \.element.pk) { index, market in
                    if index > 0 { SkyDivider() }
                    HStack(spacing: 8) {
                        Button { model.connectTo(market, app) } label: {
                            HStack {
                                VStack(alignment: .leading, spacing: 0) {
                                    if !market.name.isEmpty {
                                        Text(verbatim: market.name).skyText(.bodyLarge).foregroundStyle(Color.skyOnSurface).lineLimit(1)
                                    }
                                    Text(verbatim: SavedMarket.short(market.pk)).skyText(.bodySmall, mono: true).foregroundStyle(Color.skyOnSurfaceVariant)
                                }
                                Spacer(minLength: 0)
                                if market.ts > 0 {
                                    Text(verbatim: DexText.ago(market.ts)).skyText(.labelSmall).foregroundStyle(Color.skyOnSurfaceVariant)
                                }
                            }
                            .padding(.vertical, 8)
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(.plain)
                        .disabled(!canConnect)
                        .accessibilityIdentifier("dex-recent-\(market.pk)")
                        Button { model.removeRecent(market) } label: {
                            MaterialIcon(MI.roundedClose, size: 18).foregroundStyle(Color.skyOnSurfaceVariant).frame(width: 40, height: 40)
                        }
                        .disabled(model.busy)
                        .accessibilityLabel(Text("dex_remove_recent"))
                    }
                }
            }
        }
    }
}

/// The five tabs, scrollable on a narrow phone.
private struct DexTabStrip: View {
    @ObservedObject var model: DexModel

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 0) {
                ForEach(DexTab.allCases, id: \.self) { tab in
                    let on = model.tab == tab
                    Button { model.selectTab(tab) } label: {
                        VStack(spacing: 6) {
                            Text(verbatim: Self.label(tab)).skyText(.titleSmall).foregroundStyle(on ? Color.skyPrimary : Color.skyOnSurfaceVariant).lineLimit(1)
                            Capsule().fill(on ? Color.skyPrimary : .clear).frame(height: 3)
                        }
                        .padding(.horizontal, 14)
                        .padding(.top, 10)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .accessibilityAddTraits(on ? .isSelected : [])
                    .accessibilityIdentifier("dex-tab-\(Self.id(tab))")
                }
            }
            .padding(.horizontal, 12)
        }
        .overlay(alignment: .bottom) { Rectangle().fill(Color.skyOutlineVariant).frame(height: 1) }
    }

    static func label(_ tab: DexTab) -> String {
        switch tab {
        case .market: L10n.text("dex_tab_market")
        case .orders: L10n.text("dex_tab_orders")
        case .listings: L10n.text("dex_tab_listings")
        case .history: L10n.text("dex_tab_history")
        case .settings: L10n.text("dex_tab_settings")
        }
    }

    static func id(_ tab: DexTab) -> String {
        switch tab {
        case .market: "market"
        case .orders: "orders"
        case .listings: "listings"
        case .history: "history"
        case .settings: "settings"
        }
    }
}
