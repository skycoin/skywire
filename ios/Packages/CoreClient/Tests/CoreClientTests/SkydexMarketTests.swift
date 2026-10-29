@testable import CoreClient
import XCTest

/// What the native SkyDEX screens must do the way the web trading page did:
/// print amounts, build the links wallets scan, and keep finished trades
/// (Android: DexNativeTest.kt, the same cases), plus the market calls.
final class SkydexMarketTests: XCTestCase {
    private var server: StubServer?

    override func tearDown() async throws {
        server?.stop()
    }

    /// Market amounts are JSON floats; the page never showed exponent form.
    func testAmountsPrintAsPlainDecimals() {
        XCTAssertEqual(DexFormat.number(0.1), "0.1")
        XCTAssertEqual(DexFormat.number(5.0), "5")
        XCTAssertEqual(DexFormat.number(1e-7), "0.0000001")
        XCTAssertEqual(DexFormat.number(12.345), "12.345")
    }

    /// QrButton.jsx buildUri: known schemes only, 8 places, trailing zeros trimmed.
    func testPaymentLinksMatchThePage() {
        XCTAssertEqual(DexFormat.paymentURI(address: "1abc", amount: 0.0001, coin: "BTC"), "bitcoin:1abc?amount=0.0001")
        XCTAssertEqual(DexFormat.paymentURI(address: "2xyz", amount: 12.5, coin: "sky"), "skycoin:2xyz?amount=12.5")
        XCTAssertEqual(DexFormat.paymentURI(address: "Labc", amount: 1e-7, coin: "LTC"), "litecoin:Labc?amount=0.0000001")
        XCTAssertNil(DexFormat.paymentURI(address: "addr", amount: 1, coin: "XMR"))
        XCTAssertNil(DexFormat.paymentURI(address: "addr", amount: 0, coin: "BTC"))
        XCTAssertNil(DexFormat.paymentURI(address: "", amount: 1, coin: "BTC"))
    }

    /// Decimal keyboards in es and fr offer a comma.
    func testDecimalsAcceptEitherSeparator() {
        XCTAssertEqual(DexFormat.parseDecimal("1,5"), 1.5)
        XCTAssertEqual(DexFormat.parseDecimal(" 0.25 "), 0.25)
        XCTAssertNil(DexFormat.parseDecimal(""))
        XCTAssertNil(DexFormat.parseDecimal("abc"))
    }

    /// api.js mergeTradeHistory: only finished orders, tagged once, purged ones kept.
    func testHistoryKeepsFinishedOrdersTheMarketPurged() throws {
        let purged = DexOrder(id: "old", status: "completed", marketPk: "02aa", marketName: "Old", savedAt: 5)
        let market = [DexOrder(id: "a", status: "completed"), DexOrder(id: "b", status: "pending_payment"), DexOrder(id: "c", status: "canceled")]
        let merged = DexFormat.mergeHistory([purged], market, marketPk: "03bb", marketName: "Bazaar", now: 100)
        XCTAssertEqual(merged.map(\.id), ["old", "a", "c"])
        XCTAssertEqual(merged[1].marketName, "Bazaar")
        XCTAssertEqual(merged[1].savedAt, 100)
        XCTAssertEqual(merged[0].marketName, "Old")

        // Seen again later: refreshed, but the first saved time stays.
        let again = DexFormat.mergeHistory(merged, [DexOrder(id: "a", status: "completed", price: 2)], marketPk: "03bb", marketName: "", now: 900)
        let a = try XCTUnwrap(again.first { $0.id == "a" })
        XCTAssertEqual(a.price, 2)
        XCTAssertEqual(a.savedAt, 100)
        XCTAssertEqual(a.marketName, "Bazaar")
    }

    /// A self-trade is one order id seen as a buy and as a sell; both sides stay.
    func testHistoryKeepsBothSidesOfASelfTrade() {
        let both = [DexOrder(id: "s", type: "buy", status: "completed"), DexOrder(id: "s", type: "sell", status: "completed")]
        XCTAssertEqual(DexFormat.mergeHistory([], both, marketPk: "03bb", marketName: "", now: 1).map(\.type), ["buy", "sell"])
    }

    /// The keys api.js wrote to localStorage.
    func testLegacyStorageParses() {
        let legacy = DexFormat.parseLegacy([
            "exchange:wallets": #"{"SKY":"2abc ","BTC":"1btc","LTC":""}"#,
            "exchange:markets": #"[{"pk":"03aa","name":"Bazaar","ts":42},{"name":"no key"}]"#,
            "exchange:history": #"[{"id":"x1","type":"buy","sell_coin":"SKY","amount":1.5,"status":"completed","market_pk":"03aa","saved_at":7},{"type":"sell"}]"#,
            "unrelated": "1",
        ])
        XCTAssertEqual(legacy.wallets, ["SKY": "2abc", "BTC": "1btc"])
        XCTAssertEqual(legacy.markets, [DexFormat.LegacyMarket(pk: "03aa", name: "Bazaar", ts: 42)])
        XCTAssertEqual(legacy.history.map(\.id), ["x1"])
        XCTAssertEqual(legacy.history.first?.amount, 1.5)
        XCTAssertEqual(legacy.history.first?.savedAt, 7)
    }

    func testBrokenLegacyStorageIsIgnored() {
        let legacy = DexFormat.parseLegacy(["exchange:wallets": "{not json", "exchange:history": "null"])
        XCTAssertTrue(legacy.wallets.isEmpty)
        XCTAssertTrue(legacy.history.isEmpty)
    }

    /// A refusal comes back in the market's words; Go's null lists are empty.
    func testMarketCallsCarryTheGateAndTheRefusal() async throws {
        let server = try await StubServer.start { request in
            switch request.path {
            case "/api/orders": .response(status: 200, headers: [:], body: Data(#"{"orders":null}"#.utf8))
            case "/api/buy": .response(status: 400, headers: [:], body: Data(#"{"error":"register first","code":"not_registered"}"#.utf8))
            default: .response(status: 200, headers: [:], body: Data(#"{"products":[{"id":"p1","amount":2,"price":0.5,"sell_coin":"SKY","payment_currency":"BTC"}]}"#.utf8))
            }
        }
        self.server = server
        let client = SkydexClient(transport: LoopbackTransport(origin: server.origin)) { "pw" }
        let orders = try await client.orders()
        XCTAssertTrue(orders.isEmpty)
        let products = try await client.products()
        XCTAssertEqual(products.first?.paymentCurrency, "BTC")
        do {
            _ = try await client.buy("p1")
            XCTFail("no refusal")
        } catch let refusal as SkydexRefusal {
            XCTAssertEqual(refusal.message, "register first")
            XCTAssertEqual(refusal.code, "not_registered")
        }
        let buy = try XCTUnwrap(server.requests.first { $0.path == "/api/buy" })
        XCTAssertEqual(buy.method, "POST")
        XCTAssertEqual(buy.jsonObject?["product_id"] as? String, "p1")
        XCTAssertEqual(buy.header("Authorization"), BasicAuth.header(user: SkydexProfile.user, password: "pw"))
    }
}
