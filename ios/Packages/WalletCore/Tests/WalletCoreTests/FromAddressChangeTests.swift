@testable import WalletCore
import XCTest

/// Port of FromAddressChangeTest.kt. A send from one chosen address returns
/// its change to that address. It used to go to the wallet's first address,
/// moving coins the user had not chosen to move.
final class FromAddressChangeTests: XCTestCase {

    private let seed = KeySource(mnemonic: "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about")

    private func fakeNode(_ outputs: String) -> URLSession {
        StubSession.ok { request, _ in
            switch request.url?.path {
            case "/api/v1/health": #"{"user_verify_transaction":{"burn_factor":10,"max_transaction_size":32768,"max_decimals":3}}"#
            case "/api/v1/outputs": outputs
            default: throw UnexpectedRequest(request)
            }
        }
    }

    /// (address key hex, coins) of each output in a signed transaction.
    private func outputs(_ rawHex: String) throws -> [(String, UInt64)] {
        let b = try XCTUnwrap([UInt8](hex: rawHex))
        func u32(_ at: Int) -> Int { Int(b[at]) | Int(b[at + 1]) << 8 | Int(b[at + 2]) << 16 | Int(b[at + 3]) << 24 }
        func u64(_ at: Int) -> UInt64 { (0..<8).reduce(0) { $0 | UInt64(b[at + $1]) << (8 * UInt64($1)) } }
        var p = 4 + 1 + 32
        p += 4 + 65 * u32(p)
        p += 4 + 32 * u32(p)
        let n = u32(p)
        p += 4
        return (0..<n).map { _ in
            p += 1
            let key = Array(b[p..<(p + 20)]).hex
            let coins = u64(p + 20)
            p += 20 + 8 + 8
            return (key, coins)
        }
    }

    func testChangeReturnsToTheChosenAddress() async throws {
        let addresses = try SkyFiberWalletCore(nodeURL: "http://node.test")
            .deriveAddresses(seed: seed, receiveCount: 3, changeCount: 0).receive
        let (first, second, to) = (addresses[0], addresses[1], addresses[2])
        let held = """
            {"head_outputs":[
            {"hash":"\(String(repeating: "11", count: 32))","address":"\(first)","coins":"10.000000","hours":100,"calculated_hours":100},
            {"hash":"\(String(repeating: "22", count: 32))","address":"\(second)","coins":"100.000000","hours":1000,"calculated_hours":1000}]}
            """
        let core = try SkyFiberWalletCore(nodeURL: "http://node.test", session: fakeNode(held))
        let book = AddressBook(receive: [first, second], change: [])

        let plan = try await core.buildTx(
            seed: seed, book: book, toAddress: to, amount: 10_000_000, feeRate: nil, sendMax: false,
            fromAddress: second, inputs: nil
        )
        let sent = try outputs(core.signTx(seed: seed, plan: plan).rawHex)

        func key(_ a: String) -> String { SkycoinCrypto.decodeAddress(a)!.hex }
        XCTAssertEqual([key(to), key(second)], sent.map(\.0))
        XCTAssertEqual([10_000_000, 90_000_000], sent.map(\.1))
    }
}
