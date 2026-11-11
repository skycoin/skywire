import SwiftUI
import WalletCore

/// A fiber node's own pages, as skycoin-web has them (Android: ui/wallet/WalletNodeInfo.kt):
/// the chain, the node's peers, its unconfirmed pool, and the wallet's unspent outputs.
struct WalletNodeInfoView: View {
    private enum Tab: CaseIterable { case chain, network, pending, outputs }

    @EnvironmentObject private var model: WalletModel
    @EnvironmentObject private var dialogs: SkyDialogs
    @State private var tab = Tab.chain
    @State private var reload = 0
    @State private var loading = false
    @State private var error: String?
    @State private var chain: ChainInfo?
    @State private var peers: [PeerInfo] = []
    @State private var pending: [PendingInfo] = []
    @State private var allPending = false

    var body: some View {
        WalletScreen(title: Text("wallet_nodeinfo_title")) {
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0) {
                    SkySegmentedRow(items: Tab.allCases, selected: tab, label: { t in
                        switch t {
                        case .chain: Text("wallet_nodeinfo_tab_chain")
                        case .network: Text("wallet_nodeinfo_tab_network")
                        case .pending: Text("wallet_nodeinfo_tab_pending")
                        case .outputs: Text("wallet_nodeinfo_tab_outputs")
                        }
                    }, onSelect: { tab = $0 })
                    .accessibilityIdentifier("wallet-nodeinfo-tabs")
                    if loading {
                        MaterialSpinner(size: 24, stroke: 2).padding(.top, 24)
                    }
                    if let error {
                        Text(verbatim: error).skyText(.bodyMedium).foregroundStyle(Color.skyError).padding(.top, 18)
                    }
                    switch tab {
                    case .chain: chainPage
                    case .network: peersPage
                    case .pending: pendingPage
                    case .outputs: outputsPage
                    }
                }
                .padding(.horizontal, 20)
                .padding(.bottom, 24)
            }
        }
        .task(id: "\(tab)-\(reload)") { await load() }
    }

    private func load() async {
        loading = true
        error = nil
        do {
            switch tab {
            case .chain: chain = try await model.chainInfo()
            case .network: peers = try await model.peers()
            case .pending: pending = try await model.pending()
            case .outputs: model.loadUtxos()
            }
        } catch is CancellationError {
        } catch {
            self.error = WalletModel.report(error)
        }
        loading = false
    }

    @ViewBuilder private var chainPage: some View {
        if let c = chain {
            let ticker = model.coin.ticker
            InfoCard(rows: [
                (L10n.text("wallet_chain_blocks"), Amounts.groupThousands(String(c.height))),
                (L10n.text("wallet_chain_time"), Self.dateTime(c.timestamp)),
                (L10n.text("wallet_chain_hash"), shortAddress(c.blockHash, head: 10, tail: 8)),
                (L10n.text("wallet_chain_current_supply"), "\(Self.supply(c.currentSupply)) \(ticker)"),
                (L10n.text("wallet_chain_total_supply"), "\(Self.supply(c.totalSupply)) \(ticker)"),
            ])
        }
    }

    @ViewBuilder private var peersPage: some View {
        if !loading && error == nil && peers.isEmpty { empty(L10n.key("wallet_peers_none")) }
        ForEach(peers, id: \.address) { p in
            HStack(alignment: .top) {
                VStack(alignment: .leading, spacing: 2) {
                    Text(verbatim: p.address).skyText(.bodyMedium, bold: true).foregroundStyle(Color.skyOnSurface)
                    Text(verbatim: L10n.format("wallet_peer_line", Amounts.groupThousands(String(p.height)), Self.relative(p.lastReceived)))
                        .skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                    Text(p.trusted ? L10n.key("wallet_peer_default") : L10n.key("wallet_peer_exchange"))
                        .skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                Text(p.outgoing ? L10n.key("wallet_peer_outgoing") : L10n.key("wallet_peer_incoming"))
                    .skyText(.labelMedium).foregroundStyle(Color.skyPrimary)
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 12)
            .background(Color.skySurfaceVariant, in: .sky(14))
            .padding(.top, 12)
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("wallet-peer")
        }
    }

    @ViewBuilder private var pendingPage: some View {
        SkySegmentedRow(items: [false, true], selected: allPending, label: { all in
            Text(all ? L10n.key("wallet_pending_all") : L10n.key("wallet_pending_mine"))
        }, onSelect: { allPending = $0 })
        .padding(.top, 14)
        .accessibilityIdentifier("wallet-pending-filter")
        let shown = pending.filter { allPending || $0.mine }
        if !loading && error == nil && shown.isEmpty { empty(L10n.key("wallet_pending_none")) }
        ForEach(shown, id: \.txid) { p in
            HStack(spacing: 4) {
                VStack(alignment: .leading, spacing: 2) {
                    Text(verbatim: shortAddress(p.txid, head: 10, tail: 8)).skyText(.bodyMedium, bold: true).foregroundStyle(Color.skyOnSurface)
                    Text(verbatim: "\(model.coin.amountText(p.coins)) \(model.coin.ticker) · \(p.received.prefix(19).replacingOccurrences(of: "T", with: " "))")
                        .skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .accessibilityElement(children: .combine)
                .accessibilityIdentifier("wallet-pending-\(p.txid.prefix(8))")
                // Only the wallet's own: on a shared node, removing someone else's would be acting for them.
                if p.mine {
                    Button { confirmRemove(p.txid) } label: {
                        MaterialIcon(MI.outlinedDeleteOutline, size: 22).foregroundStyle(Color.skyError).frame(width: 44, height: 44)
                    }
                    .buttonStyle(PressStyle(layer: .skyOnSurface, shape: AnyShape(Circle())))
                    .accessibilityLabel(Text("wallet_pending_delete"))
                    .accessibilityIdentifier("wallet-pending-delete-\(p.txid.prefix(8))")
                }
            }
            .padding(.leading, 16)
            .padding(.trailing, 4)
            .padding(.vertical, 6)
            .frame(minHeight: 56)
            .background(Color.skySurfaceVariant, in: .sky(14))
            .padding(.top, 12)
        }
    }

    @ViewBuilder private var outputsPage: some View {
        let outputs = model.send.utxos
        if !loading && outputs.isEmpty { empty(L10n.key("wallet_outputs_none_wallet")) }
        ForEach(Self.byAddress(outputs), id: \.address) { group in
            Text(verbatim: shortAddress(group.address)).skyText(.labelLarge).foregroundStyle(Color.skyOnSurfaceVariant)
                .padding(.top, 18).padding(.bottom, 4)
            InfoCard(rows: group.outputs.map {
                (shortAddress($0.uxid, head: 8, tail: 6), "\(model.coin.amountText($0.coins)) \(model.coin.ticker) · \(hoursText($0.hours))")
            })
        }
    }

    private func empty(_ text: LocalizedStringKey) -> some View {
        Text(text).skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 24)
    }

    private func confirmRemove(_ txid: String) {
        dialogs.showCustom {
            TextConfirmDialog(message: L10n.key("wallet_pending_delete_confirm"), confirm: L10n.key("wallet_pending_delete"),
                              destructive: true, identifier: "wallet-pending-delete-confirm") {
                Task {
                    do {
                        try await model.deletePending(txid)
                        dialogs.snackbar(Text("wallet_pending_removed"))
                        reload += 1
                    } catch {
                        dialogs.snackbar(Text(verbatim: WalletModel.report(error)))
                    }
                }
            }
        }
    }

    /// The wallet's outputs by address, in the order the addresses first appear.
    private static func byAddress(_ outputs: [UtxoRecord]) -> [(address: String, outputs: [UtxoRecord])] {
        var order: [String] = []
        var groups: [String: [UtxoRecord]] = [:]
        for o in outputs {
            if groups[o.address] == nil { order.append(o.address) }
            groups[o.address, default: []].append(o)
        }
        return order.map { ($0, groups[$0] ?? []) }
    }

    /// A supply as the node writes it, "24239050.000000", grouped and without the empty decimals.
    static func supply(_ text: String) -> String {
        let parts = text.split(separator: ".", maxSplits: 1, omittingEmptySubsequences: false)
        let whole = String(parts.first ?? "")
        var frac = parts.count > 1 ? String(parts[1]) : ""
        while frac.hasSuffix("0") { frac.removeLast() }
        return Amounts.groupThousands(whole) + (frac.isEmpty ? "" : ".\(frac)")
    }

    private static func dateTime(_ seconds: Int64) -> String {
        let formatter = DateFormatter()
        formatter.locale = .current
        formatter.dateStyle = .medium
        formatter.timeStyle = .short
        return formatter.string(from: Date(timeIntervalSince1970: TimeInterval(seconds)))
    }

    private static func relative(_ seconds: Int64) -> String {
        let formatter = RelativeDateTimeFormatter()
        formatter.locale = .current
        return formatter.localizedString(for: Date(timeIntervalSince1970: TimeInterval(seconds)), relativeTo: Date())
    }
}

/// Label and value rows on one card.
private struct InfoCard: View {
    let rows: [(String, String)]

    var body: some View {
        VStack(spacing: 0) {
            ForEach(Array(rows.enumerated()), id: \.offset) { _, row in
                HStack(alignment: .firstTextBaseline) {
                    Text(verbatim: row.0).skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    Text(verbatim: row.1).skyText(.bodyMedium, bold: true).foregroundStyle(Color.skyOnSurface).multilineTextAlignment(.trailing)
                }
                .padding(.vertical, 10)
                .accessibilityElement(children: .combine)
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 4)
        .background(Color.skySurfaceVariant, in: .sky(14))
        .padding(.top, 12)
    }
}
