@testable import WalletCore
import XCTest

/// Port of EthFeeLevelTest.kt. The slow, normal and fast fees are skycoin's
/// eth.FeePresets: a 0.1 gwei tip, the node's suggestion with that floor,
/// and twice the suggestion, each capped at twice the base fee plus the tip.
final class EthFeeLevelTests: XCTestCase {

    private let seed = KeySource(mnemonic: "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about")
    private let to = "0xfb6916095ca1df60bb79ce92ce3ea74c37c5d359"

    private func core(_ suggestedTip: String) throws -> EthWalletCore {
        let session = StubSession.ok { request, body in
            let call = try XCTUnwrap(JSONSerialization.jsonObject(with: body) as? [String: Any])
            let result = switch call["method"] as? String {
            case "eth_getBalance": #""0xde0b6b3a7640000""# // 1 ETH
            case "eth_chainId": #""0x1""#
            case "eth_maxPriorityFeePerGas": "\"\(suggestedTip)\""
            case "eth_getBlockByNumber": #"{"baseFeePerGas":"0x2540be400"}"# // 10 gwei
            case "eth_getTransactionCount": #""0x0""#
            case "eth_estimateGas": #""0x5208""# // 21000, padded to 25200
            default: throw UnexpectedRequest(request)
            }
            return #"{"jsonrpc":"2.0","id":1,"result":\#(result)}"#
        }
        return try EthWalletCore(rpcURL: "http://rpc.test", indexerURL: nil, session: session)
    }

    private func feeGwei(_ core: EthWalletCore, _ level: FeeLevel?) async throws -> UInt64 {
        let book = try core.deriveAddresses(seed: seed, receiveCount: 1, changeCount: 0)
        return try await core.buildTx(
            seed: seed, book: book, toAddress: to, amount: 1_000_000, feeRate: nil, sendMax: false, feeLevel: level
        ).fee
    }

    func testLevelsPickTheTip() async throws {
        let c = try core("0x77359400") // 2 gwei suggested
        let slow = try await feeGwei(c, .slow)
        let normal = try await feeGwei(c, .normal)
        let unset = try await feeGwei(c, nil)
        let fast = try await feeGwei(c, .fast)
        XCTAssertEqual(25_200 * 201 / 10, slow) // cap 20.1 gwei
        XCTAssertEqual(25_200 * 22, normal)
        XCTAssertEqual(25_200 * 22, unset)
        XCTAssertEqual(25_200 * 24, fast)
    }

    func testAZeroSuggestionIsRaisedToTheLeastTip() async throws {
        let fee = try await feeGwei(core("0x0"), .normal)
        XCTAssertEqual(25_200 * 201 / 10, fee)
    }
}
