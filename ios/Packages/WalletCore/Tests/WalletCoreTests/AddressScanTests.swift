@testable import WalletCore
import os
import XCTest

/// Port of AddressScanTest.kt. Finding used addresses past a wallet's own, as
/// an xpub paid by the reward system needs.
final class AddressScanTests: XCTestCase {

    private struct Query: Equatable {
        let method: String
        let addresses: Int
    }

    private let keys = KeySource(
        mnemonic: "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
        passphrase: "",
        derivation: .bip44
    )
    private let queries = OSAllocatedUnfairLock<[Query]>(initialState: [])

    private func derive(_ seed: KeySource, _ receive: Int, _ change: Int) throws -> AddressBook {
        try SkyFiberWalletCore(nodeURL: "http://127.0.0.1:1").deriveAddresses(seed: seed, receiveCount: receive, changeCount: change)
    }

    /// A node where `used` addresses have each been paid once.
    private func core(_ used: Set<String>) throws -> SkyFiberWalletCore {
        let queries = queries
        let session = StubSession.ok { request, body in
            let url = try XCTUnwrap(request.url)
            if url.path == "/api/v1/csrf" { return #"{"csrf_token":"t"}"# }
            let post = request.httpMethod == "POST"
            // A POST without the node's token is refused by a node with CSRF on.
            if post, request.value(forHTTPHeaderField: "X-CSRF-Token") != "t" { throw UnexpectedRequest(request) }
            var fields = URLComponents(url: url, resolvingAgainstBaseURL: false)
            if post { fields?.percentEncodedQuery = String(decoding: body, as: UTF8.self) }
            let addrs = (fields?.queryItems?.first { $0.name == "addrs" }?.value ?? "").split(separator: ",").map(String.init)
            queries.withLock { $0.append(Query(method: post ? "POST" : "GET", addresses: addrs.count)) }
            switch url.path {
            case "/api/v1/transactions":
                let paid = addrs.filter(used.contains).map {
                    """
                    {"status":{"confirmed":true,"height":1},"txn":{"txid":"\($0)",
                    "inputs":[{"uxid":"u","owner":"elsewhere","coins":"1.000000"}],
                    "outputs":[{"dst":"\($0)","coins":"1.000000"}]}}
                    """
                }
                return "[\(paid.joined(separator: ","))]"
            case "/api/v1/outputs":
                return "{}"
            case "/api/v1/balance":
                return #"{"confirmed":{"coins":1,"hours":0},"predicted":{"coins":1,"hours":0}}"#
            default:
                throw UnexpectedRequest(request)
            }
        }
        return try SkyFiberWalletCore(nodeURL: "http://node.test", session: session)
    }

    func testARestoreFindsUsedAddressesAcrossGapsUnderTwenty() async throws {
        let book = try derive(keys, 120, 30)
        let used: Set = [book.receive[0], book.receive[3], book.receive[22], book.change[5]]
        let counts = try await core(used).scanUsed(seed: keys)
        XCTAssertEqual(counts.receive, 23)
        XCTAssertEqual(counts.change, 6)
    }

    func testAGapOfTwentyEndsTheScan() async throws {
        let book = try derive(keys, 120, 30)
        let counts = try await core([book.receive[0], book.receive[21]]).scanUsed(seed: keys)
        XCTAssertEqual(counts.receive, 1)
        XCTAssertEqual(counts.change, 0)
    }

    /// The reward system pays each reward to the xpub's next unused address.
    func testAWalletHoldingOneAddressFindsRewardsPaidPastIt() async throws {
        let book = try derive(keys, 120, 30)
        let counts = try await core(Set(book.receive[0...44])).scanPast(seed: keys, receiveCount: 1, changeCount: 0)
        XCTAssertEqual(counts?.receive, 45)
        XCTAssertEqual(counts?.change, 0)
    }

    func testNothingNewLeavesTheCountsAndCostsOneQueryPerChain() async throws {
        let book = try derive(keys, 120, 30)
        let counts = try await core([book.receive[0]]).scanPast(seed: keys, receiveCount: 4, changeCount: 2)
        XCTAssertEqual(counts?.receive, 4)
        XCTAssertEqual(counts?.change, 2)
        XCTAssertEqual(queries.withLock { $0 }, [Query(method: "GET", addresses: 20), Query(method: "GET", addresses: 20)])
    }

    func testTheClassicChainScansTheSameAddressesItDerives() async throws {
        let classic = KeySource(mnemonic: "some classic seed text")
        let addrs = try derive(classic, 40, 0).receive
        let counts = try await core([addrs[0], addrs[15], addrs[29]]).scanPast(seed: classic, receiveCount: 1, changeCount: 0)
        XCTAssertEqual(counts?.receive, 30)
        XCTAssertEqual(counts?.change, 0)
    }

    func testManyAddressesGoAsAPostForm() async throws {
        let book = try derive(keys, 120, 30)
        let all = book.receive + book.change
        _ = try await core([]).balance(AddressBook(receive: Array(all.prefix(60)), change: []))
        _ = try await core([]).history(AddressBook(receive: all, change: []))
        let seen = queries.withLock { $0 }
        XCTAssertEqual(seen.first, Query(method: "GET", addresses: 60))
        XCTAssertTrue(seen.contains(Query(method: "POST", addresses: 150)), "\(seen)")
    }
}
