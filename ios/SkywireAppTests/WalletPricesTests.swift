@testable import Skywire
import XCTest

/// USD prices: coinpaprika's answer read, and amounts turned between coins and dollars
/// (Android: WalletPricesTest).
final class WalletPricesTests: XCTestCase {

    func testReadsCoinpaprikasTicker() throws {
        let body = #"{"id":"sky-skycoin","symbol":"SKY","quotes":{"USD":{"price":0.07485954376531992,"volume_24h":1}}}"#
        XCTAssertEqual(try WalletPrices.parse(Data(body.utf8)), 0.07485954376531992)
        XCTAssertThrowsError(try WalletPrices.parse(Data(#"{"quotes":{}}"#.utf8)))
    }

    func testDollarsToCoinsRoundDown() {
        // 10 USD at 0.075 is 133.333… SKY; SKY shows three decimals, so 133.333.
        XCTAssertEqual(CoinSpec.sky.coinTextFromUsd("10", price: 0.075), "133.333")
        XCTAssertEqual(CoinSpec.btc.coinTextFromUsd("10", price: 81948.49), "0.00012202")
        XCTAssertNil(CoinSpec.sky.coinTextFromUsd("ten", price: 0.075))
        XCTAssertNil(CoinSpec.sky.coinTextFromUsd("10", price: 0))
        XCTAssertNil(CoinSpec.sky.coinTextFromUsd("-1", price: 0.075))
    }

    func testCoinsToPlainDollars() {
        XCTAssertEqual(CoinSpec.sky.usdPlain(133_334_000, price: 0.075), "10.00")
        XCTAssertEqual(CoinSpec.eth.usdPlain(1_000_000, price: 1500), "1.50", "0.001 ETH in gwei units")
    }

    func testOnlyTheShippedCoinsHaveAPrice() {
        XCTAssertEqual([CoinSpec.sky, .btc, .eth, .usdt].map(\.priceId), ["sky-skycoin", "btc-bitcoin", "eth-ethereum", "usdt-tether"])
        XCTAssertNil(CoinSpec(id: "fiber-1", name: "Testcoin", ticker: "TST", kind: .skyFiber, nodeUrl: "https://node.example").priceId)
    }

    /// The stored form of a quote, Android's: a price kept across a restart, or nothing when it does not read.
    func testAQuoteSurvivesBeingStored() {
        let quote = WalletPrices.Quote(usd: 0.042841804154099855, at: Date(timeIntervalSince1970: 1_795_000_000))
        XCTAssertEqual(WalletPrices.encode(quote), "0.042841804154099855|1795000000000")
        XCTAssertEqual(WalletPrices.decode(WalletPrices.encode(quote)), quote)
        for unreadable in ["", "0.04", "0.0|1795000000000", "nan|1795000000000", "abc|def", "0.04|0"] {
            XCTAssertNil(WalletPrices.decode(unreadable), unreadable)
        }
    }

    /// The wallet lost its price at every restart: the last quote now comes back from the store.
    @MainActor
    func testTheLastQuoteComesBackAfterARestart() throws {
        let suite = "WalletPricesTests-\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { UserDefaults.standard.removePersistentDomain(forName: suite) }
        let id = "test-\(UUID().uuidString)"
        XCTAssertNil(WalletPrices.last(id, defaults: defaults))
        let quote = WalletPrices.Quote(usd: 0.0431, at: Date(timeIntervalSince1970: 1_795_169_348))
        WalletPrices.keep(quote, for: id, defaults: defaults)
        WalletPrices.dropCache()
        XCTAssertEqual(WalletPrices.last(id, defaults: defaults), quote)
    }
}
