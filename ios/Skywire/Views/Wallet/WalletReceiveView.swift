import SwiftUI
import UIKit

/// Receive (Android: ui/wallet/WalletReceive.kt): the QR, tap to copy,
/// share, and the wallet's other addresses.
struct WalletReceiveView: View {
    @EnvironmentObject private var model: WalletModel
    @State private var addressSheet = false
    @State private var copied = false

    private var address: String? {
        guard let wallet = model.active else { return nil }
        return wallet.receiveAddresses.indices.contains(model.receiveIndex)
            ? wallet.receiveAddresses[model.receiveIndex]
            : wallet.receiveAddresses.first
    }

    var body: some View {
        ScrollView {
            if let address, let wallet = model.active {
                VStack(spacing: 18) {
                    Text(L10n.format("wallet_receive_only", model.coin.ticker))
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                        .multilineTextAlignment(.center)
                    QRCodeImage(content: address)
                        .frame(width: 220, height: 220)
                        .padding(16)
                        .background(RoundedRectangle(cornerRadius: 20).fill(Color.white))
                        .accessibilityIdentifier("wallet-receive-qr")
                    Button(action: { copy(address) }) {
                        HStack(spacing: 10) {
                            Text(shortAddress(address)).font(.body.weight(.semibold).monospaced())
                            Image(systemName: "doc.on.doc").font(.footnote).foregroundStyle(.secondary)
                        }
                        .padding(.horizontal, 18).padding(.vertical, 11)
                        .background(Capsule().fill(Color(.secondarySystemBackground)))
                    }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("wallet-receive-address")
                    .accessibilityValue(Text(verbatim: address))
                    Text("wallet_receive_tap_copy").font(.footnote).foregroundStyle(.secondary)
                    HStack(spacing: 12) {
                        Button(action: { copy(address) }) {
                            Text("wallet_copy").font(.body.weight(.semibold)).frame(maxWidth: .infinity, minHeight: 34)
                        }
                        .buttonStyle(.borderedProminent)
                        ShareLink(item: address) {
                            Text("wallet_share").font(.body.weight(.semibold)).frame(maxWidth: .infinity, minHeight: 34)
                        }
                        .buttonStyle(.bordered)
                    }
                    .tint(.skywire)
                    Button { addressSheet = true } label: {
                        HStack {
                            Text("wallet_other_addresses").font(.body.weight(.semibold)).foregroundStyle(.primary)
                            Spacer()
                            Text("\(wallet.receiveAddresses.count)").foregroundStyle(.secondary)
                            Image(systemName: "chevron.right").font(.footnote.weight(.semibold)).foregroundStyle(.tertiary)
                        }
                        .padding(.horizontal, 16).padding(.vertical, 14)
                        .background(RoundedRectangle(cornerRadius: 14).fill(Color(.secondarySystemBackground)))
                    }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("wallet-other-addresses")
                }
                .padding(20)
            }
        }
        .navigationTitle(Text("wallet_receive_title"))
        .navigationBarTitleDisplayMode(.inline)
        .overlay(alignment: .top) {
            if copied {
                Text("wallet_address_copied")
                    .font(.footnote.weight(.semibold))
                    .padding(.horizontal, 12).padding(.vertical, 6)
                    .background(Capsule().fill(.thinMaterial))
                    .task {
                        try? await Task.sleep(for: .seconds(1.5))
                        copied = false
                    }
            }
        }
        .sheet(isPresented: $addressSheet) { AddressSheet().environmentObject(model) }
        .onChange(of: model.active?.id) { id in
            if id == nil, model.path.last == .receive { model.path.removeLast() }
        }
    }

    private func copy(_ address: String) {
        UIPasteboard.general.string = address
        copied = true
    }
}

/// The wallet's receive addresses: pick the one to show, or derive a new one.
private struct AddressSheet: View {
    @EnvironmentObject private var model: WalletModel
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            List {
                if let wallet = model.active {
                    Section {
                        ForEach(Array(wallet.receiveAddresses.enumerated()), id: \.offset) { index, address in
                            Button {
                                model.receiveIndex = index
                                dismiss()
                            } label: {
                                HStack(spacing: 12) {
                                    Text("\(index + 1)").font(.footnote.monospacedDigit()).foregroundStyle(.secondary).frame(width: 22)
                                    VStack(alignment: .leading, spacing: 2) {
                                        Text(shortAddress(address)).font(.body.weight(.semibold).monospaced()).foregroundStyle(.primary)
                                        Text(index == 0 ? L10n.key("wallet_address_default") : L10n.key("wallet_address_unused"))
                                            .font(.footnote)
                                            .foregroundStyle(.secondary)
                                    }
                                    Spacer()
                                    if index == model.receiveIndex {
                                        Circle().fill(Color.skywire).frame(width: 8, height: 8)
                                    }
                                }
                            }
                            .accessibilityIdentifier("wallet-address-\(index + 1)")
                        }
                    } footer: {
                        Text("wallet_addresses_note")
                    }
                    Section {
                        Button("wallet_address_new") { model.generateNewAddress() }
                            .font(.body.weight(.semibold))
                            .accessibilityIdentifier("wallet-address-new")
                    }
                }
            }
            .navigationTitle(Text(L10n.format("wallet_addresses_in", model.active?.name ?? "")))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("wallet_sheet_done") { dismiss() }
                }
            }
        }
    }
}
