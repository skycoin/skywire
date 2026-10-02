import SwiftUI
import WalletCore

/// The Wallet tab (Android: ui/wallet/WalletScreen.kt and its nav graph):
/// one surface for every coin the user holds. Its own navigation stack, and
/// the 30-second refresh for as long as the tab is on screen.
struct WalletTab: View {
    @StateObject private var model = WalletModel(store: .app())

    var body: some View {
        NavigationStack(path: $model.path) {
            WalletHome()
                .navigationDestination(for: WalletRoute.self) { route in
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
        .overlay(alignment: .top) { WalletToast() }
        .task { await model.run() }
        // Outermost, so the toast overlay sees the model as well as the stack.
        .environmentObject(model)
    }
}

/// The outcome of the last action, shown for a moment (Android's snackbar).
private struct WalletToast: View {
    @EnvironmentObject private var model: WalletModel

    var body: some View {
        if let message = model.message {
            Text(message)
                .font(.footnote.weight(.semibold))
                .multilineTextAlignment(.center)
                .padding(.horizontal, 14).padding(.vertical, 8)
                .background(Capsule().fill(.thinMaterial))
                .padding(.horizontal, 20)
                .padding(.top, 4)
                .accessibilityIdentifier("wallet-message")
                .task(id: message) {
                    try? await Task.sleep(for: .seconds(3))
                    model.messageShown()
                }
        }
    }
}

/// The tab's root. With no wallet for the selected coin it opens into setup;
/// with one it is the balance screen. Either way the coin chip sits on top.
struct WalletHome: View {
    @EnvironmentObject private var model: WalletModel
    @State private var coinSheet = false

    var body: some View {
        List {
            Section {
                Button { coinSheet = true } label: { CoinChip(coin: model.coin) }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("wallet-coin-chip")
            }
            .listRowBackground(Color.clear)
            .listRowInsets(EdgeInsets(top: 4, leading: 0, bottom: 0, trailing: 0))

            if !model.ready {
                ProgressView()
            } else if model.active == nil {
                intro
            } else {
                balanceSection
                recentSection
                Section {
                    NavigationLink(value: WalletRoute.wallets) {
                        HStack {
                            Label("wallet_wallets_row", systemImage: "wallet.pass")
                            Spacer()
                            Text("\(model.coinWallets.count)").foregroundStyle(.secondary)
                        }
                    }
                    .accessibilityIdentifier("wallet-wallets-row")
                    // Which node the coin is on, shown rather than hidden: the
                    // answer to "why will this not sync" is often here.
                    if model.coin.nodeUrlEditable {
                        NavigationLink(value: WalletRoute.node) {
                            HStack {
                                Label("wallet_node_row", systemImage: "server.rack")
                                Spacer()
                                Text(model.coin.nodeUrl.components(separatedBy: "://").last ?? model.coin.nodeUrl)
                                    .foregroundStyle(.secondary)
                                    .lineLimit(1)
                                    .truncationMode(.middle)
                            }
                        }
                        .accessibilityIdentifier("wallet-node-row")
                    }
                }
            }
        }
        .navigationTitle(Text("tab_wallet"))
        .refreshable { model.refreshNow() }
        .sheet(isPresented: $coinSheet) {
            // Handed over explicitly: on iOS 16 a sheet does not always
            // inherit its presenter's environment objects.
            CoinSheet(onAddCoin: {
                coinSheet = false
                model.path.append(.addCoin)
            })
            .environmentObject(model)
        }
    }

    private var intro: some View {
        Section {
            VStack(alignment: .leading, spacing: 10) {
                Text(L10n.format("wallet_intro_title", model.coin.name)).font(.title2.weight(.bold))
                Text("wallet_intro_body").foregroundStyle(.secondary)
            }
            .padding(.vertical, 6)
            Button {
                model.startCreate()
                model.path.append(.create)
            } label: {
                IntroRow(systemImage: "plus", title: L10n.key("wallet_intro_create"), subtitle: L10n.key("wallet_intro_create_sub"))
            }
            // Plain: a list button would tint the whole row's text.
            .buttonStyle(.plain)
            .accessibilityIdentifier("wallet-create")
            Button {
                model.startRestore()
                model.path.append(.restore)
            } label: {
                IntroRow(systemImage: "arrow.down", title: L10n.key("wallet_intro_restore"), subtitle: L10n.key("wallet_intro_restore_sub"))
            }
            // Plain: a list button would tint the whole row's text.
            .buttonStyle(.plain)
            .accessibilityIdentifier("wallet-restore")
        }
    }

    @ViewBuilder
    private var balanceSection: some View {
        Section {
            VStack(alignment: .leading, spacing: 8) {
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    Text(model.coin.amountText(model.snapshot?.confirmed ?? 0))
                        .font(.largeTitle.weight(.bold))
                        .lineLimit(1)
                        .minimumScaleFactor(0.5)
                        .accessibilityIdentifier("wallet-balance")
                    Text(model.coin.ticker).font(.title3).foregroundStyle(.secondary)
                    if model.refreshing { ProgressView().controlSize(.small) }
                }
                Text(subLine).font(.subheadline).foregroundStyle(.secondary)
            }
            .padding(.vertical, 4)
            if model.stale { WalletBanner(text: staleText) }
            // A wallet whose addresses were never confirmed against the chain
            // shows the balance of the addresses it happens to hold, which
            // after a restore on a bad connection can be one of several: a
            // wrong number, said plainly. Not while a refresh is in flight —
            // that refresh is what settles it.
            if model.active?.addressScanPending == true && !model.refreshing {
                WalletBanner(text: L10n.text("wallet_scan_pending"))
            }
        }
        Section {
            HStack(spacing: 12) {
                Button { model.path.append(.receive) } label: {
                    ActionLabel(systemImage: "arrow.down", title: L10n.key("wallet_receive"))
                }
                .buttonStyle(.bordered)
                .accessibilityIdentifier("wallet-receive")
                Button { model.path.append(.send) } label: {
                    ActionLabel(systemImage: "arrow.up", title: L10n.key("wallet_send"))
                }
                .buttonStyle(.borderedProminent)
                .disabled(model.stale)
                .accessibilityIdentifier("wallet-send")
            }
            .tint(.skywire)
            .listRowBackground(Color.clear)
            .listRowInsets(EdgeInsets())
            if model.stale {
                Text("wallet_stale_send_note").font(.footnote).foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity)
                    .multilineTextAlignment(.center)
                    .listRowBackground(Color.clear)
            }
        }
    }

    private var recentSection: some View {
        Section {
            let recent = Array((model.snapshot?.txs ?? []).prefix(3))
            if recent.isEmpty {
                VStack(spacing: 6) {
                    Text("wallet_no_activity_title").font(.body.weight(.semibold))
                    Text("wallet_no_activity_body").font(.footnote).foregroundStyle(.secondary).multilineTextAlignment(.center)
                }
                .frame(maxWidth: .infinity)
                .padding(.vertical, 18)
            } else {
                ForEach(recent) { tx in
                    NavigationLink(value: WalletRoute.tx(tx.txid)) {
                        TxRow(coin: model.coin, tx: tx, showStatus: false)
                    }
                }
            }
        } header: {
            HStack {
                Text("wallet_recent")
                Spacer()
                Button("wallet_see_all") { model.path.append(.history) }
                    .font(.footnote.weight(.semibold))
                    .textCase(nil)
                    .accessibilityIdentifier("wallet-see-all")
            }
        }
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
        return L10n.format("wallet_stale_banner", at.formatted(date: .omitted, time: .shortened), age)
    }
}

/// A Receive / Send button's face: its arrow and its word, centred.
private struct ActionLabel: View {
    let systemImage: String
    let title: LocalizedStringKey

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: systemImage).font(.body.weight(.semibold))
            Text(title).font(.body.weight(.semibold))
        }
        .frame(maxWidth: .infinity, minHeight: 34)
    }
}

/// The coin's badge, name and a chevron: the switch between coins.
private struct CoinChip: View {
    let coin: CoinSpec

    var body: some View {
        HStack(spacing: 10) {
            CoinBadge(coin: coin, size: 30)
            Text(coin.name).font(.body.weight(.semibold))
            Image(systemName: "chevron.down").font(.footnote.weight(.semibold)).foregroundStyle(.secondary)
        }
        .padding(.vertical, 7)
        .padding(.leading, 7)
        .padding(.trailing, 12)
        .background(Capsule().fill(Color(.secondarySystemGroupedBackground)))
        .accessibilityElement(children: .combine)
        .accessibilityHint(Text("wallet_coin_chip_description"))
    }
}

private struct IntroRow: View {
    let systemImage: String
    let title: LocalizedStringKey
    let subtitle: LocalizedStringKey

    var body: some View {
        HStack(spacing: 14) {
            Image(systemName: systemImage)
                .font(.body.weight(.semibold))
                .foregroundStyle(Color.skywire)
                .frame(width: 40, height: 40)
                .background(Circle().fill(Color.skywire.opacity(0.15)))
            VStack(alignment: .leading, spacing: 3) {
                Text(title).font(.body.weight(.semibold)).foregroundStyle(.primary)
                Text(subtitle).font(.footnote).foregroundStyle(.secondary)
            }
            Spacer()
            Image(systemName: "chevron.right").font(.footnote.weight(.semibold)).foregroundStyle(.tertiary)
        }
        .padding(.vertical, 4)
        .contentShape(Rectangle())
    }
}

/// Every coin, searchable; pick one, remove one the user added, or add one.
private struct CoinSheet: View {
    @EnvironmentObject private var model: WalletModel
    @Environment(\.dismiss) private var dismiss
    @State private var query = ""
    /// The coin the user is being asked about.
    @State private var removing: CoinSpec?
    let onAddCoin: () -> Void

    var body: some View {
        NavigationStack {
            List {
                ForEach(filtered) { coin in
                    HStack(spacing: 13) {
                        Button {
                            model.selectCoin(coin.id)
                            dismiss()
                        } label: {
                            row(coin)
                        }
                        .buttonStyle(.plain)
                        .accessibilityIdentifier("wallet-coin-\(coin.id)")
                        // Only what the user added can go, and the control is
                        // visible rather than a swipe a user would have to know.
                        if !coin.builtIn {
                            Button {
                                removing = coin
                            } label: {
                                Image(systemName: "xmark.circle").foregroundStyle(.secondary)
                            }
                            .buttonStyle(.borderless)
                            .accessibilityLabel(Text("wallet_coin_remove"))
                        }
                    }
                    .listRowBackground(coin.id == model.coin.id ? Color(.tertiarySystemFill) : nil)
                }
                Button(action: onAddCoin) {
                    Label("wallet_coins_add", systemImage: "plus.circle.fill").font(.body.weight(.semibold))
                }
                .accessibilityIdentifier("wallet-add-coin")
            }
            .searchable(text: $query, prompt: Text(L10n.format("wallet_coins_search", model.coins.count)))
            .navigationTitle(Text("wallet_coins_title"))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("cancel") { dismiss() }
                }
            }
            // Two different conversations: with wallets on the coin there is
            // nothing to confirm, only a reason and what to do about it.
            .alert(removalTitle, isPresented: Binding(get: { removing != nil }, set: { if !$0 { removing = nil } }), presenting: removing) { coin in
                if blocked(coin) {
                    Button("wallet_coin_remove_ack") {}
                } else {
                    Button("wallet_coin_remove_confirm", role: .destructive) { model.removeCoin(coin.id) }
                    Button("cancel", role: .cancel) {}
                }
            } message: { coin in
                Text(blocked(coin) ? L10n.key("wallet_coin_remove_blocked") : L10n.key("wallet_coin_remove_body"))
            }
        }
    }

    private var filtered: [CoinSpec] {
        let q = query.trimmingCharacters(in: .whitespaces).lowercased()
        return model.coins.filter { q.isEmpty || $0.name.lowercased().contains(q) || $0.ticker.lowercased().contains(q) }
    }

    private func blocked(_ coin: CoinSpec) -> Bool {
        model.allWallets.contains { $0.coinId == coin.id }
    }

    private var removalTitle: Text {
        guard let coin = removing else { return Text(verbatim: "") }
        return Text(blocked(coin) ? L10n.format("wallet_coin_remove_blocked_title", coin.name) : L10n.format("wallet_coin_remove_title", coin.name))
    }

    private func row(_ coin: CoinSpec) -> some View {
        let count = model.allWallets.filter { $0.coinId == coin.id }.count
        return HStack(spacing: 13) {
            CoinBadge(coin: coin, size: 36)
            VStack(alignment: .leading, spacing: 2) {
                Text(coin.name).font(.body.weight(.semibold))
                Text(kindLine(coin)).font(.footnote).foregroundStyle(.secondary)
            }
            Spacer()
            Text(count == 0 ? "—" : count == 1 ? L10n.text("wallet_count_one") : L10n.format("wallet_count_many", count))
                .font(.footnote)
                .foregroundStyle(.secondary)
        }
        .contentShape(Rectangle())
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
