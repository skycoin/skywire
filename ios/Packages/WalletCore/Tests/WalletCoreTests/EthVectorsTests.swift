@testable import WalletCore
import XCTest

/// Port of EthVectorsTest.kt.
final class EthVectorsTests: XCTestCase {

    // NIST/Keccak reference outputs for the original Keccak-256 (the one
    // Ethereum uses — NOT the padded SHA3-256 that FIPS 202 later defined).
    func testKeccak256Vectors() {
        XCTAssertEqual("c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470", EthCrypto.keccak256([]).hex)
        XCTAssertEqual("4e03657aea45a94fc7d47ba826c8d667c0d1e6e33a64a036ec44f58fa12d6c45", EthCrypto.keccak256(Array("abc".utf8)).hex)
    }

    // The four checksummed examples from EIP-55 itself.
    func testEip55Checksums() throws {
        let vectors = [
            "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed",
            "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359",
            "0xdbF03B407c01E7cD3CBea99509d93f8DDDC8C6FB",
            "0xD1220A0cf47c7B9Be7A2E6BA89F429762e7b9aDb",
        ]
        for vector in vectors {
            let b = try XCTUnwrap(EthCrypto.parseAddress(vector.lowercased()))
            XCTAssertEqual(vector, try EthCrypto.checksumAddress(b))
            // The mixed-case spelling validates as itself…
            XCTAssertNotNil(EthCrypto.parseAddress(vector))
        }
        // …and a corrupted case is a typo, not a preference.
        XCTAssertNil(EthCrypto.parseAddress("0x5AAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"))
        XCTAssertNil(EthCrypto.parseAddress("0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAe"))
        XCTAssertNil(EthCrypto.parseAddress("5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"))
    }

    // The standard test mnemonic's first two m/44'/60'/0'/0/i addresses —
    // the same pair every major wallet derives for it.
    func testAddressDerivation() throws {
        let mnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
        let account = try EthCrypto.accountKey(mnemonic: mnemonic)
        XCTAssertEqual("0x9858EfFD232B4033E47d90003D41EC34EcaEda94", try EthCrypto.address(pubCompressed: EthCrypto.key(account, index: 0).pubKey()))
        XCTAssertEqual("0x6Fac4D18c912343BF86fa7049364Dd4E424Ab9C0", try EthCrypto.address(pubCompressed: EthCrypto.key(account, index: 1).pubKey()))
    }

    // The RLP examples from the Ethereum design docs.
    func testRlpEncoding() {
        XCTAssertEqual("80", Rlp.encode(Rlp.of([UInt8]())).hex)
        XCTAssertEqual("00", Rlp.encode(Rlp.of([0])).hex)
        XCTAssertEqual("80", Rlp.encode(Rlp.of(BigUInt.zero)).hex)
        XCTAssertEqual("0f", Rlp.encode(Rlp.of(BigUInt(15))).hex)
        XCTAssertEqual("820400", Rlp.encode(Rlp.of(BigUInt(1024))).hex)
        XCTAssertEqual("83646f67", Rlp.encode(Rlp.of(Array("dog".utf8))).hex)
        XCTAssertEqual("c88363617483646f67", Rlp.encode(.lst([Rlp.of(Array("cat".utf8)), Rlp.of(Array("dog".utf8))])).hex)
        XCTAssertEqual("c0", Rlp.encode(.lst([])).hex)
        // 56 bytes crosses into the long-string form: b8 then the length.
        let long = [UInt8](repeating: UInt8(ascii: "a"), count: 56)
        XCTAssertEqual("b838" + long.hex, Rlp.encode(Rlp.of(long)).hex)
    }

    // The worked example from EIP-155, end to end: its signing hash, and the
    // exact signed bytes — our RFC 6979 nonce reproduces the document's
    // signature, so this pins RLP, Keccak, low-S and the recovery id at once.
    func testEip155GoldenTransaction() throws {
        let sec = bytes("4646464646464646464646464646464646464646464646464646464646464646")
        let to = bytes("3535353535353535353535353535353535353535")
        let oneEth = BigUInt.pow10(18)
        let unsigned = Rlp.Item.lst([
            Rlp.of(BigUInt(9)), // nonce
            Rlp.of(BigUInt(20_000_000_000)), // gas price
            Rlp.of(BigUInt(21_000)), // gas limit
            Rlp.of(to),
            Rlp.of(oneEth), // 1 ETH
            Rlp.of([UInt8]()), // data
            Rlp.of(BigUInt(1)), // chain id
            Rlp.of(BigUInt.zero),
            Rlp.of(BigUInt.zero),
        ])
        let hash = EthCrypto.keccak256(Rlp.encode(unsigned))
        XCTAssertEqual("daf5a779ae972f972197303d7b574746c7ef83eadac0f2791ad23db92e4c8e53", hash.hex)

        let sig = try Secp256k1.signCompact(hash: hash, sec: sec)
        let v = BigUInt(35 + 2 + UInt64(sig[64])) // EIP-155, chain id 1
        let signed = Rlp.Item.lst([
            Rlp.of(BigUInt(9)),
            Rlp.of(BigUInt(20_000_000_000)),
            Rlp.of(BigUInt(21_000)),
            Rlp.of(to),
            Rlp.of(oneEth),
            Rlp.of([UInt8]()),
            Rlp.of(v),
            Rlp.of(BigUInt(bigEndian: Array(sig[0..<32]))),
            Rlp.of(BigUInt(bigEndian: Array(sig[32..<64]))),
        ])
        XCTAssertEqual(
            "f86c098504a817c800825208943535353535353535353535353535353535353535880"
                + "de0b6b3a76400008025a028ef61340bd939bc2195fe537567866003e1a15d3c71ff6"
                + "3e1590620aa636276a067cbe9d8997f761aecb703304b3800ccf555c9f3dc64214b2"
                + "97fb1966a3b6d83",
            Rlp.encode(signed).hex
        )
    }

    // A type-2 transaction round trip: the signature recovers to the signing
    // address, the wire bytes carry the type prefix, and the txid is their
    // keccak.
    func testEip1559SignAndRecover() throws {
        let sec = bytes("4646464646464646464646464646464646464646464646464646464646464646")
        let from = try EthCrypto.address(pubCompressed: Secp256k1.pubKeyFromSecKey(sec))
        let txn = try EthTxn(
            chainId: BigUInt(1),
            nonce: BigUInt(7),
            maxPriorityFeePerGas: BigUInt(1_500_000_000),
            maxFeePerGas: BigUInt(40_000_000_000),
            gasLimit: BigUInt(21_000),
            to: bytes("3535353535353535353535353535353535353535"),
            value: BigUInt(123_456_789),
            data: []
        )
        let signed = try txn.signed(sec: sec)
        XCTAssertEqual(0x02, signed.raw[0])
        XCTAssertEqual(EthCrypto.keccak256(signed.raw).hex, signed.hash.hex)
        let recovered = try XCTUnwrap(Secp256k1.recoverCompact(hash: txn.signingHash(), sig: signed.signature))
        XCTAssertEqual(from, try EthCrypto.address(pubCompressed: recovered))
    }

    // ERC-20 call data: 4-byte selector plus two 32-byte words.
    func testErc20CallData() throws {
        let to = [UInt8](repeating: 0x11, count: 20)
        let data = try EthTxn.erc20Transfer(to: to, amount: BigUInt(1_000_000))
        XCTAssertEqual(68, data.count)
        XCTAssertEqual("a9059cbb", Array(data[0..<4]).hex)
        XCTAssertEqual("0000000000000000000000001111111111111111111111111111111111111111", Array(data[4..<36]).hex)
        XCTAssertEqual("00000000000000000000000000000000000000000000000000000000000f4240", Array(data[36..<68]).hex)
        let balanceOf = try EthTxn.erc20BalanceOf(owner: to)
        XCTAssertEqual(36, balanceOf.count)
        XCTAssertEqual("70a08231", Array(balanceOf[0..<4]).hex)
        XCTAssertTrue(Array(balanceOf[4..<36]).hex.hasSuffix("1111111111111111"))
    }
}
