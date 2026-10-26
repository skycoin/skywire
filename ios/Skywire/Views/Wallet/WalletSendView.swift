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
    @State private var advancedOpen = false

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

                    // One account per wallet on the ETH family: there is no second address a send
                    // could leave from.
                    if !model.coin.isEthFamily {
                        SendLabel(text: L10n.text("wallet_send_from")).padding(.top, 26)
                        PickRow(title: model.send.fromAddress.map { shortAddress($0) } ?? L10n.text("wallet_send_from_wallet"),
                                value: model.coin.amountText(model.sendAvailable), identifier: "wallet-send-from") {
                            dialogs.presentSheet { FromSheet().environmentObject(model) }
                        }
                    }
                    // Coin control, fiber only: which unspent outputs fund the send.
                    if model.coin.kind == .skyFiber {
                        SendLabel(text: L10n.text("wallet_send_coins")).padding(.top, 26)
                        PickRow(title: coinsTitle, value: nil, identifier: "wallet-send-coins") {
                            model.loadUtxos()
                            dialogs.presentSheet { UtxoSheet().environmentObject(model) }
                        }
                    }

                    HStack {
                        SendLabel(text: L10n.text("wallet_send_amount"))
                        Spacer()
                        Text(verbatim: L10n.format("wallet_send_available", model.coin.amountText(model.sendAvailable), model.coin.ticker))
                            .skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                    }
                    .padding(.top, 26)
                    HStack(spacing: 10) {
                        BareField(placeholder: "0", text: Binding(
                            get: { model.send.amountText },
                            set: { model.send.amountText = $0; model.send.sendMax = false; model.send.planError = nil }
                        ), style: .headlineSmall, keyboard: .decimalPad, identifier: "wallet-send-amount")
                        Text(verbatim: model.coin.ticker).skyText(.titleMedium).foregroundStyle(Color.skyOnSurfaceVariant)
                        if model.send.extra.isEmpty { maxButton }
                    }
                    .padding(.leading, 4)
                    .padding(.trailing, 12)
                    .background(Color.skySurfaceVariant, in: .sky(14))
                    .padding(.top, 9)
                    if model.send.extra.isEmpty {
                        Text(verbatim: maxNote).skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 8)
                    }

                    // More destinations in the same transaction, fiber only, as skycoin-web's
                    // advanced form.
                    if model.coin.kind == .skyFiber {
                        ForEach(Array(model.send.extra.indices), id: \.self) { index in
                            DestinationCard(index: index)
                        }
                        HStack(spacing: 8) {
                            Button { model.addDestination() } label: { Text("wallet_send_add_destination") }
                                .buttonStyle(.skyText)
                                .accessibilityIdentifier("wallet-send-add-destination")
                            Button { dialogs.showCustom { BulkSendDialog(initial: bulkText).environmentObject(model) } } label: {
                                Text("wallet_send_bulk")
                            }
                            .buttonStyle(.skyText)
                            .accessibilityIdentifier("wallet-send-bulk")
                        }
                        .padding(.top, 10)
                        AdvancedOptions(open: $advancedOpen) { advanced }
                    }


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

                    // Coin hours to the recipient: blank keeps the automatic split, a number sends
                    // exactly that many.
                    if model.coin.kind == .skyFiber {
                        SendLabel(text: L10n.text("wallet_send_hours")).padding(.top, 26)
                        BareField(placeholder: L10n.text("wallet_send_hours_hint"), text: Binding(
                            // Kept as typed: stripping turned "12.5" into 125, and the plan names
                            // anything that is not a whole number.
                            get: { model.send.hoursText },
                            set: { model.send.hoursText = String($0.prefix(18)); model.send.plan = nil; model.send.planError = nil }
                        ), keyboard: .numberPad, identifier: "wallet-send-hours")
                        .padding(.leading, 4)
                        .padding(.trailing, 12)
                        .padding(.vertical, 4)
                        .background(Color.skySurfaceVariant, in: .sky(14))
                        .padding(.top, 9)
                    }

                    if let error = model.send.planError {
                        Text(verbatim: error).skyText(.bodySmall).foregroundStyle(Color.skyError).padding(.top, 12)
                            .accessibilityIdentifier("wallet-send-error")
                    }
                    Button {
                        // The keyboard would squeeze the review sheet, which grows with each destination.
                        UIApplication.shared.sendAction(#selector(UIResponder.resignFirstResponder), to: nil, from: nil, for: nil)
                        model.buildPlan { openReview() }
                    } label: {
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
            // A long form: scrolling puts the keyboard away rather than leaving it over the
            // fields further down.
            .scrollDismissesKeyboard(.immediately)
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
            ReviewSheet(onUnsigned: {
                guard let hex = model.unsignedHex() else { return }
                dialogs.dismissSheet()
                dialogs.showCustom {
                    RawTxDialog(title: Text("wallet_offline_unsigned_title"), message: Text("wallet_offline_unsigned_body"), hex: hex)
                }
            }, onConfirm: {
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

    /// The fiber send's less common choices: where change goes and the hours share.
    @ViewBuilder private var advanced: some View {
        SendLabel(text: L10n.text("wallet_send_change"))
        HStack(spacing: 0) {
            BareField(placeholder: L10n.text("wallet_send_change_auto"), text: Binding(
                get: { model.send.changeAddress },
                set: { model.send.changeAddress = $0.trimmingCharacters(in: .whitespaces); model.send.plan = nil; model.send.planError = nil }
            ), identifier: "wallet-send-change")
            Button { dialogs.presentSheet { ChangeSheet().environmentObject(model) } } label: { Text("wallet_send_change_select") }
                .buttonStyle(.skyText)
                .accessibilityIdentifier("wallet-send-change-select")
        }
        .padding(.leading, 4)
        .padding(.trailing, 6)
        .background(Color.skyContainerHighest, in: .sky(14))
        .padding(.top, 9)
        Text("wallet_send_change_note").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 8)
        // Automatic hours only: hours typed by hand say exactly what each destination gets.
        let manualHours = !model.send.hoursText.trimmingCharacters(in: .whitespaces).isEmpty
        SendLabel(text: L10n.text("wallet_send_share")).padding(.top, 20)
        HStack(spacing: 12) {
            SkySlider(value: Binding(
                get: { Double(model.send.sharePercent) },
                set: { model.send.sharePercent = min(100, max(10, Int($0.rounded()))); model.send.plan = nil }
            ), range: 10...100)
            .disabled(manualHours)
            .opacity(manualHours ? 0.38 : 1)
            .accessibilityLabel(Text("wallet_send_share"))
            .accessibilityIdentifier("wallet-send-share")
            Text(verbatim: L10n.format("wallet_send_share_value", model.send.sharePercent))
                .skyText(.bodyLarge, bold: true)
                .frame(width: 52, alignment: .trailing)
        }
        Text(manualHours ? L10n.key("wallet_send_share_manual") : L10n.key("wallet_send_share_note"))
            .skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
        // Offline signing, as skycoin-web: sign here what another device built, or send what it signed.
        HStack(spacing: 4) {
            Button { dialogs.showCustom { OfflineTxDialog(mode: .sign).environmentObject(model) } } label: { Text("wallet_offline_sign_title") }
                .buttonStyle(.skyText)
                .accessibilityIdentifier("wallet-offline-sign")
            Button { dialogs.showCustom { OfflineTxDialog(mode: .broadcast).environmentObject(model) } } label: { Text("wallet_offline_broadcast_title") }
                .buttonStyle(.skyText)
                .accessibilityIdentifier("wallet-offline-broadcast")
        }
        .padding(.top, 14)
    }

    private var maxButton: some View {
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

    /// The destinations as bulk text, so the dialog opens on what is already typed.
    private var bulkText: String {
        let send = model.send
        return ([DestDraft(address: send.to, amountText: send.amountText, hoursText: send.hoursText)] + send.extra)
            .filter { !$0.address.trimmingCharacters(in: .whitespaces).isEmpty || !$0.amountText.trimmingCharacters(in: .whitespaces).isEmpty }
            .map { [$0.address, $0.amountText, $0.hoursText].filter { !$0.trimmingCharacters(in: .whitespaces).isEmpty }.joined(separator: ", ") }
            .joined(separator: "\n")
    }

    private var coinsTitle: String {
        let send = model.send
        guard !send.inputs.isEmpty else { return L10n.text("wallet_send_coins_auto") }
        let picked = send.utxos.filter { send.inputs.contains($0.uxid) }.reduce(UInt64(0)) { $0 &+ $1.coins }
        return L10n.format("wallet_send_coins_picked", send.inputs.count, send.utxos.count, model.coin.amountText(picked))
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

/// The fiber send's less common choices, folded away until asked for.
private struct AdvancedOptions<Content: View>: View {
    @Binding var open: Bool
    @ViewBuilder let content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Button { open.toggle() } label: {
                HStack {
                    Text("wallet_send_advanced").skyText(.bodyLarge, bold: true).foregroundStyle(Color.skyOnSurface)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    MaterialIcon(open ? MI.outlinedKeyboardArrowUp : MI.outlinedKeyboardArrowDown, size: 18)
                        .foregroundStyle(Color.skyOnSurfaceVariant)
                }
                .padding(.horizontal, 16)
                .padding(.vertical, 13)
                .contentShape(Rectangle())
            }
            .buttonStyle(PressStyle(layer: .skyOnSurface))
            .accessibilityIdentifier("wallet-send-advanced")
            if open {
                VStack(alignment: .leading, spacing: 0) { content }
                    .padding(.horizontal, 16)
                    .padding(.bottom, 16)
            }
        }
        .background(Color.skySurfaceVariant, in: .sky(14))
        .clipShape(.sky(14))
        .padding(.top, 14)
    }
}

/// Picks the change address from the wallet's own addresses.
private struct ChangeSheet: View {
    @EnvironmentObject private var model: WalletModel
    @EnvironmentObject private var dialogs: SkyDialogs

    var body: some View {
        if let wallet = model.active {
            VStack(alignment: .leading, spacing: 0) {
                Text("wallet_send_change").skyText(.titleMedium).padding(.horizontal, 20).padding(.bottom, 8)
                ScrollView {
                    VStack(spacing: 0) {
                        ForEach(Array(wallet.receiveAddresses.enumerated()), id: \.offset) { index, address in
                            Button {
                                model.send.changeAddress = address
                                model.send.plan = nil
                                model.send.planError = nil
                                dialogs.dismissSheet()
                            } label: {
                                HStack(spacing: 12) {
                                    Text(verbatim: "\(index + 1)").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).frame(width: 16, alignment: .leading)
                                    Text(verbatim: shortAddress(address)).skyText(.bodyMedium, bold: true).foregroundStyle(Color.skyOnSurface)
                                        .frame(maxWidth: .infinity, alignment: .leading)
                                }
                                .padding(.horizontal, 20)
                                .padding(.vertical, 13)
                                .contentShape(Rectangle())
                            }
                            .buttonStyle(PressStyle(layer: .skyOnSurface))
                            .accessibilityIdentifier("wallet-change-\(index + 1)")
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

/// A destination after the first: address, amount and, optionally, its hours.
private struct DestinationCard: View {
    @EnvironmentObject private var model: WalletModel
    let index: Int

    var body: some View {
        let coin = model.coin
        VStack(spacing: 0) {
            HStack {
                SendLabel(text: L10n.format("wallet_send_destination", index + 2)).padding(.leading, 12)
                Spacer()
                Button { model.removeDestination(index) } label: {
                    MaterialIcon(MI.outlinedClose, size: 18).foregroundStyle(Color.skyOnSurfaceVariant).frame(width: 44, height: 44)
                }
                .accessibilityLabel(Text("wallet_send_remove_destination"))
                .accessibilityIdentifier("wallet-dest-remove-\(index + 2)")
            }
            BareField(placeholder: L10n.format("wallet_send_to_hint_fiber", coin.name), text: field(\.address),
                      identifier: "wallet-dest-to-\(index + 2)")
            SkyDivider(color: .skyContainerHighest)
            HStack(spacing: 0) {
                BareField(placeholder: "0", text: field(\.amountText), keyboard: .decimalPad, identifier: "wallet-dest-amount-\(index + 2)")
                Text(verbatim: coin.ticker).skyText(.titleMedium).foregroundStyle(Color.skyOnSurfaceVariant).padding(.trailing, 12)
            }
            SkyDivider(color: .skyContainerHighest)
            BareField(placeholder: L10n.text("wallet_send_dest_hours_hint"), text: field(\.hoursText), keyboard: .numberPad,
                      identifier: "wallet-dest-hours-\(index + 2)")
        }
        .padding(.horizontal, 4)
        .padding(.bottom, 6)
        .background(Color.skySurfaceVariant, in: .sky(14))
        .padding(.top, 14)
    }

    private func field(_ key: WritableKeyPath<DestDraft, String>) -> Binding<String> {
        Binding(
            get: { model.send.extra.indices.contains(index) ? model.send.extra[index][keyPath: key] : "" },
            set: { value in
                model.updateDestination(index) { draft in
                    draft[keyPath: key] = key == \DestDraft.address ? value.trimmingCharacters(in: .whitespaces) : String(value.prefix(key == \DestDraft.hoursText ? 18 : 40))
                }
            }
        )
    }
}

/// Every destination as text, one per line, skycoin-web's bulk send format.
private struct BulkSendDialog: View {
    @EnvironmentObject private var model: WalletModel
    @EnvironmentObject private var dialogs: SkyDialogs
    let initial: String
    @State private var text = ""
    @State private var problem: BulkParse?

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("wallet_send_bulk").skyText(.headlineSmall).foregroundStyle(Color.skyOnSurface)
            Text("wallet_send_bulk_body").skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 16)
            SkyOutlinedTextField(placeholder: L10n.key("wallet_send_bulk_hint"), text: Binding(get: { text }, set: { text = $0; problem = nil }),
                                 isError: problem != nil, mono: true, notch: .skyContainerHigh, identifier: "wallet-bulk-text", minLines: 5)
                .padding(.top, 12)
            if let message = problemText {
                Text(verbatim: message).skyText(.bodySmall).foregroundStyle(Color.skyError).padding(.top, 8)
                    .accessibilityIdentifier("wallet-bulk-error")
            }
            HStack(spacing: 8) {
                Spacer(minLength: 0)
                Button { dialogs.dismiss() } label: { Text("cancel") }.buttonStyle(.skyText)
                Button {
                    let parsed = parseBulk(text)
                    if case let .ok(destinations) = parsed {
                        dialogs.dismiss()
                        model.applyBulk(destinations)
                    } else {
                        problem = parsed
                    }
                } label: { Text("wallet_send_bulk_apply") }
                .buttonStyle(.skyText)
                .accessibilityIdentifier("wallet-bulk-apply")
            }
            .padding(.top, 24)
        }
        .onAppear { text = initial }
    }

    private var problemText: String? {
        switch problem {
        case .none, .ok?: nil
        case .empty?: L10n.text("wallet_send_bulk_empty")
        case let .badLine(line)?: L10n.format("wallet_send_bulk_line", line)
        case let .mixedHours(line)?: L10n.format("wallet_send_bulk_mixed", line)
        }
    }
}

/// A row that opens a picker: its choice in bold, an optional figure, a chevron.
private struct PickRow: View {
    let title: String
    let value: String?
    let identifier: String
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 12) {
                Text(verbatim: title).skyText(.bodyLarge, bold: true).foregroundStyle(Color.skyOnSurface)
                    .frame(maxWidth: .infinity, alignment: .leading)
                if let value {
                    Text(verbatim: value).skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant)
                }
                MaterialIcon(MI.outlinedKeyboardArrowDown, size: 18).foregroundStyle(Color.skyOnSurfaceVariant)
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 13)
            .background(Color.skySurfaceVariant, in: .sky(14))
            .contentShape(RoundedRectangle(cornerRadius: 14))
        }
        .buttonStyle(PressStyle(layer: .skyOnSurface, shape: AnyShape(RoundedRectangle(cornerRadius: 14))))
        .padding(.top, 9)
        .accessibilityIdentifier(identifier)
    }
}

/// Where the send spends from: the whole wallet, or one of its receive addresses.
private struct FromSheet: View {
    @EnvironmentObject private var model: WalletModel
    @EnvironmentObject private var dialogs: SkyDialogs

    var body: some View {
        if let wallet = model.active {
            let perAddress = model.snapshot?.perAddress ?? [:]
            VStack(alignment: .leading, spacing: 0) {
                Text("wallet_send_from").skyText(.titleMedium).padding(.horizontal, 20).padding(.bottom, 8)
                ScrollView {
                    VStack(spacing: 0) {
                        row(index: nil, title: L10n.text("wallet_send_from_wallet"), held: model.snapshot?.confirmed ?? 0, bold: false, identifier: "wallet-from-wallet")
                        ForEach(Array(wallet.receiveAddresses.enumerated()), id: \.offset) { index, address in
                            let held = perAddress[address] ?? 0
                            row(index: index, title: shortAddress(address), held: held, bold: held > 0, identifier: "wallet-from-\(index + 1)") {
                                model.sendFrom(address)
                            }
                        }
                    }
                }
                .frame(maxHeight: 420)
                .fixedSize(horizontal: false, vertical: true)
            }
            .padding(.bottom, 24)
        }
    }

    private func row(index: Int?, title: String, held: UInt64, bold: Bool, identifier: String,
                     pick: (() -> Void)? = nil) -> some View {
        Button {
            if let pick { pick() } else { model.sendFrom(nil) }
            dialogs.dismissSheet()
        } label: {
            HStack(spacing: 12) {
                if let index {
                    Text(verbatim: "\(index + 1)").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).frame(width: 16, alignment: .leading)
                }
                Text(verbatim: title).skyText(.bodyMedium, bold: true).foregroundStyle(Color.skyOnSurface)
                    .frame(maxWidth: .infinity, alignment: .leading)
                Text(verbatim: model.coin.amountText(held)).skyText(.bodyMedium, bold: bold)
                    .foregroundStyle(bold ? Color.skyOnSurface : Color.skyOnSurfaceVariant)
            }
            .padding(.horizontal, 20)
            .padding(.vertical, 13)
            .contentShape(Rectangle())
        }
        .buttonStyle(PressStyle(layer: .skyOnSurface))
        .accessibilityIdentifier(identifier)
    }
}

/// Coin control: which unspent outputs fund the send. A chosen from-address spends only its
/// own outputs, so only those are offered.
private struct UtxoSheet: View {
    @EnvironmentObject private var model: WalletModel
    @EnvironmentObject private var dialogs: SkyDialogs

    var body: some View {
        let send = model.send
        let offered = send.utxos.filter { send.fromAddress == nil || $0.address == send.fromAddress }
        VStack(alignment: .leading, spacing: 0) {
            Text("wallet_send_coins").skyText(.titleMedium).padding(.horizontal, 20)
            if offered.isEmpty {
                Text("wallet_send_coins_none").skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant)
                    .padding(.horizontal, 20).padding(.vertical, 10)
            } else {
                ScrollView {
                    VStack(spacing: 0) {
                        ForEach(offered, id: \.uxid) { output in
                            Button { model.toggleInput(output.uxid) } label: {
                                HStack(spacing: 6) {
                                    SkyCheckbox(checked: send.inputs.contains(output.uxid))
                                    VStack(alignment: .leading, spacing: 2) {
                                        Text(verbatim: model.coin.amountText(output.coins)).skyText(.bodyMedium, bold: true).foregroundStyle(Color.skyOnSurface)
                                        Text(verbatim: L10n.format("wallet_utxo_line", String(output.hours), shortAddress(output.address)))
                                            .skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                                    }
                                    .frame(maxWidth: .infinity, alignment: .leading)
                                }
                                .padding(.horizontal, 20)
                                .padding(.vertical, 11)
                                .contentShape(Rectangle())
                            }
                            .buttonStyle(PressStyle(layer: .skyOnSurface))
                            .accessibilityAddTraits(send.inputs.contains(output.uxid) ? .isSelected : [])
                            .accessibilityIdentifier("wallet-utxo")
                        }
                    }
                }
                .frame(maxHeight: 420)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.top, 4)
            }
            HStack(spacing: 12) {
                Button { model.clearInputs() } label: { Text("wallet_send_coins_auto").frame(maxWidth: .infinity) }
                    .buttonStyle(TonalButtonStyle(height: 46))
                    .accessibilityIdentifier("wallet-utxo-auto")
                Button { dialogs.dismissSheet() } label: { Text("wallet_done").frame(maxWidth: .infinity) }
                    .buttonStyle(FilledButtonStyle(height: 46))
                    .accessibilityIdentifier("wallet-utxo-done")
            }
            .padding(.horizontal, 20)
            .padding(.vertical, 8)
        }
        .padding(.bottom, 24)
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

/// The gas card: three tips, as skycoin-web offers, then the worst-case fee once a plan exists and
/// where that money comes from on a token send. No slider: the tip is the one honest knob, and
/// three steps of it are enough.
private struct EthFeeCard: View {
    @EnvironmentObject private var model: WalletModel

    var body: some View {
        let plan = model.send.plan
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 8) {
                FeeChoice(name: L10n.key("wallet_fee_economy"), note: L10n.key("wallet_fee_eth_slow"), selected: model.send.ethLevel == .slow,
                          identifier: "wallet-fee-slow") { pick(.slow) }
                FeeChoice(name: L10n.key("wallet_fee_normal"), note: L10n.key("wallet_fee_eth_normal"), selected: model.send.ethLevel == .normal,
                          identifier: "wallet-fee-normal") { pick(.normal) }
                FeeChoice(name: L10n.key("wallet_fee_priority"), note: L10n.key("wallet_fee_eth_fast"), selected: model.send.ethLevel == .fast,
                          identifier: "wallet-fee-fast") { pick(.fast) }
            }
            .padding(.bottom, 18)
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

    private func pick(_ level: FeeLevel) {
        model.send.ethLevel = level
        model.send.plan = nil
    }
}

/// A fee step: its name, a quiet line under it, the secondary container when chosen.
private struct FeeChoice: View {
    let name: LocalizedStringKey
    let note: LocalizedStringKey
    let selected: Bool
    var identifier: String?
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(spacing: 2) {
                Text(name).skyText(.labelLarge).foregroundStyle(selected ? Color.skyPrimary : Color.skyOnSurfaceVariant)
                Text(note).skyText(.labelSmall).foregroundStyle(Color.skyOnSurfaceVariant)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 11)
            .padding(.horizontal, 6)
            .background(selected ? Color.skySecondaryContainer : Color.skyContainerHighest, in: .sky(11))
        }
        .buttonStyle(PressStyle(layer: .skyOnSurface, shape: AnyShape(RoundedRectangle(cornerRadius: 11))))
        .accessibilityAddTraits(selected ? .isSelected : [])
        .accessibilityIdentifier(identifier ?? "")
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
        FeeChoice(name: name, note: eta, selected: rate == model.send.feeRate) {
            model.send.feeRate = rate
            model.send.plan = nil
        }
    }
}

/// What is about to be signed, in full, before the device-owner check.
private struct ReviewSheet: View {
    @EnvironmentObject private var model: WalletModel
    @EnvironmentObject private var dialogs: SkyDialogs
    let onUnsigned: () -> Void
    let onConfirm: () -> Void

    var body: some View {
        if let plan = model.send.plan {
            let coin = model.coin
            VStack(alignment: .leading, spacing: 0) {
                Text("wallet_review_title").skyText(.titleMedium)
                Text(verbatim: "\(coin.amountText(plan.amount)) \(coin.ticker)").skyText(.headlineMedium).padding(.top, 16)
                // The network is the coin's own chain: Skycoin, this fiber coin's, Bitcoin or Ethereum.
                Text(verbatim: plan.recipients.count > 1
                    ? L10n.format("wallet_review_dest_many", coin.name)
                    : L10n.format("wallet_review_dest", shortAddress(plan.toAddress), coin.name))
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
                // Fiber only: the unsigned transaction, to sign on a device that holds the keys.
                if coin.kind == .skyFiber {
                    Button(action: onUnsigned) { Text("wallet_offline_unsigned_button").frame(maxWidth: .infinity) }
                        .buttonStyle(.skyText)
                        .padding(.top, 6)
                        .accessibilityIdentifier("wallet-review-unsigned")
                }
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
            let each: [(String, String)] = plan.recipients.count > 1
                ? plan.recipients.map { (shortAddress($0.address), "\(coin.amountText($0.amount)) \(coin.ticker) · \(hoursText($0.hours ?? 0))") }
                : []
            return [
                (L10n.text("wallet_review_amount"), "\(coin.amountText(plan.amount)) \(coin.ticker)"),
            ] + each + [
                (L10n.text("wallet_hours_burned"), hoursText(plan.fee)),
            ] + (!model.send.changeAddress.isEmpty && plan.changeAmount > 0
                ? [(L10n.text("wallet_review_change_to"), plan.changeAddress.map { shortAddress($0) } ?? "")]
                : []) + [
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
                Text(verbatim: model.send.sentCount > 1
                    ? L10n.format("wallet_result_body_many", model.coin.amountText(model.send.sentAmount), model.coin.ticker)
                    : L10n.format("wallet_result_body", model.coin.amountText(model.send.sentAmount), model.coin.ticker,
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
