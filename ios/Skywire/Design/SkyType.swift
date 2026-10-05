import SwiftUI
import UIKit

// Android's type scale (ui/theme/Type.kt): Material 3 1.4.0 sizes, Quicksand Bold for
// titles, Nunito SemiBold for body and Nunito Bold for labels.

enum SkyTextStyle {
    case displayLarge, displayMedium, displaySmall
    case headlineLarge, headlineMedium, headlineSmall
    case titleLarge, titleMedium, titleSmall
    case bodyLarge, bodyMedium, bodySmall
    case labelLarge, labelMedium, labelSmall

    /// PostScript name, size, line height and tracking, all in points.
    var spec: (font: String, size: CGFloat, line: CGFloat, tracking: CGFloat) {
        switch self {
        case .displayLarge: ("Quicksand-Bold", 57, 64, -0.2)
        case .displayMedium: ("Quicksand-Bold", 45, 52, 0)
        case .displaySmall: ("Quicksand-Bold", 36, 44, 0)
        case .headlineLarge: ("Quicksand-Bold", 32, 40, 0)
        case .headlineMedium: ("Quicksand-Bold", 28, 36, 0)
        case .headlineSmall: ("Quicksand-Bold", 24, 32, 0)
        case .titleLarge: ("Quicksand-Bold", 22, 28, 0)
        case .titleMedium: ("Quicksand-Bold", 16, 24, 0.2)
        case .titleSmall: ("Quicksand-Bold", 14, 20, 0.1)
        case .bodyLarge: ("Nunito-SemiBold", 16, 24, 0.5)
        case .bodyMedium: ("Nunito-SemiBold", 14, 20, 0.2)
        case .bodySmall: ("Nunito-SemiBold", 12, 16, 0.4)
        case .labelLarge: ("Nunito-Bold", 14, 20, 0.1)
        case .labelMedium: ("Nunito-Bold", 12, 16, 0.5)
        case .labelSmall: ("Nunito-Bold", 11, 16, 0.5)
        }
    }

    /// The style's font at the current text size (Android's sp follow the system font scale).
    func uiFont(mono: Bool = false) -> UIFont {
        let spec = spec
        let base = mono
            ? UIFont.monospacedSystemFont(ofSize: spec.size, weight: .semibold)
            : UIFont(name: spec.font, size: spec.size) ?? .systemFont(ofSize: spec.size)
        return UIFontMetrics(forTextStyle: .body).scaledFont(for: base)
    }
}

private struct SkyTextModifier: ViewModifier {
    let style: SkyTextStyle
    let mono: Bool
    var tracking: CGFloat?
    // Read so a text-size change redraws with the new metrics.
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize

    func body(content: Content) -> some View {
        let font = style.uiFont(mono: mono)
        let line = UIFontMetrics(forTextStyle: .body).scaledValue(for: style.spec.line)
        // Material centres glyphs in a fixed line box: pad to it.
        let extra = max(0, line - font.lineHeight)
        content
            .font(Font(font))
            .tracking(tracking ?? style.spec.tracking)
            .lineSpacing(extra)
            .padding(.vertical, extra / 2)
    }
}

extension View {
    func skyText(_ style: SkyTextStyle, mono: Bool = false, tracking: CGFloat? = nil) -> some View {
        modifier(SkyTextModifier(style: style, mono: mono, tracking: tracking))
    }
}
