import CoreImage
import PhotosUI
import SwiftUI
import UIKit
import VisionKit
import WalletCore

/// Send (Android: ui/wallet/WalletSend.kt): recipient, amount, the per-chain fee card, then
/// review → the device-owner check → sign on the phone → broadcast.
struct WalletSendView: View {
    @EnvironmentObject private var model: WalletModel
    @EnvironmentObject private var dialogs: SkyDialogs
    @State private var scanning = false
    @State private var picking = false
    @State private var photo: PhotosPickerItem?

    var body: some View {
        WalletScreen(title: Text("wallet_send_title")) {
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    SendLabel(text: L10n.text("wallet_send_to"))
                    HStack(spacing: 0) {
                        BareField(placeholder: toHint, text: Binding(
                            get: { model.send.to },
                            set: { model.send.to = $0; model.send.planError = nil }
                        ), identifier: "wallet-send-to")
                        Button {
                            if let pasted = UIPasteboard.general.string?.trimmingCharacters(in: .whitespacesAndNewlines) {
                                model.send.to = pasted
                                model.send.planError = nil
                            }
                        } label: { Text("wallet_paste") }
                        .buttonStyle(.skyText)
                        scanButton
                    }
                    .padding(.leading, 4)
                    .padding(.trailing, 6)
                    .padding(.vertical, 4)
                    .background(Color.skySurfaceVariant, in: .sky(14))
                    .padding(.top, 9)

                    HStack {
                        SendLabel(text: L10n.text("wallet_send_amount"))
                        Spacer()
                        Text(verbatim: L10n.format("wallet_send_available", model.coin.amountText(model.snapshot?.confirmed ?? 0), model.coin.ticker))
                            .skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                    }
                    .padding(.top, 26)
                    HStack(spacing: 10) {
                        BareField(placeholder: "0", text: Binding(
                            get: { model.send.amountText },
                            set: { model.send.amountText = $0; model.send.sendMax = false; model.send.planError = nil }
                        ), style: .headlineSmall, keyboard: .decimalPad, identifier: "wallet-send-amount")
                        Text(verbatim: model.coin.ticker).skyText(.titleMedium).foregroundStyle(Color.skyOnSurfaceVariant)
                        Button {
                            let max = model.sendMaxAmount()
                            model.send.amountText = Amounts.format(max, exponent: model.coin.exponent, minDecimals: model.coin.displayDecimals)
                                .replacingOccurrences(of: ",", with: "")
                            model.send.sendMax = true
                            model.send.planError = nil
                        } label: { Text("wallet_max") }
                        .buttonStyle(.skyText)
                        .accessibilityIdentifier("wallet-send-max")
                    }
                    .padding(.leading, 4)
                    .padding(.trailing, 12)
                    .background(Color.skySurfaceVariant, in: .sky(14))
                    .padding(.top, 9)
                    Text(verbatim: maxNote).skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 8)

                    SendLabel(text: L10n.text("wallet_fee")).padding(.top, 26)
                    Group {
                        switch model.coin.kind {
                        case .btc: BtcFeeCard()
                        case .eth, .erc20: EthFeeCard()
                        case .skyFiber: FiberFeeCard()
                        }
                    }
                    .padding(16)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(Color.skySurfaceVariant, in: .sky(14))
                    .padding(.top, 9)

                    if let error = model.send.planError {
                        Text(verbatim: error).skyText(.bodySmall).foregroundStyle(Color.skyError).padding(.top, 12)
                            .accessibilityIdentifier("wallet-send-error")
                    }
                    Button { model.buildPlan { openReview() } } label: {
                        HStack(spacing: 10) {
                            if model.send.planning { MaterialSpinner(size: 16, stroke: 2, color: .skyOnSurface.opacity(0.38)) }
                            Text("wallet_review")
                        }
                        .frame(maxWidth: .infinity)
                    }
                    .buttonStyle(FilledButtonStyle(height: 52))
                    .disabled(model.send.planning || model.stale)
                    .padding(.top, 26)
                    .padding(.bottom, 24)
                    .accessibilityIdentifier("wallet-send-review")
                }
                .padding(.horizontal, 20)
            }
        }
        .task { await model.loadFeePresets() }
        .onChange(of: model.active?.id) { id in
            if id == nil, model.path.last == .send { model.path.removeLast() }
        }
        .photosPicker(isPresented: $picking, selection: $photo, matching: .images)
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
    }

    /// Android's scan icon. iOS adds a photo of a code; the camera only where VisionKit can scan.
    @ViewBuilder
    private var scanButton: some View {
        let icon = MaterialIcon(MI.outlinedQrCodeScanner, size: 20).foregroundStyle(Color.skyOnSurfaceVariant)
            .frame(width: 40, height: 40).contentShape(Circle())
        if Self.cameraScanAvailable {
            Menu {
                Button { scanning = true } label: { Text("wallet_scan_description") }
                Button { picking = true } label: { Text("wallet_scan_photo") }
            } label: { icon }
            .frame(width: 48, height: 48)
            .accessibilityLabel(Text("wallet_scan_description"))
        } else {
            Button { picking = true } label: { icon }
                .buttonStyle(PressStyle(layer: .skyOnSurface, shape: AnyShape(Circle())))
                .frame(width: 48, height: 48)
                .accessibilityLabel(Text("wallet_scan_photo"))
                .accessibilityIdentifier("wallet-send-scan-photo")
        }
    }

    private func openReview() {
        dialogs.presentSheet {
            // The check runs over the sheet, which closes once it has passed: a refusal leaves
            // the plan on screen to sign or go back from.
            ReviewSheet(onConfirm: {
                Task {
                    guard await WalletAuth.confirm(reason: L10n.text("wallet_bio_send_title")) else { return }
                    dialogs.dismissSheet()
                    model.signAndSend { model.path.append(.result) }
                }
            })
            .environmentObject(model)
        }
    }

    /// A scanned code as the recipient: plain addresses and bitcoin:/skycoin: style URIs, the part
    /// after the scheme and before any query (and before an EIP-681 "@chain").
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

/// The send form's section label: labelSmall, upper case.
private struct SendLabel: View {
    let text: String

    var body: some View {
        Text(verbatim: text.uppercased()).skyText(.labelSmall).foregroundStyle(Color.skyOnSurfaceVariant)
    }
}

/// A TextField with no container or indicator (Android's transparentFieldColors): 56 pt, 16 pt in.
private struct BareField: View {
    let placeholder: String
    @Binding var text: String
    var style: SkyTextStyle = .bodyLarge
    var keyboard: UIKeyboardType = .default
    let identifier: String

    var body: some View {
        ZStack(alignment: .leading) {
            if text.isEmpty {
                Text(verbatim: placeholder).skyText(.bodyLarge).foregroundStyle(Color.skyOnSurfaceVariant)
                    .lineLimit(1).allowsHitTesting(false)
            }
            TextField(text: $text, prompt: Text(verbatim: "")) { Text(verbatim: placeholder) }
                .font(Font(style.uiFont(bold: style == .headlineSmall)))
                .foregroundStyle(Color.skyOnSurface)
                .tint(.skyPrimary)
                .keyboardType(keyboard)
                .autocorrectionDisabled()
                .textInputAutocapitalization(.never)
                .accessibilityIdentifier(identifier)
        }
        .padding(.horizontal, 16)
        .frame(maxWidth: .infinity, minHeight: 56)
    }
}

/// A fee card's line: the label, then the value in bold.
private struct FeeLine: View {
    let label: String
    let value: String

    var body: some View {
        HStack {
            Text(verbatim: label).skyText(.bodyLarge)
            Spacer()
            Text(verbatim: value).skyText(.bodyLarge, bold: true)
        }
    }
}

/// A quieter line under a fee card's divider.
private struct FeeNote: View {
    let label: String
    var value: String? = nil

    var body: some View {
        HStack {
            Text(verbatim: label)
            Spacer()
            if let value { Text(verbatim: value) }
        }
        .skyText(.bodyMedium)
        .foregroundStyle(Color.skyOnSurfaceVariant)
    }
}

/// The Coin Hours card: what burns now, what remains after.
private struct FiberFeeCard: View {
    @EnvironmentObject private var model: WalletModel

    var body: some View {
        let hours = model.snapshot?.hours ?? 0
        // Before a plan exists the burn is a projection off the whole balance, a tenth of held
        // hours, replaced by exact numbers once planned.
        let burned = model.send.plan?.fee ?? (hours + 9) / 10
        let after = hours >= burned ? hours - burned : 0
        VStack(alignment: .leading, spacing: 0) {
            FeeLine(label: L10n.text("wallet_hours_burned"), value: hoursText(burned))
            SkyDivider(color: .skyContainerHighest).padding(.vertical, 13)
            FeeNote(label: L10n.text("wallet_hours_after"), value: hoursText(after))
            Text(verbatim: L10n.format("wallet_fee_note_fiber", model.coin.ticker))
                .skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 12)
        }
    }
}

/// The gas card: EIP-1559 prices itself, so this shows rather than asks: the worst-case fee once
/// a plan exists, and where that money comes from on a token send.
private struct EthFeeCard: View {
    @EnvironmentObject private var model: WalletModel

    var body: some View {
        let plan = model.send.plan
        VStack(alignment: .leading, spacing: 0) {
            FeeLine(label: L10n.text("wallet_fee_network"),
                    value: plan.map { "\(Amounts.format($0.fee, exponent: 9, minDecimals: 6)) ETH" } ?? L10n.text("wallet_fee_at_review"))
            if let plan, let gas = plan.vsize {
                SkyDivider(color: .skyContainerHighest).padding(.vertical, 13)
                FeeNote(label: L10n.format("wallet_fee_gas", gas, plan.feeRate ?? 0))
            }
            Text(model.coin.kind == .erc20 ? L10n.key("wallet_fee_note_erc20") : L10n.key("wallet_fee_note_eth"))
                .skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 12)
        }
    }
}

/// The sat/vB card: presets, slider, estimated fee.
private struct BtcFeeCard: View {
    @EnvironmentObject private var model: WalletModel

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            if let presets = model.send.presets {
                HStack(spacing: 8) {
                    preset(L10n.key("wallet_fee_economy"), L10n.key("wallet_fee_eta_economy"), presets.economy)
                    preset(L10n.key("wallet_fee_normal"), L10n.key("wallet_fee_eta_normal"), presets.normal)
                    preset(L10n.key("wallet_fee_priority"), L10n.key("wallet_fee_eta_priority"), presets.priority)
                }
                .padding(.bottom, 18)
            }
            FeeLine(label: L10n.text("wallet_fee_rate"), value: L10n.format("wallet_fee_rate_value", model.send.feeRate))
            SkySlider(value: Binding(
                get: { Double(model.send.feeRate) },
                set: { model.send.feeRate = max(1, Int($0)); model.send.plan = nil }
            ), range: 1...60)
            .padding(.top, 4)
            .accessibilityIdentifier("wallet-fee-slider")
            if let plan = model.send.plan, let vsize = plan.vsize {
                SkyDivider(color: .skyContainerHighest).padding(.vertical, 13)
                FeeNote(label: L10n.format("wallet_fee_estimate", vsize), value: "\(Amounts.format(plan.fee, exponent: 8, minDecimals: 8)) BTC")
            }
        }
    }

    private func preset(_ name: LocalizedStringKey, _ eta: LocalizedStringKey, _ rate: Int) -> some View {
        let selected = rate == model.send.feeRate
        return Button {
            model.send.feeRate = rate
            model.send.plan = nil
        } label: {
            VStack(spacing: 2) {
                Text(name).skyText(.labelLarge).foregroundStyle(selected ? Color.skyPrimary : Color.skyOnSurfaceVariant)
                Text(eta).skyText(.labelSmall).foregroundStyle(Color.skyOnSurfaceVariant)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 11)
            .padding(.horizontal, 6)
            .background(selected ? Color.skySecondaryContainer : Color.skyContainerHighest, in: .sky(11))
        }
        .buttonStyle(PressStyle(layer: .skyOnSurface, shape: AnyShape(RoundedRectangle(cornerRadius: 11))))
    }
}

/// What is about to be signed, in full, before the device-owner check.
private struct ReviewSheet: View {
    @EnvironmentObject private var model: WalletModel
    @EnvironmentObject private var dialogs: SkyDialogs
    let onConfirm: () -> Void

    var body: some View {
        if let plan = model.send.plan {
            let coin = model.coin
            VStack(alignment: .leading, spacing: 0) {
                Text("wallet_review_title").skyText(.titleMedium)
                Text(verbatim: "\(coin.amountText(plan.amount)) \(coin.ticker)").skyText(.headlineMedium).padding(.top, 16)
                // The network is the coin's own chain: Skycoin, this fiber coin's, Bitcoin or Ethereum.
                Text(verbatim: L10n.format("wallet_review_dest", shortAddress(plan.toAddress), coin.name))
                    .skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 8)
                VStack(spacing: 0) {
                    ForEach(Array(rows(plan).enumerated()), id: \.offset) { index, row in
                        if index > 0 { SkyDivider(color: .skyContainerHighest) }
                        HStack {
                            Text(verbatim: row.0).skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant)
                            Spacer()
                            Text(verbatim: row.1).skyText(.bodyMedium, bold: true)
                        }
                        .padding(.vertical, 13)
                    }
                }
                .padding(.horizontal, 15)
                .padding(.vertical, 2)
                .background(Color.skySurfaceVariant, in: .sky(14))
                .padding(.top, 18)
                // Android's weights: 1 to 1.4.
                WeightedRow(weights: [1, 1.4], spacing: 12) {
                    Button { dialogs.dismissSheet() } label: { Text("wallet_review_back").frame(maxWidth: .infinity) }
                        .buttonStyle(TonalButtonStyle(height: 50))
                    Button(action: onConfirm) {
                        HStack(spacing: 10) {
                            if model.send.sending { MaterialSpinner(size: 16, stroke: 2, color: .skyOnSurface.opacity(0.38)) }
                            Text("wallet_review_sign")
                        }
                        .frame(maxWidth: .infinity)
                    }
                    .buttonStyle(FilledButtonStyle(height: 50))
                    .disabled(model.send.sending)
                    .accessibilityIdentifier("wallet-review-sign")
                }
                .padding(.top, 18)
            }
            .padding(.horizontal, 20)
            .padding(.bottom, 26)
        }
    }

    /// The plan's lines, per chain: label and value.
    private func rows(_ plan: TxPlan) -> [(String, String)] {
        let coin = model.coin
        let confirmed = model.snapshot?.confirmed ?? 0
        switch coin.kind {
        case .btc:
            let total = plan.amount &+ plan.fee
            return [
                (L10n.text("wallet_review_amount"), "\(coin.amountText(plan.amount)) BTC"),
                (L10n.format("wallet_review_miner_fee", plan.feeRate ?? 0), "\(Amounts.format(plan.fee, exponent: 8, minDecimals: 8)) BTC"),
                (L10n.text("wallet_review_total"), "\(Amounts.format(total, exponent: 8, minDecimals: 8)) BTC"),
                (L10n.format("wallet_review_balance_after", "BTC"), Amounts.format(confirmed >= total ? confirmed - total : 0, exponent: 8, minDecimals: 8)),
            ]
        case .eth:
            let total = plan.amount &+ plan.fee
            // The worst case, not a quote: unspent gas is never charged.
            return [
                (L10n.text("wallet_review_amount"), "\(coin.amountText(plan.amount)) ETH"),
                (L10n.text("wallet_review_network_fee"), "≤ \(Amounts.format(plan.fee, exponent: 9, minDecimals: 6)) ETH"),
                (L10n.text("wallet_review_total"), "≤ \(Amounts.format(total, exponent: 9, minDecimals: 6)) ETH"),
                (L10n.format("wallet_review_balance_after", "ETH"), coin.amountText(confirmed >= total ? confirmed - total : 0)),
            ]
        case .erc20:
            // Amount and fee live in different currencies: no total row to add them into.
            return [
                (L10n.text("wallet_review_amount"), "\(coin.amountText(plan.amount)) \(coin.ticker)"),
                (L10n.text("wallet_review_network_fee"), "≤ \(Amounts.format(plan.fee, exponent: 9, minDecimals: 6)) ETH"),
                (L10n.format("wallet_review_balance_after", coin.ticker), coin.amountText(confirmed >= plan.amount ? confirmed - plan.amount : 0)),
            ]
        case .skyFiber:
            let hoursNow = model.snapshot?.hours ?? 0
            let spent = plan.fee &+ (plan.hoursToRecipient ?? 0)
            return [
                (L10n.text("wallet_review_amount"), "\(coin.amountText(plan.amount)) \(coin.ticker)"),
                (L10n.text("wallet_hours_burned"), hoursText(plan.fee)),
                (L10n.format("wallet_review_balance_after", coin.ticker), coin.amountText(confirmed >= plan.amount ? confirmed - plan.amount : 0)),
                (L10n.text("wallet_review_hours_after"), hoursText(hoursNow - min(hoursNow, spent))),
            ]
        }
    }
}

/// The broadcast confirmation: a full screen, not a toast.
struct WalletResultView: View {
    @EnvironmentObject private var model: WalletModel
    @EnvironmentObject private var dialogs: SkyDialogs

    var body: some View {
        ScrollView {
            VStack(spacing: 0) {
                MaterialIcon(MI.outlinedCheckCircle, size: 32).foregroundStyle(Color.skySuccess)
                    .frame(width: 64, height: 64)
                    .background(Color.skySuccess.opacity(0.15), in: Circle())
                    .padding(.top, 60)
                Text("wallet_result_title").skyText(.headlineSmall).multilineTextAlignment(.center).padding(.top, 22)
                Text(verbatim: L10n.format("wallet_result_body", model.coin.amountText(model.send.sentAmount), model.coin.ticker,
                                           shortAddress(model.send.sentTo, head: 6, tail: 4)))
                    .skyText(.bodyLarge)
                    .foregroundStyle(Color.skyOnSurfaceVariant)
                    .multilineTextAlignment(.center)
                    .padding(.top, 10)
                if let txid = model.send.sentTxid {
                    Button {
                        UIPasteboard.general.string = txid
                        dialogs.snackbar(Text("wallet_txid_copied"))
                    } label: {
                        HStack(spacing: 9) {
                            Text(verbatim: shortAddress(txid, head: 10, tail: 8)).skyText(.bodyMedium, bold: true).foregroundStyle(Color.skyOnSurface)
                            MaterialIcon(MI.outlinedContentCopy, size: 15).foregroundStyle(Color.skyOnSurfaceVariant)
                        }
                        .padding(.horizontal, 16)
                        .padding(.vertical, 11)
                        .background(Color.skySurfaceVariant, in: .sky(22))
                    }
                    .buttonStyle(PressStyle(layer: .skyOnSurface, shape: AnyShape(Capsule())))
                    .padding(.top, 22)
                    .accessibilityIdentifier("wallet-result-txid")
                    .accessibilityValue(Text(verbatim: txid))
                }
                Button {
                    model.resetSend()
                    model.path.removeAll()
                } label: { Text("wallet_result_done").frame(maxWidth: .infinity) }
                .buttonStyle(FilledButtonStyle(height: 52))
                .padding(.top, 38)
                .accessibilityIdentifier("wallet-result-done")
                Button {
                    model.resetSend()
                    model.path = [.history]
                } label: { Text("wallet_result_history").frame(maxWidth: .infinity) }
                .buttonStyle(.skyText)
                .padding(.top, 6)
                .padding(.bottom, 24)
            }
            .padding(.horizontal, 24)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Color.skyBackground)
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
