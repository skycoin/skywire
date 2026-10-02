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
    if !tx.confirmed { return .warning }
    return tx.incoming ? .success : .primary
}

/// "Today", "Yesterday", or the day and month in the phone's language.
func dayLabel(_ timestamp: Int64) -> String {
    let date = Date(timeIntervalSince1970: TimeInterval(timestamp))
    let calendar = Calendar.current
    if calendar.isDateInToday(date) { return L10n.text("wallet_day_today") }
    if calendar.isDateInYesterday(date) { return L10n.text("wallet_day_yesterday") }
    return date.formatted(.dateTime.day().month(.wide))
}

/// The time of day, as the phone's 12/24-hour setting writes it.
func timeLabel(_ timestamp: Int64) -> String {
    Date(timeIntervalSince1970: TimeInterval(timestamp)).formatted(date: .omitted, time: .shortened)
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
                Text(String(coin.ticker.prefix(4)))
                    .font(.system(size: size * 0.3, weight: .bold))
                    .foregroundStyle(Color.skywire)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .background(Color.skywire.opacity(0.15))
            }
        }
        .frame(width: size, height: size)
        .clipShape(Circle())
        .accessibilityHidden(true)
    }
}

// MARK: Transactions

/// One transaction row — recent activity and full history share it.
struct TxRow: View {
    let coin: CoinSpec
    let tx: CachedTx
    let showStatus: Bool

    var body: some View {
        HStack(spacing: 13) {
            Image(systemName: tx.incoming ? "arrow.down" : "arrow.up")
                .font(.footnote.weight(.semibold))
                .foregroundStyle(.secondary)
                .frame(width: 34, height: 34)
                .background(Circle().fill(Color(.tertiarySystemFill)))
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 7) {
                    Text(tx.incoming ? L10n.key("wallet_received_verb") : L10n.key("wallet_sent_verb")).font(.body.weight(.semibold))
                    if showStatus {
                        Circle().fill(txColor(tx)).frame(width: 6, height: 6)
                        Text(tx.confirmed ? L10n.key("wallet_tx_confirmed") : L10n.key("wallet_filter_pending"))
                            .font(.caption)
                            .foregroundStyle(txColor(tx))
                    }
                }
                Text(partyLine)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer(minLength: 8)
            VStack(alignment: .trailing, spacing: 2) {
                Text((tx.incoming ? "+" : "−") + coin.amountText(tx.amount))
                    .font(.body.weight(.semibold))
                    .foregroundStyle(txColor(tx))
                Text(timeLabel(tx.timestamp)).font(.footnote).foregroundStyle(.secondary)
            }
        }
        .padding(.vertical, 2)
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

/// The two-column numbered word grid — backup and reveal share it. No copy:
/// the words are not selectable and there is no button.
struct SeedGrid: View {
    let words: [String]

    var body: some View {
        LazyVGrid(columns: [GridItem(.flexible(), spacing: 10), GridItem(.flexible(), spacing: 10)], spacing: 10) {
            ForEach(Array(words.enumerated()), id: \.offset) { index, word in
                HStack(spacing: 10) {
                    Text("\(index + 1)")
                        .font(.footnote.monospacedDigit())
                        .foregroundStyle(.secondary)
                        .frame(width: 20, alignment: .trailing)
                    Text(word).font(.body.weight(.semibold))
                        .accessibilityIdentifier("seed-word-\(index + 1)")
                    Spacer(minLength: 0)
                }
                .padding(.horizontal, 12)
                .padding(.vertical, 11)
                .background(RoundedRectangle(cornerRadius: 12).fill(Color(.secondarySystemBackground)))
            }
        }
    }
}

/// An amber note above the balance: the numbers on screen are old, or
/// incomplete.
struct WalletBanner: View {
    let text: String

    var body: some View {
        HStack(alignment: .top, spacing: 9) {
            Image(systemName: "clock").font(.footnote)
            Text(text).font(.footnote)
        }
        .foregroundStyle(Color.warning)
        .padding(.horizontal, 14)
        .padding(.vertical, 12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(RoundedRectangle(cornerRadius: 12).fill(Color.warning.opacity(0.12)))
    }
}
