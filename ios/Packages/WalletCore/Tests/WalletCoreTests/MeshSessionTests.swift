@testable import WalletCore
import XCTest

/// Requests to `.dmsg` hosts go out on the mesh session and every other host on the plain one,
/// as Android's proxy selector sends them (WalletMeshTest.onlyMeshHostsGoThroughTheResolver).
final class MeshSessionTests: XCTestCase {

    private func answering(_ who: String) -> URLSession { StubSession.ok { _, _ in #""\#(who)""# } }

    func testOnlyMeshHostsGoThroughTheMeshSession() async throws {
        let http = HttpClient(session: answering("plain"), mesh: answering("mesh"))
        let label = "aong2hr4en7v6bnxr3az5hzruad7qsbv27xr5ajioyicfaor3n2mc"
        let viaMesh = try await http.get(URL(string: "http://\(label).dmsg:6420/api/v1/health")!).body
        XCTAssertEqual(viaMesh, #""mesh""#)
        let upper = try await http.get(URL(string: "http://\(label).DMSG:6420/api/v1/health")!).body
        XCTAssertEqual(upper, #""mesh""#)
        let direct = try await http.get(URL(string: "https://node.skycoin.com/api/v1/health")!).body
        XCTAssertEqual(direct, #""plain""#)
    }

    /// A failover list may mix both kinds; each node is asked on its own kind of session.
    func testFailoverPicksTheSessionPerNode() async throws {
        let mesh = StubSession.make { _, _ in (200, #""mesh""#) }
        let plain = StubSession.make { request, _ in
            if request.url?.host == "down.test" { throw URLError(.cannotConnectToHost) }
            return (200, #""plain""#)
        }
        let http = HttpClient(session: plain, failover: NodeFailover(["https://down.test", "http://node1.dmsg:6420"]), mesh: mesh)
        let body = try await http.get(URL(string: "https://down.test/api/v1/health")!).body
        XCTAssertEqual(body, #""mesh""#)
    }
}
