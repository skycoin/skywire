import CoreClient
import XCTest

/// Which path updates the app reports to the core as a move (Android: NetworkMovesTest, MeteredFlagTest).
final class NetworkMovesTests: XCTestCase {
    private let wifi = NetworkAttachment(interface: "en0", addresses: ["192.168.1.20", "2001:db8::20"])

    func testTheBaselineIsNoMove() {
        var moves = NetworkMoves()
        XCTAssertFalse(moves.observe(wifi))
        XCTAssertFalse(moves.observe(wifi), "a repeated update is no move")
        XCTAssertEqual(moves.current, wifi)
    }

    func testAnotherInterfaceIsAMove() {
        var moves = NetworkMoves()
        _ = moves.observe(wifi)
        XCTAssertTrue(moves.observe(NetworkAttachment(interface: "pdp_ip0", addresses: ["10.20.30.40"])))
    }

    func testAnAddressGainedIsNoMoveButOneLostIs() {
        var moves = NetworkMoves()
        _ = moves.observe(wifi)
        let privacy = NetworkAttachment(interface: "en0", addresses: wifi.addresses.union(["2001:db8::99"]))
        XCTAssertFalse(moves.observe(privacy), "an IPv6 privacy address arriving re-dialed every session")
        XCTAssertTrue(moves.observe(NetworkAttachment(interface: "en0", addresses: ["2001:db8::99"])))
    }

    func testComingBackAfterTheNetworkWentAwayIsAMove() {
        var moves = NetworkMoves()
        _ = moves.observe(wifi)
        moves.lost()
        XCTAssertNil(moves.current)
        XCTAssertTrue(moves.observe(wifi))
    }

    func testANetworkLostBeforeTheBaselineLeavesTheNextOneTheBaseline() {
        var moves = NetworkMoves()
        moves.lost()
        XCTAssertFalse(moves.observe(wifi))
    }

    func testTheMeteredFlagIsNewsOnlyWhenItChanges() {
        var flag = MeteredFlag()
        XCTAssertTrue(flag.observe(false), "the core is told the first value")
        XCTAssertFalse(flag.observe(false))
        XCTAssertTrue(flag.observe(true))
        XCTAssertFalse(flag.observe(true))
        XCTAssertTrue(flag.observe(false))
    }

    func testAnEventWithoutAMeteredFlagLeavesItOut() throws {
        let data = try JSONEncoder().encode(NetworkEvent(event: "moved", metered: nil, ifname: nil))
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        XCTAssertEqual(object as NSDictionary, ["event": "moved", "source": "ios"] as NSDictionary)
    }
}
