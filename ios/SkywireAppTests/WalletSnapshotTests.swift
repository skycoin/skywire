@testable import Skywire
import XCTest

/// What the cached chain view says about a wallet's addresses (Android: WalletExposureTest).
final class WalletSnapshotTests: XCTestCase {
    private func tx(_ incoming: Bool, _ own: String...) -> CachedTx {
        CachedTx(txid: own.joined(separator: ","), incoming: incoming, amount: 1, party: nil, timestamp: 0,
                 confirmed: false, confirmations: 0, fee: nil, own: own)
    }

    func testOnlyAddressesThatSentAreExposed() {
        let snapshot = WalletSnapshot(txs: [tx(true, "a1"), tx(false, "a2", "a3"), tx(true, "a3")],
                                      perAddress: ["a1": 5, "a2": 0, "a3": 7])
        XCTAssertEqual(snapshot.exposedAddresses, ["a2", "a3"])
        XCTAssertEqual(snapshot.exposedWithCoins, ["a3"])
    }

    /// A cache file written before the per-address balances and own addresses still reads.
    func testAnOlderCacheStillReads() throws {
        let old = #"{"confirmed":5,"predicted":5,"spendableOutputs":1,"fetchedAtMs":10,"historyFetchedAtMs":10,"#
            + #""txs":[{"txid":"t","incoming":true,"amount":5,"timestamp":1,"confirmed":true,"confirmations":3}]}"#
        let snapshot = try JSONDecoder().decode(WalletSnapshot.self, from: Data(old.utf8))
        XCTAssertEqual(snapshot.confirmed, 5)
        XCTAssertEqual(snapshot.perAddress, [:])
        XCTAssertEqual(snapshot.txs.first?.own, [])
        let again = try JSONDecoder().decode(WalletSnapshot.self, from: JSONEncoder().encode(
            WalletSnapshot(txs: [tx(false, "a2")], perAddress: ["a2": 3])))
        XCTAssertEqual(again.perAddress, ["a2": 3])
        XCTAssertEqual(again.txs.first?.own, ["a2"])
    }
}

/// Saved addresses: one per line, receive then change (Android: WalletAddressesCsvTest).
final class WalletAddressesCsvTests: XCTestCase {
    func testEveryAddressOnItsOwnLine() {
        let wallet = WalletMeta(id: "w", coinId: "BTC", name: "BTC", createdAtMs: 0, receiveAddresses: ["r1", "r2"], changeAddresses: ["c1"])
        XCTAssertEqual(addressesCsv(wallet), "r1\nr2\nc1")
    }
}
