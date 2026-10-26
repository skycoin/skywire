@testable import Skywire
import XCTest

/// Payment requests on Receive, and what a scanned or pasted code fills on Send (Android: WalletPaymentTest).
final class WalletPaymentTests: XCTestCase {
    private let sky = "2iNNt6fm9LszSWe51693BeyNUKX34pPaLx8"
    private let eth = "0xfb6916095ca1df60bb79Ce92cE3Ea74c37c5d359"
    private let usdt = CoinSpec.usdt.contract!

    func testSkyRequestCarriesAmountHoursAndMessage() {
        XCTAssertEqual(CoinSpec.sky.paymentRequest(address: sky, amountText: "1.5", hoursText: "20", message: "lunch").uri,
                       "skycoin:\(sky)?amount=1.5&hours=20&message=lunch")
    }

    func testNothingAskedIsTheBareAddress() {
        XCTAssertNil(CoinSpec.sky.paymentRequest(address: sky, amountText: "", hoursText: " ", message: "").uri)
    }

    func testBadValuesShowNoRequest() {
        // SKY carries six decimals; seven is not an amount on this chain.
        let r = CoinSpec.sky.paymentRequest(address: sky, amountText: "1.0000001", hoursText: "", message: "")
        XCTAssertTrue(r.badAmount)
        XCTAssertNil(r.uri)
        XCTAssertTrue(CoinSpec.sky.paymentRequest(address: sky, amountText: "1", hoursText: "2.5", message: "").badHours)
    }

    func testEtherAmountsAreWei() {
        XCTAssertEqual(CoinSpec.eth.paymentRequest(address: eth, amountText: "1.5", hoursText: "", message: "x").uri,
                       "ethereum:\(eth)?value=1500000000000000000")
    }

    func testTokenRequestIsATransferToTheWallet() {
        XCTAssertEqual(CoinSpec.usdt.paymentRequest(address: eth, amountText: "2.5", hoursText: "", message: "").uri,
                       "ethereum:\(usdt)/transfer?address=\(eth)&uint256=2500000")
    }

    func testScanFillsAddressAmountAndHours() {
        let f = CoinSpec.sky.readScan("skycoin:\(sky)?amount=1.250&hours=7")
        XCTAssertEqual(f.address, sky)
        XCTAssertEqual(f.amountText, "1.25")
        XCTAssertEqual(f.hoursText, "7")
    }

    func testScanDropsAnAmountTooFineForTheCoin() {
        XCTAssertNil(CoinSpec.sky.readScan("skycoin:\(sky)?amount=0.0000001").amountText)
    }

    func testTokenTransferForThisTokenFillsTheRecipient() {
        let f = CoinSpec.usdt.readScan("ethereum:\(usdt)@1/transfer?address=\(eth)&uint256=2.5e6")
        XCTAssertEqual(f.address, eth)
        XCTAssertEqual(f.amountText, "2.5")
    }

    /// The contract is the URI's first address; filling it as the recipient loses the coins.
    func testTokenTransferForAnotherCoinFillsNothing() {
        let other = "0x6B175474E89094C44Da98b954EedeAC495271d0F"
        let f = CoinSpec.usdt.readScan("ethereum:\(other)/transfer?address=\(eth)&uint256=1")
        XCTAssertTrue(f.wrongToken)
        XCTAssertNil(f.address)
        XCTAssertTrue(CoinSpec.eth.readScan("ethereum:\(usdt)/transfer?address=\(eth)&uint256=1").wrongToken)
    }

    func testEtherAmountIsNotReadAsTokens() {
        let f = CoinSpec.usdt.readScan("ethereum:\(eth)?value=1e18")
        XCTAssertEqual(f.address, eth)
        XCTAssertNil(f.amountText)
        XCTAssertEqual(CoinSpec.eth.readScan("ethereum:\(eth)?value=1e18").amountText, "1")
    }

    /// A bare address, as most codes are, fills only the address.
    func testBareAddress() {
        XCTAssertEqual(CoinSpec.sky.readScan(" \(sky) "), ScanFill(address: sky, amountText: nil, hoursText: nil))
    }
}
