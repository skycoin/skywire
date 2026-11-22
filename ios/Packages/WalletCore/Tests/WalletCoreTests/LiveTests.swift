@testable import WalletCore
import XCTest

// Ports of LiveNodeTest.kt and EthLiveTest.kt: live checks against the
// shipped endpoints — parsing production JSON, not a mock's idea of it. Off
// by default (CI must not depend on the internet):
//
//   SKYWIRE_NET_TESTS=1 swift test --filter Live

private func netTestsEnabled() throws {
    try XCTSkipUnless(ProcessInfo.processInfo.environment["SKYWIRE_NET_TESTS"] == "1", "set SKYWIRE_NET_TESTS=1")
}

private let liveSession: URLSession = {
    let config = URLSessionConfiguration.ephemeral
    config.timeoutIntervalForRequest = 30
    return URLSession(configuration: config)
}()

final class LiveNodeTests: XCTestCase {

    // A Skycoin distribution address: public, funded, with on-chain history.
    private let richAddress = "R6aHqKWSQfvpdo2fGSrq4F1RYXkBWR9HHJ"

    private func core() throws -> SkyFiberWalletCore {
        try SkyFiberWalletCore(nodeURL: "http://node.skycoin.com", session: liveSession)
    }

    func testBalanceAndHistoryParseFromProduction() async throws {
        try netTestsEnabled()
        let book = AddressBook(receive: [richAddress], change: [])
        // The address may have been emptied since distribution — the point
        // is that production JSON parses, not what it holds.
        _ = try await core().balance(book)

        let history = try await core().history(book)
        XCTAssertFalse(history.isEmpty, "distribution address has transactions")
        let tx = try XCTUnwrap(history.last)
        XCTAssertEqual(tx.txid.count, 64)
        XCTAssertTrue(tx.confirmed)
        XCTAssertGreaterThan(tx.confirmations, 0)
        XCTAssertGreaterThan(tx.timestamp, 1_400_000_000)
    }
}

final class EthLiveTests: XCTestCase {

    // Publicly known, funded, with deep history on both APIs.
    private let richAddress = "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045"
    private let usdtContract = "0xdAC17F958D2ee523a2206206994597C13D831ec7"

    func testNativeBalanceAndHistoryParseFromProduction() async throws {
        try netTestsEnabled()
        let core = try EthWalletCore(
            rpcURL: "https://ethereum-rpc.publicnode.com",
            indexerURL: "https://eth.blockscout.com",
            session: liveSession
        )
        let book = AddressBook(receive: [richAddress], change: [])
        let balance = try await core.balance(book)
        XCTAssertGreaterThan(balance.confirmed, 0, "known-funded address reads a balance")

        let history = try await core.history(book)
        XCTAssertFalse(history.isEmpty, "known-active address has history")
        let tx = try XCTUnwrap(history.first { $0.confirmed })
        XCTAssertTrue(tx.txid.hasPrefix("0x") && tx.txid.count == 66)
        XCTAssertGreaterThan(tx.confirmations, 0)
        XCTAssertGreaterThan(tx.timestamp, 1_400_000_000)
    }

    func testTokenBalanceAndTransfersParseFromProduction() async throws {
        try netTestsEnabled()
        let core = try EthWalletCore(
            rpcURL: "https://ethereum-rpc.publicnode.com",
            indexerURL: "https://eth.blockscout.com",
            session: liveSession,
            token: EthWalletCore.Erc20Token(contract: usdtContract, decimals: 6)
        )
        let book = AddressBook(receive: [richAddress], change: [])
        // The point is that balanceOf decodes and transfers parse — what the
        // address holds today is its own business.
        _ = try await core.balance(book)
        let transfers = try await core.history(book)
        XCTAssertFalse(transfers.isEmpty, "address has USDT transfer history")
        XCTAssertTrue(transfers[0].txid.hasPrefix("0x"))
    }
}
