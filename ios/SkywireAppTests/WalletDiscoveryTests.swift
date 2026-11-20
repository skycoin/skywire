@testable import Skywire
import os
import WalletCore
import XCTest

/// The reward system pays each reward to an xpub's next unused address, and the wallet finds
/// them on a refresh (Android: WalletRepository.discoverAddresses and keepFreshReceive).
@MainActor
final class WalletDiscoveryTests: XCTestCase {
    private static let phrase = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

    private var directory: URL!
    private var store: WalletStore!

    override func setUp() async throws {
        directory = FileManager.default.temporaryDirectory.appendingPathComponent("wallet-\(UUID().uuidString)", isDirectory: true)
        NodeStub.used.withLock { $0 = [] }
        NodeStub.queries.withLock { $0 = [] }
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [NodeStub.self]
        let session = URLSession(configuration: config)
        let group = Bundle.main.object(forInfoDictionaryKey: "SkywireWalletKeychainGroup") as? String
        let seeds = WalletSeedStore(service: "com.skycoin.skywire.wallet.tests.\(UUID().uuidString)", accessGroup: group)
        store = WalletStore(directory: directory, seeds: seeds, session: session, meshSession: session)
    }

    override func tearDown() async throws {
        for wallet in store.wallets { try? store.seeds.deleteSeed(wallet.id) }
        try? FileManager.default.removeItem(at: directory)
    }

    func testARefreshFindsRewardsPaidPastTheWalletsAddress() async throws {
        let keys = KeySource(mnemonic: Self.phrase, derivation: .bip44)
        let book = try SkyFiberWalletCore(nodeURL: CoinSpec.sky.nodeUrl, session: .shared)
            .deriveAddresses(seed: keys, receiveCount: 60, changeCount: 0)
        NodeStub.used.withLock { $0 = Set(book.receive.prefix(45)) }
        let wallet = try await store.addWallet(.sky, name: "", mnemonic: Self.phrase, restored: false, derivation: .bip44)
        XCTAssertEqual(wallet.receiveAddresses.count, 1)

        _ = try await store.refresh(wallet.id)
        // The 45 paid ones, and the next one because the last of them is used.
        XCTAssertEqual(store.wallet(wallet.id)?.receiveAddresses, Array(book.receive.prefix(46)))
        XCTAssertTrue(NodeStub.queries.withLock { $0 }.contains(20), "nothing looked past the wallet's addresses")

        // Inside the two minutes a refresh does not look again.
        NodeStub.queries.withLock { $0 = [] }
        _ = try await store.refresh(wallet.id)
        XCTAssertFalse(NodeStub.queries.withLock { $0 }.contains(20), "a second look within the interval")
        XCTAssertEqual(store.wallet(wallet.id)?.receiveAddresses.count, 46)
    }
}

/// A node where each address in `used` was paid once. It notes how many addresses each
/// transactions query named: a look past the wallet asks about 20 at a time.
private final class NodeStub: URLProtocol, @unchecked Sendable {
    static let used = OSAllocatedUnfairLock<Set<String>>(initialState: [])
    static let queries = OSAllocatedUnfairLock<[Int]>(initialState: [])

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

    override func startLoading() {
        guard let url = request.url else { return }
        var fields = URLComponents(url: url, resolvingAgainstBaseURL: false)
        if request.httpMethod == "POST" { fields?.percentEncodedQuery = String(decoding: Self.body(of: request), as: UTF8.self) }
        let addrs = (fields?.queryItems?.first { $0.name == "addrs" }?.value ?? "").split(separator: ",").map(String.init)
        let used = Self.used.withLock { $0 }
        let body: String
        switch url.path {
        case "/api/v1/csrf":
            body = #"{"csrf_token":"t"}"#
        case "/api/v1/transactions":
            Self.queries.withLock { $0.append(addrs.count) }
            let paid = addrs.filter(used.contains).map {
                """
                {"status":{"confirmed":true,"height":1},"txn":{"txid":"\($0)",
                "inputs":[{"uxid":"u","owner":"elsewhere","coins":"1.000000"}],
                "outputs":[{"dst":"\($0)","coins":"1.000000"}]}}
                """
            }
            body = "[\(paid.joined(separator: ","))]"
        case "/api/v1/outputs":
            body = "{}"
        case "/api/v1/balance":
            body = #"{"confirmed":{"coins":45000000,"hours":0},"predicted":{"coins":45000000,"hours":0}}"#
        default:
            client?.urlProtocol(self, didFailWithError: URLError(.unsupportedURL))
            return
        }
        let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    /// A body set on the request reaches a protocol as a stream.
    private static func body(of request: URLRequest) -> Data {
        if let data = request.httpBody { return data }
        guard let stream = request.httpBodyStream else { return Data() }
        stream.open()
        defer { stream.close() }
        var data = Data()
        var buffer = [UInt8](repeating: 0, count: 4096)
        while stream.hasBytesAvailable {
            let n = stream.read(&buffer, maxLength: buffer.count)
            if n <= 0 { break }
            data.append(buffer, count: n)
        }
        return data
    }
}
