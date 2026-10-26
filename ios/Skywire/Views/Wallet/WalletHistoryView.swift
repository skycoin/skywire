import SwiftUI
import UIKit
import WalletCore

private enum TxFilter: Hashable { case all, sent, received, pending }

/// Full history (Android: ui/wallet/WalletHistory.kt): filter chips, transactions grouped by day.
struct WalletHistoryView: View {
    @EnvironmentObject private var model: WalletModel
    @EnvironmentObject private var dialogs: SkyDialogs
    @State private var filter = TxFilter.all

    private var txs: [CachedTx] {
        let address = model.historyAddress
        return (model.snapshot?.txs ?? []).filter { tx in
            let kind: Bool = switch filter {
            case .all: true
            case .sent: !tx.incoming
            case .received: tx.incoming
            case .pending: !tx.confirmed
            }
            return kind && (address.map { tx.own.contains($0) } ?? true)
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
        WalletScreen(title: Text("wallet_history_title")) {
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0) {
                    ScrollView(.horizontal, showsIndicators: false) {
                        HStack(spacing: 8) {
                            chip(L10n.key("wallet_filter_all"), .all)
                            chip(L10n.key("wallet_filter_sent"), .sent)
                            chip(L10n.key("wallet_filter_received"), .received)
                            chip(L10n.key("wallet_filter_pending"), .pending)
                            addressChip
                        }
                        .padding(.horizontal, 20)
                        .padding(.vertical, 4)
                    }
                    .accessibilityIdentifier("wallet-history-filter")
                    if groups.isEmpty {
                        emptyState
                    } else {
                        ForEach(groups, id: \.0) { day, dayTxs in
                            Text(verbatim: day.uppercased()).skyText(.labelSmall).foregroundStyle(Color.skyOnSurfaceVariant)
                                .padding(.horizontal, 20)
                                .padding(.top, 22)
                                .padding(.bottom, 10)
                            VStack(spacing: 0) {
                                ForEach(Array(dayTxs.enumerated()), id: \.element.id) { index, tx in
                                    Button { model.path.append(.tx(tx.txid)) } label: {
                                        TxRow(coin: model.coin, tx: tx, showStatus: true, topDivider: index > 0)
                                    }
                                    .buttonStyle(PressStyle(layer: .skyOnSurface))
                                }
                            }
                            .foregroundStyle(Color.skyOnSurface)
                            .background(Color.skySurfaceVariant)
                            .clipShape(.sky(SkyRadius.large))
                            .padding(.horizontal, 20)
                        }
                    }
                }
                .padding(.bottom, 24)
            }
        }
    }

    private func chip(_ label: LocalizedStringKey, _ value: TxFilter) -> some View {
        let selected = filter == value
        return Button { filter = value } label: {
            Text(label).skyText(.labelLarge)
                .foregroundStyle(selected ? Color.skyPrimary : Color.skyOnSurfaceVariant)
                .padding(.horizontal, 15)
                .padding(.vertical, 9)
                .background(selected ? Color.skySecondaryContainer : Color.skySurfaceVariant, in: .sky(18))
        }
        .buttonStyle(PressStyle(layer: .skyOnSurface, shape: AnyShape(RoundedRectangle(cornerRadius: 18))))
        .accessibilityAddTraits(selected ? .isSelected : [])
    }

    /// The address the list is narrowed to, or All addresses; a tap picks another.
    private var addressChip: some View {
        let address = model.historyAddress
        return Button { dialogs.presentSheet { HistoryAddressSheet().environmentObject(model) } } label: {
            Text(verbatim: address.map { shortAddress($0) } ?? L10n.text("wallet_history_all_addresses")).skyText(.labelLarge)
                .foregroundStyle(address != nil ? Color.skyPrimary : Color.skyOnSurfaceVariant)
                .padding(.horizontal, 15)
                .padding(.vertical, 9)
                .background(address != nil ? Color.skySecondaryContainer : Color.skySurfaceVariant, in: .sky(18))
        }
        .buttonStyle(PressStyle(layer: .skyOnSurface, shape: AnyShape(RoundedRectangle(cornerRadius: 18))))
        .accessibilityAddTraits(address != nil ? .isSelected : [])
        .accessibilityIdentifier("wallet-history-address")
    }

    /// "Nothing has moved in or out of this wallet" is a claim about the chain, ours to make only
    /// when the history arrived: it is the half of a refresh that can be too big to fetch.
    private var emptyState: some View {
        let unfetched = model.snapshot?.historyBehind != false
        return VStack(spacing: 0) {
            Circle().fill(Color.skySurfaceVariant).frame(width: 52, height: 52)
            Text(unfetched ? L10n.key("wallet_history_unfetched_title") : L10n.key("wallet_history_empty_title"))
                .skyText(.titleMedium).padding(.top, 20)
            Text(verbatim: emptyBody(unfetched: unfetched))
                .skyText(.bodyMedium)
                .foregroundStyle(Color.skyOnSurfaceVariant)
                .multilineTextAlignment(.center)
                .padding(.top, 8)
            if !unfetched {
                Button { model.path.append(.receive) } label: { Text("wallet_history_show_address") }
                    .buttonStyle(.tonal)
                    .padding(.top, 20)
            }
        }
        .frame(maxWidth: .infinity)
        .padding(.horizontal, 40)
        .padding(.vertical, 80)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("wallet-history-empty")
    }

    private func emptyBody(unfetched: Bool) -> String {
        if unfetched { return L10n.format("wallet_history_unfetched_body", model.coin.ticker) }
        if filter == .all { return L10n.format("wallet_history_empty_all", model.coin.ticker) }
        return L10n.format("wallet_history_empty_filter", model.coin.ticker)
    }
}

/// Picks which of the wallet's addresses the history shows.
private struct HistoryAddressSheet: View {
    @EnvironmentObject private var model: WalletModel
    @EnvironmentObject private var dialogs: SkyDialogs

    var body: some View {
        if let wallet = model.active {
            VStack(alignment: .leading, spacing: 0) {
                Text("wallet_history_address_pick").skyText(.titleMedium).padding(.horizontal, 20).padding(.vertical, 8)
                ScrollView {
                    VStack(alignment: .leading, spacing: 0) {
                        ForEach(Array(([nil] + (wallet.receiveAddresses + wallet.changeAddresses).map(Optional.some)).enumerated()), id: \.offset) { _, address in
                            Button {
                                model.historyAddress = address
                                dialogs.dismissSheet()
                            } label: {
                                Text(verbatim: address.map { shortAddress($0) } ?? L10n.text("wallet_history_all_addresses"))
                                    .skyText(.bodyMedium, bold: address == model.historyAddress)
                                    .foregroundStyle(address == model.historyAddress ? Color.skyPrimary : Color.skyOnSurface)
                                    .padding(.horizontal, 20)
                                    .padding(.vertical, 13)
                                    .frame(maxWidth: .infinity, alignment: .leading)
                                    .contentShape(Rectangle())
                            }
                            .buttonStyle(PressStyle(layer: .skyOnSurface))
                        }
                    }
                }
                .frame(maxHeight: 420)
                .fixedSize(horizontal: false, vertical: true)
            }
            .padding(.bottom, 24)
        }
    }
}

/// One transaction, in full.
struct WalletTxView: View {
    @EnvironmentObject private var model: WalletModel
    @EnvironmentObject private var dialogs: SkyDialogs
    @Environment(\.openURL) private var openURL
    let txid: String

    var body: some View {
        WalletScreen(title: Text("wallet_tx_title")) {
            if let tx = model.snapshot?.txs.first(where: { $0.txid == txid }) {
                ScrollView {
                    VStack(spacing: 0) {
                        header(tx)
                        details(tx)
                        explorer(tx)
                    }
                    .padding(.horizontal, 20)
                }
            } else {
                Text("wallet_history_empty_title").skyText(.bodyLarge).foregroundStyle(Color.skyOnSurfaceVariant)
                    .frame(maxWidth: .infinity)
                    .padding(40)
                    .frame(maxHeight: .infinity, alignment: .top)
            }
        }
    }

    private func header(_ tx: CachedTx) -> some View {
        VStack(spacing: 0) {
            Text(verbatim: (tx.incoming ? "+" : "−") + model.coin.amountText(tx.amount) + " " + model.coin.ticker)
                .skyText(.headlineMedium)
                .foregroundStyle(txColor(tx))
                .multilineTextAlignment(.center)
            HStack(spacing: 7) {
                Circle().fill(txColor(tx)).frame(width: 6, height: 6)
                Text(tx.confirmed ? L10n.key("wallet_tx_confirmed") : L10n.key("wallet_tx_pending_long")).skyText(.labelLarge)
            }
            .foregroundStyle(txColor(tx))
            .padding(.horizontal, 13)
            .padding(.vertical, 7)
            .background(txColor(tx).opacity(0.12), in: .sky(SkyRadius.large))
            .padding(.top, 12)
        }
        .frame(maxWidth: .infinity)
        .padding(.top, 12)
        .padding(.bottom, 24)
    }

    private func details(_ tx: CachedTx) -> some View {
        VStack(spacing: 0) {
            detail(tx.incoming ? L10n.key("wallet_tx_from") : L10n.key("wallet_tx_to"), tx.party.map { shortAddress($0) } ?? "—", first: true)
            detail(L10n.key("wallet_tx_wallet"), model.active?.name ?? "—")
            detail(L10n.key("wallet_tx_date"), wallFormat("d MMMM, HH:mm", Date(timeIntervalSince1970: TimeInterval(tx.timestamp))))
            if let price = model.usdPrice {
                // Today's price, not the price on the day: skycoin-web says the same.
                detail(L10n.key("wallet_tx_usd_now"), L10n.format("wallet_usd_value", model.coin.usdText(tx.amount, price: price)))
            }
            detail(L10n.key("wallet_tx_fee"), feeText(model.coin, tx.fee))
            detail(L10n.key("wallet_tx_confirmations"),
                   tx.confirmed ? Amounts.groupThousands(String(tx.confirmations)) : L10n.text("wallet_tx_confirmations_pending"))
            SkyDivider(color: .skyContainerHighest)
            Button {
                UIPasteboard.general.string = tx.txid
                dialogs.snackbar(Text("wallet_txid_copied"))
            } label: {
                HStack(spacing: 0) {
                    Text("wallet_tx_id").skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant)
                    Spacer()
                    Text(verbatim: shortAddress(tx.txid, head: 8, tail: 6)).skyText(.bodyMedium, bold: true).foregroundStyle(Color.skyOnSurface)
                    MaterialIcon(MI.outlinedContentCopy, size: 15).foregroundStyle(Color.skyOnSurfaceVariant).padding(.leading, 8)
                }
                .padding(.vertical, 14)
                .contentShape(Rectangle())
            }
            .buttonStyle(PressStyle(layer: .skyOnSurface))
            .accessibilityIdentifier("wallet-tx-id")
        }
        .padding(.horizontal, 16)
        .foregroundStyle(Color.skyOnSurface)
        .background(Color.skySurfaceVariant, in: .sky(SkyRadius.large))
    }

    private func detail(_ label: LocalizedStringKey, _ value: String, first: Bool = false) -> some View {
        VStack(spacing: 0) {
            if !first { SkyDivider(color: .skyContainerHighest) }
            HStack {
                Text(label).skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant)
                Spacer()
                Text(verbatim: value).skyText(.bodyMedium, bold: true)
            }
            .padding(.vertical, 14)
        }
    }

    @ViewBuilder
    private func explorer(_ tx: CachedTx) -> some View {
        if let template = model.coin.explorerTxUrl, let url = URL(string: template.replacingOccurrences(of: "%s", with: tx.txid)) {
            Button { openURL(url) } label: {
                HStack(spacing: 9) {
                    Text("wallet_tx_explorer")
                    MaterialIcon(MI.outlinedOpenInNew, size: 15)
                }
                .frame(maxWidth: .infinity)
            }
            .buttonStyle(TonalButtonStyle(height: 50))
            .padding(.top, 14)
            Text(verbatim: L10n.format("wallet_tx_explorer_note", url.host ?? L10n.text("wallet_tx_explorer_fallback")))
                .skyText(.bodySmall)
                .foregroundStyle(Color.skyOnSurfaceVariant)
                .multilineTextAlignment(.center)
                .frame(maxWidth: .infinity)
                .padding(.top, 12)
                .padding(.bottom, 24)
        }
    }
}
