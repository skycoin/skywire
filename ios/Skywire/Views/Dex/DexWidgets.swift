import CoreClient
import SwiftUI
import UIKit

// SkyDEX's small pieces (Android: ui/dex/DexWidgets.kt).

extension Color {
    /// Green as text: the accent holds on navy, but falls under 3:1 on white, so light mode takes a darker shade.
    static let dexGood = Color(light: 0x15803D, dark: 0x22C275)
    /// Amber as text, for the same reason.
    static let dexBusy = Color(light: 0xB45309, dark: 0xF59E0B)
}

enum DexText {
    /// Ids and hashes: the first eight characters, as the page's tables showed them.
    static func shortId(_ id: String) -> String { id.count <= 12 ? id : String(id.prefix(8)) + "…" }

    /// A seller or market key in a card: 8…4.
    static func shortKey(_ pk: String) -> String { pk.count <= 14 ? pk : String(pk.prefix(8)) + "…" + String(pk.suffix(4)) }

    static func millis(_ iso: String) -> Int64? { Skychat.parseTime(iso) }

    /// The market sends RFC 3339 times; shown in the phone's zone and the app's language.
    static func dateTime(_ iso: String) -> String {
        guard let ms = millis(iso) else { return iso.isEmpty ? "—" : iso }
        let formatter = DateFormatter()
        formatter.locale = L10n.locale
        formatter.dateStyle = .medium
        formatter.timeStyle = .short
        return formatter.string(from: Date(timeIntervalSince1970: Double(ms) / 1000))
    }

    static func time(_ iso: String) -> String {
        guard let ms = millis(iso) else { return iso }
        let formatter = DateFormatter()
        formatter.locale = L10n.locale
        formatter.timeStyle = .short
        return formatter.string(from: Date(timeIntervalSince1970: Double(ms) / 1000))
    }

    /// "5 minutes ago", in the app's language.
    static func ago(_ ms: Int64) -> String {
        guard ms > 0 else { return "" }
        let formatter = RelativeDateTimeFormatter()
        formatter.locale = L10n.locale
        return formatter.localizedString(for: Date(timeIntervalSince1970: Double(ms) / 1000), relativeTo: Date())
    }
}

/// The market spells it both ways.
private let cancelled: Set<String> = ["cancelled", "canceled"]

private enum Tone {
    case good, busy, bad, quiet, info

    var colors: (bg: Color, fg: Color) {
        switch self {
        case .good: (Color.dexGood.opacity(0.16), .dexGood)
        case .busy: (Color.dexBusy.opacity(0.16), .dexBusy)
        case .bad: (Color.skyError.opacity(0.16), .skyError)
        case .info: (Color.skyPrimary.opacity(0.16), .skyPrimary)
        case .quiet: (Color.skySurfaceVariant, .skyOnSurfaceVariant)
        }
    }
}

private struct Badge: View {
    let text: String
    let tone: Tone

    var body: some View {
        Text(verbatim: text).skyText(.labelSmall).foregroundStyle(tone.colors.fg)
            .padding(.horizontal, 8).padding(.vertical, 3)
            .background(tone.colors.bg, in: .sky(6))
    }
}

/// An order or history status (Orders.jsx statusMap). Unknown states show as the market wrote them.
struct StatusBadge: View {
    let status: String

    var body: some View {
        switch status {
        case "pending_payment": Badge(text: L10n.text("dex_status_pending_payment"), tone: .busy)
        case "paid": Badge(text: L10n.text("dex_status_paid"), tone: .info)
        case "confirmed": Badge(text: L10n.text("dex_status_confirmed"), tone: .info)
        case "active": Badge(text: L10n.text("dex_status_active"), tone: .info)
        case "completed": Badge(text: L10n.text("dex_status_completed"), tone: .good)
        case _ where cancelled.contains(status): Badge(text: L10n.text("dex_status_cancelled"), tone: .bad)
        case "expired": Badge(text: L10n.text("dex_status_expired"), tone: .quiet)
        case "pending": Badge(text: L10n.text("dex_status_pending"), tone: .busy)
        default: Badge(text: status, tone: .quiet)
        }
    }
}

struct TypeBadge: View {
    let type: String

    var body: some View {
        if type == "buy" {
            Badge(text: L10n.text("dex_type_buy"), tone: .good)
        } else {
            Badge(text: L10n.text("dex_type_sell"), tone: .info)
        }
    }
}

/// Once a buyer picks a confirmed listing's product, the seller is committed (MyListings.jsx).
struct ProductStateBadge: View {
    let listing: DexListing

    var body: some View {
        if listing.status == "confirmed" && !listing.productStatus.isEmpty && listing.productStatus != "active" {
            switch listing.productStatus {
            case "frozen": Badge(text: L10n.text("dex_product_frozen"), tone: .busy)
            case "sold": Badge(text: L10n.text("dex_product_sold"), tone: .good)
            case _ where cancelled.contains(listing.productStatus): Badge(text: L10n.text("dex_status_cancelled"), tone: .bad)
            default: Badge(text: listing.productStatus, tone: .quiet)
            }
        }
    }
}

/// Pending deposit → Confirmed → Listed as product. A confirmed listing is already on the market.
struct LifecycleChips: View {
    let status: String

    var body: some View {
        let reached = status == "pending" ? 0 : 2
        let labels = [L10n.text("dex_step_pending"), L10n.text("dex_step_confirmed"), L10n.text("dex_step_product")]
        HStack(spacing: 4) {
            ForEach(Array(labels.enumerated()), id: \.offset) { i, label in
                Badge(text: label, tone: i < reached ? .good : (i == reached ? .info : .quiet))
                if i < labels.count - 1 {
                    Text(verbatim: "→").skyText(.labelSmall).foregroundStyle(Color.skyOnSurfaceVariant)
                }
            }
        }
    }
}

struct ConfirmationsBar: View {
    let confirmations: Int
    let required: Int

    var body: some View {
        let need = required > 0 ? required : 2
        VStack(spacing: 4) {
            HStack {
                Text("dex_confirmations").skyText(.labelSmall)
                Spacer()
                Text(verbatim: L10n.format("dex_fraction", confirmations, need)).skyText(.labelSmall)
            }
            .foregroundStyle(Color.skyOnSurface)
            ProgressView(value: min(1, max(0, Double(confirmations) / Double(need)))).tint(Color.skyPrimary)
        }
    }
}

/// A banner across the top of a tab: onboarding, or a market with nothing to sell.
struct DexBanner: View {
    let text: String
    let warn: Bool
    var action: String?
    var onAction: () -> Void = {}

    var body: some View {
        VStack(alignment: .trailing, spacing: 4) {
            Text(verbatim: text).skyText(.bodyMedium).foregroundStyle(Color.skyOnSurface).frame(maxWidth: .infinity, alignment: .leading)
            if let action {
                Button(action: onAction) { Text(verbatim: action) }.buttonStyle(.skyText)
            }
        }
        .padding(14)
        .background((warn ? Color.skyWarning : Color.skyPrimary).opacity(0.12), in: .sky(SkyRadius.small))
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier(warn ? "dex-banner-warn" : "dex-banner")
    }
}

/// A card holding one record or one form.
struct DexCard<Content: View>: View {
    @ViewBuilder let content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 0) { content }
            .padding(16)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Color.skySurfaceVariant, in: .sky(SkyRadius.small))
    }
}

/// One labelled value inside a card.
struct DexField: View {
    let label: String
    let value: String
    var mono = false

    var body: some View {
        HStack(alignment: .top, spacing: 0) {
            Text(verbatim: label).skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).frame(width: 120, alignment: .leading)
            Text(verbatim: value).skyText(.bodySmall, mono: mono).foregroundStyle(Color.skyOnSurface).frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(.vertical, 2)
    }
}

struct DexHint: View {
    let text: String

    var body: some View {
        Text(verbatim: text).skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).frame(maxWidth: .infinity, alignment: .leading)
    }
}

struct DexErrorLine: View {
    let text: String

    var body: some View {
        Text(verbatim: text).skyText(.bodySmall).foregroundStyle(Color.skyError).frame(maxWidth: .infinity, alignment: .leading)
            .accessibilityIdentifier("dex-action-error")
    }
}

/// Where to send coins: the address in full, with Copy and a QR a wallet can
/// scan. Losing this address, or retyping it wrong, is how funds get lost.
struct AddressBlock: View {
    @ObservedObject var model: DexModel
    let address: String
    let amount: Double
    let coin: String
    let qrTitle: String
    let qrHint: String?
    @EnvironmentObject private var dialogs: SkyDialogs

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(verbatim: address).skyText(.bodySmall, mono: true).foregroundStyle(Color.skyOnSurface).textSelection(.enabled)
                .padding(10).frame(maxWidth: .infinity, alignment: .leading)
                .background(Color.skyBackground, in: .sky(10))
            HStack(spacing: 8) {
                Button {
                    UIPasteboard.general.string = address
                    model.notice = L10n.text("copied_to_clipboard")
                } label: {
                    HStack(spacing: 6) {
                        MaterialIcon(MI.roundedContentCopy, size: 18)
                        Text("dex_copy_address")
                    }
                }
                .buttonStyle(.outlined)
                .accessibilityIdentifier("dex-copy-address")
                Button {
                    dialogs.showCustom { PaymentQr(model: model, address: address, amount: amount, coin: coin, title: qrTitle, hint: qrHint) }
                } label: { MaterialIcon(MI.roundedQrCode2, size: 18) }
                .buttonStyle(.outlined)
                .accessibilityLabel(Text("dex_show_qr"))
                .accessibilityIdentifier("dex-show-qr")
            }
        }
    }
}

/// A full-size QR for a phone wallet (QrButton.jsx). With a known URI scheme it
/// carries the amount too; the switch falls back to a bare address for wallets
/// that do not understand the link. Dark on white in both themes.
private struct PaymentQr: View {
    @ObservedObject var model: DexModel
    let address: String
    let amount: Double
    let coin: String
    let title: String
    let hint: String?
    @EnvironmentObject private var dialogs: SkyDialogs
    @State private var withAmount = true

    var body: some View {
        let uri = DexFormat.paymentURI(address: address, amount: amount, coin: coin)
        let value = uri.flatMap { withAmount ? $0 : nil } ?? address
        VStack(alignment: .leading, spacing: 10) {
            Text(verbatim: title).skyText(.headlineSmall).foregroundStyle(Color.skyOnSurface)
            ScrollView {
                VStack(alignment: .leading, spacing: 10) {
                    if amount > 0 && !coin.isEmpty {
                        Text(verbatim: L10n.format("dex_send_exactly", DexFormat.number(amount), coin)).skyText(.bodyMedium, bold: true).foregroundStyle(Color.skyOnSurface)
                    }
                    QRCodeImage(content: value)
                        .frame(width: 220, height: 220)
                        .padding(8)
                        .background(Color.white, in: .sky(SkyRadius.small))
                        .frame(maxWidth: .infinity)
                        .accessibilityIdentifier("dex-qr")
                    if uri != nil {
                        Toggle(isOn: $withAmount) {
                            Text(verbatim: L10n.format("dex_qr_include_amount", coin)).skyText(.bodySmall).foregroundStyle(Color.skyOnSurface)
                        }
                        .toggleStyle(.skySwitch)
                    }
                    Text(verbatim: address).skyText(.bodySmall, mono: true).foregroundStyle(Color.skyOnSurface)
                    let base = uri != nil && withAmount ? L10n.format("dex_qr_hint_link", coin) : L10n.text("dex_qr_hint_plain")
                    Text(verbatim: hint.map { base + " " + $0 } ?? base).skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                    if let uri, withAmount {
                        Button {
                            UIPasteboard.general.string = uri
                            model.notice = L10n.text("copied_to_clipboard")
                        } label: { Text("dex_copy_link") }
                        .buttonStyle(.skyText)
                    }
                }
            }
            .frame(maxHeight: 520)
            HStack {
                Spacer()
                Button {
                    UIPasteboard.general.string = address
                    model.notice = L10n.text("copied_to_clipboard")
                } label: { Text("dex_copy_address") }
                .buttonStyle(.skyText)
                Button { dialogs.dismiss() } label: { Text("dex_close") }
                    .buttonStyle(.skyText)
                    .accessibilityIdentifier("dex-qr-close")
            }
        }
    }
}
