import SwiftUI
import UIKit

// Android's palette (ui/theme/Theme.kt): brand-locked, one scheme per appearance.

extension Color {
    init(light: UInt32, dark: UInt32) {
        self.init(UIColor { $0.userInterfaceStyle == .dark ? UIColor(hex: dark) : UIColor(hex: light) })
    }

    init(hex: UInt32, opacity: Double = 1) {
        self.init(UIColor(hex: hex).withAlphaComponent(opacity))
    }

    static let skyPrimary = Color(light: 0x0F7BF4, dark: 0x4AA3FF)
    static let skyOnPrimary = Color(light: 0xFFFFFF, dark: 0x04264D)
    static let skyBackground = Color(light: 0xFFFFFF, dark: 0x0A101C)
    static let skyOnBackground = Color(light: 0x0B1526, dark: 0xECF2FB)
    static let skyOnSurface = Color(light: 0x0B1526, dark: 0xECF2FB)
    static let skySurfaceVariant = Color(light: 0xFAFCFF, dark: 0x121C2F)
    static let skyOnSurfaceVariant = Color(light: 0x44536B, dark: 0x93A3BD)
    static let skyOutline = Color(light: 0x56657C, dark: 0x64758F)
    static let skyOutlineVariant = Color(light: 0xE7EEF9, dark: 0x1E2B45)
    static let skyPrimaryContainer = Color(light: 0xE4EEFD, dark: 0x0E3D77)
    static let skyOnPrimaryContainer = Color(light: 0x0B57C9, dark: 0xCFE4FF)
    static let skySecondaryContainer = Color(light: 0xEEF3FB, dark: 0x16233B)
    static let skyOnSecondaryContainer = Color(light: 0x101A2B, dark: 0xD7E4F6)
    static let skyContainerLowest = Color(light: 0xFFFFFF, dark: 0x070D17)
    static let skyContainerLow = Color(light: 0xFBFCFE, dark: 0x0C1424)
    static let skyContainer = Color(light: 0xF6F9FD, dark: 0x101A2E)
    static let skyContainerHigh = Color(light: 0xF0F5FC, dark: 0x16223A)
    static let skyContainerHighest = Color(light: 0xE9EFF8, dark: 0x1C2A46)
    // Material 3 baseline roles the theme leaves alone (they show on Android).
    static let skyError = Color(light: 0xB3261E, dark: 0xF2B8B5)
    static let skyErrorContainer = Color(light: 0xF9DEDC, dark: 0x8C1D18)
    static let skyOnErrorContainer = Color(light: 0x410E0B, dark: 0xF9DEDC)
    static let skyInverseSurface = Color(light: 0x322F35, dark: 0xE6E0E9)
    static let skyInverseOnSurface = Color(light: 0xF5EFF7, dark: 0x322F35)

    // SkyAccents: identical in both themes.
    static let skySuccess = Color(hex: 0x22C275)
    static let skySuccessBright = Color(hex: 0x5CF2A8)
    static let skyWarning = Color(hex: 0xF59E0B)
    static let skyDangerBright = Color(hex: 0xFF7B6E)
    /// The logo bitmap's own blue, drawn untinted on the lock and launch screens.
    static let skyLogo = Color(hex: 0x0072FF)
}

extension UIColor {
    convenience init(hex: UInt32) {
        self.init(red: CGFloat(hex >> 16 & 0xFF) / 255, green: CGFloat(hex >> 8 & 0xFF) / 255,
                  blue: CGFloat(hex & 0xFF) / 255, alpha: 1)
    }
}

enum SkyGradient {
    /// SkyHeroGradient: hero surfaces (the SkyVPN card).
    static let hero = LinearGradient(colors: [Color(hex: 0x1685FA), Color(hex: 0x0B57C9), Color(hex: 0x0A3F97)],
                                     startPoint: .topLeading, endPoint: .bottomTrailing)
    /// SkyButtonGradient: the round primary actions (Connect, the bar's cloud).
    static let button = LinearGradient(colors: [Color(hex: 0x3D9BFF), Color(hex: 0x0F7BF4), Color(hex: 0x0B4FBC)],
                                       startPoint: .topLeading, endPoint: .bottomTrailing)
}

/// Android's Shapes scale (dp = pt).
enum SkyRadius {
    static let extraSmall: CGFloat = 8
    static let small: CGFloat = 12
    static let medium: CGFloat = 16
    static let large: CGFloat = 22
    static let extraLarge: CGFloat = 28
}

extension Shape where Self == RoundedRectangle {
    static func sky(_ radius: CGFloat) -> RoundedRectangle {
        RoundedRectangle(cornerRadius: radius, style: .circular)
    }
}

extension View {
    /// Android's elevation shadow, approximated (no exact iOS equivalent).
    func skyElevation(_ dp: CGFloat) -> some View {
        shadow(color: .black.opacity(dp == 0 ? 0 : 0.10 + dp * 0.008), radius: dp, y: dp / 2)
    }
}

/// Settings ▸ Theme (Android ThemeMode).
enum ThemeMode: String, CaseIterable {
    case system, light, dark

    var colorScheme: ColorScheme? {
        switch self {
        case .system: nil
        case .light: .light
        case .dark: .dark
        }
    }
}
