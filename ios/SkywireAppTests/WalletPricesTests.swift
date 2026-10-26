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
}
