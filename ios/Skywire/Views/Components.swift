import SwiftUI
import UIKit

extension Color {
    /// The brand blue: Android's primary in both themes.
    static let skywire = Color.skyPrimary
    static let success = Color.skySuccess
    static let warning = Color.skyWarning
}

/// A label on the left, a value on the right.
struct InfoRow: View {
    let label: Text
    let value: String
    var monospaced = false
    var valueColor: Color = .primary

    var body: some View {
        HStack(alignment: .firstTextBaseline) {
            label.foregroundStyle(.secondary).layoutPriority(1)
            Spacer(minLength: 12)
            // Wraps rather than truncates: a version or an error is read whole.
            Text(value)
                .font(monospaced ? .body.monospaced() : .body)
                .foregroundStyle(valueColor)
                .multilineTextAlignment(.trailing)
                .fixedSize(horizontal: false, vertical: true)
                .textSelection(.enabled)
        }
    }
}

enum Format {
    /// A public key as a screen shows it: the first ten and the last eight
    /// characters (Android: shortPk).
    static func shortPK(_ pk: String) -> String {
        pk.count <= 20 ? pk : "\(pk.prefix(10))…\(pk.suffix(8))"
    }

    /// "1d 2h 3m 4s", each unit from the catalogue, larger units only once
    /// they are non-zero (Android: formatUptime).
    static func uptime(_ seconds: Double) -> String {
        let total = Int(seconds)
        let days = total / 86_400
        let hours = total % 86_400 / 3_600
        let minutes = total % 3_600 / 60
        var parts: [String] = []
        if days > 0 { parts.append(L10n.format("unit_days", String(days))) }
        if hours > 0 || days > 0 { parts.append(L10n.format("unit_hours", String(hours))) }
        if minutes > 0 || hours > 0 || days > 0 { parts.append(L10n.format("unit_minutes", String(minutes))) }
        parts.append(L10n.format("unit_seconds", String(total % 60)))
        return parts.joined(separator: L10n.text("unit_separator"))
    }

    /// "1h 04m 12s", "4m 05s", "9s": zero-padded after the leading unit (Android: formatDuration).
    static func duration(_ seconds: Double) -> String {
        let total = max(0, Int(seconds))
        let h = total / 3_600, m = total % 3_600 / 60, s = total % 60
        let pad = { (n: Int) in String(format: "%02d", n) }
        var parts: [String] = []
        if h > 0 {
            parts = [L10n.format("unit_hours", String(h)), L10n.format("unit_minutes", pad(m)), L10n.format("unit_seconds", pad(s))]
        } else if m > 0 {
            parts = [L10n.format("unit_minutes", String(m)), L10n.format("unit_seconds", pad(s))]
        } else {
            parts = [L10n.format("unit_seconds", String(s))]
        }
        return parts.joined(separator: L10n.text("unit_separator"))
    }

    /// `text` for display with a break opportunity between every character
    /// (a zero-width space), so a key wraps anywhere without the hyphen
    /// iOS would otherwise insert, which reads as part of the key. Display
    /// only: copy the original.
    static func breakable(_ text: String) -> String {
        text.map(String.init).joined(separator: "\u{200B}")
    }

    /// A two-letter country code as its flag; nil for anything else
    /// (Android: flagEmoji).
    static func flag(_ country: String) -> String? {
        let code = country.uppercased()
        guard code.count == 2, code.allSatisfy({ ("A"..."Z").contains($0) }) else { return nil }
        let base: UInt32 = 0x1F1E6 // REGIONAL INDICATOR SYMBOL LETTER A
        return String(String.UnicodeScalarView(code.unicodeScalars.compactMap { Unicode.Scalar(base + $0.value - 65) }))
    }

    /// Bytes as B, KB, MB, GB or TB with one decimal (Android: formatBytes).
    static func bytes(_ count: Int64) -> String {
        guard count >= 1024 else { return "\(count) B" }
        var value = Double(count) / 1024
        let units = ["KB", "MB", "GB", "TB"]
        var unit = 0
        while value >= 1024, unit < units.count - 1 {
            value /= 1024
            unit += 1
        }
        return String(format: "%.1f %@", locale: Locale(identifier: "en_US_POSIX"), value, units[unit])
    }

    /// The visor's words for what an app is doing (`detailed_status`,
    /// pkg/app/appserver/app_state.go), translated where the catalogue knows
    /// them, passed through otherwise (Android: appStatusText).
    static func appStatus(_ detail: String) -> String {
        switch detail.lowercased() {
        case AppDetail.starting: L10n.text("app_status_starting")
        case AppDetail.running: L10n.text("app_status_running")
        case AppDetail.connecting: L10n.text("app_status_connecting")
        case AppDetail.reconnecting: L10n.text("app_status_reconnecting")
        case AppDetail.shuttingDown: L10n.text("app_status_shutting_down")
        case AppDetail.stopped: L10n.text("app_status_stopped")
        default: detail
        }
    }

    /// A service's health word, translated where the catalogue knows it; the
    /// visor's own word otherwise (Android: healthText).
    static func health(_ status: String) -> String {
        switch status.lowercased() {
        case "healthy": L10n.text("health_healthy")
        case "unhealthy": L10n.text("health_unhealthy")
        case "connecting": L10n.text("health_connecting")
        case "error": L10n.text("health_error")
        default: status
        }
    }
}

/// The visor's `detailed_status` words, lowercased.
enum AppDetail {
    static let starting = "starting"
    static let running = "running"
    static let connecting = "connecting"
    static let reconnecting = "connection failed, reconnecting"
    static let shuttingDown = "shutting down"
    static let stopped = "stopped"
}

/// The string catalogue from code that is not a view. Its keys are Android's
/// (values/strings.xml), so the three languages seed from the Android
/// catalogues (`ios/scripts/seed-strings.py`); views use the same keys through
/// `Text("key")`. The catalogue test in SkywireTests checks that every key the
/// sources use exists in all three languages with matching placeholders.
enum L10n {
    /// A key for a view, chosen at run time. Write the key as a literal at the
    /// call (a switch over literals, not an interpolation): the catalogue test
    /// finds keys by reading the sources, and `LocalizedStringKey("a_\(b)")`
    /// would look up "a_%@".
    static func key(_ key: String) -> LocalizedStringKey {
        LocalizedStringKey(key)
    }

    static func text(_ key: String) -> String {
        NSLocalizedString(key, comment: "")
    }

    /// The key's text with `arguments` in its placeholders (`%1$@`, `%1$lld`:
    /// Android's `%1$s` and `%1$d`, converted when seeded).
    static func format(_ key: String, _ arguments: any CVarArg...) -> String {
        String(format: NSLocalizedString(key, comment: ""), locale: .current, arguments: arguments)
    }
}
