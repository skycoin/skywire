import Foundation
import XCTest

/// The files the Kotlin suites read, copied byte for byte into
/// Tests/Fixtures. Where each comes from:
///
/// - btc-fixtures.json: android/wallet-core/src/test/resources.
/// - create.golden: skycoin's src/transaction/testdata, read by
///   SkycoinCreateFixtureTest through SkycoinVectors.
/// - input-hashes.golden, seed-0000.golden … seed-0002.golden: skycoin's
///   src/cipher/testsuite/testdata, read by SkycoinGoldenTest.
/// - test6-bip44.wlt, test6-passphrase-bip44.wlt: skycoin's
///   src/wallet/testdata, read by SkycoinBip44Test.
/// - skycoin-fixtures.json: the older combined copy the Kotlin suite read
///   before it moved to create.golden and btc-fixtures.json; kept, and
///   checked to hold the same vectors.
///
/// The skycoin files are from github.com/skycoin/skycoin at the version
/// go.mod pins, v0.28.7-0.20261022015045-3b3712d25ce0. Copy them again from
/// `$(go env GOMODCACHE)/github.com/skycoin/skycoin@<version>/` when it moves.
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

/// The error `body` throws, or nil when it returns: Kotlin's
/// assertFailsWith for async code, which XCTAssertThrowsError cannot take.
func thrown<T>(_ body: () async throws -> T) async -> Error? {
    do {
        _ = try await body()
        return nil
    } catch {
        return error
    }
}
