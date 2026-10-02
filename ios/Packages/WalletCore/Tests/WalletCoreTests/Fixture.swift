import Foundation
import XCTest

/// The Kotlin suites' resources, copied byte for byte from
/// android/wallet-core/src/test/resources into Tests/Fixtures.
enum Fixture {
    static func data(_ name: String) throws -> Data {
        let url = try XCTUnwrap(
            Bundle.module.url(forResource: name, withExtension: nil, subdirectory: "Fixtures"),
            "fixture \(name) missing"
        )
        return try Data(contentsOf: url)
    }

    static func decode<T: Decodable>(_ type: T.Type, _ name: String) throws -> T {
        try JSONDecoder().decode(type, from: data(name))
    }
}

func bytes(_ hex: String, file: StaticString = #filePath, line: UInt = #line) -> [UInt8] {
    guard let b = [UInt8](hex: hex) else {
        XCTFail("bad hex in test: \(hex)", file: file, line: line)
        return []
    }
    return b
}
