import CoreClient
import CoreImage
@testable import Skywire
import WalletCore
import XCTest

/// The wallet's store against the Simulator's real Keychain, inside the app
/// (hosted, for the app's entitlements, as SecretStoreTests). Network-free:
/// fresh phrases need no address scan, and a second add of an account takes
/// the adopt path. Each test has its own Keychain service and directory and
/// removes both.
@MainActor
final class WalletStoreTests: XCTestCase {
    private static let phrase = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

    private var directory: URL!
    private var service = ""
    private var group: String?
    private var store: WalletStore!

    override func setUp() async throws {
        directory = FileManager.default.temporaryDirectory.appendingPathComponent("wallet-\(UUID().uuidString)", isDirectory: true)
        service = "com.skycoin.skywire.wallet.tests.\(UUID().uuidString)"
        group = Bundle.main.object(forInfoDictionaryKey: "SkywireWalletKeychainGroup") as? String
        store = WalletStore(directory: directory, seeds: WalletSeedStore(service: service, accessGroup: group))
    }

    override func tearDown() async throws {
        for wallet in store.wallets { try? store.seeds.deleteSeed(wallet.id) }
        try? FileManager.default.removeItem(at: directory)
    }

    /// Its own group, from the xcconfig, signed with the Simulator's prefix:
    /// not the one SecretStore shares with the packet-tunnel extension.
    func testTheWalletHasItsOwnKeychainGroup() {
        XCTAssertEqual(group?.hasSuffix(".com.skycoin.skywire.wallet"), true, "\(group ?? "nil")")
        let shared = Bundle.main.object(forInfoDictionaryKey: "SkywireKeychainGroup") as? String
        XCTAssertNotEqual(group, shared)
    }

    /// The phrase is a Keychain item readable only while the phone is
    /// unlocked, never migrated off it, in the wallet's group alone.
    func testAPhraseIsSealedForThisDeviceOnly() async throws {
        let meta = try await store.addWallet(.sky, name: "", mnemonic: Self.phrase, restored: false)
        let attributes = try XCTUnwrap(try store.seeds.attributes(meta.id))
        XCTAssertEqual(attributes[kSecAttrAccessible as String] as? String, kSecAttrAccessibleWhenUnlockedThisDeviceOnly as String)
        XCTAssertEqual(attributes[kSecAttrAccessGroup as String] as? String, group)
        XCTAssertEqual(try store.seeds.seed(meta.id), Self.phrase)

        // The group SecretStore (and later the extension) uses cannot see it.
        let shared = Bundle.main.object(forInfoDictionaryKey: "SkywireKeychainGroup") as? String
        XCTAssertNil(try WalletSeedStore(service: service, accessGroup: shared).seed(meta.id))
    }

    /// A created wallet holds the phrase's first address, is settled (a fresh
    /// phrase has nothing to scan for), becomes the coin's active wallet, and
    /// is still there for a store opened over the same directory.
    func testCreateDerivesTheFirstAddressAndPersists() async throws {
        let meta = try await store.addWallet(.sky, name: "", mnemonic: Self.phrase, restored: false)
        let first = SkycoinCrypto.addressFromPubKey(try SkycoinCrypto.generateKeyPairs(seed: Array(Self.phrase.utf8), count: 1)[0].public)
        XCTAssertEqual(meta.receiveAddresses, [first])
        XCTAssertEqual(meta.name, "SKY")
        XCTAssertFalse(meta.addressScanPending)
        XCTAssertEqual(store.activeWalletId("SKY"), meta.id)

        let reopened = WalletStore(directory: directory, seeds: store.seeds)
        XCTAssertEqual(reopened.wallets, [meta])
        XCTAssertEqual(reopened.activeWalletId("SKY"), meta.id)
    }

    /// The registry and caches stay out of backups: the phrases never leave
    /// the phone, so a restored backup must not bring wallets without keys.
    func testTheWalletDirectoryIsKeptOutOfBackups() async throws {
        _ = try await store.addWallet(.sky, name: "", mnemonic: Self.phrase, restored: false)
        let values = try directory.resourceValues(forKeys: [.isExcludedFromBackupKey])
        XCTAssertEqual(values.isExcludedFromBackup, true)
    }

    /// ETH and every ERC-20 are one account: creating the ETH wallet gives
    /// USDT the same one, a token added later adopts it, and adding the same
    /// account to a coin that has it adopts rather than duplicates.
    func testAnEthereumWalletIsEveryTokensWallet() async throws {
        let eth = try await store.addWallet(.eth, name: "", mnemonic: Self.phrase, restored: false)
        XCTAssertEqual(eth.receiveAddresses, ["0x9858EfFD232B4033E47d90003D41EC34EcaEda94"])
        let usdt = try XCTUnwrap(store.wallets.first { $0.coinId == "USDT" })
        XCTAssertEqual(usdt.receiveAddresses, eth.receiveAddresses)
        XCTAssertNotEqual(usdt.id, eth.id, "a copy per coin, so removing one strands nothing")
        XCTAssertEqual(try store.seeds.seed(usdt.id), Self.phrase)
        XCTAssertEqual(store.activeWalletId("USDT"), usdt.id)

        let token = try store.addErc20Token(name: "Dai", ticker: "dai", contract: "0x6B175474E89094C44Da98b954EedeAC495271d0F", decimals: 18, icon: nil)
        XCTAssertEqual(token.ticker, "DAI")
        let adopted = store.wallets.filter { $0.coinId == token.id }
        XCTAssertEqual(adopted.map(\.receiveAddresses), [eth.receiveAddresses], "one wallet per account, not one per sibling")

        let again = try await store.addWallet(.usdt, name: "", mnemonic: Self.phrase, restored: false)
        XCTAssertEqual(again.id, usdt.id)
        XCTAssertEqual(store.wallets.filter { $0.coinId == "USDT" }.count, 1)
    }

    /// A fiber coin is its own chain: no mirroring between them.
    func testFiberCoinsDoNotShareWallets() async throws {
        let coin = try store.addFiberCoin(name: "Test fiber", ticker: "tfc", nodeUrl: "https://fiber.example/", icon: nil)
        XCTAssertEqual(coin.nodeUrl, "https://fiber.example")
        _ = try await store.addWallet(.sky, name: "", mnemonic: Self.phrase, restored: false)
        XCTAssertTrue(store.wallets.allSatisfy { $0.coinId == "SKY" })
    }

    /// A coin holding wallets is refused (removing it would cascade into
    /// erasing phrases); a built-in never goes; an empty user coin does, and
    /// the selection moves off it.
    func testCoinRemovalRules() async throws {
        XCTAssertThrowsError(try store.removeUserCoin("SKY"))
        let coin = try store.addFiberCoin(name: "Test fiber", ticker: "TFC", nodeUrl: "https://fiber.example", icon: nil)
        store.setSelectedCoin(coin.id)
        let meta = try await store.addWallet(coin, name: "", mnemonic: Self.phrase, restored: false)
        XCTAssertThrowsError(try store.removeUserCoin(coin.id)) { error in
            XCTAssertNotNil(error as? WalletStoreError)
        }
        try store.removeWallet(meta.id)
        XCTAssertEqual(try store.removeUserCoin(coin.id)?.id, coin.id)
        XCTAssertNil(store.coin(coin.id))
        XCTAssertEqual(store.selectedCoinId, "SKY")
    }

    /// Removing a wallet erases its phrase and hands "active" to a sibling.
    func testRemoveWalletErasesItsPhrase() async throws {
        let first = try await store.addWallet(.sky, name: "", mnemonic: Self.phrase, restored: false)
        let other = "legal winner thank year wave sausage worth useful legal winner thank yellow"
        let second = try await store.addWallet(.sky, name: "second", mnemonic: other, restored: false)
        XCTAssertEqual(store.activeWalletId("SKY"), second.id)
        try store.removeWallet(second.id)
        XCTAssertNil(try store.seeds.seed(second.id))
        XCTAssertEqual(store.activeWalletId("SKY"), first.id)
        XCTAssertEqual(store.wallets.map(\.id), [first.id])
    }

    /// An invalid phrase is refused before anything is stored.
    func testAnInvalidPhraseIsRefused() async {
        do {
            _ = try await store.addWallet(.sky, name: "", mnemonic: "abandon abandon abandon", restored: false)
            XCTFail("an invalid phrase was accepted")
        } catch {
            XCTAssertTrue(store.wallets.isEmpty)
        }
    }

    func testNodeOverride() throws {
        try store.setNodeUrl(coinId: "SKY", url: " http://192.168.1.5:6420/ ")
        XCTAssertEqual(store.coin("SKY")?.nodeUrl, "http://192.168.1.5:6420")
        XCTAssertEqual(store.defaultNodeUrls["SKY"], "https://node.skycoin.com")
        try store.setNodeUrl(coinId: "SKY", url: "")
        XCTAssertEqual(store.coin("SKY")?.nodeUrl, "https://node.skycoin.com")
        XCTAssertThrowsError(try store.setNodeUrl(coinId: "SKY", url: "ftp://node.example"))
    }

    /// A scan reports what the chain has seen; an address the wallet already
    /// handed out stays even when the chain has not seen it yet.
    func testSettledAddressesOnlyGrow() {
        let meta = WalletMeta(id: "w", coinId: "BTC", name: "BTC", createdAtMs: 1, receiveAddresses: ["a", "b", "c"], changeAddresses: ["x"])
        let settled = settledAddresses(meta, scanned: AddressBook(receive: ["a", "b"], change: ["x", "y"]), nowMs: 42)
        XCTAssertEqual(settled.receiveAddresses, ["a", "b", "c"])
        XCTAssertEqual(settled.changeAddresses, ["x", "y"])
        XCTAssertEqual(settled.addressScanAtMs, 42)
    }

    /// A history that did not arrive keeps the last one and says how old it
    /// is; one that did replaces it.
    func testMergeSnapshotKeepsTheLastHistoryWhenTheFetchMissed() {
        let tx = TxRecord(txid: "t", incoming: true, amount: 5, party: nil, timestamp: 10, confirmed: true, confirmations: 3, fee: 1)
        let balance = WalletBalance(confirmed: 7, predicted: 8, hours: 9, spendableOutputs: 1)
        let first = mergeSnapshot(balance: balance, history: [tx], previous: nil, nowMs: 100)
        XCTAssertEqual(first.txs.map(\.txid), ["t"])
        XCTAssertFalse(first.historyBehind)
        let missed = mergeSnapshot(balance: balance, history: nil, previous: first, nowMs: 200)
        XCTAssertEqual(missed.txs.map(\.txid), ["t"])
        XCTAssertEqual(missed.historyFetchedAtMs, 100)
        XCTAssertTrue(missed.historyBehind)
        let never = mergeSnapshot(balance: balance, history: nil, previous: nil, nowMs: 300)
        XCTAssertTrue(never.txs.isEmpty)
        XCTAssertTrue(never.historyBehind)
    }

    /// The Receive QR reads back as the address, with the same decoder the
    /// chat page's scanner uses.
    func testTheReceiveQRReadsBackAsTheAddress() throws {
        for address in ["2EFSW8YqFDG3x6mwfbDjBk6M9eMc6WUoFHZ", "bc1qcr8te4kr609gcawutmrza0j4xv80jy8z306fyu", "0x9858EfFD232B4033E47d90003D41EC34EcaEda94"] {
            let image = try XCTUnwrap(QRCodeImage.render(address)?.cgImage)
            XCTAssertEqual(QrDecoder.decode(CIImage(cgImage: image)), address)
        }
    }

    /// A comma-decimal keyboard's "0,5" is read as 0.5; a dot stays a dot.
    func testAmountInputTakesTheLocalDecimalSeparator() {
        let separator = Locale.current.decimalSeparator ?? "."
        XCTAssertEqual(WalletModel.amountInput("0\(separator)5"), "0.5")
    }
}
