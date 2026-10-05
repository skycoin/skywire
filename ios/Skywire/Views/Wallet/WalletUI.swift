import CoreImage.CIFilterBuiltins
import LocalAuthentication
import os
import SwiftUI
import UIKit
import WalletCore

// What the wallet screens share (Android: ui/wallet/WalletUi.kt and
// CoinIcons.kt).

/// "2GgFvq…7uQ" — how every address is shown outside copy/share.
func shortAddress(_ address: String, head: Int = 8, tail: Int = 6) -> String {
    address.count <= head + tail + 1 ? address : "\(address.prefix(head))…\(address.suffix(tail))"
}

func hoursText(_ hours: UInt64) -> String { Amounts.groupThousands(String(hours)) }

/// Fee line for a history row / detail: burned hours, BTC, or ETH gas.
func feeText(_ coin: CoinSpec, _ fee: UInt64?) -> String {
    guard let fee else { return "—" }
    switch coin.kind {
    case .btc: return "\(Amounts.format(fee, exponent: 8, minDecimals: 8)) BTC"
    // Always the gas, always in ETH — a token has no fee of its own.
    case .eth, .erc20: return "\(Amounts.format(fee, exponent: 9, minDecimals: 6)) ETH"
    case .skyFiber: return L10n.format("wallet_fee_hours_value", hoursText(fee))
    }
}

/// The status colour every transaction row and pill uses.
func txColor(_ tx: CachedTx) -> Color {
    if !tx.confirmed { return .skyWarning }
    return tx.incoming ? .skySuccess : .skyOnSurface
}

/// "Today", "Yesterday", or "5 October" (Android's `d MMMM`, the phone's month names).
func dayLabel(_ timestamp: Int64) -> String {
    let date = Date(timeIntervalSince1970: TimeInterval(timestamp))
    let calendar = Calendar.current
    if calendar.isDateInToday(date) { return L10n.text("wallet_day_today") }
    if calendar.isDateInYesterday(date) { return L10n.text("wallet_day_yesterday") }
    return wallFormat("d MMMM", date)
}

/// The time of day as Android writes it: 24-hour `HH:mm`.
func timeLabel(_ timestamp: Int64) -> String {
    wallFormat("HH:mm", Date(timeIntervalSince1970: TimeInterval(timestamp)))
}

/// A date in a fixed Android pattern, with the phone's language for the month names.
func wallFormat(_ pattern: String, _ date: Date) -> String {
    let formatter = DateFormatter()
    formatter.locale = .current
    formatter.dateFormat = pattern
    return formatter.string(from: date)
}

// MARK: Coin badges

/// Badge artwork. The four built-ins ship with their real logos (the same
/// PNGs Android bundles, from the CC0 cryptocurrency-icons set — no network
/// fetch at render time); a user-added coin carries an image its owner picked
/// at creation, stored under CoinSpec.icon; ticker letters when there is
/// neither.
enum CoinIcons {
    static func builtInLogo(_ coinId: String) -> UIImage? {
        switch coinId {
        case CoinSpec.sky.id: UIImage(named: "coin_sky")
        case CoinSpec.btc.id: UIImage(named: "coin_btc")
        case CoinSpec.eth.id: UIImage(named: "coin_eth")
        case CoinSpec.usdt.id: UIImage(named: "coin_usdt")
        default: nil
        }
    }

    /// 192 px covers the largest badge (46 pt) on a 3x screen and then some.
    static let side: CGFloat = 192

    static func directory(_ store: WalletStore) -> URL {
        store.directory.appendingPathComponent("coin_icons", isDirectory: true)
    }

    static func image(_ name: String, in store: WalletStore) -> UIImage? {
        UIImage(contentsOfFile: directory(store).appendingPathComponent(name).path)
    }

    /// The picked image as the coin's badge: centre-cropped square, scaled
    /// down to badge size, PNG, in the app's own storage (the picker's grant
    /// does not outlive the pick). Returns the stored file's name.
    static func importIcon(_ data: Data, into store: WalletStore) throws -> String {
        guard let source = UIImage(data: data), let cg = source.cgImage else {
            throw WalletStoreError(L10n.text("wallet_add_coin_icon_unreadable"))
        }
        let sideIn = min(cg.width, cg.height)
        let crop = CGRect(x: (cg.width - sideIn) / 2, y: (cg.height - sideIn) / 2, width: sideIn, height: sideIn)
        guard let square = cg.cropping(to: crop) else { throw WalletStoreError(L10n.text("wallet_add_coin_icon_unreadable")) }
        let target = min(CGFloat(sideIn), side)
        let format = UIGraphicsImageRendererFormat()
        format.scale = 1
        let scaled = UIGraphicsImageRenderer(size: CGSize(width: target, height: target), format: format).image { _ in
            UIImage(cgImage: square, scale: 1, orientation: source.imageOrientation)
                .draw(in: CGRect(x: 0, y: 0, width: target, height: target))
        }
        guard let png = scaled.pngData() else { throw WalletStoreError(L10n.text("wallet_add_coin_icon_unreadable")) }
        let dir = directory(store)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let name = "coin-\(UUID().uuidString.lowercased().prefix(8)).png"
        try png.write(to: dir.appendingPathComponent(name), options: .atomic)
        return name
    }

    static func delete(_ name: String, in store: WalletStore) {
        try? FileManager.default.removeItem(at: directory(store).appendingPathComponent(name))
    }
}

/// The coin's logo everywhere in the tab.
struct CoinBadge: View {
    @EnvironmentObject private var wallet: WalletModel
    let coin: CoinSpec
    var size: CGFloat = 30

    var body: some View {
        Group {
            if let logo = CoinIcons.builtInLogo(coin.id) ?? coin.icon.flatMap({ CoinIcons.image($0, in: wallet.store) }) {
                Image(uiImage: logo).resizable().scaledToFill()
            } else {
                Text(verbatim: String(coin.ticker.prefix(4)))
                    .skyText(.labelSmall)
                    .foregroundStyle(Color.skyPrimary)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .background(Color.skySecondaryContainer)
            }
        }
        .frame(width: size, height: size)
        .clipShape(Circle())
        .accessibilityHidden(true)
    }
}

// MARK: Transactions

/// One transaction row: the recent card and the full history share it (Android: TxRow).
struct TxRow: View {
    let coin: CoinSpec
    let tx: CachedTx
    let showStatus: Bool
    var topDivider = false

    var body: some View {
        VStack(spacing: 0) {
            if topDivider {
                SkyDivider(color: .skyContainerHighest).padding(.horizontal, 16)
            }
            HStack(spacing: 13) {
                MaterialIcon(MI.outlinedArrowDownward, size: 16)
                    .foregroundStyle(Color.skyOnSurfaceVariant)
                    .rotationEffect(.degrees(tx.incoming ? 0 : 180))
                    .frame(width: 36, height: 36)
                    .background(Color.skyContainerHighest, in: Circle())
                VStack(alignment: .leading, spacing: 0) {
                    HStack(spacing: 7) {
                        Text(tx.incoming ? L10n.key("wallet_received_verb") : L10n.key("wallet_sent_verb")).skyText(.bodyLarge, bold: true)
                        if showStatus {
                            Circle().fill(txColor(tx)).frame(width: 6, height: 6)
                            Text(tx.confirmed ? L10n.key("wallet_tx_confirmed") : L10n.key("wallet_filter_pending"))
                                .skyText(.labelSmall).foregroundStyle(txColor(tx))
                        }
                    }
                    Text(verbatim: partyLine).skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).lineLimit(1)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                VStack(alignment: .trailing, spacing: 0) {
                    Text(verbatim: (tx.incoming ? "+" : "\u{2212}") + coin.amountText(tx.amount))
                        .skyText(.bodyLarge, bold: true).foregroundStyle(txColor(tx))
                    Text(verbatim: timeLabel(tx.timestamp)).skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                }
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 13)
            .contentShape(Rectangle())
        }
        .foregroundStyle(Color.skyOnSurface)
        .accessibilityElement(children: .combine)
    }

    private var partyLine: String {
        guard let party = tx.party else { return L10n.format("wallet_self_line", coin.ticker) }
        return tx.incoming
            ? L10n.format("wallet_from_line", shortAddress(party, head: 6, tail: 4), coin.ticker)
            : L10n.format("wallet_to_line", shortAddress(party, head: 6, tail: 4), coin.ticker)
    }
}

// MARK: QR

/// A receive address as a QR code: black on white whatever the theme —
/// scanners want the contrast. Drawn with Core Image's generator, pixels
/// scaled without smoothing.
struct QRCodeImage: View {
    let content: String

    var body: some View {
        if let image = Self.render(content) {
            Image(uiImage: image)
                .interpolation(.none)
                .resizable()
                .scaledToFit()
                .accessibilityLabel(Text(content))
        }
    }

    static func render(_ text: String) -> UIImage? {
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data(text.utf8)
        filter.correctionLevel = "M"
        guard let output = filter.outputImage else { return nil }
        let scaled = output.transformed(by: CGAffineTransform(scaleX: 12, y: 12))
        guard let cg = CIContext().createCGImage(scaled, from: scaled.extent) else { return nil }
        return UIImage(cgImage: cg)
    }
}

// MARK: The device-owner check

/// Asks for Face ID, Touch ID or the passcode before a send or a reveal —
/// or lets it through on a phone with nothing set up to ask with, as
/// Android's confirmWithBiometrics does (there is then nothing to check
/// against; the app lock's own check works the same way).
enum WalletAuth {
    private static let log = Logger(subsystem: Bundle.main.bundleIdentifier ?? "skywire", category: "wallet")

    static func confirm(reason: String) async -> Bool {
        let context = LAContext()
        guard context.canEvaluatePolicy(.deviceOwnerAuthentication, error: nil) else { return true }
        do {
            return try await context.evaluatePolicy(.deviceOwnerAuthentication, localizedReason: reason)
        } catch {
            // Why a send or a reveal did not go ahead (cancelled, failed,
            // not interactive): the LAError code, nothing else.
            log.notice("device-owner check refused: \((error as NSError).code, privacy: .public)")
            return false
        }
    }
}

// MARK: The recovery phrase on screen

/// What iOS offers in place of Android's FLAG_SECURE on the phrase screens.
/// No API keeps a screenshot from being taken, so: the words are covered
/// while the screen is recorded, mirrored or AirPlayed (UIScreen.isCaptured)
/// and whenever the app is not frontmost (so the app switcher's snapshot
/// holds no words); a screenshot, which can only be noticed after it is
/// saved, gets a warning to delete it.
struct SeedPrivacy: ViewModifier {
    @Environment(\.scenePhase) private var scenePhase
    @State private var captured = UIScreen.main.isCaptured
    @State private var screenshotTaken = false

    func body(content: Content) -> some View {
        content
            .overlay {
                if captured || scenePhase != .active {
                    ZStack {
                        Rectangle().fill(.ultraThickMaterial).ignoresSafeArea()
                        Image(systemName: "eye.slash").font(.system(size: 40)).foregroundStyle(.secondary)
                    }
                    .accessibilityIdentifier("seed-covered")
                }
            }
            .onReceive(NotificationCenter.default.publisher(for: UIScreen.capturedDidChangeNotification)) { _ in
                captured = UIScreen.main.isCaptured
            }
            .onReceive(NotificationCenter.default.publisher(for: UIApplication.userDidTakeScreenshotNotification)) { _ in
                screenshotTaken = true
            }
            .alert(Text("wallet_screenshot_title"), isPresented: $screenshotTaken) {
                Button("ok") {}
            } message: {
                Text("wallet_screenshot_body")
            }
    }
}

/// SeedGrid: the words in numbered pairs, left to right then down. Not selectable.
struct SeedGrid: View {
    let words: [String]

    var body: some View {
        VStack(spacing: 10) {
            ForEach(Array(stride(from: 0, to: words.count, by: 2)), id: \.self) { index in
                HStack(spacing: 10) {
                    cell(index)
                    if index + 1 < words.count { cell(index + 1) } else { Color.clear.frame(maxWidth: .infinity) }
                }
            }
        }
    }

    private func cell(_ index: Int) -> some View {
        HStack(spacing: 10) {
            Text(verbatim: "\(index + 1)").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).frame(width: 16, alignment: .leading)
            Text(verbatim: words[index]).skyText(.bodyLarge, bold: true)
                .accessibilityIdentifier("seed-word-\(index + 1)")
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 13)
        .frame(maxWidth: .infinity)
        .background(Color.skySurfaceVariant, in: .sky(SkyRadius.small))
    }
}

/// The amber banner: the numbers on screen are old, or incomplete.
struct WalletBanner: View {
    let text: String

    var body: some View {
        HStack(alignment: .top, spacing: 9) {
            MaterialIcon(MI.outlinedSchedule, size: 16)
            Text(verbatim: text).skyText(.bodySmall)
        }
        .foregroundStyle(Color.skyWarning)
        .padding(.horizontal, 14)
        .padding(.vertical, 13)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.skyWarning.opacity(0.12), in: .sky(SkyRadius.small))
    }
}

/// A wallet row: icon, bold title, value, chevron, on surfaceVariant (Wallets, Node).
struct WalletNavRow: View {
    let icon: String
    let title: LocalizedStringKey
    var value: String? = nil
    var valueMaxWidth: CGFloat? = nil
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 12) {
                MaterialIcon(icon, size: 18).foregroundStyle(Color.skyOnSurfaceVariant)
                Text(title).skyText(.bodyLarge, bold: true).frame(maxWidth: .infinity, alignment: .leading)
                if let value {
                    Text(verbatim: value).skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant).lineLimit(1)
                        .frame(maxWidth: valueMaxWidth, alignment: .trailing)
                }
                MaterialIcon(MI.outlinedKeyboardArrowRight, size: 18).foregroundStyle(Color.skyOnSurfaceVariant)
            }
            .foregroundStyle(Color.skyOnSurface)
            .padding(.horizontal, 16)
            .padding(.vertical, 15)
            .background(Color.skySurfaceVariant, in: .sky(SkyRadius.medium))
            .contentShape(RoundedRectangle(cornerRadius: SkyRadius.medium))
        }
        .buttonStyle(PressStyle(layer: .skyOnSurface, shape: AnyShape(RoundedRectangle(cornerRadius: SkyRadius.medium))))
    }
}

/// A wallet screen's frame: SkyTopBar over the content, the screen background behind.
struct WalletScreen<Content: View>: View {
    let title: Text
    var help: HelpTopic? = nil
    @ViewBuilder let content: Content
    @EnvironmentObject private var model: WalletModel
    @EnvironmentObject private var navigator: Navigator

    var body: some View {
        VStack(spacing: 0) {
            // A wallet screen pops the wallet's own stack; its root leaves the tab.
            SkyTopBar(title: title, onBack: {
                if model.path.isEmpty { navigator.back() } else { model.path.removeLast() }
            }, help: help)
            content.frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .background(Color.skyBackground)
    }
}
