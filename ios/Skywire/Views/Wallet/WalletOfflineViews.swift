import SwiftUI
import UIKit

/// Signing away from the network, fiber only (Android: ui/wallet/WalletOffline.kt): a planned
/// send carried out unsigned, a pasted one signed here, a signed one broadcast here.
enum OfflineMode { case sign, broadcast }

/// A raw transaction to carry somewhere else: selectable, copyable, shareable.
struct RawTxDialog: View {
    @EnvironmentObject private var dialogs: SkyDialogs
    let title: Text
    let message: Text
    let hex: String
    @State private var copied = false

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            title.skyText(.headlineSmall).foregroundStyle(Color.skyOnSurface)
            message.skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 16)
            ScrollView {
                Text(verbatim: hex).skyText(.bodySmall, mono: true).foregroundStyle(Color.skyOnSurface)
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(10)
                    .accessibilityIdentifier("wallet-raw-hex")
            }
            .frame(maxHeight: 220)
            .fixedSize(horizontal: false, vertical: true)
            .background(Color.skyContainerHighest, in: .sky(10))
            .padding(.top, 12)
            if copied {
                Text("wallet_offline_copied").skyText(.bodySmall).foregroundStyle(Color.skyPrimary).padding(.top, 8)
            }
            HStack(spacing: 4) {
                Button { dialogs.dismiss() } label: { Text("wallet_done") }.buttonStyle(.skyText)
                Spacer(minLength: 0)
                ShareLink(item: hex) { Text("wallet_share") }.buttonStyle(.skyText)
                Button {
                    UIPasteboard.general.string = hex
                    copied = true
                } label: { Text("wallet_copy") }
                .buttonStyle(.skyText)
                .accessibilityIdentifier("wallet-raw-copy")
            }
            .padding(.top, 24)
        }
    }
}

/// Sign a pasted unsigned transaction with this wallet's keys, or broadcast a pasted signed one.
/// Signing asks for Face ID like a send does, and sends nothing: the signed transaction is shown
/// to be carried back.
struct OfflineTxDialog: View {
    @EnvironmentObject private var model: WalletModel
    @EnvironmentObject private var dialogs: SkyDialogs
    let mode: OfflineMode
    @State private var input = ""
    @State private var busy = false
    @State private var error: String?
    @State private var sent: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text(mode == .sign ? L10n.key("wallet_offline_sign_title") : L10n.key("wallet_offline_broadcast_title"))
                .skyText(.headlineSmall).foregroundStyle(Color.skyOnSurface)
            Text(mode == .sign ? L10n.key("wallet_offline_sign_body") : L10n.key("wallet_offline_broadcast_body"))
                .skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 16)
            if let sent {
                Text(verbatim: L10n.format("wallet_offline_sent", sent)).skyText(.bodyMedium, bold: true)
                    .textSelection(.enabled)
                    .padding(.top, 12)
                    .accessibilityIdentifier("wallet-offline-sent")
            } else {
                SkyOutlinedTextField(placeholder: L10n.key("wallet_offline_tx_hint"), text: Binding(
                    get: { input }, set: { input = $0.trimmingCharacters(in: .whitespacesAndNewlines); error = nil }
                ), isError: error != nil, mono: true, notch: .skyContainerHigh, identifier: "wallet-offline-input", minLines: 4)
                .padding(.top, 12)
                Button {
                    if let pasted = UIPasteboard.general.string?.trimmingCharacters(in: .whitespacesAndNewlines) {
                        input = pasted
                        error = nil
                    }
                } label: { Text("wallet_paste") }
                .buttonStyle(.skyText)
                .accessibilityIdentifier("wallet-offline-paste")
            }
            if let error {
                Text(verbatim: error).skyText(.bodySmall).foregroundStyle(Color.skyError)
                    .accessibilityIdentifier("wallet-offline-error")
            }
            HStack(spacing: 8) {
                Spacer(minLength: 0)
                Button { dialogs.dismiss() } label: { Text(sent == nil ? L10n.key("cancel") : L10n.key("wallet_done")) }
                    .buttonStyle(.skyText)
                if sent == nil {
                    Button { run() } label: {
                        HStack(spacing: 8) {
                            if busy { MaterialSpinner(size: 16, stroke: 2) }
                            Text(mode == .sign ? L10n.key("wallet_offline_sign") : L10n.key("wallet_offline_broadcast"))
                        }
                    }
                    .buttonStyle(.skyText)
                    .disabled(input.isEmpty || busy)
                    .accessibilityIdentifier("wallet-offline-run")
                }
            }
            .padding(.top, 24)
        }
    }

    private func run() {
        busy = true
        error = nil
        Task {
            defer { busy = false }
            switch mode {
            case .sign:
                guard await WalletAuth.confirm(reason: L10n.text("wallet_bio_send_title")) else { return }
                do {
                    let signed = try await model.signRaw(input)
                    dialogs.dismiss()
                    dialogs.showCustom {
                        RawTxDialog(title: Text("wallet_offline_signed_title"), message: Text("wallet_offline_signed_body"), hex: signed)
                    }
                } catch {
                    self.error = WalletModel.report(error)
                }
            case .broadcast:
                do {
                    sent = try await model.broadcastRaw(input)
                } catch {
                    self.error = WalletModel.report(error)
                }
            }
        }
    }
}
