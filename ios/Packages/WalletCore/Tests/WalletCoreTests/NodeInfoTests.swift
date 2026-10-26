@testable import WalletCore
import XCTest

/// Port of NodeInfoTest.kt. A node checked before it is used: what it
/// reports, and when the wallet refuses it.
final class NodeInfoTests: XCTestCase {

    private func answering(_ body: String) -> URLSession { StubSession.ok { _, _ in body } }

    // The fields node.skycoin.com's /api/v1/health answered with on 2026-10-22.
    private static func health(_ version: String) -> String {
        """
        {"blockchain":{"head":{"seq":195720}},"version":{"version":"\(version)"},
        "coin":"skycoin","csrf_enabled":false,"blockchain_pubkey":"0328c576d3f420e7682058a981173a4b374c7cc5ff55bf394d3cf57059bbe6456a",
        "user_verify_transaction":{"burn_factor":10,"max_transaction_size":32768,"max_decimals":3},
        "fiber":{"name":"skycoin","display_name":"Skycoin","ticker":"SKY","explorer_url":"https://explorer.skycoin.com"}}
        """
    }

    func testFiberNodeNamesItsCoin() async throws {
        let info = try await SkyFiberWalletCore(nodeURL: "http://node.test", session: answering(Self.health("0.27.1"))).nodeInfo()
        XCTAssertEqual("Skycoin", info.coinName)
        XCTAssertEqual("SKY", info.ticker)
        XCTAssertEqual("0.27.1", info.version)
        XCTAssertEqual(195720, info.height)
        XCTAssertEqual(10, info.burnFactor)
        XCTAssertEqual("https://explorer.skycoin.com", info.explorerURL)
        XCTAssertNil(info.problem)
    }

    func testSyncAndPeers() async throws {
        let client = StubSession.ok { request, _ in
            switch request.url?.path {
            case "/api/v1/health":
                Self.health("0.27.1").replacingOccurrences(of: #""coin":"skycoin""#, with: #""coin":"skycoin","open_connections":0"#)
            case "/api/v1/blockchain/progress": #"{"current":195700,"highest":195720,"peers":[]}"#
            default: throw UnexpectedRequest(request)
            }
        }
        let info = try await SkyFiberWalletCore(nodeURL: "http://node.test", session: client).nodeInfo()
        XCTAssertEqual(0, info.connections)
        XCTAssertEqual(false, info.synced)
        // A node without the progress call is not reported as behind.
        let noProgress = StubSession.make { request, _ in
            request.url?.path.hasSuffix("/progress") == true ? (404, "404 page not found") : (200, Self.health("0.27.1"))
        }
        let synced = try await SkyFiberWalletCore(nodeURL: "http://node.test", session: noProgress).nodeInfo().synced
        XCTAssertNil(synced)
    }

    func testOldNodesAreRefused() async throws {
        let info = try await SkyFiberWalletCore(nodeURL: "http://node.test", session: answering(Self.health("0.23.9"))).nodeInfo()
        XCTAssertEqual(.tooOld, info.problem)
    }

    func testVersionFloor() {
        let floor = [0, 24, 0]
        XCTAssertTrue(SkyFiberWalletCore.versionBelow("0.23.1", floor: floor))
        XCTAssertFalse(SkyFiberWalletCore.versionBelow("0.24.0", floor: floor))
        XCTAssertFalse(SkyFiberWalletCore.versionBelow("v0.28.0-rc1", floor: floor))
        XCTAssertFalse(SkyFiberWalletCore.versionBelow("", floor: floor), "an unknown version is not held against a node")
    }

    func testEthereumOffMainnetIsRefused() async throws {
        let core = try EthWalletCore(rpcURL: "http://rpc.test", indexerURL: nil, session: answering(#"{"jsonrpc":"2.0","id":1,"result":"0x5"}"#))
        let problem = try await core.nodeInfo().problem
        XCTAssertEqual(.wrongChain, problem)
    }
}
