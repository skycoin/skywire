@testable import WalletCore
import XCTest

/// Port of MultiDestinationTest.kt. A fiber send to several destinations.
/// The expected inputs, outputs and inner hashes are what skycoin's
/// transaction.Create returns for the same unspent outputs at the version
/// go.mod pins, with change to the first address.
final class MultiDestinationTests: XCTestCase {

    private static let seed = KeySource(mnemonic: "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about")
    private static let first = "2EFSW8YqFDG3x6mwfbDjBk6M9eMc6WUoFHZ"
    private static let second = "24qhX1ZC9F8iDaT6R13j4fgJyQvumW2izpm"
    private static let third = "oJAsEpQrwQdVs6D12ufJ4T25EcCQYg9XFX"
    private static let fourth = "Zr7NGdVCLTPCbCrCBeYQFYHt389XhEUcfk"
    private static let spent = "2c4433f46d6078b329217c2ceac3e31afae11533d5f63e49ea70b1458048b2a1"

    private static let outputs = """
        {"head_outputs":[
        {"hash":"0c238c905d91745963347b93e44436a7fa5daefeabd16ea34c04bdc8d5270869","block_seq":5,"address":"\(first)","coins":"10.000000","hours":100,"calculated_hours":100},
        {"hash":"\(spent)","block_seq":6,"address":"\(second)","coins":"100.000000","hours":1000,"calculated_hours":1000}]}
        """

    private static func core() throws -> SkyFiberWalletCore {
        let session = StubSession.ok { request, _ in
            switch request.url?.path {
            case "/api/v1/health": #"{"user_verify_transaction":{"burn_factor":10,"max_transaction_size":32768,"max_decimals":3}}"#
            case "/api/v1/outputs": outputs
            default: throw UnexpectedRequest(request)
            }
        }
        return try SkyFiberWalletCore(nodeURL: "http://node.test", session: session)
    }

    private static let book = AddressBook(receive: [first, second], change: [])

    private static func innerHash(_ core: SkyFiberWalletCore, _ plan: TxPlan) throws -> String {
        String(Array(try core.signTx(seed: seed, plan: plan).rawHex)[10..<74])
    }

    private static func send(
        _ core: SkyFiberWalletCore, hoursTo: UInt64? = nil, more: [Recipient] = [], changeAddress: String? = nil,
        shareFactor: Decimal? = nil, amount: UInt64 = 20_000_000, sendMax: Bool = false
    ) async throws -> TxPlan {
        try await core.buildTx(
            seed: seed, book: book, toAddress: third, amount: amount, feeRate: nil, sendMax: sendMax,
            hoursTo: hoursTo, more: more, changeAddress: changeAddress, shareFactor: shareFactor
        )
    }

    func testAutomaticHoursFollowTheCoins() async throws {
        let core = try Self.core()
        let plan = try await Self.send(core, more: [Recipient(address: Self.fourth, amount: 5_000_000)])
        XCTAssertEqual(25_000_000, plan.amount)
        XCTAssertEqual(
            [Recipient(address: Self.third, amount: 20_000_000, hours: 360), Recipient(address: Self.fourth, amount: 5_000_000, hours: 90)],
            plan.recipients
        )
        XCTAssertEqual(75_000_000, plan.changeAmount)
        XCTAssertEqual(450, plan.hoursChange)
        XCTAssertEqual("8cdc4be4cb7e4424c9bb4a500075a005c118775c4d3dc281cad56bed133e4309", try Self.innerHash(core, plan))
    }

    func testManualHoursGoToEachDestination() async throws {
        let core = try Self.core()
        let plan = try await Self.send(core, hoursTo: 50, more: [Recipient(address: Self.fourth, amount: 5_000_000, hours: 30)])
        XCTAssertEqual([50, 30], plan.recipients.map(\.hours))
        XCTAssertEqual(820, plan.hoursChange)
        XCTAssertEqual("13c23f5b5bf3144ce2efe6efd6f2cc552c5670201a6a94c8aaba1e5ca6c4f1f9", try Self.innerHash(core, plan))
    }

    func testChangeGoesToTheAddressAsked() async throws {
        let core = try Self.core()
        let plan = try await Self.send(core, changeAddress: Self.fourth)
        XCTAssertEqual(Self.fourth, plan.changeAddress)
        XCTAssertEqual(80_000_000, plan.changeAmount)
        XCTAssertEqual("79a463487be5b9083d1f0c330deed5160210fb171cb4745b535e989dde72bb4b", try Self.innerHash(core, plan))
    }

    func testShareFactorSetsTheHoursSentOnward() async throws {
        let core = try Self.core()
        let cases: [(String, [UInt64], String)] = [
            ("0.1", [72, 18, 810], "c6d604ec6052bb8dd0358eb0f0be54d40a581a3cbad164a5e8cb0e553f87173f"),
            ("0.25", [180, 45, 675], "5928013c3b40aef3dce60d13fa2ce49d4d230bfbc51db92d56efc43b6c4ea101"),
            ("0.73", [526, 131, 243], "ef56f22e3a3e42f69bc161b0c2c398c701c4493c05a62bc8b3a703561e93f164"),
            ("1", [720, 180, 0], "1c672237d11bb4b948f82249a4f7fee9791266cba872f11a97f29fdcf9c8d0b9"),
        ]
        for (share, hours, inner) in cases {
            let plan = try await Self.send(
                core, more: [Recipient(address: Self.fourth, amount: 5_000_000)], shareFactor: Decimal(string: share)
            )
            XCTAssertEqual(hours, plan.recipients.map { $0.hours ?? 0 } + [plan.hoursChange ?? 0], "share \(share)")
            XCTAssertEqual(inner, try Self.innerHash(core, plan), "share \(share)")
        }
    }

    // skycoin's transaction.Create output for the two-destination send above, unsigned.
    private static let goUnsigned = "01010000008cdc4be4cb7e4424c9bb4a500075a005c118775c4d3dc281cad56bed133e4309"
        + "01000000" + String(repeating: "00", count: 65) + "01000000" + spent
        + "0300000000730e4768ecc67163e20a7107c3814a18700ccb90002d31010000000068010000000000000051a19bde64aeff9c"
        + "6515a7886838340716cfd7ba404b4c00000000005a0000000000000000b111ba744917645301e900a180feba35191a0551"
        + "c068780400000000c201000000000000"

    func testUnsignedMatchesTheReferenceAndSignsLikeASend() async throws {
        let core = try Self.core()
        let plan = try await Self.send(core, more: [Recipient(address: Self.fourth, amount: 5_000_000)])
        XCTAssertEqual(Self.goUnsigned, core.unsignedHex(plan))
        // Signatures are deterministic (RFC 6979), so signing the pasted
        // transaction gives the bytes the send itself would have broadcast.
        let signedRaw = try await core.signRaw(seed: Self.seed, book: Self.book, rawHex: Self.goUnsigned).rawHex
        XCTAssertEqual(try core.signTx(seed: Self.seed, plan: plan).rawHex, signedRaw)
        XCTAssertEqual(Self.goUnsigned, core.unsignedHex(plan), "signing must not change the unsigned form")
    }

    func testRawTransactionsAreChecked() async throws {
        let core = try Self.core()
        func signRaw(_ raw: String, _ book: AddressBook? = nil) async -> WalletError? {
            await thrown { try await core.signRaw(seed: Self.seed, book: book ?? Self.book, rawHex: raw) } as? WalletError
        }
        func isBad(_ e: WalletError?) -> Bool { if case .badTransaction? = e { true } else { false } }
        let short = await signRaw(String(Self.goUnsigned.dropLast(2)))
        XCTAssertTrue(isBad(short), "\(String(describing: short))")
        let long = await signRaw(Self.goUnsigned + "00")
        XCTAssertTrue(isBad(long), "\(String(describing: long))")
        // A changed output no longer matches the inner hash.
        let changed = await signRaw(Self.goUnsigned.replacingOccurrences(of: "68010000", with: "69010000"))
        XCTAssertTrue(isBad(changed), "\(String(describing: changed))")
        let unsigned = await thrown { try await core.broadcastRaw(Self.goUnsigned) } as? WalletError
        XCTAssertTrue(isBad(unsigned), "\(String(describing: unsigned))")
        let stranger = await signRaw(Self.goUnsigned, AddressBook(receive: [Self.first], change: []))
        XCTAssertEqual(WalletError.notInWallet, stranger)
    }

    func testHoursForSomeDestinationsOnlyAreRefused() async throws {
        let core = try Self.core()
        let error = await thrown { try await Self.send(core, more: [Recipient(address: Self.fourth, amount: 5_000_000, hours: 30)]) }
        guard case .invalidAmount? = error as? WalletError else { return XCTFail("got \(String(describing: error))") }
    }

    func testMaxWithSeveralDestinationsIsRefused() async throws {
        let core = try Self.core()
        let error = await thrown {
            try await Self.send(core, more: [Recipient(address: Self.fourth, amount: 5_000_000)], amount: 0, sendMax: true)
        }
        guard case .invalidAmount? = error as? WalletError else { return XCTFail("got \(String(describing: error))") }
    }
}
