@testable import WalletCore
import XCTest

/// Port of PaymentUriTest.kt.
final class PaymentUriTests: XCTestCase {

    private let sky = "2iNNt6fm9LszSWe51693BeyNUKX34pPaLx8"
    private let eth = "0xfb6916095ca1df60bb79Ce92cE3Ea74c37c5d359"
    private let usdt = "0xdAC17F958D2ee523a2206206994597C13D831ec7"

    func testFiberRoundTrip() {
        let uri = PaymentUri.fiber(scheme: "skycoin", address: sky, amount: "1.5", hours: 20, message: "for lunch & tea")
        XCTAssertEqual("skycoin:\(sky)?amount=1.5&hours=20&message=for%20lunch%20%26%20tea", uri)
        let back = PaymentUri.parse(uri)
        XCTAssertEqual("skycoin", back.scheme)
        XCTAssertEqual(sky, back.address)
        XCTAssertEqual(Decimal(string: "1.5"), back.amount)
        XCTAssertEqual(20, back.hours)
        XCTAssertEqual("for lunch & tea", back.message)
    }

    func testEmptyFieldsLeaveTheBareUri() {
        XCTAssertEqual("skycoin:\(sky)", PaymentUri.fiber(scheme: "skycoin", address: sky, amount: nil, hours: nil, message: ""))
    }

    func testSchemeFromCoinName() {
        XCTAssertEqual("skycoin", PaymentUri.schemeFor("Skycoin"))
        XCTAssertEqual("mycoin", PaymentUri.schemeFor("My Coin!"))
        XCTAssertEqual("skycoin", PaymentUri.schemeFor("123"))
    }

    func testBareAddressAndMalformedValues() {
        let bare = PaymentUri.parse("  \(sky) ")
        XCTAssertNil(bare.scheme)
        XCTAssertEqual(sky, bare.address)
        XCTAssertNil(bare.amount)
        let bad = PaymentUri.parse("skycoin://\(sky)?amount=1,5&hours=-2")
        XCTAssertEqual(sky, bad.address)
        XCTAssertNil(bad.amount)
        XCTAssertNil(bad.hours)
    }

    // BIP 21's own examples.
    func testBitcoinBip21() {
        let p = PaymentUri.parse("bitcoin:175tWpb8K1S7NmH4Zx6rewF9WQrcZv245W?amount=20.3&label=Luke-Jr")
        XCTAssertEqual("175tWpb8K1S7NmH4Zx6rewF9WQrcZv245W", p.address)
        XCTAssertEqual(Decimal(string: "20.3"), p.amount)
        XCTAssertEqual(
            "bitcoin:175tWpb8K1S7NmH4Zx6rewF9WQrcZv245W?amount=50&message=Donation%20for%20project%20xyz",
            PaymentUri.bitcoin(address: "175tWpb8K1S7NmH4Zx6rewF9WQrcZv245W", amount: "50", message: "Donation for project xyz")
        )
    }

    // EIP-681's own examples, value in wei with an exponent.
    func testEtherValue() {
        let p = PaymentUri.parse("ethereum:\(eth)@1?value=2.014e18")
        XCTAssertEqual(eth, p.address)
        XCTAssertEqual(Decimal(string: "2.014"), p.amount)
        XCTAssertNil(p.contract)
        XCTAssertEqual(
            "ethereum:\(eth)?value=1500000000000000000",
            PaymentUri.ether(address: eth, wei: BigUInt(decimal: "1500000000000000000"))
        )
        XCTAssertNil(PaymentUri.parse("ethereum:\(eth)?value=0.5").amount)
    }

    func testTokenTransferNamesTheRecipientNotTheContract() {
        let uri = PaymentUri.erc20(contract: usdt, to: eth, units: BigUInt(2_500_000))
        XCTAssertEqual("ethereum:\(usdt)/transfer?address=\(eth)&uint256=2500000", uri)
        let p = PaymentUri.parse("ethereum:\(usdt)@1/transfer?address=\(eth)&uint256=2.5e6")
        XCTAssertEqual(eth, p.address)
        XCTAssertEqual(usdt, p.contract)
        XCTAssertEqual(BigUInt(2_500_000), p.tokenUnits)
        XCTAssertNil(p.amount)
    }
}
