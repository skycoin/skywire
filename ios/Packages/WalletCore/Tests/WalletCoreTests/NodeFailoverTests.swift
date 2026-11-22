@testable import WalletCore
import os
import XCTest

/// Port of NodeFailoverTest.kt. Each test uses its own hosts: a failed node is remembered across
/// instances.
final class NodeFailoverTests: XCTestCase {

    private let asked = OSAllocatedUnfairLock<[String]>(initialState: [])

    /// Answers `codes` by host, or drops the connection for a host it does not know.
    private func client(_ nodes: [String], _ codes: [String: Int]) -> HttpClient {
        let asked = asked
        let session = StubSession.make { request, _ in
            let url = request.url!
            asked.withLock { $0.append(url.absoluteString) }
            guard let code = codes[url.host ?? ""] else { throw URLError(.cannotConnectToHost) }
            return (code, "{}")
        }
        return HttpClient(session: session, failover: NodeFailover(nodes))
    }

    private func get(_ c: HttpClient, _ url: String) async throws -> Int {
        try await c.get(URL(string: url)!).status
    }

    func testAServerErrorMovesOnLikeADeadNode() async throws {
        let c = client(["https://a1.test", "https://b1.test"], ["a1.test": 503, "b1.test": 200])
        let status = try await get(c, "https://a1.test/api/v1/health")
        XCTAssertEqual(status, 200)
        XCTAssertEqual(asked.withLock { $0 }, ["https://a1.test/api/v1/health", "https://b1.test/api/v1/health"])
    }

    func testThrottlingMovesOnToo() async throws {
        let c = client(["https://a2.test", "https://b2.test"], ["a2.test": 429, "b2.test": 200])
        let status = try await get(c, "https://a2.test/api/v1/health")
        XCTAssertEqual(status, 200)
    }

    func testTheLastNodesErrorIsTheAnswer() async throws {
        let c = client(["https://a3.test", "https://b3.test"], ["b3.test": 500])
        let status = try await get(c, "https://a3.test/api/v1/health")
        XCTAssertEqual(status, 500)
    }

    func testEachNodeKeepsItsOwnPath() async throws {
        let c = client(["https://a4.test/sky/", "https://b4.test/node"], ["b4.test": 200])
        _ = try await get(c, "https://a4.test/sky/api/v1/outputs?addrs=x,y")
        XCTAssertEqual(asked.withLock { $0.last }, "https://b4.test/node/api/v1/outputs?addrs=x,y")
    }

    /// A node that just failed is skipped, so a dead first choice costs one timeout, not one per call.
    func testADeadNodeIsSkippedForAWhile() async throws {
        let c = client(["https://a5.test", "https://b5.test"], ["b5.test": 200])
        _ = try await get(c, "https://a5.test/api/v1/health")
        _ = try await get(c, "https://a5.test/api/v1/balance")
        XCTAssertEqual(asked.withLock { $0 }, [
            "https://a5.test/api/v1/health", "https://b5.test/api/v1/health", "https://b5.test/api/v1/balance",
        ])
    }

    /// The Ethereum indexer rides the same client at its own address, and stays there.
    func testAnotherHostIsLeftAlone() async throws {
        let c = client(["https://a6.test", "https://b6.test"], ["b6.test": 200, "scout6.test": 200])
        _ = try await get(c, "https://scout6.test/api?module=account")
        XCTAssertEqual(asked.withLock { $0 }, ["https://scout6.test/api?module=account"])
    }

    func testEveryNodeDownFailsWithTheLastError() async throws {
        let c = client(["https://a7.test", "https://b7.test"], [:])
        do {
            _ = try await get(c, "https://a7.test/api/v1/health")
            XCTFail("an answer with no node up")
        } catch let error as URLError {
            XCTAssertEqual(error.code, .cannotConnectToHost)
        }
        XCTAssertEqual(asked.withLock { $0.count }, 2)
    }
}
