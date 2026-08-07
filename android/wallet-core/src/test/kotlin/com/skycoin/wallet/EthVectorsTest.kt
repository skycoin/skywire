package com.skycoin.wallet

import com.skycoin.wallet.eth.EthCrypto
import com.skycoin.wallet.eth.EthTxn
import com.skycoin.wallet.eth.Rlp
import java.math.BigInteger
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

class EthVectorsTest {

    // NIST/Keccak reference outputs for the original Keccak-256 (the one
    // Ethereum uses — NOT the padded SHA3-256 that FIPS 202 later defined).
    @Test
    fun keccak256Vectors() {
        assertEquals(
            "c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470",
            EthCrypto.keccak256(ByteArray(0)).toHex(),
        )
        assertEquals(
            "4e03657aea45a94fc7d47ba826c8d667c0d1e6e33a64a036ec44f58fa12d6c45",
            EthCrypto.keccak256("abc".toByteArray()).toHex(),
        )
    }

    // The four checksummed examples from EIP-55 itself.
    @Test
    fun eip55Checksums() {
        val vectors = listOf(
            "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed",
            "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359",
            "0xdbF03B407c01E7cD3CBea99509d93f8DDDC8C6FB",
            "0xD1220A0cf47c7B9Be7A2E6BA89F429762e7b9aDb",
        )
        for (vector in vectors) {
            val bytes = EthCrypto.parseAddress(vector.lowercase())
            assertNotNull(bytes)
            assertEquals(vector, EthCrypto.checksumAddress(bytes))
            // The mixed-case spelling validates as itself…
            assertNotNull(EthCrypto.parseAddress(vector))
        }
        // …and a corrupted case is a typo, not a preference.
        assertNull(EthCrypto.parseAddress("0x5AAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"))
        assertNull(EthCrypto.parseAddress("0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAe"))
        assertNull(EthCrypto.parseAddress("5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"))
    }

    // The standard test mnemonic's first two m/44'/60'/0'/0/i addresses —
    // the same pair every major wallet derives for it.
    @Test
    fun addressDerivation() {
        val mnemonic =
            "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
        val account = EthCrypto.accountKey(mnemonic)
        assertEquals(
            "0x9858EfFD232B4033E47d90003D41EC34EcaEda94",
            EthCrypto.address(EthCrypto.key(account, 0).pubKey()),
        )
        assertEquals(
            "0x6Fac4D18c912343BF86fa7049364Dd4E424Ab9C0",
            EthCrypto.address(EthCrypto.key(account, 1).pubKey()),
        )
    }

    // The RLP examples from the Ethereum design docs.
    @Test
    fun rlpEncoding() {
        assertEquals("80", Rlp.encode(Rlp.of(ByteArray(0))).toHex())
        assertEquals("00", Rlp.encode(Rlp.of(byteArrayOf(0))).toHex())
        assertEquals("80", Rlp.encode(Rlp.of(BigInteger.ZERO)).toHex())
        assertEquals("0f", Rlp.encode(Rlp.of(BigInteger.valueOf(15))).toHex())
        assertEquals("820400", Rlp.encode(Rlp.of(BigInteger.valueOf(1024))).toHex())
        assertEquals("83646f67", Rlp.encode(Rlp.of("dog".toByteArray())).toHex())
        assertEquals(
            "c88363617483646f67",
            Rlp.encode(Rlp.Lst(listOf(Rlp.of("cat".toByteArray()), Rlp.of("dog".toByteArray())))).toHex(),
        )
        assertEquals("c0", Rlp.encode(Rlp.Lst(emptyList())).toHex())
        // 56 bytes crosses into the long-string form: b8 then the length.
        val long = ByteArray(56) { 'a'.code.toByte() }
        assertEquals("b838" + long.toHex(), Rlp.encode(Rlp.of(long)).toHex())
    }

    // The worked example from EIP-155, end to end: its signing hash, and the
    // exact signed bytes — our RFC 6979 nonce reproduces the document's
    // signature, so this pins RLP, Keccak, low-S and the recovery id at once.
    @Test
    fun eip155GoldenTransaction() {
        val sec = "4646464646464646464646464646464646464646464646464646464646464646".hexToBytes()
        val to = "3535353535353535353535353535353535353535".hexToBytes()
        val unsigned = Rlp.Lst(
            listOf(
                Rlp.of(BigInteger.valueOf(9)), // nonce
                Rlp.of(BigInteger.valueOf(20_000_000_000)), // gas price
                Rlp.of(BigInteger.valueOf(21_000)), // gas limit
                Rlp.of(to),
                Rlp.of(BigInteger.TEN.pow(18)), // 1 ETH
                Rlp.of(ByteArray(0)), // data
                Rlp.of(BigInteger.ONE), // chain id
                Rlp.of(BigInteger.ZERO),
                Rlp.of(BigInteger.ZERO),
            ),
        )
        val hash = EthCrypto.keccak256(Rlp.encode(unsigned))
        assertEquals(
            "daf5a779ae972f972197303d7b574746c7ef83eadac0f2791ad23db92e4c8e53",
            hash.toHex(),
        )

        val sig = Secp256k1.signCompact(hash, sec)
        val recid = sig[64].toInt()
        val v = BigInteger.valueOf(35L + 2L + recid) // EIP-155, chain id 1
        val signed = Rlp.Lst(
            listOf(
                Rlp.of(BigInteger.valueOf(9)),
                Rlp.of(BigInteger.valueOf(20_000_000_000)),
                Rlp.of(BigInteger.valueOf(21_000)),
                Rlp.of(to),
                Rlp.of(BigInteger.TEN.pow(18)),
                Rlp.of(ByteArray(0)),
                Rlp.of(v),
                Rlp.of(BigInteger(1, sig.copyOfRange(0, 32))),
                Rlp.of(BigInteger(1, sig.copyOfRange(32, 64))),
            ),
        )
        assertEquals(
            "f86c098504a817c800825208943535353535353535353535353535353535353535880" +
                "de0b6b3a76400008025a028ef61340bd939bc2195fe537567866003e1a15d3c71ff6" +
                "3e1590620aa636276a067cbe9d8997f761aecb703304b3800ccf555c9f3dc64214b2" +
                "97fb1966a3b6d83",
            Rlp.encode(signed).toHex(),
        )
    }

    // A type-2 transaction round trip: the signature recovers to the signing
    // address, the wire bytes carry the type prefix, and the txid is their
    // keccak.
    @Test
    fun eip1559SignAndRecover() {
        val sec = "4646464646464646464646464646464646464646464646464646464646464646".hexToBytes()
        val from = EthCrypto.address(Secp256k1.pubKeyFromSecKey(sec))
        val txn = EthTxn(
            chainId = BigInteger.ONE,
            nonce = BigInteger.valueOf(7),
            maxPriorityFeePerGas = BigInteger.valueOf(1_500_000_000),
            maxFeePerGas = BigInteger.valueOf(40_000_000_000),
            gasLimit = BigInteger.valueOf(21_000),
            to = "3535353535353535353535353535353535353535".hexToBytes(),
            value = BigInteger.valueOf(123_456_789),
            data = ByteArray(0),
        )
        val signed = txn.signed(sec)
        assertEquals(0x02, signed.raw[0].toInt())
        assertEquals(EthCrypto.keccak256(signed.raw).toHex(), signed.hash.toHex())
        val recovered = Secp256k1.recoverCompact(txn.signingHash(), signed.signature)
        assertNotNull(recovered)
        assertEquals(from, EthCrypto.address(recovered))
    }

    // ERC-20 call data: 4-byte selector plus two 32-byte words.
    @Test
    fun erc20CallData() {
        val to = ByteArray(20) { 0x11 }
        val data = EthTxn.erc20Transfer(to, BigInteger.valueOf(1_000_000))
        assertEquals(68, data.size)
        assertEquals("a9059cbb", data.copyOfRange(0, 4).toHex())
        assertEquals(
            "0000000000000000000000001111111111111111111111111111111111111111",
            data.copyOfRange(4, 36).toHex(),
        )
        assertEquals(
            "00000000000000000000000000000000000000000000000000000000000f4240",
            data.copyOfRange(36, 68).toHex(),
        )
        val balanceOf = EthTxn.erc20BalanceOf(to)
        assertEquals(36, balanceOf.size)
        assertEquals("70a08231", balanceOf.copyOfRange(0, 4).toHex())
        assertTrue(balanceOf.copyOfRange(4, 36).toHex().endsWith("1111111111111111"))
    }
}
