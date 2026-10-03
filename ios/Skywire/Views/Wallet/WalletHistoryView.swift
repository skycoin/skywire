import SwiftUI
import UIKit
import WalletCore

private enum TxFilter: Hashable { case all, sent, received, pending }

/// Full history (Android: ui/wallet/WalletHistory.kt): filter chips,
/// transactions grouped by day.
struct WalletHistoryView: View {
    @EnvironmentObject private var model: WalletModel
    @State private var filter = TxFilter.all

    private var txs: [CachedTx] {
        (model.snapshot?.txs ?? []).filter {
            switch filter {
            case .all: true
            case .sent: !$0.incoming
            case .received: $0.incoming
            case .pending: !$0.confirmed
            }
        }
    }

    /// Day label → its transactions, in the list's order.
    private var groups: [(String, [CachedTx])] {
        var out: [(String, [CachedTx])] = []
        for tx in txs {
            let day = dayLabel(tx.timestamp)
            if let last = out.indices.last, out[last].0 == day {
                out[last].1.append(tx)
            } else {
                out.append((day, [tx]))
            }
        }
        return out
    }

    var body: some View {
        List {
            Section {
                Picker(selection: $filter) {
                    Text("wallet_filter_all").tag(TxFilter.all)
                    Text("wallet_filter_sent").tag(TxFilter.sent)
                    Text("wallet_filter_received").tag(TxFilter.received)
                    Text("wallet_filter_pending").tag(TxFilter.pending)
                } label: {
                    EmptyView()
                }
                .pickerStyle(.segmented)
                .listRowBackground(Color.clear)
                .listRowInsets(EdgeInsets())
                .accessibilityIdentifier("wallet-history-filter")
            }
            if groups.isEmpty {
                emptyState
            } else {
                ForEach(groups, id: \.0) { day, dayTxs in
                    Section {
                        ForEach(dayTxs) { tx in
                            NavigationLink(value: WalletRoute.tx(tx.txid)) {
                                TxRow(coin: model.coin, tx: tx, showStatus: true)
                            }
                        }
                    } header: {
                        Text(day)
                    }
                }
            }
        }
        .navigationTitle(Text("wallet_history_title"))
        .navigationBarTitleDisplayMode(.inline)
    }

    /// "Nothing has moved in or out of this wallet" is a claim about the
    /// chain, ours to make only when the list beside the balance actually
    /// arrived: the history is the half of a refresh that can be too big to
    /// fetch, so an empty list can equally mean it never came.
    private var emptyState: some View {
        let unfetched = model.snapshot?.historyBehind != false
        return Section {
            VStack(spacing: 10) {
                Text(unfetched ? L10n.key("wallet_history_unfetched_title") : L10n.key("wallet_history_empty_title"))
                    .font(.headline)
                Text(emptyBody(unfetched: unfetched))
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                if !unfetched {
                    Button("wallet_history_show_address") { model.path.append(.receive) }
                        .buttonStyle(.bordered)
                        .tint(.skywire)
                }
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 40)
            .accessibilityIdentifier("wallet-history-empty")
        }
    }

    private func emptyBody(unfetched: Bool) -> String {
        if unfetched { return L10n.format("wallet_history_unfetched_body", model.coin.ticker) }
        if filter == .all { return L10n.format("wallet_history_empty_all", model.coin.ticker) }
        return L10n.format("wallet_history_empty_filter", model.coin.ticker)
    }
}

/// One transaction, in full.
struct WalletTxView: View {
    @EnvironmentObject private var model: WalletModel
    @Environment(\.openURL) private var openURL
    let txid: String
    @State private var copied = false

    var body: some View {
        List {
            if let tx = model.snapshot?.txs.first(where: { $0.txid == txid }) {
                Section {
                    VStack(spacing: 12) {
                        Text((tx.incoming ? "+" : "−") + model.coin.amountText(tx.amount) + " " + model.coin.ticker)
                            .font(.title.weight(.bold))
                            .foregroundStyle(txColor(tx))
                            .multilineTextAlignment(.center)
                        HStack(spacing: 7) {
                            Circle().fill(txColor(tx)).frame(width: 6, height: 6)
                            Text(tx.confirmed ? L10n.key("wallet_tx_confirmed") : L10n.key("wallet_tx_pending_long"))
                                .font(.footnote.weight(.semibold))
                        }
                        .foregroundStyle(txColor(tx))
                        .padding(.horizontal, 13).padding(.vertical, 7)
                        .background(Capsule().fill(txColor(tx).opacity(0.12)))
                    }
                    .frame(maxWidth: .infinity)
                    .padding(.vertical, 8)
                    .listRowBackground(Color.clear)
                }
                Section {
                    InfoRow(label: Text(tx.incoming ? L10n.key("wallet_tx_from") : L10n.key("wallet_tx_to")),
                            value: tx.party.map { shortAddress($0) } ?? "—", monospaced: tx.party != nil)
                    InfoRow(label: Text("wallet_tx_wallet"), value: model.active?.name ?? "—")
                    InfoRow(label: Text("wallet_tx_date"),
                            value: Date(timeIntervalSince1970: TimeInterval(tx.timestamp)).formatted(.dateTime.day().month(.wide).hour().minute()))
                    InfoRow(label: Text("wallet_tx_fee"), value: feeText(model.coin, tx.fee))
                    InfoRow(label: Text("wallet_tx_confirmations"),
                            value: tx.confirmed ? Amounts.groupThousands(String(tx.confirmations)) : L10n.text("wallet_tx_confirmations_pending"))
                    Button {
                        UIPasteboard.general.string = tx.txid
                        copied = true
                    } label: {
                        HStack {
                            Text("wallet_tx_id").foregroundStyle(.secondary)
                            Spacer()
                            Text(shortAddress(tx.txid, head: 8, tail: 6)).font(.body.weight(.semibold).monospaced()).foregroundStyle(.primary)
                            Image(systemName: "doc.on.doc").font(.footnote).foregroundStyle(.secondary)
                        }
                    }
                    .accessibilityIdentifier("wallet-tx-id")
                }
                if let template = model.coin.explorerTxUrl, let url = URL(string: template.replacingOccurrences(of: "%s", with: tx.txid)) {
                    Section {
                        Button {
                            openURL(url)
                        } label: {
                            Label("wallet_tx_explorer", systemImage: "arrow.up.right.square")
                        }
                    } footer: {
                        Text(L10n.format("wallet_tx_explorer_note", url.host ?? L10n.text("wallet_tx_explorer_fallback")))
                    }
                }
            } else {
                Text("wallet_history_empty_title").foregroundStyle(.secondary)
            }
        }
        .navigationTitle(Text("wallet_tx_title"))
        .navigationBarTitleDisplayMode(.inline)
        .overlay(alignment: .top) {
            if copied {
                Text("wallet_txid_copied")
                    .font(.footnote.weight(.semibold))
                    .padding(.horizontal, 12).padding(.vertical, 6)
                    .background(Capsule().fill(.thinMaterial))
                    .task {
                        try? await Task.sleep(for: .seconds(1.5))
                        copied = false
                    }
            }
        }
    }
}
