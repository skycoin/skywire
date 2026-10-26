import SwiftUI
import UIKit

/// Receive (Android: ui/wallet/WalletReceive.kt): the QR, tap to copy, share, and the
/// wallet's other addresses.
struct WalletReceiveView: View {
    @EnvironmentObject private var model: WalletModel
    @EnvironmentObject private var dialogs: SkyDialogs
    @State private var requestOpen = false
    @State private var amount = ""
    @State private var hours = ""
    @State private var message = ""

    private var address: String? {
        guard let wallet = model.active else { return nil }
        return wallet.receiveAddresses.indices.contains(model.receiveIndex)
            ? wallet.receiveAddresses[model.receiveIndex]
            : wallet.receiveAddresses.first
    }

    var body: some View {
        WalletScreen(title: Text("wallet_receive_title")) {
            ScrollView {
                if let address, let wallet = model.active {
                    let request = model.coin.paymentRequest(address: address, amountText: amount, hoursText: hours, message: message)
                    // The QR, Copy and Share carry the request once one is filled in; the chip stays the address.
                    let shared = request.uri ?? address
                    VStack(spacing: 0) {
                        Text(verbatim: L10n.format("wallet_receive_only", model.coin.ticker))
                            .skyText(.bodyMedium)
                            .foregroundStyle(Color.skyOnSurfaceVariant)
                            .multilineTextAlignment(.center)
                        QRCodeImage(content: shared)
                            .frame(width: 220, height: 220)
                            .clipShape(RoundedRectangle(cornerRadius: SkyRadius.small))
                            .padding(16)
                            .background(Color.white, in: .sky(20))
                            .padding(.top, 20)
                            .accessibilityIdentifier("wallet-receive-qr")
                        Button { copy(address) } label: {
                            HStack(spacing: 10) {
                                Text(verbatim: shortAddress(address)).skyText(.bodyLarge, bold: true).foregroundStyle(Color.skyOnSurface)
                                MaterialIcon(MI.outlinedContentCopy, size: 16).foregroundStyle(Color.skyOnSurfaceVariant)
                            }
                            .padding(.horizontal, 18)
                            .padding(.vertical, 12)
                            .background(Color.skySurfaceVariant, in: .sky(24))
                        }
                        .buttonStyle(PressStyle(layer: .skyOnSurface, shape: AnyShape(Capsule())))
                        .padding(.top, 20)
                        .accessibilityIdentifier("wallet-receive-address")
                        .accessibilityValue(Text(verbatim: address))
                        Text("wallet_receive_tap_copy").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 8)
                        HStack(spacing: 12) {
                            Button { copy(shared, request: request.uri != nil) } label: {
                                Text(request.uri != nil ? L10n.key("wallet_request_copy") : L10n.key("wallet_copy")).frame(maxWidth: .infinity)
                            }
                            .buttonStyle(FilledButtonStyle(height: 50))
                            .accessibilityIdentifier("wallet-receive-copy")
                            ShareLink(item: shared) { Text("wallet_share").frame(maxWidth: .infinity) }
                                .buttonStyle(TonalButtonStyle(height: 50))
                        }
                        .padding(.top, 26)
                        requestCard(request)
                            .padding(.top, 12)
                        Button { dialogs.presentSheet { AddressSheet().environmentObject(model) } } label: {
                            HStack(spacing: 12) {
                                Text("wallet_other_addresses").skyText(.bodyLarge, bold: true).foregroundStyle(Color.skyOnSurface)
                                    .frame(maxWidth: .infinity, alignment: .leading)
                                Text(verbatim: "\(wallet.receiveAddresses.count)").skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant)
                                MaterialIcon(MI.outlinedKeyboardArrowRight, size: 16).foregroundStyle(Color.skyOnSurfaceVariant)
                            }
                            .padding(.horizontal, 16)
                            .padding(.vertical, 15)
                            .background(Color.skySurfaceVariant, in: .sky(SkyRadius.medium))
                        }
                        .buttonStyle(PressStyle(layer: .skyOnSurface, shape: AnyShape(RoundedRectangle(cornerRadius: SkyRadius.medium))))
                        .padding(.top, 12)
                        .padding(.bottom, 24)
                        .accessibilityIdentifier("wallet-other-addresses")
                    }
                    .padding(.horizontal, 20)
                }
            }
        }
        .onChange(of: model.active?.id) { id in
            if id == nil, model.path.last == .receive { model.path.removeLast() }
        }
    }

    private func copy(_ text: String, request: Bool = false) {
        UIPasteboard.general.string = text
        dialogs.snackbar(Text(request ? L10n.key("wallet_request_copied") : L10n.key("wallet_address_copied")))
    }

    /// Request a specific amount: the hours on fiber coins, a message off the Ethereum family.
    private func requestCard(_ request: PaymentRequest) -> some View {
        let coin = model.coin
        return VStack(alignment: .leading, spacing: 0) {
            Button { requestOpen.toggle() } label: {
                HStack {
                    Text("wallet_request_title").skyText(.bodyLarge, bold: true).foregroundStyle(Color.skyOnSurface)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    MaterialIcon(requestOpen ? MI.outlinedKeyboardArrowUp : MI.outlinedKeyboardArrowDown, size: 18)
                        .foregroundStyle(Color.skyOnSurfaceVariant)
                }
                .padding(.horizontal, 16)
                .padding(.vertical, 15)
                .contentShape(Rectangle())
            }
            .buttonStyle(PressStyle(layer: .skyOnSurface))
            .accessibilityIdentifier("wallet-request")
            if requestOpen {
                VStack(alignment: .leading, spacing: 8) {
                    SkyOutlinedTextField(label: LocalizedStringKey(L10n.format("wallet_request_amount", coin.ticker)),
                                         text: Binding(get: { amount }, set: { amount = String($0.prefix(40)) }),
                                         isError: request.badAmount, keyboard: .decimalPad, notch: .skySurfaceVariant,
                                         identifier: "wallet-request-amount")
                    if coin.kind == .skyFiber {
                        SkyOutlinedTextField(label: L10n.key("wallet_request_hours"),
                                             text: Binding(get: { hours }, set: { hours = String($0.prefix(18)) }),
                                             isError: request.badHours, keyboard: .numberPad, notch: .skySurfaceVariant,
                                             identifier: "wallet-request-hours")
                    }
                    if !coin.isEthFamily {
                        SkyOutlinedTextField(label: L10n.key("wallet_request_message"),
                                             text: Binding(get: { message }, set: { message = String($0.prefix(requestMessageMax)) }),
                                             notch: .skySurfaceVariant, identifier: "wallet-request-message", prose: true)
                    }
                    Text(request.badAmount ? L10n.key("wallet_request_bad_amount")
                         : request.badHours ? L10n.key("wallet_request_bad_hours") : L10n.key("wallet_request_note"))
                        .skyText(.bodySmall)
                        .foregroundStyle(request.badAmount || request.badHours ? Color.skyError : Color.skyOnSurfaceVariant)
                        .padding(.top, 2)
                }
                .padding(.horizontal, 16)
                .padding(.bottom, 16)
            }
        }
        .background(Color.skySurfaceVariant, in: .sky(SkyRadius.medium))
        .accessibilityElement(children: .contain)
    }
}

/// The wallet's receive addresses: pick the one to show, see what each holds, open one's history
/// or outputs, or derive a new one.
private struct AddressSheet: View {
    @EnvironmentObject private var model: WalletModel
    @EnvironmentObject private var dialogs: SkyDialogs
    @State private var hideEmpty = false

    var body: some View {
        if let wallet = model.active {
            let perAddress = model.snapshot?.perAddress ?? [:]
            VStack(alignment: .leading, spacing: 0) {
                Text(verbatim: L10n.format("wallet_addresses_in", wallet.name)).skyText(.titleMedium).padding(.horizontal, 20)
                Text("wallet_addresses_note").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                    .padding(.horizontal, 20).padding(.vertical, 6)
                Button { hideEmpty.toggle() } label: {
                    HStack(spacing: 8) {
                        SkyCheckbox(checked: hideEmpty)
                        Text("wallet_addresses_hide_empty").skyText(.bodyMedium).foregroundStyle(Color.skyOnSurface)
                    }
                    .padding(.horizontal, 16)
                    .padding(.vertical, 8)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .contentShape(Rectangle())
                }
                .buttonStyle(PressStyle(layer: .skyOnSurface))
                .accessibilityAddTraits(hideEmpty ? .isSelected : [])
                .accessibilityIdentifier("wallet-addresses-hide-empty")
                ScrollView {
                    VStack(spacing: 0) {
                        ForEach(Array(wallet.receiveAddresses.enumerated()), id: \.offset) { index, address in
                            let held = perAddress[address]
                            // The address being shown stays, so hiding never takes away what is on screen.
                            if !(hideEmpty && (held ?? 0) == 0 && index != model.receiveIndex) {
                                row(index, address, held: held)
                            }
                        }
                    }
                }
                .frame(maxHeight: 420)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.top, 8)
                Button { model.generateNewAddress() } label: { Text("wallet_address_new").frame(maxWidth: .infinity) }
                    .buttonStyle(TonalButtonStyle(height: 48))
                    .padding(.horizontal, 20)
                    .padding(.vertical, 8)
                    .accessibilityIdentifier("wallet-address-new")
            }
            .padding(.bottom, 24)
        }
    }

    private func row(_ index: Int, _ address: String, held: UInt64?) -> some View {
        HStack(spacing: 12) {
            Button {
                model.receiveIndex = index
                dialogs.dismissSheet()
            } label: {
                HStack(spacing: 12) {
                    Text(verbatim: "\(index + 1)").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).frame(width: 16, alignment: .leading)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(verbatim: shortAddress(address)).skyText(.bodyMedium, bold: true)
                        if index == 0 {
                            Text("wallet_address_default").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                        } else if (held ?? 0) == 0 {
                            // With coins on it the balance line says more than "never used" ever could.
                            Text("wallet_address_unused").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                        }
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                    if let held {
                        Text(verbatim: model.coin.amountText(held))
                            .skyText(.bodyMedium, bold: held > 0)
                            .foregroundStyle(held > 0 ? Color.skyOnSurface : Color.skyOnSurfaceVariant)
                    }
                    if index == model.receiveIndex {
                        Circle().fill(Color.skyPrimary).frame(width: 8, height: 8)
                    }
                }
                .padding(.leading, 20)
                .padding(.vertical, 13)
                .contentShape(Rectangle())
            }
            .buttonStyle(PressStyle(layer: .skyOnSurface))
            .accessibilityIdentifier("wallet-address-\(index + 1)")
            Menu {
                Button {
                    model.historyAddress = address
                    dialogs.dismissSheet()
                    model.path.append(.history)
                } label: { Text("wallet_address_history") }
                if model.coin.kind == .skyFiber {
                    Button {
                        dialogs.presentSheet { OutputsSheet(address: address).environmentObject(model) }
                    } label: { Text("wallet_address_outputs") }
                }
            } label: {
                MaterialIcon(MI.outlinedMoreVert, size: 18).foregroundStyle(Color.skyOnSurfaceVariant).frame(width: 32, height: 44)
            }
            .accessibilityLabel(Text("wallet_address_actions"))
            .accessibilityIdentifier("wallet-address-actions-\(index + 1)")
            .padding(.trailing, 12)
        }
    }
}

/// The unspent outputs one address holds, as skycoin-web's per-address outputs view. Fiber only.
private struct OutputsSheet: View {
    @EnvironmentObject private var model: WalletModel
    let address: String

    var body: some View {
        let outputs = model.send.utxos.filter { $0.address == address }
        VStack(alignment: .leading, spacing: 0) {
            Text(verbatim: L10n.format("wallet_outputs_title", shortAddress(address))).skyText(.titleMedium).padding(.horizontal, 20)
            if outputs.isEmpty {
                Text("wallet_outputs_none").skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant)
                    .padding(.horizontal, 20).padding(.vertical, 12)
            }
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    ForEach(outputs, id: \.uxid) { output in
                        VStack(alignment: .leading, spacing: 2) {
                            Text(verbatim: "\(model.coin.amountText(output.coins)) \(model.coin.ticker)").skyText(.bodyMedium, bold: true)
                            Text(verbatim: L10n.format("wallet_utxo_line", hoursText(output.hours), shortAddress(output.uxid, head: 10, tail: 8)))
                                .skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                        }
                        .padding(.horizontal, 20)
                        .padding(.vertical, 10)
                        .accessibilityElement(children: .combine)
                        .accessibilityIdentifier("wallet-output")
                    }
                }
            }
            .frame(maxHeight: 420)
            .fixedSize(horizontal: false, vertical: true)
        }
        .padding(.bottom, 24)
        .onAppear { model.loadUtxos() }
    }
}
