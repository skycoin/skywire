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

                struct Variant: Decodable {
                    let stringUnit: Unit
                }

                struct Variations: Decodable {
                    let plural: [String: Variant]?
                }

                /// A plain string…
                let stringUnit: Unit?
                /// …or a plural's forms by quantity (an Android <plurals>).
                let variations: Variations?

                /// The text the checks read: the string, or the plural's
                /// "other" form, which every language has.
                var primary: Unit? { stringUnit ?? variations?.plural?["other"]?.stringUnit }

                /// Every text in it: the string, or each plural form.
                var units: [Unit] { stringUnit.map { [$0] } ?? variations?.plural?.values.map(\.stringUnit) ?? [] }
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
                let english = localizations["en"]?.primary?.value ?? ""
                for language in Self.languages {
                    guard let localization = localizations[language], localization.primary != nil else {
                        XCTFail("\(name): \(key) has no \(language)")
                        continue
                    }
                    for unit in localization.units {
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
    }

    func testPlaceholdersMatchInEveryLanguage() throws {
        let catalogue = try Self.catalogue("Localizable")
        for (key, entry) in catalogue.strings {
            let localizations = entry.localizations ?? [:]
            let english = Self.placeholders(localizations["en"]?.primary?.value ?? "")
            for language in Self.languages.dropFirst() {
                let translated = Self.placeholders(localizations[language]?.primary?.value ?? "")
                XCTAssertEqual(translated, english, "\(key) \(language)")
            }
            // A plural's other forms may leave the count out ("one wallet"),
            // never bring in something "other" does not have.
            for (language, localization) in localizations {
                let other = Set(Self.placeholders(localization.primary?.value ?? ""))
                for unit in localization.units {
                    XCTAssertTrue(Set(Self.placeholders(unit.value)).isSubset(of: other), "\(key) \(language): \(unit.value)")
                }
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

    /// A literal that names a known string is one the scan sees. A key
    /// reaching a LocalizedStringKey any other way (a parameter, a switch's
    /// result) shows as the raw key on screen (G6).
    func testEveryKeyLiteralIsSeenByTheScan() throws {
        let known = try Self.knownKeys()
        let used = try Self.usedKeys()
        let anyLiteral = try NSRegularExpression(pattern: "\"([a-z][a-z0-9_]*)\"")
        var unseen: [String] = []
        for (file, number, line) in try Self.sourceLines() {
            let range = NSRange(line.startIndex..., in: line)
            for match in anyLiteral.matches(in: line, range: range) {
                let key = (line as NSString).substring(with: match.range(at: 1))
                if known.contains(key), !used.contains(key) {
                    unseen.append("\(file):\(number): \(key)")
                }
            }
        }
        XCTAssertEqual(unseen, [], "write these through L10n.key or L10n.text")
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
        // The first branch of `L10n.key(flag ? "a" : "b")`, which `literal`
        // takes for a label.
        let ternary = try NSRegularExpression(pattern: "L10n\\.(?:text|format|key)\\([^\"]*\\? \"([a-z][a-z0-9_]*)\"\\s*:")
        var keys = Set<String>()
        for (_, _, line) in try sourceLines() {
            let range = NSRange(line.startIndex..., in: line)
            guard trigger.firstMatch(in: line, range: range) != nil else { continue }
            for match in literal.matches(in: line, range: range) + ternary.matches(in: line, range: range) {
                keys.insert((line as NSString).substring(with: match.range(at: 1)))
            }
        }
        return keys
    }

    /// Every non-comment line of the app's sources, with its file and number.
    static func sourceLines() throws -> [(file: String, number: Int, line: String)] {
        let sources = iosDir.appendingPathComponent("Skywire")
        let files = FileManager.default.enumerator(at: sources, includingPropertiesForKeys: nil)?
            .compactMap { $0 as? URL }.filter { $0.pathExtension == "swift" } ?? []
        XCTAssertFalse(files.isEmpty, "no sources under \(sources.path)")
        var lines: [(file: String, number: Int, line: String)] = []
        for file in files {
            for (index, line) in try String(contentsOf: file, encoding: .utf8).components(separatedBy: "\n").enumerated()
            where !line.trimmingCharacters(in: .whitespaces).hasPrefix("//") {
                lines.append((file.lastPathComponent, index + 1, line))
            }
        }
        return lines
    }

    /// Every string the catalogue can be seeded with: Android's names and
    /// the iOS-only ones (seed-strings.py's two sources).
    static func knownKeys() throws -> Set<String> {
        let strings = iosDir.appendingPathComponent("../android/app/src/main/res/values/strings.xml")
        let xml = try String(contentsOf: strings, encoding: .utf8)
        let name = try NSRegularExpression(pattern: "<(?:string|plurals) name=\"([^\"]+)\"")
        var keys = Set(name.matches(in: xml, range: NSRange(xml.startIndex..., in: xml))
            .map { (xml as NSString).substring(with: $0.range(at: 1)) })
        let supplement = try JSONSerialization.jsonObject(
            with: Data(contentsOf: iosDir.appendingPathComponent("scripts/strings-ios.json"))
        ) as? [String: Any]
        if let iosOnly = supplement?["Localizable"] as? [String: Any] {
            keys.formUnion(iosOnly.keys)
        }
        return keys
    }
}
