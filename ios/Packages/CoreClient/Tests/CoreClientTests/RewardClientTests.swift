@testable import CoreClient
import XCTest

/// The reward routes against what a real lite core answered
/// (ios/scripts/record-api-fixtures.sh sets, reads and deletes an account xpub).
final class RewardClientTests: XCTestCase {
    private var visor: FakeVisor!
    private var server: StubServer!
    private var client: CoreClient!

    static let xpub = "xpub6DMLxE9HpaMf7wCMK2ikiAshrkvHBWfdyFFhdajSpi1h2PvCN51rLzxfsqV9EPnrmqpRmnutibiKJX39acErEGmU5syK9ir6wmMAnc2XQWL"

    override func setUp() async throws {
        visor = FakeVisor(accountPassword: CoreClientTests.password)
        server = try await visor.start()
        client = CoreClient(transport: LoopbackTransport(origin: server.origin)) { CoreClientTests.password }
    }

    override func tearDown() async throws {
        server?.stop()
    }

    func testReadsTheAddress() async throws {
        let address = try await client.rewardAddress()
        XCTAssertEqual(address, Self.xpub)
    }

    /// After a delete the visor answers an empty string, not the address it held.
    func testNoAddressIsEmpty() async throws {
        visor.serveVariant("reward-none")
        let address = try await client.rewardAddress()
        XCTAssertEqual(address, "")
    }

    func testSetSendsTheOneField() async throws {
        try await client.setRewardAddress(Self.xpub)
        let body = try XCTUnwrap(server.requests.last { $0.method == "PUT" }?.jsonObject)
        XCTAssertEqual(body.keys.sorted(), ["reward_address"])
        XCTAssertEqual(body["reward_address"] as? String, Self.xpub)
        XCTAssertEqual(visor.served.last, "reward-put")
    }

    /// The visor's refusal of a single address comes back in its own words.
    func testASingleAddressIsRefused() async throws {
        visor.serveVariant("reward-put-single")
        do {
            try await client.setRewardAddress("2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6")
            XCTFail("a single address was taken")
        } catch let CoreClientError.http(method, _, status, message) {
            XCTAssertEqual(method, "PUT")
            XCTAssertEqual(status, 400)
            XCTAssertTrue(message.hasPrefix("a single skycoin address is no longer accepted"), message)
        }
    }

    func testDelete() async throws {
        try await client.deleteRewardAddress()
        XCTAssertEqual(visor.served.last, "reward-delete")
    }

    func testXpubShape() {
        XCTAssertTrue(RewardXpub.looksValid(Self.xpub))
        XCTAssertFalse(RewardXpub.looksValid("2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6"))
        XCTAssertFalse(RewardXpub.looksValid(String(Self.xpub.prefix(60))))
        XCTAssertFalse(RewardXpub.looksValid("zpub" + Self.xpub.dropFirst(4)))
    }
}
