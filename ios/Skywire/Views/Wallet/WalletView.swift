import SwiftUI
import WalletCore

/// The Wallet tab (Android: ui/wallet/WalletScreen.kt and its nav graph):
/// one surface for every coin the user holds. Its own navigation stack, and
/// the 30-second refresh for as long as the tab is on screen.
struct WalletTab: View {
    @StateObject private var model = WalletModel(store: .app())
    @EnvironmentObject private var dialogs: SkyDialogs

    var body: some View {
        // The wallet's own stack, cross-faded as Android's NavHost does.
        ZStack {
            layer(WalletHome(), shown: model.path.isEmpty)
            ForEach(Array(model.path.enumerated()), id: \.offset) { index, route in
                layer(destination(route), shown: index == model.path.count - 1)
                    .transition(.opacity)
            }
        }
        .animation(Navigator.fade, value: model.path)
        .task { await model.run() }
        .onChange(of: model.message) { message in
            guard let message else { return }
            dialogs.snackbar(Text(verbatim: message))
            model.messageShown()
        }
        .environmentObject(model)
    }

    private func layer(_ view: some View, shown: Bool) -> some View {
        view.stackLayer(shown: shown)
    }

    @ViewBuilder private func destination(_ route: WalletRoute) -> some View {
        switch route {
        case .create: WalletSeedView()
        case .verify: WalletVerifyView()
        case .restore: WalletRestoreView()
        case .receive: WalletReceiveView()
        case .send: WalletSendView()
        case .result: WalletResultView()
        case .history: WalletHistoryView()
        case .tx(let txid): WalletTxView(txid: txid)
        case .wallets: WalletManageView()
        case .reveal(let id): WalletRevealView(walletId: id)
        case .addCoin: WalletAddCoinView()
        case .node: WalletNodeView()
        }
    }
}

/// The tab's root (Android: WalletScreen): the coin chip, then setup or the balance.
struct WalletHome: View {
    @EnvironmentObject private var model: WalletModel
    @EnvironmentObject private var dialogs: SkyDialogs

    var body: some View {
        WalletScreen(title: Text("tab_wallet"), help: .wallet) {
            if !model.ready {
                MaterialSpinner(size: 20, stroke: 2)
            } else {
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 0) {
                        Button { openCoins() } label: { CoinChip(coin: model.coin) }
                            .buttonStyle(PressStyle())
                            .accessibilityIdentifier("wallet-coin-chip")
                        if model.active == nil {
                            intro
                        } else {
                            balance
                            recent
                            WalletNavRow(icon: MI.outlinedAccountBalanceWallet, title: L10n.key("wallet_wallets_row"),
                                         value: "\(model.coinWallets.count)") { model.path.append(.wallets) }
                                .padding(.top, 14)
                                .accessibilityIdentifier("wallet-wallets-row")
                            // Which node the coin is on, shown: "why will this not sync" is often here.
                            WalletNavRow(icon: MI.outlinedDns, title: L10n.key("wallet_node_row"),
                                         value: model.coin.nodeUrl.components(separatedBy: "://").last ?? model.coin.nodeUrl,
                                         valueMaxWidth: 150) { model.path.append(.node) }
                                .padding(.top, 10)
                                .accessibilityIdentifier("wallet-node-row")
                        }
                    }
                    .padding(EdgeInsets(top: 8, leading: 20, bottom: 16, trailing: 20))
                }
            }
        }
    }

    private func openCoins() {
        dialogs.presentSheet {
            CoinSheet { model.path.append(.addCoin) }.environmentObject(model)
        }
    }

    private var intro: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text(verbatim: L10n.format("wallet_intro_title", model.coin.name)).skyText(.headlineMedium)
            Text("wallet_intro_body").skyText(.bodyLarge).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 12)
            IntroCard(icon: MI.outlinedAdd, title: L10n.key("wallet_intro_create"), subtitle: L10n.key("wallet_intro_create_sub")) {
                model.startCreate()
                model.path.append(.create)
            }
            .padding(.top, 32)
            .accessibilityIdentifier("wallet-create")
            IntroCard(icon: MI.outlinedArrowDownward, title: L10n.key("wallet_intro_restore"), subtitle: L10n.key("wallet_intro_restore_sub")) {
                model.startRestore()
                model.path.append(.restore)
            }
            .padding(.top, 12)
            .accessibilityIdentifier("wallet-restore")
        }
        .padding(.top, 38)
    }

    @ViewBuilder
    private var balance: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Text(verbatim: model.coin.amountText(model.snapshot?.confirmed ?? 0))
                    .skyText(.displaySmall).lineLimit(1).minimumScaleFactor(0.5)
                    .accessibilityIdentifier("wallet-balance")
                Text(verbatim: model.coin.ticker).skyText(.titleMedium).foregroundStyle(Color.skyOnSurfaceVariant)
                if model.refreshing { MaterialSpinner(size: 14, stroke: 2) }
            }
            Text(verbatim: subLine).skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 9)
        }
        .padding(.top, 22)
        if model.stale {
            WalletBanner(text: staleText).padding(.top, 18)
        }
        // Addresses never confirmed against the chain: possibly a wrong number, said plainly.
        if model.active?.addressScanPending == true && !model.refreshing {
            WalletBanner(text: L10n.text("wallet_scan_pending")).padding(.top, 18)
        }
        HStack(spacing: 12) {
            Button { model.path.append(.receive) } label: { actionLabel(MI.outlinedArrowDownward, L10n.key("wallet_receive")) }
                .buttonStyle(.tonal)
                .accessibilityIdentifier("wallet-receive")
            Button { model.path.append(.send) } label: { actionLabel(MI.outlinedArrowUpward, L10n.key("wallet_send")) }
                .buttonStyle(.filled)
                .disabled(model.stale)
                .accessibilityIdentifier("wallet-send")
        }
        .padding(.top, 22)
        if model.stale {
            Text("wallet_stale_send_note").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                .multilineTextAlignment(.center).frame(maxWidth: .infinity).padding(.top, 10)
        }
    }

    /// Receive and Send: an arrow and the word, on a 50 pt pill.
    private func actionLabel(_ icon: String, _ title: LocalizedStringKey) -> some View {
        HStack(spacing: 8) {
            MaterialIcon(icon, size: 17)
            Text(title).skyText(.labelLarge)
        }
        .frame(maxWidth: .infinity, minHeight: 42)
    }

    @ViewBuilder
    private var recent: some View {
        HStack {
            Text(L10n.text("wallet_recent").uppercased()).skyText(.labelSmall).foregroundStyle(Color.skyOnSurfaceVariant)
            Spacer()
            Button { model.path.append(.history) } label: {
                Text("wallet_see_all").skyText(.labelLarge).foregroundStyle(Color.skyPrimary)
            }
            .buttonStyle(PressStyle())
            .accessibilityIdentifier("wallet-see-all")
        }
        .padding(.top, 32)
        .padding(.bottom, 12)
        let txs = Array((model.snapshot?.txs ?? []).prefix(3))
        VStack(spacing: 0) {
            if txs.isEmpty {
                VStack(spacing: 0) {
                    Text("wallet_no_activity_title").skyText(.bodyLarge, bold: true)
                    Text("wallet_no_activity_body").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                        .multilineTextAlignment(.center).padding(.top, 6)
                }
                .frame(maxWidth: .infinity)
                .padding(.vertical, 34)
                .padding(.horizontal, 20)
            } else {
                ForEach(Array(txs.enumerated()), id: \.element.id) { index, tx in
                    Button { model.path.append(.tx(tx.txid)) } label: {
                        TxRow(coin: model.coin, tx: tx, showStatus: false, topDivider: index > 0)
                    }
                    .buttonStyle(PressStyle(layer: .skyOnSurface))
                }
            }
        }
        .foregroundStyle(Color.skyOnSurface)
        .background(Color.skySurfaceVariant, in: .sky(SkyRadius.medium))
        .clipShape(.sky(SkyRadius.medium))
    }

    private var subLine: String {
        let snapshot = model.snapshot
        switch model.coin.kind {
        case .btc: return L10n.format("wallet_btc_sub", snapshot?.spendableOutputs ?? 0)
        case .eth: return L10n.text("wallet_eth_sub")
        case .erc20: return L10n.format("wallet_erc20_sub", model.coin.ticker)
        case .skyFiber: return L10n.format("wallet_hours_sub", hoursText(snapshot?.hours ?? 0))
        }
    }

    private var staleText: String {
        guard let snapshot = model.snapshot, snapshot.fetchedAtMs > 0 else { return L10n.text("wallet_stale_never") }
        let at = Date(timeIntervalSince1970: TimeInterval(snapshot.fetchedAtMs) / 1000)
        let minutes = max(1, (nowMs() - snapshot.fetchedAtMs) / 60_000)
        let age = minutes >= 60
            ? L10n.format("wallet_stale_age_hours", minutes / 60, minutes % 60)
            : L10n.format("wallet_stale_age_minutes", minutes)
        return L10n.format("wallet_stale_banner", wallFormat("HH:mm", at), age)
    }
}

/// The coin chip: badge, name, a chevron; 22 pt corners on surfaceVariant.
private struct CoinChip: View {
    let coin: CoinSpec

    var body: some View {
        HStack(spacing: 10) {
            CoinBadge(coin: coin, size: 30)
            Text(verbatim: coin.name).skyText(.bodyLarge, bold: true)
            MaterialIcon(MI.outlinedKeyboardArrowDown, size: 16).foregroundStyle(Color.skyOnSurfaceVariant)
        }
        .foregroundStyle(Color.skyOnSurface)
        .padding(EdgeInsets(top: 7, leading: 7, bottom: 7, trailing: 12))
        .background(Color.skySurfaceVariant, in: .sky(SkyRadius.large))
        .accessibilityElement(children: .combine)
        .accessibilityHint(Text("wallet_coin_chip_description"))
    }
}

/// IntroCard: a 44 pt disc with the icon, bold title, subtitle, chevron.
struct IntroCard: View {
    let icon: String
    let title: LocalizedStringKey
    let subtitle: LocalizedStringKey
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 14) {
                MaterialIcon(icon, size: 20).foregroundStyle(Color.skyPrimary)
                    .frame(width: 44, height: 44).background(Color.skySecondaryContainer, in: Circle())
                VStack(alignment: .leading, spacing: 0) {
                    Text(title).skyText(.bodyLarge, bold: true)
                    Text(subtitle).skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 3)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                MaterialIcon(MI.outlinedKeyboardArrowRight, size: 16).foregroundStyle(Color.skyOnSurfaceVariant)
            }
            .foregroundStyle(Color.skyOnSurface)
            .padding(20)
            .background(Color.skySurfaceVariant, in: .sky(SkyRadius.medium))
            .contentShape(RoundedRectangle(cornerRadius: SkyRadius.medium))
        }
        .buttonStyle(PressStyle(layer: .skyOnSurface, shape: AnyShape(RoundedRectangle(cornerRadius: SkyRadius.medium))))
    }
}

/// Every coin, searchable; pick one, remove one the user added, or add one (a bottom sheet).
private struct CoinSheet: View {
    let onAddCoin: () -> Void
    @EnvironmentObject private var model: WalletModel
    @EnvironmentObject private var dialogs: SkyDialogs
    @State private var query = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("wallet_coins_title").skyText(.titleMedium).padding(.horizontal, 20).padding(.vertical, 4)
            SkyOutlinedTextField(placeholder: LocalizedStringKey(stringLiteral: L10n.format("wallet_coins_search", model.coins.count)),
                                 text: $query, notch: .skyContainerLow, leadingIcon: MI.outlinedSearch, radius: SkyRadius.small)
                .padding(.horizontal, 20).padding(.vertical, 12)
            ForEach(filtered) { coin in
                HStack(spacing: 13) {
                    CoinBadge(coin: coin, size: 36)
                    VStack(alignment: .leading, spacing: 0) {
                        Text(verbatim: coin.name).skyText(.bodyLarge, bold: true)
                        Text(verbatim: kindLine(coin)).skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 2)
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                    Text(verbatim: countText(coin)).skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                    if !coin.builtIn {
                        Button { remove(coin) } label: {
                            MaterialIcon(MI.outlinedClose, size: 16).foregroundStyle(Color.skyOnSurfaceVariant).frame(width: 28, height: 28)
                        }
                        .buttonStyle(PressStyle())
                        .accessibilityLabel(Text("wallet_coin_remove"))
                    }
                }
                .padding(.horizontal, 20)
                .padding(.vertical, 13)
                .background(coin.id == model.coin.id ? Color.skySurfaceVariant : .clear)
                .contentShape(Rectangle())
                .onTapGesture {
                    model.selectCoin(coin.id)
                    dialogs.dismissSheet()
                }
                .accessibilityElement(children: .combine)
                .accessibilityAddTraits(.isButton)
                .accessibilityIdentifier("wallet-coin-\(coin.id)")
            }
            Button {
                dialogs.dismissSheet()
                onAddCoin()
            } label: {
                HStack(spacing: 13) {
                    MaterialIcon(MI.outlinedAdd, size: 18).foregroundStyle(Color.skyPrimary)
                        .frame(width: 36, height: 36).background(Color.skyContainerHighest, in: Circle())
                    Text("wallet_coins_add").skyText(.bodyLarge, bold: true).foregroundStyle(Color.skyPrimary)
                    Spacer()
                }
                .padding(.horizontal, 20)
                .padding(.vertical, 15)
                .contentShape(Rectangle())
            }
            .buttonStyle(PressStyle(layer: .skyOnSurface))
            .accessibilityIdentifier("wallet-add-coin")
        }
        .padding(.bottom, 28)
    }

    private var filtered: [CoinSpec] {
        let q = query.trimmingCharacters(in: .whitespaces).lowercased()
        return model.coins.filter { q.isEmpty || $0.name.lowercased().contains(q) || $0.ticker.lowercased().contains(q) }
    }

    private func countText(_ coin: CoinSpec) -> String {
        let count = model.allWallets.filter { $0.coinId == coin.id }.count
        return count == 0 ? "—" : count == 1 ? L10n.text("wallet_count_one") : L10n.format("wallet_count_many", count)
    }

    /// With wallets on the coin there is nothing to confirm: only a reason and what to do.
    private func remove(_ coin: CoinSpec) {
        if model.allWallets.contains(where: { $0.coinId == coin.id }) {
            dialogs.show(SkyDialog(title: Text(verbatim: L10n.format("wallet_coin_remove_blocked_title", coin.name)),
                                   message: Text("wallet_coin_remove_blocked"),
                                   actions: [SkyDialog.Action(label: Text("wallet_coin_remove_ack"))]))
        } else {
            dialogs.show(SkyDialog(title: Text(verbatim: L10n.format("wallet_coin_remove_title", coin.name)),
                                   message: Text("wallet_coin_remove_body"),
                                   actions: [SkyDialog.Action(label: Text("cancel")),
                                             SkyDialog.Action(label: Text("wallet_coin_remove_confirm")) { model.removeCoin(coin.id) }]))
        }
    }

    private func kindLine(_ coin: CoinSpec) -> String {
        if coin.id == CoinSpec.sky.id { return L10n.text("wallet_coin_native") }
        switch coin.kind {
        case .btc: return L10n.text("wallet_coin_btc")
        case .eth: return L10n.text("wallet_coin_eth")
        case .erc20: return L10n.text("wallet_coin_erc20")
        case .skyFiber: return L10n.text("wallet_coin_fiber")
        }
    }
}
