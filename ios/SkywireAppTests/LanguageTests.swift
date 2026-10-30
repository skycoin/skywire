@testable import Skywire
import XCTest

/// The language chosen in Settings decides a plural's form, not the phone's locale.
@MainActor
final class LanguageTests: XCTestCase {
    private var bundle: Bundle!
    private var locale: Locale!

    override func setUp() {
        bundle = L10n.bundle
        locale = L10n.locale
    }

    override func tearDown() {
        L10n.bundle = bundle
        L10n.locale = locale
    }

    /// Spanish has a "many" form English lacks: under the phone's English rules a million hops
    /// would read "1,000,000 saltos", in Spanish's own "1.000.000 de saltos".
    func testPluralsFollowTheAppsLanguage() throws {
        let suite = "LanguageTests-\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let settings = AppSettings(defaults: defaults)

        settings.language = .spanish
        XCTAssertEqual(L10n.format("hub_hops", 1_000_000), "1.000.000 de saltos")
        XCTAssertEqual(L10n.format("hub_hops", 1), "1 salto")
        settings.language = .english
        XCTAssertEqual(L10n.format("hub_hops", 1), "1 hop")
        XCTAssertEqual(L10n.format("hub_hops", 2), "2 hops")
    }
}
