@testable import Skywire
import XCTest

/// Wallet calls to .dmsg nodes: the hex key rewritten to its DNS label (Android: WalletMeshTest).
final class WalletMeshTests: XCTestCase {
    // The deployment's Skycoin node, as config gen names it (cmd/skywire-cli/commands/config/gen.go).
    private let hex = "039a6d1e3c237f5f05b78ec19e9f31a007f84835d7ef1e812876102281d1db74c1"
    // What cipher.PubKey.DNSLabel gives for the same key.
    private let label = "aong2hr4en7v6bnxr3az5hzruad7qsbv27xr5ajioyicfaor3n2mc"

    func testHexKeyBecomesTheBase32Label() {
        XCTAssertEqual(WalletMesh.dnsLabel(hex), label)
        XCTAssertEqual(WalletMesh.normalize(" http://\(hex).dmsg:6420 "), "http://\(label).dmsg:6420")
        XCTAssertEqual(WalletMesh.normalize("http://\(hex.uppercased()).DMSG/api"), "http://\(label).dmsg/api")
        XCTAssertEqual(WalletMesh.normalize("https://node.skycoin.com"), "https://node.skycoin.com")
    }

    // Foundation takes the 66-character label, OkHttp and DNS do not (RFC 1035 2.3.4), so both apps
    // keep the label form.
    func testBothFormsAreMeshAndTheLabelIsKept() {
        XCTAssertEqual(URL(string: "http://\(label).dmsg:6420")?.host, "\(label).dmsg")
        XCTAssertTrue(WalletMesh.isMeshURL("http://\(hex).dmsg:6420"))
        XCTAssertFalse(WalletMesh.isMeshURL("https://node.skycoin.com"))
    }

    func testTheRowShortensTheLabel() {
        XCTAssertEqual(WalletMesh.shortURL("http://\(label).dmsg:6420"), "http://aong2hr4…r3n2mc.dmsg:6420")
        XCTAssertEqual(WalletMesh.shortURL("https://node.skycoin.com"), "https://node.skycoin.com")
    }
}
