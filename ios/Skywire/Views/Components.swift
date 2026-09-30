import SwiftUI
import UIKit

extension Color {
    /// The brand blue (Android's SkywireBlue), lighter in dark mode as the
    /// Android theme's dark primary is.
    static let skywire = Color(UIColor { traits in
        traits.userInterfaceStyle == .dark
            ? UIColor(red: 0x4A / 255, green: 0xA3 / 255, blue: 1, alpha: 1)
            : UIColor(red: 0, green: 0x72 / 255, blue: 1, alpha: 1)
    })

    /// Connected / healthy, and in-progress (Android's SkyAccents).
    static let success = Color.green
    static let warning = Color.orange
}

/// A small filled circle in a state's colour.
struct StatusDot: View {
    let color: Color

    var body: some View {
        Circle().fill(color).frame(width: 10, height: 10)
    }
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
