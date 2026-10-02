@testable import WalletCore
import XCTest

/// The Swift half of ios/scripts/wallet-crosscheck.sh, line for line the
/// Kotlin CrossCheckDump (android/wallet-core/src/test): for three fixed
/// seeds, the addresses every chain derives and one fully signed transaction
/// per chain (Skycoin, Bitcoin, Ethereum, an ERC-20 transfer), written to
/// the file WALLET_CROSSCHECK_OUT names. The script diffs the two files,
/// which must be identical. Skipped when the variable is unset.
final class CrossCheckDumpTests: XCTestCase {

    /// The two Trezor 12-word vectors and their 24-word one (entropy 0x80 × 32).
    static let seeds = [
        "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
        "legal winner thank year wave sausage worth useful legal winner thank yellow",
        "letter advice cage absurd amount doctor acoustic avoid letter advice cage absurd "
            + "amount doctor acoustic avoid letter advice cage absurd amount doctor acoustic bless",
    ]
    static let usdt = "0xdAC17F958D2ee523a2206206994597C13D831ec7"

    func testDump() throws {
        let out = ProcessInfo.processInfo.environment["WALLET_CROSSCHECK_OUT"] ?? ""
        try XCTSkipIf(out.isEmpty, "set WALLET_CROSSCHECK_OUT=<file>")
        var lines = [String]()
        for (i, mnemonic) in Self.seeds.enumerated() { try dumpSeed(i + 1, mnemonic, &lines) }
        try (lines.joined(separator: "\n") + "\n").write(toFile: out, atomically: true, encoding: .utf8)
    }

    private func dumpSeed(_ n: Int, _ mnemonic: String, _ lines: inout [String]) throws {
        lines.append("seed \(n): \(mnemonic)")
        lines.append("seed \(n) valid: \(Bip39.validate(mnemonic))")

        // Skycoin: two outputs on addresses 0 and 1, spent to address 2,
        // change back to address 0.
        let keys = try SkycoinCrypto.generateKeyPairs(seed: Array(mnemonic.utf8), count: 3)
        let sky = keys.map { SkycoinCrypto.addressFromPubKey($0.public) }
        for (i, a) in sky.enumerated() { lines.append("SKY address \(i): \(a)") }
        let uxid0 = Hashes.sha256(Array("crosscheck \(n) ux 0".utf8))
        let uxid1 = Hashes.sha256(Array("crosscheck \(n) ux 1".utf8))
        let created = try SkycoinCreate.create(
            unspents: [
                SkycoinCreate.UxBalance(hash: uxid0, hashHex: uxid0.hex, bkSeq: 10, address: sky[0], coins: 5_000_000, initialHours: 100, hours: 120),
                SkycoinCreate.UxBalance(hash: uxid1, hashHex: uxid1.hex, bkSeq: 20, address: sky[1], coins: 2_000_000, initialHours: 40, hours: 50),
            ],
            to: [SkycoinCreate.Destination(address: sky[2], coins: 6_000_000)],
            changeAddress: sky[0],
            burnFactor: 10
        )
        var secretOf = [String: [UInt8]]()
        for (i, a) in sky.enumerated() { secretOf[a] = keys[i].secret }
        var txn = created.txn
        try txn.signInputs(keys: created.txn.inputs.map { uxid in
            secretOf[created.spends.first { $0.hash == uxid }!.address]!
        })
        lines.append("SKY plan: fee \(created.feeHours) hours-out \(created.hoursToDestinations) "
            + "change \(created.changeCoins)/\(created.changeHours)")
        lines.append("SKY tx: \(txn.serializeHex())")
        lines.append("SKY txid: \(txn.txidHex())")

        // Bitcoin: one input on receive 0, one on change 0; paid to receive
        // 2 with change to change 1.
        let account = try Bip84.accountKey(mnemonic: mnemonic)
        let receive = try (0..<3).map { Bip84.address(pubKey: try Bip84.key(account, change: 0, index: UInt32($0)).pubKey()) }
        let change = try (0..<2).map { Bip84.address(pubKey: try Bip84.key(account, change: 1, index: UInt32($0)).pubKey()) }
        for (i, a) in receive.enumerated() { lines.append("BTC receive \(i): \(a)") }
        for (i, a) in change.enumerated() { lines.append("BTC change \(i): \(a)") }
        let k0 = try Bip84.key(account, change: 0, index: 0)
        let c0 = try Bip84.key(account, change: 1, index: 0)
        var btc = try BtcTxn(
            inputs: [
                BtcTxn.Input(txid: Hashes.sha256(Array("crosscheck \(n) btc 0".utf8)).hex, vout: 1, valueSats: 150_000, pubKeyHash: Hashes.hash160(k0.pubKey())),
                BtcTxn.Input(txid: Hashes.sha256(Array("crosscheck \(n) btc 1".utf8)).hex, vout: 0, valueSats: 80_000, pubKeyHash: Hashes.hash160(c0.pubKey())),
            ],
            outputs: [
                BtcTxn.Output(valueSats: 200_000, scriptPubKey: BtcAddress.scriptPubKey(receive[2])!),
                BtcTxn.Output(valueSats: 27_000, scriptPubKey: BtcAddress.scriptPubKey(change[1])!),
            ]
        )
        try btc.sign(keys: [k0.key, c0.key])
        lines.append("BTC tx: \(btc.serialize().hex)")
        lines.append("BTC txid: \(btc.txid()) vsize \(btc.vsize())")

        // Ethereum: 0.0123 ETH from address 0 to address 1, and 1.234567
        // USDT from address 0 to address 2.
        let ethAccount = try EthCrypto.accountKey(mnemonic: mnemonic)
        let eth = try (0..<3).map { try EthCrypto.address(pubCompressed: EthCrypto.key(ethAccount, index: UInt32($0)).pubKey()) }
        for (i, a) in eth.enumerated() { lines.append("ETH address \(i): \(a)") }
        let sender = try EthCrypto.key(ethAccount, index: 0).key
        let native = try EthTxn(
            chainId: BigUInt(1),
            nonce: BigUInt(UInt64(n)),
            maxPriorityFeePerGas: BigUInt(1_500_000_000),
            maxFeePerGas: BigUInt(30_000_000_000),
            gasLimit: BigUInt(21_000),
            to: EthCrypto.parseAddress(eth[1])!,
            value: BigUInt(decimal: "12300000000000000")!,
            data: []
        ).signed(sec: sender)
        lines.append("ETH tx: \(native.raw.hex)")
        lines.append("ETH txid: 0x\(native.hash.hex)")
        let token = try EthTxn(
            chainId: BigUInt(1),
            nonce: BigUInt(UInt64(n) + 1),
            maxPriorityFeePerGas: BigUInt(1_500_000_000),
            maxFeePerGas: BigUInt(30_000_000_000),
            gasLimit: BigUInt(65_000),
            to: EthCrypto.parseAddress(Self.usdt)!,
            value: .zero,
            data: EthTxn.erc20Transfer(to: EthCrypto.parseAddress(eth[2])!, amount: BigUInt(1_234_567))
        ).signed(sec: sender)
        lines.append("ERC20 tx: \(token.raw.hex)")
        lines.append("ERC20 txid: 0x\(token.hash.hex)")
    }
}
