@testable import WalletCore
import XCTest

/// Port of HistoryOwnAddressTest.kt. Each transaction names the wallet's
/// own addresses it used: where a send's coins left from, and where a
/// receive's coins arrived.
final class HistoryOwnAddressTests: XCTestCase {

    private static let mine1 = "2GgFvqoyk9RjwVzj8tqfcXVXB4orBwoc9qv"
    private static let mine2 = "R6aHqKWSQfvpdo2fGSrq4F1RYXkBWR9HHJ"
    private static let other = "WADSeEwEQVbtUy8CfcVimyxX1KjTRkvfoK"

    private func fakeNode(_ transactions: String) -> URLSession {
        StubSession.ok { request, _ in
            guard request.url?.path == "/api/v1/transactions" else { throw UnexpectedRequest(request) }
            return transactions
        }
    }

    func testSendAndReceiveNameTheirOwnAddresses() async throws {
        let (mine1, mine2, other) = (Self.mine1, Self.mine2, Self.other)
        let txs = """
            [
            {"status":{"confirmed":true,"height":3},"time":100,"txn":{"txid":"send","timestamp":100,
             "inputs":[{"uxid":"a","owner":"\(mine2)","coins":"5.000000"},{"uxid":"b","owner":"\(mine1)","coins":"1.000000"},
                       {"uxid":"c","owner":"\(mine2)","coins":"1.000000"}],
             "outputs":[{"dst":"\(other)","coins":"6.000000"},{"dst":"\(mine2)","coins":"1.000000"}]}},
            {"status":{"confirmed":true,"height":2},"time":90,"txn":{"txid":"recv","timestamp":90,
             "inputs":[{"uxid":"d","owner":"\(other)","coins":"3.000000"}],
             "outputs":[{"dst":"\(mine1)","coins":"2.000000"},{"dst":"\(other)","coins":"1.000000"}]}}]
            """
        let core = try SkyFiberWalletCore(nodeURL: "http://node.test", session: fakeNode(txs))
        let history = try await core.history(AddressBook(receive: [mine1, mine2], change: []))
        let byTxid = Dictionary(uniqueKeysWithValues: history.map { ($0.txid, $0) })

        XCTAssertEqual([mine2, mine1], byTxid["send"]?.own)
        XCTAssertEqual([mine1], byTxid["recv"]?.own)
    }
}
