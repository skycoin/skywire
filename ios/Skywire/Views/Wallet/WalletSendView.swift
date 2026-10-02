import CoreImage
import PhotosUI
import SwiftUI
import UIKit
import VisionKit
import WalletCore

/// Send (Android: ui/wallet/WalletSend.kt): recipient, amount, the per-chain
/// fee card, then review → the device-owner check → sign on the phone →
/// broadcast.
struct WalletSendView: View {
    @EnvironmentObject private var model: WalletModel
    @State private var reviewSheet = false
    @State private var scanning = false
    @State private var photo: PhotosPickerItem?

    var body: some View {
        Form {
            Section {
                HStack {
                    TextField(toHint, text: Binding(
                        get: { model.send.to },
                        set: { model.send.to = $0; model.send.planError = nil }
                    ))
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    // An address reads best monospaced; the hint does not.
                    .font(model.send.to.isEmpty ? .body : .body.monospaced())
                    .accessibilityIdentifier("wallet-send-to")
                    Button("wallet_paste") {
                        if let pasted = UIPasteboard.general.string?.trimmingCharacters(in: .whitespacesAndNewlines) {
                            model.send.to = pasted
                            model.send.planError = nil
                        }
                    }
                    .buttonStyle(.borderless)
                    .font(.body.weight(.semibold))
                }
                // The camera where the phone can scan (VisionKit's scanner:
                // never on the Simulator); a photo of a code everywhere.
                if Self.cameraScanAvailable {
                    Button { scanning = true } label: { Label("wallet_scan_description", systemImage: "qrcode.viewfinder") }
                }
                PhotosPicker(selection: $photo, matching: .images) {
                    Label("wallet_scan_photo", systemImage: "photo")
                }
                .accessibilityIdentifier("wallet-send-scan-photo")
            } header: {
                Text("wallet_send_to")
            }

            Section {
                HStack(spacing: 10) {
                    TextField("0", text: Binding(
                        get: { model.send.amountText },
                        set: { model.send.amountText = $0; model.send.sendMax = false; model.send.planError = nil }
                    ))
                    .keyboardType(.decimalPad)
                    .font(.title2.weight(.bold))
                    .accessibilityIdentifier("wallet-send-amount")
                    Text(model.coin.ticker).foregroundStyle(.secondary)
                    Button("wallet_max") {
                        let max = model.sendMaxAmount()
                        model.send.amountText = Amounts.format(max, exponent: model.coin.exponent, minDecimals: model.coin.displayDecimals)
                            .replacingOccurrences(of: ",", with: "")
                        model.send.sendMax = true
                        model.send.planError = nil
                    }
                    .buttonStyle(.borderless)
                    .font(.body.weight(.semibold))
                    .accessibilityIdentifier("wallet-send-max")
                }
            } header: {
                HStack {
                    Text("wallet_send_amount")
                    Spacer()
                    Text(L10n.format("wallet_send_available", model.coin.amountText(model.snapshot?.confirmed ?? 0), model.coin.ticker))
                        .textCase(nil)
                }
            } footer: {
                Text(maxNote)
            }

            Section {
                switch model.coin.kind {
                case .btc: BtcFeeCard()
                case .eth, .erc20: EthFeeCard()
                case .skyFiber: FiberFeeCard()
                }
            } header: {
                Text("wallet_fee")
            }

            Section {
                if let error = model.send.planError {
                    Text(error).font(.footnote).foregroundStyle(.red).accessibilityIdentifier("wallet-send-error")
                }
                Button {
                    model.buildPlan { reviewSheet = true }
                } label: {
                    HStack {
                        Spacer()
                        if model.send.planning { ProgressView().controlSize(.small) }
                        Text("wallet_review").font(.body.weight(.semibold))
                        Spacer()
                    }
                }
                .disabled(model.send.planning || model.stale)
                .accessibilityIdentifier("wallet-send-review")
            }
        }
        .navigationTitle(Text("wallet_send_title"))
        .navigationBarTitleDisplayMode(.inline)
        .task { await model.loadFeePresets() }
        .onChange(of: model.active?.id) { id in
            if id == nil, model.path.last == .send { model.path.removeLast() }
        }
        .onChange(of: photo) { item in
            guard let item else { return }
            Task {
                let data = try? await item.loadTransferable(type: Data.self)
                photo = nil
                let found = data.flatMap { CIImage(data: $0) }.map(QrDecoder.decode) ?? ""
                if found.isEmpty {
                    model.message = L10n.text("wallet_scan_none")
                } else {
                    take(found)
                }
            }
        }
        .sheet(isPresented: $scanning) {
            QRScannerSheet { code in
                scanning = false
                take(code)
            }
        }
        .sheet(isPresented: $reviewSheet) {
            // The check runs over the sheet, which closes once it has passed:
            // a refusal leaves the plan on screen to sign or go back from.
            ReviewSheet(onConfirm: {
                Task {
                    guard await WalletAuth.confirm(reason: L10n.text("wallet_bio_send_title")) else { return }
                    reviewSheet = false
                    model.signAndSend { model.path.append(.result) }
                }
            })
            .presentationDetents([.medium, .large])
            .environmentObject(model)
        }
    }

    /// A scanned code as the recipient: plain addresses and bitcoin:/skycoin:
    /// style URIs — the part after the scheme, before any query (and before
    /// an EIP-681 "@chain", which no address contains).
    private func take(_ raw: String) {
        var address = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        if let colon = address.lastIndex(of: ":") { address = String(address[address.index(after: colon)...]) }
        if let query = address.firstIndex(of: "?") { address = String(address[..<query]) }
        if let at = address.firstIndex(of: "@") { address = String(address[..<at]) }
        model.send.to = address
        model.send.planError = nil
    }

    static var cameraScanAvailable: Bool {
        DataScannerViewController.isSupported && DataScannerViewController.isAvailable
    }

    private var toHint: String {
        switch model.coin.kind {
        case .btc: L10n.text("wallet_send_to_hint_btc")
        case .eth, .erc20: L10n.text("wallet_send_to_hint_eth")
        case .skyFiber: L10n.format("wallet_send_to_hint_fiber", model.coin.name)
        }
    }

    private var maxNote: String {
        switch model.coin.kind {
        case .btc: L10n.text("wallet_max_note_btc")
        case .eth: L10n.text("wallet_max_note_eth")
        case .erc20: L10n.text("wallet_max_note_erc20")
        case .skyFiber: L10n.format("wallet_max_note_fiber", model.coin.ticker)
        }
    }
}

/// The Coin Hours card: what burns now, what remains after.
private struct FiberFeeCard: View {
    @EnvironmentObject private var model: WalletModel

    var body: some View {
        let hours = model.snapshot?.hours ?? 0
        // Before a plan exists the burn is a projection off the whole balance
        // — a tenth of held hours — replaced by exact numbers once planned.
        let burned = model.send.plan?.fee ?? (hours + 9) / 10
        let after = hours >= burned ? hours - burned : 0
        InfoRow(label: Text("wallet_hours_burned"), value: hoursText(burned))
        InfoRow(label: Text("wallet_hours_after"), value: hoursText(after), valueColor: .secondary)
        Text(L10n.format("wallet_fee_note_fiber", model.coin.ticker)).font(.footnote).foregroundStyle(.secondary)
    }
}

/// The gas card: EIP-1559 prices itself, so this shows rather than asks —
/// the worst-case fee once a plan exists, and where that money comes from
/// on a token send.
private struct EthFeeCard: View {
    @EnvironmentObject private var model: WalletModel

    var body: some View {
        let plan = model.send.plan
        InfoRow(
            label: Text("wallet_fee_network"),
            value: plan.map { "\(Amounts.format($0.fee, exponent: 9, minDecimals: 6)) ETH" } ?? L10n.text("wallet_fee_at_review")
        )
        if let plan, let gas = plan.vsize {
            Text(L10n.format("wallet_fee_gas", gas, plan.feeRate ?? 0)).font(.subheadline).foregroundStyle(.secondary)
        }
        Text(model.coin.kind == .erc20 ? L10n.key("wallet_fee_note_erc20") : L10n.key("wallet_fee_note_eth"))
            .font(.footnote)
            .foregroundStyle(.secondary)
    }
}

/// The sat/vB card: presets, slider, estimated fee.
private struct BtcFeeCard: View {
    @EnvironmentObject private var model: WalletModel

    var body: some View {
        if let presets = model.send.presets {
            HStack(spacing: 8) {
                preset(L10n.key("wallet_fee_economy"), L10n.key("wallet_fee_eta_economy"), presets.economy)
                preset(L10n.key("wallet_fee_normal"), L10n.key("wallet_fee_eta_normal"), presets.normal)
                preset(L10n.key("wallet_fee_priority"), L10n.key("wallet_fee_eta_priority"), presets.priority)
            }
            .buttonStyle(.plain)
        }
        HStack {
            Text("wallet_fee_rate")
            Spacer()
            Text(L10n.format("wallet_fee_rate_value", model.send.feeRate)).font(.body.weight(.semibold))
        }
        Slider(
            value: Binding(
                get: { Double(model.send.feeRate) },
                set: { model.send.feeRate = max(1, Int($0)); model.send.plan = nil }
            ),
            in: 1...60,
            step: 1
        )
        .accessibilityIdentifier("wallet-fee-slider")
        if let plan = model.send.plan, let vsize = plan.vsize {
            HStack {
                Text(L10n.format("wallet_fee_estimate", vsize)).foregroundStyle(.secondary)
                Spacer()
                Text("\(Amounts.format(plan.fee, exponent: 8, minDecimals: 8)) BTC").foregroundStyle(.secondary)
            }
            .font(.subheadline)
        }
    }

    private func preset(_ name: LocalizedStringKey, _ eta: LocalizedStringKey, _ rate: Int) -> some View {
        let selected = rate == model.send.feeRate
        return Button {
            model.send.feeRate = rate
            model.send.plan = nil
        } label: {
            VStack(spacing: 2) {
                Text(name).font(.subheadline.weight(.semibold)).foregroundStyle(selected ? Color.skywire : .secondary)
                Text(eta).font(.caption2).foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 10)
            .background(RoundedRectangle(cornerRadius: 11).fill(selected ? Color.skywire.opacity(0.15) : Color(.tertiarySystemFill)))
        }
    }
}

/// What is about to be signed, in full, before the device-owner check.
private struct ReviewSheet: View {
    @EnvironmentObject private var model: WalletModel
    @Environment(\.dismiss) private var dismiss
    let onConfirm: () -> Void

    var body: some View {
        if let plan = model.send.plan {
            let coin = model.coin
            let confirmed = model.snapshot?.confirmed ?? 0
            NavigationStack {
                List {
                    Section {
                        VStack(alignment: .leading, spacing: 8) {
                            Text("\(coin.amountText(plan.amount)) \(coin.ticker)").font(.title.weight(.bold))
                            // The network is the coin's own chain — Skycoin,
                            // this fiber coin's, Bitcoin or Ethereum.
                            Text(L10n.format("wallet_review_dest", shortAddress(plan.toAddress), coin.name))
                                .font(.subheadline)
                                .foregroundStyle(.secondary)
                        }
                        .listRowBackground(Color.clear)
                    }
                    Section {
                        switch coin.kind {
                        case .btc:
                            let total = plan.amount &+ plan.fee
                            InfoRow(label: Text("wallet_review_amount"), value: "\(coin.amountText(plan.amount)) BTC")
                            InfoRow(label: Text(L10n.format("wallet_review_miner_fee", plan.feeRate ?? 0)),
                                    value: "\(Amounts.format(plan.fee, exponent: 8, minDecimals: 8)) BTC")
                            InfoRow(label: Text("wallet_review_total"), value: "\(Amounts.format(total, exponent: 8, minDecimals: 8)) BTC")
                            InfoRow(label: Text(L10n.format("wallet_review_balance_after", "BTC")),
                                    value: Amounts.format(confirmed >= total ? confirmed - total : 0, exponent: 8, minDecimals: 8))
                        case .eth:
                            let total = plan.amount &+ plan.fee
                            InfoRow(label: Text("wallet_review_amount"), value: "\(coin.amountText(plan.amount)) ETH")
                            // The worst case, not a quote: unspent gas is never charged.
                            InfoRow(label: Text("wallet_review_network_fee"), value: "≤ \(Amounts.format(plan.fee, exponent: 9, minDecimals: 6)) ETH")
                            InfoRow(label: Text("wallet_review_total"), value: "≤ \(Amounts.format(total, exponent: 9, minDecimals: 6)) ETH")
                            InfoRow(label: Text(L10n.format("wallet_review_balance_after", "ETH")),
                                    value: coin.amountText(confirmed >= total ? confirmed - total : 0))
                        case .erc20:
                            // Amount and fee live in different currencies: no
                            // total row to add them into.
                            InfoRow(label: Text("wallet_review_amount"), value: "\(coin.amountText(plan.amount)) \(coin.ticker)")
                            InfoRow(label: Text("wallet_review_network_fee"), value: "≤ \(Amounts.format(plan.fee, exponent: 9, minDecimals: 6)) ETH")
                            InfoRow(label: Text(L10n.format("wallet_review_balance_after", coin.ticker)),
                                    value: coin.amountText(confirmed >= plan.amount ? confirmed - plan.amount : 0))
                        case .skyFiber:
                            let hoursNow = model.snapshot?.hours ?? 0
                            let spent = plan.fee &+ (plan.hoursToRecipient ?? 0)
                            InfoRow(label: Text("wallet_review_amount"), value: "\(coin.amountText(plan.amount)) \(coin.ticker)")
                            InfoRow(label: Text("wallet_hours_burned"), value: hoursText(plan.fee))
                            InfoRow(label: Text(L10n.format("wallet_review_balance_after", coin.ticker)),
                                    value: coin.amountText(confirmed >= plan.amount ? confirmed - plan.amount : 0))
                            InfoRow(label: Text("wallet_review_hours_after"), value: hoursText(hoursNow - min(hoursNow, spent)))
                        }
                    }
                    Section {
                        Button(action: onConfirm) {
                            HStack {
                                Spacer()
                                if model.send.sending { ProgressView().controlSize(.small) }
                                Text("wallet_review_sign").font(.body.weight(.semibold))
                                Spacer()
                            }
                        }
                        .disabled(model.send.sending)
                        .accessibilityIdentifier("wallet-review-sign")
                        Button { dismiss() } label: {
                            Text("wallet_review_back").frame(maxWidth: .infinity)
                        }
                    }
                }
                .navigationTitle(Text("wallet_review_title"))
                .navigationBarTitleDisplayMode(.inline)
            }
        }
    }
}

/// The broadcast confirmation — a full screen, not a toast.
struct WalletResultView: View {
    @EnvironmentObject private var model: WalletModel
    @State private var copied = false

    var body: some View {
        ScrollView {
            VStack(spacing: 14) {
                Image(systemName: "checkmark.circle.fill")
                    .font(.system(size: 56))
                    .foregroundStyle(Color.success)
                    .padding(.top, 40)
                Text("wallet_result_title").font(.title2.weight(.bold)).multilineTextAlignment(.center)
                Text(L10n.format("wallet_result_body", model.coin.amountText(model.send.sentAmount), model.coin.ticker,
                                 shortAddress(model.send.sentTo, head: 6, tail: 4)))
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                if let txid = model.send.sentTxid {
                    Button {
                        UIPasteboard.general.string = txid
                        copied = true
                    } label: {
                        HStack(spacing: 9) {
                            Text(shortAddress(txid, head: 10, tail: 8)).font(.body.weight(.semibold).monospaced())
                            Image(systemName: "doc.on.doc").font(.footnote).foregroundStyle(.secondary)
                        }
                        .padding(.horizontal, 16).padding(.vertical, 11)
                        .background(Capsule().fill(Color(.secondarySystemBackground)))
                    }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("wallet-result-txid")
                    .accessibilityValue(Text(verbatim: txid))
                }
                Button {
                    model.resetSend()
                    model.path.removeAll()
                } label: {
                    Text("wallet_result_done").font(.body.weight(.semibold)).frame(maxWidth: .infinity, minHeight: 36)
                }
                .buttonStyle(.borderedProminent)
                .tint(.skywire)
                .padding(.top, 24)
                .accessibilityIdentifier("wallet-result-done")
                Button("wallet_result_history") {
                    model.resetSend()
                    model.path = [.history]
                }
            }
            .padding(24)
        }
        .navigationBarBackButtonHidden()
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

/// VisionKit's live scanner, QR codes only, the first one read wins.
private struct QRScannerSheet: UIViewControllerRepresentable {
    let onFound: (String) -> Void

    func makeUIViewController(context: Context) -> DataScannerViewController {
        let scanner = DataScannerViewController(
            recognizedDataTypes: [.barcode(symbologies: [.qr])],
            qualityLevel: .balanced,
            recognizesMultipleItems: false,
            isHighFrameRateTrackingEnabled: false,
            isHighlightingEnabled: true
        )
        scanner.delegate = context.coordinator
        return scanner
    }

    func updateUIViewController(_ scanner: DataScannerViewController, context: Context) {
        if !scanner.isScanning { try? scanner.startScanning() }
    }

    func makeCoordinator() -> Coordinator { Coordinator(onFound: onFound) }

    @MainActor
    final class Coordinator: NSObject, DataScannerViewControllerDelegate {
        let onFound: (String) -> Void
        private var done = false

        init(onFound: @escaping (String) -> Void) { self.onFound = onFound }

        func dataScanner(_ dataScanner: DataScannerViewController, didAdd addedItems: [RecognizedItem], allItems: [RecognizedItem]) {
            guard !done else { return }
            for item in addedItems {
                if case .barcode(let code) = item, let text = code.payloadStringValue, !text.isEmpty {
                    done = true
                    dataScanner.stopScanning()
                    onFound(text)
                    return
                }
            }
        }
    }
}
