@testable import Skywire
import XCTest

/// The chat page's language follows the app's, System included (Android: ChatPageLangTest).
@MainActor
final class ChatPageLangTests: XCTestCase {
    nonisolated(unsafe) private var saved: Any?

    override func setUp() {
        saved = UserDefaults.standard.object(forKey: "AppleLanguages")
    }

    override func tearDown() {
        if let saved {
            UserDefaults.standard.set(saved, forKey: "AppleLanguages")
        } else {
            UserDefaults.standard.removeObject(forKey: "AppleLanguages")
        }
    }

    func testExplicitChoicesNameTheirLanguage() {
        XCTAssertEqual(ChatPage.pageLang(.chineseSimplified), "zh")
        XCTAssertEqual(ChatPage.pageLang(.spanish), "es")
        XCTAssertEqual(ChatPage.pageLang(.french), "fr")
        XCTAssertNil(ChatPage.pageLang(.english))
    }

    /// System on a phone in each shipped language loads what the explicit choice loads, and a
    /// phone in a language the app does not ship gets the English page.
    func testSystemFollowsThePhone() {
        for language in AppLanguage.allCases where language != .system {
            UserDefaults.standard.set([language.tag!], forKey: "AppleLanguages")
            XCTAssertEqual(ChatPage.pageLang(.system), ChatPage.pageLang(language), "phone in \(language.tag!)")
        }
        UserDefaults.standard.set(["de"], forKey: "AppleLanguages")
        XCTAssertNil(ChatPage.pageLang(.system))
    }

    func testTheLanguageRidesInTheURL() {
        let base = URL(string: "http://127.0.0.1:8001/")!
        XCTAssertEqual(ChatPage.themed(base, dark: true, lang: "fr").absoluteString, "http://127.0.0.1:8001/?theme=dark&lang=fr")
        XCTAssertEqual(ChatPage.themed(base, dark: false, lang: nil).absoluteString, "http://127.0.0.1:8001/?theme=light")
    }
}
