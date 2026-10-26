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
        let coin = try store.addFiberCoin(name: "Test fiber", ticker: "tfc", nodeUrl: "https://fiber.example/", info: NodeInfo(), icon: nil)
        XCTAssertEqual(coin.nodeUrl, "https://fiber.example")
        _ = try await store.addWallet(.sky, name: "", mnemonic: Self.phrase, restored: false)
        XCTAssertTrue(store.wallets.allSatisfy { $0.coinId == "SKY" })
    }

    /// A blank name or ticker is the node's, as skycoin-web takes them, and the explorer link is
    /// the one the node names; with nothing typed and nothing from the node, the add is refused.
    func testAFiberCoinIsNamedByItsNode() throws {
        let info = NodeInfo(coinName: "Testcoin", ticker: "tst", explorerURL: "https://explorer.test")
        let coin = try store.addFiberCoin(name: " ", ticker: "", nodeUrl: "https://fiber.example", info: info, icon: nil)
        XCTAssertEqual(coin.name, "Testcoin")
        XCTAssertEqual(coin.ticker, "TST")
        XCTAssertEqual(coin.explorerTxUrl, "https://explorer.test/app/transaction/%s")
        let typed = try store.addFiberCoin(name: "Mine", ticker: "MN", nodeUrl: "https://fiber.example", info: info, icon: nil)
        XCTAssertEqual(typed.name, "Mine")
        XCTAssertEqual(typed.ticker, "MN")
        XCTAssertThrowsError(try store.addFiberCoin(name: "", ticker: "", nodeUrl: "https://fiber.example", info: NodeInfo(), icon: nil))
    }

    /// A coin holding wallets is refused (removing it would cascade into
    /// erasing phrases); a built-in never goes; an empty user coin does, and
    /// the selection moves off it.
    func testCoinRemovalRules() async throws {
        XCTAssertThrowsError(try store.removeUserCoin("SKY"))
        let coin = try store.addFiberCoin(name: "Test fiber", ticker: "TFC", nodeUrl: "https://fiber.example", info: NodeInfo(), icon: nil)
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
        try store.setNodeUrls(coinId: "SKY", urls: [" http://192.168.1.5:6420/ "])
        XCTAssertEqual(store.coin("SKY")?.nodeUrl, "http://192.168.1.5:6420")
        XCTAssertEqual(store.coin("SKY")?.tryNodes, ["http://192.168.1.5:6420", "https://node.skycoin.com"])
        XCTAssertEqual(store.defaultCoins["SKY"]?.nodeUrl, "https://node.skycoin.com")
        try store.setNodeUrls(coinId: "SKY", urls: [])
        XCTAssertEqual(store.coin("SKY")?.nodeUrl, "https://node.skycoin.com")
        XCTAssertThrowsError(try store.setNodeUrls(coinId: "SKY", urls: ["ftp://node.example"]))
    }

    /// The list is kept in its order, without the shipped address or repeats, newline-joined as
    /// Android stores it, so a value from the single-node days reads as a list of one.
    func testANodeListKeepsItsOrder() throws {
        try store.setNodeUrls(coinId: "SKY", urls: ["https://b.example", "https://node.skycoin.com", "https://a.example/", "https://b.example"])
        XCTAssertEqual(store.registry.nodeUrls["SKY"], "https://b.example\nhttps://a.example")
        XCTAssertEqual(store.coin("SKY")?.tryNodes, ["https://b.example", "https://a.example", "https://node.skycoin.com"])
        var old = WalletRegistry()
        old.nodeUrls["SKY"] = "http://10.0.0.5:6420"
        XCTAssertEqual(old.nodeLists["SKY"], ["http://10.0.0.5:6420"])
    }

    /// Every coin's node moves, the Ethereum family's indexer with it when asked, and the shipped
    /// address is stored as nothing so a later release is not held to it (Android: setNodeUrls).
    func testNodeAndIndexerOverride() throws {
        try store.setNodeUrls(coinId: "BTC", urls: ["https://blockstream.info/api"])
        XCTAssertEqual(store.coin("BTC")?.nodeUrl, "https://blockstream.info/api")
        XCTAssertNil(store.coin("BTC")?.indexerUrl)

        try store.setNodeUrls(coinId: "ETH", urls: ["https://rpc.example"], indexerUrl: "https://scout.example/")
        XCTAssertEqual(store.coin("ETH")?.nodeUrl, "https://rpc.example")
        XCTAssertEqual(store.coin("ETH")?.indexerUrl, "https://scout.example")
        XCTAssertEqual(store.coin("USDT"), CoinSpec.usdt, "a choice made for ETH does not move USDT")
        XCTAssertThrowsError(try store.setNodeUrls(coinId: "ETH", urls: ["https://rpc.example"], indexerUrl: "scout"))

        // nil leaves the indexer as it is.
        try store.setNodeUrls(coinId: "ETH", urls: ["https://rpc2.example"], indexerUrl: nil)
        XCTAssertEqual(store.coin("ETH")?.indexerUrl, "https://scout.example")

        try store.setNodeUrls(coinId: "ETH", urls: [CoinSpec.ethNode], indexerUrl: CoinSpec.ethIndexer)
        XCTAssertEqual(store.coin("ETH"), CoinSpec.eth)
        XCTAssertNil(store.registry.nodeUrls["ETH"])
        XCTAssertNil(store.registry.indexerUrls["ETH"])
    }

    // Android's WalletNodeUrlTest, case for case.

    private let fiber = CoinSpec(id: "fiber-1", name: "Testcoin", ticker: "TST", kind: .skyFiber, nodeUrl: "https://node.example")

    func testTheShippedAddressIsUsedWhenNothingIsSet() {
        XCTAssertEqual(fiber.withNodeOverride([:]), fiber)
    }

    func testAnAddressTheUserSetWins() {
        let moved = fiber.withNodeOverride(["fiber-1": ["http://10.0.0.5:6420"]])
        XCTAssertEqual(moved.nodeUrl, "http://10.0.0.5:6420")
        XCTAssertEqual(moved.id, fiber.id)
        XCTAssertEqual(moved.ticker, fiber.ticker)
        XCTAssertEqual(moved.kind, fiber.kind)
    }

    func testAnotherCoinsOverrideIsNotThisOnes() {
        XCTAssertEqual(fiber.withNodeOverride(["SKY": ["http://elsewhere"]]), fiber)
    }

    func testBlankAndWhitespaceMeanTheShippedAddress() {
        XCTAssertEqual(fiber.withNodeOverride(["fiber-1": [""]]), fiber)
        XCTAssertEqual(fiber.withNodeOverride(["fiber-1": ["   "]]), fiber)
    }

    func testAnOverrideEqualToTheShippedAddressChangesNothing() {
        XCTAssertEqual(fiber.withNodeOverride(["fiber-1": ["https://node.example"]]), fiber)
    }

    func testBitcoinsNodeMovesLikeAnyOther() {
        let moved = CoinSpec.btc.withNodeOverride(["BTC": ["https://blockstream.info"]])
        XCTAssertEqual(moved.nodeUrl, "https://blockstream.info")
        XCTAssertNil(moved.indexerUrl)
    }

    func testTheEthereumIndexerMovesOnItsOwn() {
        let node = CoinSpec.eth.withNodeOverride(["ETH": ["https://rpc.example"]])
        XCTAssertEqual(node.nodeUrl, "https://rpc.example")
        XCTAssertEqual(node.indexerUrl, CoinSpec.ethIndexer)
        let indexer = CoinSpec.eth.withNodeOverride([:], indexerOverrides: ["ETH": "https://scout.example"])
        XCTAssertEqual(indexer.nodeUrl, CoinSpec.ethNode)
        XCTAssertEqual(indexer.indexerUrl, "https://scout.example")
    }

    func testATokenIsMovedByItsOwnId() {
        XCTAssertEqual(CoinSpec.usdt.withNodeOverride(["ETH": ["https://rpc.example"]], indexerOverrides: ["ETH": "https://rpc.example"]), CoinSpec.usdt)
        let usdt = CoinSpec.usdt.withNodeOverride([:], indexerOverrides: ["USDT": "https://scout.example"])
        XCTAssertEqual(usdt.indexerUrl, "https://scout.example")
    }

    func testACoinWithoutAnIndexerNeverGainsOne() {
        XCTAssertEqual(fiber.withNodeOverride([:], indexerOverrides: ["fiber-1": "https://scout.example"]), fiber)
    }

    func testABlankIndexerMeansTheShippedOne() {
        XCTAssertEqual(CoinSpec.eth.withNodeOverride([:], indexerOverrides: ["ETH": "  "]), CoinSpec.eth)
    }

    // Android's WalletNodeOrderTest.

    func testWithoutOwnNodesTheShippedAddressStandsAlone() {
        let spec = CoinSpec.sky.withNodeOverride(["SKY": []])
        XCTAssertEqual(spec, CoinSpec.sky, "an empty list changes nothing")
        XCTAssertEqual(spec.tryNodes, [CoinSpec.sky.nodeUrl])
    }

    func testEarlierMeansHigherPriority() {
        let spec = CoinSpec.sky.withNodeOverride(["SKY": ["https://mine.example", "http://lan:8080"]])
        XCTAssertEqual(spec.nodeUrl, "https://mine.example", "the first node is the one in force")
        XCTAssertEqual(spec.tryNodes, ["https://mine.example", "http://lan:8080", CoinSpec.sky.nodeUrl],
                       "the shipped address rides behind the list as the fallback")
    }

    func testNamingTheShippedAddressDoesNotDuplicateIt() {
        let spec = CoinSpec.sky.withNodeOverride(["SKY": [CoinSpec.sky.nodeUrl, "https://mine.example"]])
        XCTAssertEqual(spec.tryNodes, ["https://mine.example", CoinSpec.sky.nodeUrl], "the fallback is never duplicated and always last")
    }

    func testBlanksAreDropped() {
        let spec = CoinSpec.sky.withNodeOverride(["SKY": ["", "  ", "https://mine.example"]])
        XCTAssertEqual(spec.tryNodes, ["https://mine.example", CoinSpec.sky.nodeUrl])
    }

    func testTheShippedNodesAreEncrypted() {
        XCTAssertTrue(CoinSpec.sky.nodeUrl.hasPrefix("https://"))
        XCTAssertTrue(CoinSpec.btc.nodeUrl.hasPrefix("https://"))
        XCTAssertTrue(CoinSpec.eth.nodeUrl.hasPrefix("https://"))
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

    // MARK: Key sources (Android: WalletMetaDerivationTest, and #5772's wallet kinds)

    /// The wallet type is stored with each wallet, and wallets stored before it existed stay classic.
    func testWalletsStoredBeforeTheChoiceAreStandard() throws {
        let old = #"{"id":"w-1","coinId":"SKY","name":"SKY","createdAtMs":1,"receiveAddresses":["a"]}"#
        XCTAssertEqual(try JSONDecoder().decode(WalletMeta.self, from: Data(old.utf8)).derivation, .standard)
        let meta = WalletMeta(id: "w-2", coinId: "SKY", name: "SKY 2", createdAtMs: 1, receiveAddresses: ["a"], derivation: .bip44)
        XCTAssertEqual(try JSONDecoder().decode(WalletMeta.self, from: JSONEncoder().encode(meta)).derivation, .bip44)
    }

    /// A BIP 44 Skycoin wallet derives other addresses than the classic one from the same phrase,
    /// keeps its type, and has extended public keys; the classic one has none.
    func testABip44SkycoinWallet() async throws {
        let classic = try await store.addWallet(.sky, name: "", mnemonic: Self.phrase, restored: false)
        let bip44 = try await store.addWallet(.sky, name: "", mnemonic: Self.phrase, restored: false, derivation: .bip44)
        XCTAssertNotEqual(bip44.id, classic.id, "the same phrase as another type is another wallet")
        XCTAssertEqual(bip44.derivation, .bip44)
        let expected = try SkyFiberWalletCore(nodeURL: CoinSpec.sky.nodeUrl, session: .shared)
            .deriveAddresses(seed: KeySource(mnemonic: Self.phrase, derivation: .bip44), receiveCount: 1, changeCount: 0)
        XCTAssertEqual(bip44.receiveAddresses, expected.receive)
        XCTAssertNotEqual(bip44.receiveAddresses, classic.receiveAddresses)
        XCTAssertTrue(try XCTUnwrap(try store.extendedPublicKeys(walletId: bip44.id)).account.hasPrefix("xpub"))
        XCTAssertNil(try store.extendedPublicKeys(walletId: classic.id))
    }

    /// A passphrase changes the keys, is sealed beside the phrase, and goes with it; the classic
    /// fiber wallet has no use for one and stores none.
    func testAPassphraseIsSealedAndGoesWithThePhrase() async throws {
        let plain = try await store.addWallet(.btc, name: "", mnemonic: Self.phrase, restored: false)
        let guarded = try await store.addWallet(.btc, name: "", mnemonic: Self.phrase, restored: false, passphrase: "pw")
        XCTAssertNotEqual(guarded.receiveAddresses, plain.receiveAddresses)
        XCTAssertEqual(try store.revealPassphrase(guarded.id), "pw")
        XCTAssertEqual(try store.revealPassphrase(plain.id), "")
        try store.removeWallet(guarded.id)
        XCTAssertEqual(try store.seeds.passphrase(guarded.id), "", "the passphrase outlived its wallet")

        let classic = try await store.addWallet(.sky, name: "", mnemonic: Self.phrase, restored: false, passphrase: "pw")
        XCTAssertEqual(try store.revealPassphrase(classic.id), "")
    }

    /// Legacy Bitcoin wallets use P2PKH addresses, which start with 1.
    func testALegacyBitcoinWallet() async throws {
        let legacy = try await store.addWallet(.btc, name: "", mnemonic: Self.phrase, restored: false, derivation: .bip44)
        XCTAssertTrue(legacy.receiveAddresses[0].hasPrefix("1"), legacy.receiveAddresses[0])
    }

    /// A classic fiber wallet's non-standard seed is taken exactly as typed, case and spaces
    /// included; as a phrase it is refused.
    func testANonStandardSeedIsTakenAsTyped() async throws {
        let seed = "My  Old Seed"
        do {
            _ = try await store.addWallet(.sky, name: "", mnemonic: seed, restored: false)
            XCTFail("a non-standard seed restored as a phrase")
        } catch {}
        let meta = try await store.addWallet(.sky, name: "", mnemonic: seed, restored: false, exact: true)
        let first = SkycoinCrypto.addressFromPubKey(try SkycoinCrypto.generateKeyPairs(seed: Array(seed.utf8), count: 1)[0].public)
        XCTAssertEqual(meta.receiveAddresses, [first])
        XCTAssertEqual(try store.revealSeed(meta.id), seed)
    }
}
