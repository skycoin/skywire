import SwiftUI
import UIKit

/// Receive (Android: ui/wallet/WalletReceive.kt): the QR, tap to copy, share, and the
/// wallet's other addresses.
struct WalletReceiveView: View {
    @EnvironmentObject private var model: WalletModel
    @EnvironmentObject private var dialogs: SkyDialogs

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
                    VStack(spacing: 0) {
                        Text(verbatim: L10n.format("wallet_receive_only", model.coin.ticker))
                            .skyText(.bodyMedium)
                            .foregroundStyle(Color.skyOnSurfaceVariant)
                            .multilineTextAlignment(.center)
                        QRCodeImage(content: address)
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
                            Button { copy(address) } label: { Text("wallet_copy").frame(maxWidth: .infinity) }
                                .buttonStyle(FilledButtonStyle(height: 50))
                            ShareLink(item: address) { Text("wallet_share").frame(maxWidth: .infinity) }
                                .buttonStyle(TonalButtonStyle(height: 50))
                        }
                        .padding(.top, 26)
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

    private func copy(_ address: String) {
        UIPasteboard.general.string = address
        dialogs.snackbar(Text("wallet_address_copied"))
    }
}

/// The wallet's receive addresses: pick the one to show, or derive a new one.
private struct AddressSheet: View {
    @EnvironmentObject private var model: WalletModel
    @EnvironmentObject private var dialogs: SkyDialogs

    var body: some View {
        if let wallet = model.active {
            VStack(alignment: .leading, spacing: 0) {
                Text(verbatim: L10n.format("wallet_addresses_in", wallet.name)).skyText(.titleMedium).padding(.horizontal, 20)
                Text("wallet_addresses_note").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                    .padding(.horizontal, 20).padding(.vertical, 6)
                ScrollView {
                    VStack(spacing: 0) {
                        ForEach(Array(wallet.receiveAddresses.enumerated()), id: \.offset) { index, address in
                            Button {
                                model.receiveIndex = index
                                dialogs.dismissSheet()
                            } label: {
                                HStack(spacing: 12) {
                                    Text(verbatim: "\(index + 1)").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).frame(width: 16, alignment: .leading)
                                    VStack(alignment: .leading, spacing: 2) {
                                        Text(verbatim: shortAddress(address)).skyText(.bodyMedium, bold: true)
                                        Text(index == 0 ? L10n.key("wallet_address_default") : L10n.key("wallet_address_unused"))
                                            .skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                                    }
                                    .frame(maxWidth: .infinity, alignment: .leading)
                                    if index == model.receiveIndex {
                                        Circle().fill(Color.skyPrimary).frame(width: 8, height: 8)
                                    }
                                }
                                .padding(.horizontal, 20)
                                .padding(.vertical, 13)
                                .contentShape(Rectangle())
                            }
                            .buttonStyle(PressStyle(layer: .skyOnSurface))
                            .accessibilityIdentifier("wallet-address-\(index + 1)")
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
}
