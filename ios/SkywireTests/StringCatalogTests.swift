import Foundation
import XCTest

/// The app's string catalogues, read from the source tree (playbook item
/// 2.8, Android's TranslationCatalogTest idea): every key in English,
/// Simplified Chinese and Spanish, the same placeholders in each, and exactly
/// the keys the Swift sources use. ios/scripts/seed-strings.py writes the
/// catalogues; this is what keeps them honest.
final class StringCatalogTests: XCTestCase {
    static let languages = ["en", "zh-Hans", "es"]

    /// ios/, from this file's path. The Simulator reads the Mac's disk.
    static let iosDir = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()

    struct Catalogue: Decodable {
        struct Entry: Decodable {
            struct Localization: Decodable {
                struct Unit: Decodable {
                    let state: String
                    let value: String
                }

                let stringUnit: Unit
            }

            let localizations: [String: Localization]?
        }

        let sourceLanguage: String
        let strings: [String: Entry]
    }

    static func catalogue(_ name: String) throws -> Catalogue {
        let url = iosDir.appendingPathComponent("Skywire/\(name).xcstrings")
        return try JSONDecoder().decode(Catalogue.self, from: Data(contentsOf: url))
    }

    func testEveryKeyHasTheThreeLanguages() throws {
        for name in ["Localizable", "InfoPlist"] {
            let catalogue = try Self.catalogue(name)
            XCTAssertEqual(catalogue.sourceLanguage, "en")
            XCTAssertFalse(catalogue.strings.isEmpty, name)
            for (key, entry) in catalogue.strings {
                let localizations = entry.localizations ?? [:]
                let english = localizations["en"]?.stringUnit.value ?? ""
                for language in Self.languages {
                    guard let unit = localizations[language]?.stringUnit else {
                        XCTFail("\(name): \(key) has no \(language)")
                        continue
                    }
                    XCTAssertEqual(unit.state, "translated", "\(name): \(key) \(language)")
                    // Only a separator may be empty, where a language needs
                    // none (Chinese joins "1天2小时" with nothing).
                    if !english.trimmingCharacters(in: .whitespaces).isEmpty {
                        XCTAssertFalse(unit.value.isEmpty, "\(name): \(key) \(language) is empty")
                    }
                }
            }
        }
    }

    func testPlaceholdersMatchInEveryLanguage() throws {
        let catalogue = try Self.catalogue("Localizable")
        for (key, entry) in catalogue.strings {
            let localizations = entry.localizations ?? [:]
            let english = Self.placeholders(localizations["en"]?.stringUnit.value ?? "")
            for language in Self.languages.dropFirst() {
                let translated = Self.placeholders(localizations[language]?.stringUnit.value ?? "")
                XCTAssertEqual(translated, english, "\(key) \(language)")
            }
            // Android's %s and %d never reach iOS unconverted.
            XCTAssertFalse(english.contains { $0.hasSuffix("s") || $0.hasSuffix("$d") || $0 == "%d" }, "\(key): \(english)")
        }
    }

    /// Every key the sources use is in the catalogue, and nothing else is.
    func testTheCatalogueHasExactlyTheKeysTheSourcesUse() throws {
        let catalogue = Set(try Self.catalogue("Localizable").strings.keys)
        let used = try Self.usedKeys()
        XCTAssertFalse(used.isEmpty)
        XCTAssertEqual(used.subtracting(catalogue).sorted(), [], "used but not in the catalogue: run ios/scripts/seed-strings.py")
        XCTAssertEqual(catalogue.subtracting(used).sorted(), [], "in the catalogue but unused: run ios/scripts/seed-strings.py")
    }

    /// The placeholders of `text`, sorted: `%1$@`, `%lld`, …
    static func placeholders(_ text: String) -> [String] {
        let pattern = try! NSRegularExpression(pattern: "%(?:\\d+\\$)?(?:@|lld|ld|d|s|%)")
        return pattern.matches(in: text, range: NSRange(text.startIndex..., in: text))
            .map { (text as NSString).substring(with: $0.range) }
            .filter { $0 != "%%" }
            .sorted()
    }

    /// The keys the app's sources use, found as seed-strings.py finds them.
    static func usedKeys() throws -> Set<String> {
        let trigger = try NSRegularExpression(
            pattern: "\\b(?:Text|Label|Button|Toggle|Section|Picker|TextField|LocalizedStringKey|L10n\\.(?:text|format|key)|navigationTitle)\\("
        )
        let literal = try NSRegularExpression(pattern: "(?<!systemImage: )(?<!systemName: )\"([a-z][a-z0-9_]*)\"(?!\\s*:)")
        var keys = Set<String>()
        let sources = iosDir.appendingPathComponent("Skywire")
        let files = FileManager.default.enumerator(at: sources, includingPropertiesForKeys: nil)?
            .compactMap { $0 as? URL }.filter { $0.pathExtension == "swift" } ?? []
        XCTAssertFalse(files.isEmpty, "no sources under \(sources.path)")
        for file in files {
            for line in try String(contentsOf: file, encoding: .utf8).components(separatedBy: "\n") {
                let range = NSRange(line.startIndex..., in: line)
                guard !line.trimmingCharacters(in: .whitespaces).hasPrefix("//"),
                      trigger.firstMatch(in: line, range: range) != nil
                else { continue }
                for match in literal.matches(in: line, range: range) {
                    keys.insert((line as NSString).substring(with: match.range(at: 1)))
                }
            }
        }
        return keys
    }
}
