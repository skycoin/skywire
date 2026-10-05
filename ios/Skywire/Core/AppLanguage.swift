import Foundation

/// The language the interface is drawn in (Android: core/AppLanguage.kt, the same stored names).
/// Only the app follows it: logs and what the visor reports stay as written.
enum AppLanguage: String, CaseIterable {
    case system = "SYSTEM", english = "ENGLISH", chineseSimplified = "CHINESE_SIMPLIFIED", spanish = "SPANISH"

    /// The localisation it names in the bundle; nil for the phone's choice.
    var tag: String? {
        switch self {
        case .system: nil
        case .english: "en"
        case .chineseSimplified: "zh-Hans"
        case .spanish: "es"
        }
    }

    /// The localisation in force: the chosen one, or the one iOS would pick for this app now.
    /// Not `Bundle.main.preferredLocalizations`: that is fixed at launch, by the choice just undone.
    var resolvedTag: String {
        if let tag { return tag }
        let phone = UserDefaults.standard.stringArray(forKey: "AppleLanguages") ?? Locale.preferredLanguages
        let shipped = Bundle.main.localizations.filter { $0 != "Base" }
        return Bundle.preferredLocalizations(from: shipped, forPreferences: phone).first ?? "en"
    }

    var locale: Locale { Locale(identifier: resolvedTag) }

    /// The strings to draw with. iOS reads AppleLanguages only at launch, so the bundle is
    /// switched here and in the views' locale for the change to show at once.
    var bundle: Bundle {
        Bundle.main.path(forResource: resolvedTag, ofType: "lproj").flatMap(Bundle.init(path:)) ?? .main
    }

    /// Records the choice where iOS keeps an app's language too (Settings ▸ Skywire ▸ Language),
    /// so a relaunch and that page agree with it.
    func persist(in defaults: UserDefaults = .standard) {
        if let tag {
            defaults.set([tag], forKey: "AppleLanguages")
        } else {
            defaults.removeObject(forKey: "AppleLanguages")
        }
    }

    /// What iOS has for this app now: a choice made in its Settings page shows here as well.
    static func current(in defaults: UserDefaults = .standard) -> AppLanguage {
        guard let first = (defaults.persistentDomain(forName: Bundle.main.bundleIdentifier ?? "")?["AppleLanguages"] as? [String])?.first
        else { return .system }
        let language = Locale(identifier: first).language.languageCode?.identifier
        return allCases.first { $0.tag.map { Locale(identifier: $0).language.languageCode?.identifier == language } ?? false } ?? .system
    }
}
