@testable import WalletCore
import os
import XCTest

/// Port of NodeViewsTest.kt. The node pages: the chain, the peers and the
/// unconfirmed pool, read as node.skycoin.com answers them.
final class NodeViewsTests: XCTestCase {

    private static let mine = "2iNNt6fm9LszSWe51693BeyNUKX34pPaLx8"

    /// The core, and the txid the fake node was last asked to delete.
    private func core() throws -> (SkyFiberWalletCore, OSAllocatedUnfairLock<String?>) {
        let deleted = OSAllocatedUnfairLock<String?>(initialState: nil)
        let session = StubSession.ok { request, _ in
            let url = try XCTUnwrap(request.url)
            switch url.path {
            case "/api/v1/blockchain/metadata":
                return #"{"head":{"seq":195720,"block_hash":"6144bdc1","timestamp":1792385083,"fee":2601377}}"#
            case "/api/v1/coinSupply":
                return #"{"current_supply":"24239050.000000","total_supply":"25000000.000000","max_supply":"100000000.000000"}"#
            case "/api/v1/network/connections":
                return #"""
                    {"connections":[{"id":339,"address":"106.43.109.53:44870","last_sent":1792666593,"last_received":1792666590,
                    "outgoing":false,"height":195720,"user_agent":"skycoin:0.27.1","is_trusted_peer":true}]}
                    """#
            case "/api/v1/pendingTxs":
                if request.httpMethod == "DELETE" {
                    let txid = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems?.first { $0.name == "txid" }?.value
                    deleted.withLock { $0 = txid }
                    return "{}"
                }
                return """
                    [{"transaction":{"txid":"aa","inputs":[{"uxid":"u1","owner":"\(Self.mine)","coins":"5.000000"}],
                    "outputs":[{"dst":"x","coins":"4.000000"},{"dst":"y","coins":"1.000000"}]},"received":"2026-10-22T10:56:06Z"},
                    {"transaction":{"txid":"bb","inputs":[{"uxid":"u2","owner":"z","coins":"2.000000"}],
                    "outputs":[{"dst":"w","coins":"2.000000"}]},"received":"2026-10-22T11:00:00Z"}]
                    """
            case "/api/v1/csrf":
                return #"{"csrf_token":"t"}"#
            default:
                throw UnexpectedRequest(request)
            }
        }
        return (try SkyFiberWalletCore(nodeURL: "http://node.test", session: session), deleted)
    }

    func testChain() async throws {
        let c = try await core().0.chainInfo()
        XCTAssertEqual(195720, c.height)
        XCTAssertEqual("6144bdc1", c.blockHash)
        XCTAssertEqual("24239050.000000", c.currentSupply)
        XCTAssertEqual("25000000.000000", c.totalSupply)
    }

    func testPeers() async throws {
        let peers = try await core().0.peers()
        XCTAssertEqual(1, peers.count)
        let p = try XCTUnwrap(peers.first)
        XCTAssertEqual("106.43.109.53:44870", p.address)
        XCTAssertEqual(true, p.trusted)
        XCTAssertEqual(1792666590, p.lastReceived)
    }

    func testPendingMarksTheWalletsOwn() async throws {
        let (core, deleted) = try core()
        let p = try await core.pending(AddressBook(receive: [Self.mine], change: []))
        XCTAssertEqual(["aa", "bb"], p.map(\.txid))
        XCTAssertEqual([true, false], p.map(\.mine))
        XCTAssertEqual(5_000_000, p[0].coins)
        try await core.deletePending(txid: "aa")
        XCTAssertEqual("aa", deleted.withLock { $0 })
    }
}
